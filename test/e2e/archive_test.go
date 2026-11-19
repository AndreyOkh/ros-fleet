package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// CreateReleaseArchive packages a binary executable into a standard tar.gz archive.
func CreateReleaseArchive(t *testing.T, binaryName string, content []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name:     binaryName,
		Mode:     0755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("CreateReleaseArchive: write header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("CreateReleaseArchive: write content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("CreateReleaseArchive: close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("CreateReleaseArchive: close gzip: %v", err)
	}
	return buf.Bytes()
}

// CreateTarGz packages arbitrary files into a tar.gz archive.
func CreateTarGz(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("CreateTarGz: write header for %s: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("CreateTarGz: write content for %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("CreateTarGz: close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("CreateTarGz: close gzip: %v", err)
	}
	return buf.Bytes()
}

// CreateSha256sums computes the sha256 checksums of provided files and formats as sha256sums.txt.
func CreateSha256sums(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	for name, data := range files {
		h := sha256.Sum256(data)
		hashStr := hex.EncodeToString(h[:])
		fmt.Fprintf(&buf, "%s  %s\n", hashStr, name)
	}
	return buf.Bytes()
}

// CreateCorruptTarGz returns corrupted/truncated gzip bytes for error path testing.
func CreateCorruptTarGz(t *testing.T) []byte {
	valid := CreateReleaseArchive(t, "ros-fleet", []byte("#!/bin/sh\necho test\n"))
	if len(valid) > 20 {
		return valid[:20] // truncated gzip stream
	}
	return []byte{0x1f, 0x8b, 0x08, 0x00, 0xde, 0xad, 0xbe, 0xef}
}

// CreateMismatchedChecksum generates a sha256sums.txt entry with an intentionally wrong hash.
func CreateMismatchedChecksum(t *testing.T, filename string) []byte {
	return []byte(fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), filename))
}

// BuildMockBinaryPayload creates an executable script mimicking ros-fleet version output.
func BuildMockBinaryPayload(t *testing.T, versionStr string) []byte {
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"-version\" ]; then\n  echo \"ros-fleet version %s (commit mock1234, built at 2026-11-18T12:00:00Z)\"\n  exit 0\nfi\necho \"ros-fleet mock binary version %s\"\nexit 0\n", versionStr, versionStr)
	return []byte(script)
}

var (
	compiledBinariesMu sync.Mutex
	compiledBinaries   = make(map[string][]byte)
)

// BuildRealBinaryPayload compiles a genuine ros-fleet binary with the specified version string.
// Results are cached in memory so compilation happens at most once per version.
func BuildRealBinaryPayload(t *testing.T, versionStr string) []byte {
	compiledBinariesMu.Lock()
	defer compiledBinariesMu.Unlock()

	if data, ok := compiledBinaries[versionStr]; ok {
		return data
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "ros-fleet")
	modRoot := findModuleRoot()
	ldflags := fmt.Sprintf("-X main.version=%s -X github.com/AndreyOkh/ros-fleet/internal/version.Version=%s", versionStr, versionStr)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", outPath, filepath.Join(modRoot, "cmd/ros-fleet"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("BuildRealBinaryPayload: failed to build ros-fleet binary for %s: %v\nOutput: %s", versionStr, err, string(out))
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("BuildRealBinaryPayload: failed to read built binary: %v", err)
	}

	compiledBinaries[versionStr] = data
	return data
}
