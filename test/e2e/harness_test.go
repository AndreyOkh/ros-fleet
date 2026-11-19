package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	testBinPath string
	buildOnce   sync.Once
)

func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "../.."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "../.."
}

func TestMain(m *testing.M) {
	exitCode := runTestMain(m)
	os.Exit(exitCode)
}

func runTestMain(m *testing.M) int {
	// 1. Build ros-fleet binary once for the entire test suite
	tmpDir, err := os.MkdirTemp("", "ros-fleet-e2e-main-*")
	if err != nil {
		log.Fatalf("TestMain: failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testBinPath = filepath.Join(tmpDir, "ros-fleet")
	modRoot := findModuleRoot()
	ldflags := "-X main.version=v2.0.0 -X github.com/AndreyOkh/ros-fleet/internal/version.Version=v2.0.0"
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", testBinPath, filepath.Join(modRoot, "cmd/ros-fleet"))
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmpDir)
		log.Fatalf("TestMain: failed to build ros-fleet binary: %v\nOutput: %s", err, string(out))
	}

	return m.Run()
}

type CLIOptions struct {
	Args       []string
	Env        []string
	Dir        string
	BinaryPath string
	Timeout    time.Duration
	Stdin      io.Reader
}

type CLIResult struct {
	Stdout         string
	Stderr         string
	CombinedOutput string
	ExitCode       int
	Duration       time.Duration
	Err            error
}

type Harness struct {
	T          *testing.T
	TempDir    string
	BinPath    string
	Env        []string
	MockGitHub *MockGitHubServer
	Daemon     *RunningDaemon
}

func NewHarness(t *testing.T) *Harness {
	tempDir := t.TempDir()
	h := &Harness{
		T:       t,
		TempDir: tempDir,
		BinPath: testBinPath,
		Env:     SanitizedEnv(),
	}
	t.Cleanup(func() {
		if h.Daemon != nil {
			h.Daemon.Stop()
		}
		if h.MockGitHub != nil {
			h.MockGitHub.Close()
		}
	})
	return h
}

// StagedBinaryCopy creates an isolated copy of ros-fleet in TempDir for self-update tests.
func (h *Harness) StagedBinaryCopy() string {
	staged := filepath.Join(h.TempDir, "ros-fleet")
	data, err := os.ReadFile(h.BinPath)
	if err != nil {
		h.T.Fatalf("StagedBinaryCopy: read testBin: %v", err)
	}
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		h.T.Fatalf("StagedBinaryCopy: write staged: %v", err)
	}
	h.BinPath = staged
	return staged
}

// RunCLI executes the ros-fleet binary with the given options and captures output.
func (h *Harness) RunCLI(opts CLIOptions) CLIResult {
	bin := opts.BinaryPath
	if bin == "" {
		bin = h.BinPath
	}
	dir := opts.Dir
	if dir == "" {
		dir = h.TempDir
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, opts.Args...)
	cmd.Dir = dir

	// Merge environment
	envMap := make(map[string]string)
	for _, envStr := range h.Env {
		parts := strings.SplitN(envStr, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	for _, envStr := range opts.Env {
		parts := strings.SplitN(envStr, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	var mergedEnv []string
	for k, v := range envMap {
		mergedEnv = append(mergedEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = mergedEnv

	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()

	return CLIResult{
		Stdout:         stdoutStr,
		Stderr:         stderrStr,
		CombinedOutput: stdoutStr + stderrStr,
		ExitCode:       exitCode,
		Duration:       duration,
		Err:            err,
	}
}

// BuildCustomBinary builds ros-fleet with custom -ldflags for testing build metadata overrides.
func BuildCustomBinary(t *testing.T, ldflags string) string {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "ros-fleet-custom")
	args := []string{"build", "-trimpath"}
	if ldflags != "" {
		args = append(args, "-ldflags="+ldflags)
	}
	args = append(args, "-o", binPath, filepath.Join(findModuleRoot(), "cmd/ros-fleet"))

	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("BuildCustomBinary failed: %v\nOutput: %s", err, string(out))
	}
	return binPath
}

// BuildCustomBinaryWithMetadata builds ros-fleet injecting metadata into both the canonical
// internal/version package and package main for comprehensive build-time override verification.
func BuildCustomBinaryWithMetadata(t *testing.T, ver, commit, date string) string {
	var flags []string
	if ver != "" {
		flags = append(flags,
			fmt.Sprintf("-X github.com/AndreyOkh/ros-fleet/internal/version.Version=%s", ver),
			fmt.Sprintf("-X main.version=%s", ver),
		)
	}
	if commit != "" {
		flags = append(flags,
			fmt.Sprintf("-X github.com/AndreyOkh/ros-fleet/internal/version.Commit=%s", commit),
			fmt.Sprintf("-X main.commit=%s", commit),
		)
	}
	if date != "" {
		flags = append(flags,
			fmt.Sprintf("-X github.com/AndreyOkh/ros-fleet/internal/version.BuildDate=%s", date),
			fmt.Sprintf("-X main.date=%s", date),
		)
	}
	return BuildCustomBinary(t, strings.Join(flags, " "))
}
