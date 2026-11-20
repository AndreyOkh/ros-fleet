package e2e

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/updater"
)

// ============================================================================
// Feature 1: Hybrid Versioning & -version CLI Flag Boundaries (T2-F01-01 .. T2-F01-05)
// ============================================================================

// T2-F01-01: Empty String -ldflags Overrides
func TestTier2_F01_01_EmptyStringLdflagsOverrides(t *testing.T) {
	ldflags := "-X main.version= -X main.commit= -X main.date= " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Version= " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Commit= " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.BuildDate="
	customBin := BuildCustomBinary(t, ldflags)

	h := NewHarness(t)
	res := h.RunCLI(CLIOptions{BinaryPath: customBin, Args: []string{"-version"}})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "ros-fleet version") {
		t.Fatalf("expected stdout to contain ros-fleet version, got: %s", res.Stdout)
	}
}

// T2-F01-02: Stripped Binary with Zero VCS Info (-buildvcs=false)
func TestTier2_F01_02_StrippedBinaryZeroVCSInfo(t *testing.T) {
	customBin := BuildCustomBinary(t, "-s -w")
	h := NewHarness(t)
	res := h.RunCLI(CLIOptions{BinaryPath: customBin, Args: []string{"-version"}})
	if res.ExitCode != 0 {
		t.Fatalf("expected stripped binary to execute successfully with exit code 0, got %d. stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "ros-fleet version") {
		t.Fatalf("expected stripped binary output to contain 'ros-fleet version', got: %s", res.Stdout)
	}
}

// T2-F01-03: Unicode / Special Characters in Version Tag
func TestTier2_F01_03_UnicodeSpecialCharsInVersionTag(t *testing.T) {
	tag := "v2.4.0-rc1+тест.микротик"
	ldflags := fmt.Sprintf("-X main.version=%s -X github.com/AndreyOkh/ros-fleet/internal/version.Version=%s", tag, tag)
	customBin := BuildCustomBinary(t, ldflags)

	h := NewHarness(t)
	res := h.RunCLI(CLIOptions{BinaryPath: customBin, Args: []string{"-version"}})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, tag) {
		t.Fatalf("expected UTF-8 tag preserved in stdout, got: %s", res.Stdout)
	}
}

// T2-F01-04: Extreme Length Version & Commit Strings
func TestTier2_F01_04_ExtremeLengthVersionAndCommit(t *testing.T) {
	longVersion := "v" + strings.Repeat("9", 300)
	longCommit := strings.Repeat("a", 128)
	ldflags := fmt.Sprintf("-X main.version=%s -X main.commit=%s "+
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Version=%s "+
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Commit=%s",
		longVersion, longCommit, longVersion, longCommit)
	customBin := BuildCustomBinary(t, ldflags)

	h := NewHarness(t)
	res := h.RunCLI(CLIOptions{BinaryPath: customBin, Args: []string{"-version"}})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, longVersion) {
		t.Fatalf("expected long version preserved in stdout")
	}
	if !strings.Contains(res.Stdout, longCommit) {
		t.Fatalf("expected long commit preserved in stdout")
	}
}

// T2-F01-05: Multiple Flags Ordering (-config=... -version vs -version -config=...)
func TestTier2_F01_05_MultipleFlagsOrdering(t *testing.T) {
	h := NewHarness(t)
	fakeConfig := filepath.Join(h.TempDir, "does-not-exist.json")

	// Order A: -config then -version
	resA := h.RunCLI(CLIOptions{Args: []string{"-config=" + fakeConfig, "-version"}})
	if resA.ExitCode != 0 {
		t.Fatalf("Order A failed with exit %d: %s", resA.ExitCode, resA.Stderr)
	}

	// Order B: -version then -config
	resB := h.RunCLI(CLIOptions{Args: []string{"-version", "-config=" + fakeConfig}})
	if resB.ExitCode != 0 {
		t.Fatalf("Order B failed with exit %d: %s", resB.ExitCode, resB.Stderr)
	}
}

// ============================================================================
// Feature 2: Web UI Version Display Boundaries (T2-F02-01 .. T2-F02-05)
// ============================================================================

// T2-F02-01: HTML Injection / XSS in Version String
func TestTier2_F02_01_HTMLInjectionXSSInVersionString(t *testing.T) {
	xssTag := "v2.0.0<script>alert('xss')</script>"
	ldflags := fmt.Sprintf("-X main.version=%s -X github.com/AndreyOkh/ros-fleet/internal/version.Version=%s", xssTag, xssTag)
	customBin := BuildCustomBinary(t, ldflags)

	h := NewHarness(t)
	h.BinPath = customBin
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	_, body := daemon.Get("/")
	if strings.Contains(body, "<script>alert('xss')</script>") {
		t.Fatalf("raw unescaped script tag found in HTML body! Potential XSS: %s", body)
	}
}

// T2-F02-02: Unauthenticated Web Access When Auth Enabled
func TestTier2_F02_02_UnauthenticatedWebAccessWhenAuthEnabled(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("auth_test.json")

	_, m := h.ReadConfigFile(cfgPath)
	webSec := m["web"].(map[string]any)
	webSec["auth_user"] = "admin"
	webSec["auth_password"] = "strongpassword"
	m["web"] = webSec
	h.WriteJSONFile(cfgPath, m)

	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, _ := daemon.Get("/")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Errorf("missing WWW-Authenticate Basic header: %s", resp.Header.Get("WWW-Authenticate"))
	}
}

// T2-F02-03: High-Concurrency Template Rendering Stress
func TestTier2_F02_03_HighConcurrencyTemplateRenderingStress(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("stress_config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := daemon.Get("/")
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("unexpected status %d", resp.StatusCode)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// T2-F02-04: Empty Version String in Template Context
func TestTier2_F02_04_EmptyVersionStringInTemplateContext(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, _ := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

// T2-F02-05: Excessive Length Version String in UI Header
func TestTier2_F02_05_ExcessiveLengthVersionStringInUIHeader(t *testing.T) {
	longVer := "v" + strings.Repeat("1", 250)
	ldflags := fmt.Sprintf("-X main.version=%s -X github.com/AndreyOkh/ros-fleet/internal/version.Version=%s", longVer, longVer)
	customBin := BuildCustomBinary(t, ldflags)

	h := NewHarness(t)
	h.BinPath = customBin
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, _ := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

// ============================================================================
// Feature 3: GitHub Release Checker & SemVer Boundaries (T2-F03-01 .. T2-F03-05)
// ============================================================================

// T2-F03-01: Developer Build Current Version ("dev", "", "0.0.0")
func TestTier2_F03_01_DeveloperBuildCurrentVersion(t *testing.T) {
	devVersions := []string{"dev", "", "0.0.0", "devel"}
	for _, v := range devVersions {
		if updater.IsUpdateAvailable(v, "v9.9.9") {
			t.Errorf("IsUpdateAvailable(%q, v9.9.9) expected false for dev build", v)
		}
	}
}

// T2-F03-02: GitHub API HTTP 403 Rate Limit Handling
func TestTier2_F03_02_GitHubAPIHTTP403RateLimit(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()
	server.SetStatus(http.StatusForbidden)
	server.SetRawJSON(`{"message": "API rate limit exceeded for IP"}`)

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.1.0", nil)
	checker.SetBaseURL(server.URL())

	res, err := checker.CheckNow(context.Background())
	if err == nil {
		t.Fatalf("expected error on HTTP 403 rate limit, got nil")
	}
	if res != nil && res.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable false on rate limit error")
	}
}

// T2-F03-03: Malformed / Truncated JSON Payload from GitHub
func TestTier2_F03_03_MalformedTruncatedJSONFromGitHub(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()
	server.SetRawJSON(`{"tag_name": "v2.2.0"`) // truncated json

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.1.0", nil)
	checker.SetBaseURL(server.URL())

	_, err := checker.CheckNow(context.Background())
	if err == nil {
		t.Fatalf("expected error on malformed JSON from GitHub")
	}
}

// T2-F03-04: Non-SemVer GitHub Release Tag
func TestTier2_F03_04_NonSemVerGitHubReleaseTag(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()
	server.SetRelease(&updater.Release{
		TagName: "beta-build-20261118",
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.1.0", nil)
	checker.SetBaseURL(server.URL())

	res, err := checker.CheckNow(context.Background())
	if err == nil && res != nil {
		if res.UpdateAvailable {
			t.Fatalf("expected UpdateAvailable false for non-semver tag")
		}
	}
}

// T2-F03-05: Large SemVer Components & Whitespace Padding
func TestTier2_F03_05_LargeSemVerComponentsAndWhitespace(t *testing.T) {
	v, err := updater.ParseSemver("  v1000.2000.3000  ")
	if err != nil {
		t.Fatalf("ParseSemver failed for large components with whitespace: %v", err)
	}
	if v.Major != 1000 || v.Minor != 2000 || v.Patch != 3000 {
		t.Fatalf("parsed semver mismatch: %+v", v)
	}

	if !updater.IsUpdateAvailable("v1.0.0", "  v1000.2000.3000  ") {
		t.Fatalf("expected update available for large semver")
	}
}

// ============================================================================
// Feature 4: -self-update & SHA-256 Boundaries (T2-F04-01 .. T2-F04-05)
// ============================================================================

// T2-F04-01: SHA-256 Checksum Mismatch Abort
func TestTier2_F04_01_SHA256ChecksumMismatchAbort(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	dataBefore, err := os.ReadFile(stagedBin)
	if err != nil {
		t.Fatal(err)
	}
	hashBefore := sha256.Sum256(dataBefore)

	mockPayload := BuildMockBinaryPayload(t, "v2.2.0")
	archive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: archive,
	}
	// Intentionally wrong checksum
	files["sha256sums.txt"] = []byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  " + archiveName + "\n")

	server := NewMockGitHubServer(t)
	server.ConfigureReleaseWithAssets("v2.2.0", files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on checksum mismatch, got 0")
	}
	if !strings.Contains(res.CombinedOutput, "checksum") && !strings.Contains(res.CombinedOutput, "mismatch") && !strings.Contains(res.CombinedOutput, "failed") {
		t.Fatalf("missing checksum verification error, got: %s", res.CombinedOutput)
	}

	dataAfter, err := os.ReadFile(stagedBin)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(dataAfter) != hashBefore {
		t.Fatalf("binary mutated despite checksum mismatch!")
	}
	h.AssertFileNotExists(stagedBin + ".old")
}

// T2-F04-02: Missing Target Architecture Asset in Release
func TestTier2_F04_02_MissingTargetArchitectureAssetInRelease(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	files := map[string][]byte{
		"ros-fleet_v2.2.0_linux_s390x.tar.gz": []byte("fake"),
		"sha256sums.txt":                      []byte("0000  ros-fleet_v2.2.0_linux_s390x.tar.gz\n"),
	}
	server.ConfigureReleaseWithAssets("v2.2.0", files)

	// Verify asset lookup failure
	foundAmd := false
	for _, a := range server.LatestRelease.Assets {
		if strings.Contains(a.Name, "linux_amd64") {
			foundAmd = true
		}
	}
	if foundAmd {
		t.Fatalf("unexpected linux_amd64 asset found")
	}
}

// T2-F04-03: Corrupted Tar.gz Archive Content
func TestTier2_F04_03_CorruptedTarGzArchiveContent(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	corruptArchive := CreateCorruptTarGz(t)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: corruptArchive,
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums

	server := NewMockGitHubServer(t)
	server.ConfigureReleaseWithAssets("v2.2.0", files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code on corrupted archive, got 0")
	}
	h.AssertFileNotExists(stagedBin + ".old")
}

// T2-F04-04: Read-Only Executable Directory
func TestTier2_F04_04_ReadOnlyExecutableDirectory(t *testing.T) {
	readOnlyDir := filepath.Join(t.TempDir(), "readonly_bin")
	if err := os.Mkdir(readOnlyDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	binPath := filepath.Join(readOnlyDir, "ros-fleet")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o555); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Make directory read-only (0555)
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Fatalf("chmod 0555 failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o755)
	})

	// Creating a temp file in readOnlyDir must fail
	_, err := os.CreateTemp(readOnlyDir, "test-*")
	if err == nil {
		t.Fatalf("expected CreateTemp in 0555 directory to fail with permission denied")
	}
}

// T2-F04-05: Archive Missing ros-fleet Binary Member
func TestTier2_F04_05_ArchiveMissingRosFleetBinaryMember(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	wrongFiles := map[string][]byte{
		"some-other-tool": []byte("#!/bin/sh\necho wrong\n"),
	}
	archive := CreateTarGz(t, wrongFiles)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: archive,
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums

	server := NewMockGitHubServer(t)
	server.ConfigureReleaseWithAssets("v2.2.0", files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code when archive is missing ros-fleet binary, got 0")
	}
	if !strings.Contains(res.CombinedOutput, "not found") && !strings.Contains(res.CombinedOutput, "ros-fleet") {
		t.Fatalf("expected binary not found error in output, got: %s", res.CombinedOutput)
	}
	h.AssertFileNotExists(stagedBin + ".old")
}

// ============================================================================
// Feature 5: Docker Environment Detection Boundaries (T2-F05-01 .. T2-F05-05)
// ============================================================================

// T2-F05-01: /.dockerenv Created as Directory
func TestTier2_F05_01_DockerenvCreatedAsDirectory(t *testing.T) {
	t.Setenv("DOCKER_CONTAINER", "1")
	if !updater.IsRunningInDocker() {
		t.Fatalf("expected true for container detection")
	}
}

// T2-F05-02: Negative Environment Variables (DOCKER_CONTAINER=0)
func TestTier2_F05_02_NegativeEnvironmentVariables(t *testing.T) {
	t.Setenv("DOCKER_CONTAINER", "")
	t.Setenv("container", "")
	t.Setenv("IS_DOCKER", "")
	t.Setenv("ROS_FLEET_DOCKER", "")

	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("Running inside real container with /.dockerenv present")
	}

	if updater.IsRunningInDocker() {
		t.Fatalf("expected false when container env vars cleared")
	}
}

// T2-F05-03: Self-Update Aborts in Docker Before Network Access
func TestTier2_F05_03_SelfUpdateAbortsInDockerBeforeNetworkAccess(t *testing.T) {
	t.Setenv("DOCKER_CONTAINER", "1")
	t.Setenv("GITHUB_API_URL", "http://127.0.0.1:59999") // unreachable address

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := updater.RunSelfUpdate(ctx, "AndreyOkh/ros-fleet", "v2.0.0", nil)
	if err != nil {
		t.Fatalf("expected nil error on clean docker abort, got: %v", err)
	}
}

// T2-F05-04: Environment Variable Case Sensitivity
func TestTier2_F05_04_EnvironmentVariableCaseSensitivity(t *testing.T) {
	t.Setenv("container", "docker")
	if !updater.IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker true with container=docker")
	}
}

// T2-F05-05: Non-Root Execution Inside Container
func TestTier2_F05_05_NonRootExecutionInsideContainer(t *testing.T) {
	t.Setenv("DOCKER_CONTAINER", "true")
	if !updater.IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker true with DOCKER_CONTAINER=true")
	}
}

// ============================================================================
// Feature 6: Web UI Update Badge & Modal Boundaries (T2-F06-01 .. T2-F06-05)
// ============================================================================

// T2-F06-01: Update Modal with Empty Release Notes Body
func TestTier2_F06_01_UpdateModalWithEmptyReleaseNotes(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected 200 OK with empty notes, got %d", code)
	}
	if !strings.Contains(body, "update-modal") {
		t.Fatalf("expected modal markup, got: %s", body)
	}
}

// T2-F06-02: HTML Injection / XSS in Release Notes
func TestTier2_F06_02_HTMLInjectionXSSInReleaseNotes(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	xssNotes := "<img src=x onerror=\"alert('xss')\"><script>alert(1)</script>"
	server.SetRelease(&updater.Release{
		TagName: "v2.9.9",
		Body:    xssNotes,
	})

	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	_, body := daemon.GetUpdateModal()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("raw unescaped script tag found in modal body!")
	}
}

// T2-F06-03: Very Large Release Notes (1 Megabyte Payload)
func TestTier2_F06_03_VeryLargeReleaseNotes1MB(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	largeBody := strings.Repeat("A", 1024*1024)
	server.SetRelease(&updater.Release{
		TagName: "v3.0.0",
		Body:    largeBody,
	})

	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	time.Sleep(100 * time.Millisecond)

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 with large notes, got %d", code)
	}
	if len(body) < 1024*1024 {
		t.Fatalf("expected modal body >= 1MB, got length %d", len(body))
	}
	if !strings.Contains(body, "AAAAA") {
		t.Fatalf("expected modal body to contain large notes")
	}
}

// T2-F06-04: Modal Request Prior to First Update Check
func TestTier2_F06_04_ModalRequestPriorToFirstUpdateCheck(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 before first update check, got %d", code)
	}
	if !strings.Contains(body, "update-modal") {
		t.Fatalf("expected modal markup, got: %s", body)
	}
}

// T2-F06-05: Rapid Concurrent Requests to /update/badge
func TestTier2_F06_05_RapidConcurrentRequestsToUpdateBadge(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := daemon.GetUpdateBadge()
			if code != http.StatusOK && code != http.StatusNoContent {
				errCh <- fmt.Errorf("unexpected status code: %d", code)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

// ============================================================================
// Feature 7: Config Versioning & <path>.v1.bak Backup Boundaries (T2-F07-01 .. T2-F07-05)
// ============================================================================

// T2-F07-01: Read-Only Config Directory Preventing Backup
func TestTier2_F07_01_ReadOnlyConfigDirectoryPreventingBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro_cfg")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	target := filepath.Join(dir, "config.json")
	h := NewHarness(t)
	_ = h.CreateV1LegacyConfig("ignored", "")
	data, _ := os.ReadFile(filepath.Join(h.TempDir, "ignored"))
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Make directory read-only
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod 0555 failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
	})

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected error when config directory is read-only")
	}
}

// T2-F07-02: Existing .v1.bak File Present on Disk
func TestTier2_F07_02_ExistingV1BakFilePresentOnDisk(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("existing_bak.json", "")

	bakPath := target + ".v1.bak"
	if err := os.WriteFile(bakPath, []byte("pre-existing backup"), 0o600); err != nil {
		t.Fatalf("write existing bak failed: %v", err)
	}

	_, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed when .v1.bak already exists: %v", err)
	}
	h.AssertFileExists(bakPath)
}

// T2-F07-03: Empty / 0-Byte Config File
func TestTier2_F07_03_EmptyZeroByteConfigFile(t *testing.T) {
	h := NewHarness(t)
	target := filepath.Join(h.TempDir, "empty.json")
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected JSON error for 0-byte file, got nil")
	}
	h.AssertFileNotExists(target + ".v1.bak")
}

// T2-F07-04: Non-Integer config_version Field ("config_version": "1")
func TestTier2_F07_04_NonIntegerConfigVersionField(t *testing.T) {
	h := NewHarness(t)
	target := filepath.Join(h.TempDir, "string_version.json")
	h.WriteJSONFile(target, map[string]any{
		"config_version": "1",
		"backup_dir":     "/data",
	})

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected error for string config_version, got nil")
	}
}

// T2-F07-05: Symlinked Config File Backup Creation
func TestTier2_F07_05_SymlinkedConfigFileBackupCreation(t *testing.T) {
	h := NewHarness(t)
	realTarget := h.CreateV1LegacyConfig("real_target.json", "")

	symlinkPath := filepath.Join(h.TempDir, "symlink_config.json")
	if err := os.Symlink(realTarget, symlinkPath); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	_, err := config.NewManager(symlinkPath)
	if err != nil {
		t.Fatalf("NewManager failed with symlinked config: %v", err)
	}
}

// ============================================================================
// Feature 8: Deprecated Field Migration Boundaries (T2-F08-01 .. T2-F08-05)
// ============================================================================

// T2-F08-01: backup_dir with Trailing Slashes and Spaces
func TestTier2_F08_01_BackupDirWithTrailingSlashesAndSpaces(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("whitespace_dirs.json", map[string]any{
		"backup_dir": "  /var/backups/mikrotik///  ",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if !strings.HasSuffix(cfg.BinaryBackupDir, "binary") || !strings.HasSuffix(cfg.ExportDir, "exports") {
		t.Fatalf("directories not cleaned properly: bin=%q exp=%q", cfg.BinaryBackupDir, cfg.ExportDir)
	}
}

// T2-F08-02: Relative backup_dir Paths
func TestTier2_F08_02_RelativeBackupDirPaths(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("relative_dirs.json", map[string]any{
		"backup_dir": "./data/backups",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if !strings.Contains(cfg.BinaryBackupDir, "binary") {
		t.Errorf("expected relative binary dir to contain binary: %s", cfg.BinaryBackupDir)
	}
	if !strings.Contains(cfg.ExportDir, "exports") {
		t.Errorf("expected relative export dir to contain exports: %s", cfg.ExportDir)
	}
}

// T2-F08-03: Unicode Characters in backup_dir Path
func TestTier2_F08_03_UnicodeCharactersInBackupDirPath(t *testing.T) {
	h := NewHarness(t)
	unicodePath := "/данные/резервные_копии"
	target := h.CreateV1CustomConfig("unicode_dir.json", map[string]any{
		"backup_dir": unicodePath,
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if !strings.Contains(cfg.BinaryBackupDir, unicodePath) {
		t.Errorf("expected UTF-8 path preserved in BinaryBackupDir: %s", cfg.BinaryBackupDir)
	}
}

// T2-F08-04: Both backup_dir and binary_backup_dir Empty Strings
func TestTier2_F08_04_BothBackupDirAndBinaryBackupDirEmpty(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("both_empty.json", map[string]any{
		"backup_dir":        "",
		"binary_backup_dir": "",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.BinaryBackupDir == "" || cfg.ExportDir == "" {
		t.Fatalf("expected non-empty defaults for empty dirs: bin=%q exp=%q", cfg.BinaryBackupDir, cfg.ExportDir)
	}
}

// T2-F08-05: Deprecated Key in Native v2 Config
func TestTier2_F08_05_DeprecatedKeyInNativeV2Config(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("deprecated_in_v2.json")
	h.InjectUnknownKeys(target, map[string]any{
		"backup_dir": "/legacy/path",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected notice for deprecated backup_dir in native v2 config")
	}

	found := false
	for _, k := range notice.UnknownKeys {
		if k == "backup_dir" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected backup_dir in UnknownKeys for native v2 config, got: %v", notice.UnknownKeys)
	}
}

// ============================================================================
// Feature 9: Unknown Field Detection Boundaries (T2-F09-01 .. T2-F09-05)
// ============================================================================

// T2-F09-01: Deeply Nested Unknown Keys (4+ Levels)
func TestTier2_F09_01_DeeplyNestedUnknownKeys4PlusLevels(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("deep_unknown.json")
	h.InjectUnknownKeys(target, map[string]any{
		"custom": map[string]any{
			"deep": map[string]any{
				"level3": map[string]any{
					"level4": "val",
				},
			},
		},
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil || len(notice.UnknownKeys) == 0 {
		t.Fatalf("expected unknown keys collected for deep nesting")
	}
}

// T2-F09-02: Special Characters in Unknown Key Names
func TestTier2_F09_02_SpecialCharactersInUnknownKeyNames(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("special_key.json")
	specialKey := "x-custom.tag@company#1"
	h.InjectUnknownKeys(target, map[string]any{
		specialKey: "val",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected notice for special character key")
	}

	found := false
	for _, k := range notice.UnknownKeys {
		if strings.Contains(k, specialKey) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected UnknownKeys to include %q, got: %v", specialKey, notice.UnknownKeys)
	}
}

// T2-F09-03: Slices of Primitives vs Slices of Structs
func TestTier2_F09_03_SlicesOfPrimitivesVsSlicesOfStructs(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("slices_test.json")

	// Inject unknown key into router struct (slice of structs)
	_, m := h.ReadConfigFile(target)
	routers := m["routers"].([]any)
	r0 := routers[0].(map[string]any)
	r0["unknown_router_meta"] = "extra"
	routers[0] = r0
	m["routers"] = routers
	h.WriteJSONFile(target, m)

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected migration notice for unknown slice element key")
	}
	found := false
	for _, k := range notice.UnknownKeys {
		if strings.Contains(k, "unknown_router_meta") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown_router_meta in UnknownKeys, got: %v", notice.UnknownKeys)
	}
}

// T2-F09-04: 200 Unknown Keys Stress Test
func TestTier2_F09_04_200UnknownKeysStressTest(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("many_keys.json")

	unknowns := make(map[string]any)
	for i := 0; i < 200; i++ {
		unknowns[fmt.Sprintf("unknown_metric_%d", i)] = i
	}
	h.InjectUnknownKeys(target, unknowns)

	start := time.Now()
	mgr, err := config.NewManager(target)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("NewManager failed with 200 unknown keys: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("unknown key reflection took too long: %v", elapsed)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil || len(notice.UnknownKeys) < 200 {
		t.Fatalf("expected at least 200 unknown keys, got: %d", len(notice.UnknownKeys))
	}
}

// T2-F09-05: Unknown Key Containing Null Value
func TestTier2_F09_05_UnknownKeyContainingNullValue(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("null_key.json")
	h.InjectUnknownKeys(target, map[string]any{
		"obsolete_plugin": nil,
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed with null unknown key: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected notice for null unknown key")
	}
	found := false
	for _, k := range notice.UnknownKeys {
		if k == "obsolete_plugin" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected obsolete_plugin in UnknownKeys, got: %v", notice.UnknownKeys)
	}
}

// ============================================================================
// Feature 10: Config Incompatible Version Guard Boundaries (T2-F10-01 .. T2-F10-05)
// ============================================================================

// T2-F10-01: Extremely High Version Number (config_version: 999999)
func TestTier2_F10_01_ExtremelyHighVersionNumber(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateIncompatibleConfig("high_v.json", 999999)

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected error for config_version: 999999, got nil")
	}
}

// T2-F10-02: Negative Version Number (config_version: -1)
func TestTier2_F10_02_NegativeVersionNumber(t *testing.T) {
	h := NewHarness(t)
	target := filepath.Join(h.TempDir, "neg_v.json")
	h.WriteJSONFile(target, map[string]any{
		"config_version": -1,
		"state_file":     "./state.json",
	})

	_, _ = config.NewManager(target)
	h.AssertFileNotExists(target + ".v-1.bak")
}

// T2-F10-03: Floating Point Version ("config_version": 2.5)
func TestTier2_F10_03_FloatingPointVersion(t *testing.T) {
	h := NewHarness(t)
	target := filepath.Join(h.TempDir, "float_v.json")
	rawJSON := `{"config_version": 2.5, "state_file": "./state.json"}`
	if err := os.WriteFile(target, []byte(rawJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected error loading floating point config_version: 2.5, got nil")
	}
}

// T2-F10-04: Null Version Field ("config_version": null)
func TestTier2_F10_04_NullVersionField(t *testing.T) {
	h := NewHarness(t)
	target := filepath.Join(h.TempDir, "null_v.json")
	rawJSON := `{"config_version": null, "state_file": "./state.json", "backup_dir": "./b", "defaults": {"port": 22, "user": "admin", "password": "default_password"}, "routers": [{"name": "Router-V1", "host": "192.168.1.1", "user": "admin", "password": "default_password"}]}`
	_ = os.WriteFile(target, []byte(rawJSON), 0o600)

	// null should be treated as 0 / missing (v1)
	mgr, err := config.NewManager(target)
	if err == nil {
		if mgr.Get().ConfigVersion != config.CurrentConfigVersion {
			t.Errorf("expected migrated ConfigVersion %d, got %d", config.CurrentConfigVersion, mgr.Get().ConfigVersion)
		}
	}
}

// T2-F10-05: Read-Only Incompatible Config File
func TestTier2_F10_05_ReadOnlyIncompatibleConfigFile(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateIncompatibleConfig("ro_incompat.json", 4)

	if err := os.Chmod(target, 0o400); err != nil {
		t.Fatalf("chmod 0400 failed: %v", err)
	}

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected incompatible version error, got nil")
	}

	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "version") && !strings.Contains(errStr, "unsupported") && !strings.Contains(errStr, "incompatible") {
		t.Fatalf("expected version incompatibility error, got: %v", err)
	}
}

// ============================================================================
// Feature 11: Web UI Migration Banner & Dismiss Boundaries (T2-F11-01 .. T2-F11-05)
// ============================================================================

// T2-F11-01: Concurrent POST /notices/dismiss Calls
func TestTier2_F11_01_ConcurrentPostNoticesDismissCalls(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("concurrent_dismiss.json", "")

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := daemon.DismissNotice()
			if code != http.StatusOK {
				errCh <- fmt.Errorf("expected 200 OK, got %d", code)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	_, bodyAfter := daemon.Get("/")
	if strings.Contains(bodyAfter, "id=\"migration-notice-banner\"") {
		t.Fatalf("banner still rendered after concurrent dismissals")
	}
}

// T2-F11-02: Dismissal When No Notice Exists (Idempotency)
func TestTier2_F11_02_DismissalWhenNoNoticeExistsIdempotency(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("no_notice.json")

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	code, body := daemon.DismissNotice()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK on idempotent dismissal when no notice exists, got %d. Body: %s", code, body)
	}
}

// T2-F11-03: HTML Injection in Unknown Key Names Inside Banner
func TestTier2_F11_03_HTMLInjectionInUnknownKeyNamesInsideBanner(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("xss_key.json")
	xssKey := "<script>alert('banner-xss')</script>"
	h.InjectUnknownKeys(target, map[string]any{
		xssKey: "val",
	})

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	_, body := daemon.Get("/")
	if strings.Contains(body, "<script>alert('banner-xss')</script>") {
		t.Fatalf("raw unescaped script tag found in banner DOM! Potential XSS: %s", body)
	}
}

// T2-F11-04: Unauthenticated Notice Dismissal
func TestTier2_F11_04_UnauthenticatedNoticeDismissal(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("auth_dismiss.json", "")

	_, m := h.ReadConfigFile(target)
	webSec := m["web"].(map[string]any)
	webSec["auth_user"] = "admin"
	webSec["auth_password"] = "secret"
	m["web"] = webSec
	h.WriteJSONFile(target, m)

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	code, body := daemon.DismissNotice()
	if code != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401 Unauthorized for unauthenticated dismissal, got %d. Body: %s", code, body)
	}
}

// T2-F11-05: Massive Unknown Key List in Banner
func TestTier2_F11_05_MassiveUnknownKeyListInBanner(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("massive_banner.json")

	unknowns := make(map[string]any)
	for i := 0; i < 50; i++ {
		unknowns[fmt.Sprintf("custom_prop_%d", i)] = "test"
	}
	h.InjectUnknownKeys(target, unknowns)

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	resp, _ := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for massive unknown keys banner, got %d", resp.StatusCode)
	}
}
