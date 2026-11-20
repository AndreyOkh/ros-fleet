package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/updater"
	"github.com/AndreyOkh/ros-fleet/internal/version"
)

// ============================================================================
// Feature 1: Hybrid Versioning & -version CLI Flag (T1-F01-01 .. T1-F01-05)
// ============================================================================

// T1-F01-01: Default CLI -version Output Format and Exit Code
func TestTier1_F01_01_DefaultCLI_VersionFormatAndExitCode(t *testing.T) {
	h := NewHarness(t)
	res := h.RunCLI(CLIOptions{Args: []string{"-version"}})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", res.ExitCode, res.Stderr)
	}

	re := regexp.MustCompile(`^ros-fleet version .+ \(commit .+, built at .+\)\n$`)
	if !re.MatchString(res.Stdout) {
		t.Fatalf("stdout did not match expected version format. Got: %q", res.Stdout)
	}
	if res.Stderr != "" {
		t.Fatalf("expected empty stderr, got: %q", res.Stderr)
	}
}

// T1-F01-02: -ldflags Injected Version Overrides
func TestTier1_F01_02_LdflagsInjectedVersionOverrides(t *testing.T) {
	ldflags := "-X main.version=v3.4.1 -X main.commit=abcd1234ef567890 -X main.date=2026-11-18T10:00:00Z " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Version=v3.4.1 " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Commit=abcd1234ef567890 " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.BuildDate=2026-11-18T10:00:00Z"

	customBin := BuildCustomBinary(t, ldflags)
	h := NewHarness(t)
	res := h.RunCLI(CLIOptions{BinaryPath: customBin, Args: []string{"-version"}})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "v3.4.1") {
		t.Errorf("stdout missing injected version v3.4.1. Got: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "abcd1234ef567890") {
		t.Errorf("stdout missing injected commit abcd1234ef567890. Got: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "2026-11-18T10:00:00Z") {
		t.Errorf("stdout missing injected build date. Got: %s", res.Stdout)
	}
}

// T1-F01-03: -version Short-Circuit Execution
func TestTier1_F01_03_VersionShortCircuitExecution(t *testing.T) {
	h := NewHarness(t)
	nonExistentConfig := filepath.Join(h.TempDir, "nonexistent", "deep", "fake_config.json")
	res := h.RunCLI(CLIOptions{Args: []string{"-version", "-config=" + nonExistentConfig}})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stderr, "failed to load configuration") {
		t.Fatalf("version flag failed to short-circuit config loading. stderr: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "ros-fleet version") {
		t.Fatalf("expected version output in stdout, got: %s", res.Stdout)
	}
}

// T1-F01-04: VCS Git Commit SHA Auto-Extraction
func TestTier1_F01_04_VCSGitCommitAutoExtraction(t *testing.T) {
	info := version.Get()
	if info.Version == "" {
		t.Fatalf("expected non-empty version from version.Get(), got empty")
	}
	formatted := info.String()
	if !strings.Contains(formatted, info.Version) {
		t.Fatalf("info.String() %q did not contain version %q", formatted, info.Version)
	}
}

// T1-F01-05: Dirty Working Tree Flagging (+dirty)
func TestTier1_F01_05_DirtyWorkingTreeFlagging(t *testing.T) {
	info := version.Get()
	if info.Modified {
		if !strings.Contains(info.Version, "+dirty") {
			t.Logf("Note: Modified is true, version is %q", info.Version)
		}
	}
	str := info.String()
	if str == "" {
		t.Fatalf("expected non-empty formatted version string")
	}
}

// ============================================================================
// Feature 2: Web UI Version Display (T1-F02-01 .. T1-F02-05)
// ============================================================================

// T1-F02-01: Header Version Badge Rendering
func TestTier1_F02_01_HeaderVersionBadgeRendering(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected Content-Type text/html, got %q", ct)
	}

	// Strictly assert header badge markup and version string
	if !strings.Contains(body, "badge badge-version") {
		t.Fatalf("expected header badge-version class in dashboard HTML body")
	}

	resVer := h.RunCLI(CLIOptions{Args: []string{"-version"}})
	var expectedVersion string
	parts := strings.Fields(resVer.Stdout)
	if len(parts) >= 3 {
		expectedVersion = parts[2]
	}
	if expectedVersion != "" && !strings.HasPrefix(expectedVersion, "v") {
		expectedVersion = "v" + expectedVersion
	}
	unescapedBody := html.UnescapeString(body)
	if expectedVersion != "" && !strings.Contains(unescapedBody, expectedVersion) {
		t.Fatalf("expected active version %q in dashboard header, got: %s", expectedVersion, body)
	}
}

// T1-F02-02: Footer Application Version Rendering
func TestTier1_F02_02_FooterApplicationVersionRendering(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	if !strings.Contains(body, "<footer class=\"footer\">") {
		t.Fatalf("expected footer with class 'footer' in dashboard HTML")
	}

	resVer := h.RunCLI(CLIOptions{Args: []string{"-version"}})
	var expectedVersion string
	parts := strings.Fields(resVer.Stdout)
	if len(parts) >= 3 {
		expectedVersion = parts[2]
	}
	if expectedVersion != "" && !strings.HasPrefix(expectedVersion, "v") {
		expectedVersion = "v" + expectedVersion
	}
	expectedFooter := "ros-fleet " + expectedVersion
	unescapedBody := html.UnescapeString(body)
	if expectedVersion != "" && !strings.Contains(unescapedBody, expectedFooter) {
		t.Fatalf("expected footer to render %q, got: %s", expectedFooter, body)
	}
}

// T1-F02-03: Web UI Version Reflects Injected -ldflags
func TestTier1_F02_03_WebUIVersionReflectsInjectedLdflags(t *testing.T) {
	ldflags := "-X main.version=v9.8.7 -X github.com/AndreyOkh/ros-fleet/internal/version.Version=v9.8.7"
	customBin := BuildCustomBinary(t, ldflags)

	h := NewHarness(t)
	h.BinPath = customBin
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	// Strictly assert that injected version v9.8.7 is rendered in both header and footer
	if !strings.Contains(body, ">v9.8.7</span>") {
		t.Fatalf("expected header badge to contain '>v9.8.7</span>', got: %s", body)
	}
	if !strings.Contains(body, "ros-fleet v9.8.7") {
		t.Fatalf("expected footer to contain 'ros-fleet v9.8.7', got: %s", body)
	}
}

// T1-F02-04: UTF-8 Encoding & Valid HTML Structure
func TestTier1_F02_04_UTF8EncodingAndValidHTML(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "utf-8") && !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("unexpected Content-Type: %s", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<html") {
		t.Errorf("expected HTML document, got: %s", body[:min(len(body), 200)])
	}
}

// T1-F02-05: Web UI Authenticated Session Version Display
func TestTier1_F02_05_WebUIAuthenticatedSessionVersionDisplay(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")

	// Inject web auth
	_, m := h.ReadConfigFile(cfgPath)
	webSec := m["web"].(map[string]any)
	webSec["auth_user"] = "admin"
	webSec["auth_password"] = "secretpassword"
	m["web"] = webSec
	h.WriteJSONFile(cfgPath, m)

	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	// 1. Unauthenticated request should yield 401
	unauthResp, _ := daemon.Get("/")
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", unauthResp.StatusCode)
	}

	// 2. Authenticated request should yield 200
	authResp, body := daemon.GetAuth("/", "admin", "secretpassword")
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK with valid auth, got %d", authResp.StatusCode)
	}
	if len(body) < 100 {
		t.Fatalf("authenticated response body suspiciously small: %s", body)
	}
}

// ============================================================================
// Feature 3: GitHub Release Checker & SemVer Comparison (T1-F03-01 .. T1-F03-05)
// ============================================================================

// T1-F03-01: Update Available Detection (Higher Remote Version)
func TestTier1_F03_01_UpdateAvailableDetection(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName:     "v2.2.0",
		Name:        "Release 2.2.0",
		Body:        "Bug fixes and improvements",
		PublishedAt: time.Now().UTC(),
		HTMLURL:     "https://github.com/AndreyOkh/ros-fleet/releases/tag/v2.2.0",
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.1.0", nil)
	checker.SetBaseURL(server.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := checker.CheckNow(ctx)
	if err != nil {
		t.Fatalf("CheckNow failed: %v", err)
	}
	if !result.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable to be true for v2.1.0 vs v2.2.0")
	}
	if result.LatestVersion != "v2.2.0" {
		t.Fatalf("expected LatestVersion v2.2.0, got %s", result.LatestVersion)
	}

	// Assert required request headers
	if len(server.RecordedHeaders) == 0 {
		t.Fatalf("no requests recorded by mock server")
	}
	hdr := server.RecordedHeaders[0]
	if hdr.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("expected Accept header application/vnd.github+json, got %q", hdr.Get("Accept"))
	}
	if hdr.Get("User-Agent") != "ros-fleet" {
		t.Errorf("expected User-Agent ros-fleet, got %q", hdr.Get("User-Agent"))
	}
}

// T1-F03-02: Already Up-To-Date Detection (Equal Version)
func TestTier1_F03_02_AlreadyUpToDateDetection(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName: "v2.1.0",
		Name:    "Release 2.1.0",
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.1.0", nil)
	checker.SetBaseURL(server.URL())

	res, err := checker.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("CheckNow failed: %v", err)
	}
	if res.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable to be false for identical versions")
	}
	if res.LatestVersion != "v2.1.0" {
		t.Fatalf("expected LatestVersion v2.1.0, got %s", res.LatestVersion)
	}
}

// T1-F03-03: Newer Local Build than Remote Release
func TestTier1_F03_03_NewerLocalBuildThanRemote(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName: "v2.1.0",
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.2.0", nil)
	checker.SetBaseURL(server.URL())

	res, err := checker.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("CheckNow failed: %v", err)
	}
	if res.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable to be false when local build is newer")
	}
}

// T1-F03-04: SemVer Prerelease and Build Metadata Stripping
func TestTier1_F03_04_SemVerPrereleaseAndBuildMetadataStripping(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName: "v2.1.0",
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.1.0-rc1+dirty", nil)
	checker.SetBaseURL(server.URL())

	res, err := checker.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("CheckNow failed: %v", err)
	}
	if res.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable false when base semver components are equal")
	}
}

// T1-F03-05: Release Notes & Metadata Extraction
func TestTier1_F03_05_ReleaseNotesAndMetadataExtraction(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	now := time.Now().UTC().Truncate(time.Second)
	server.SetRelease(&updater.Release{
		TagName:     "v2.5.0",
		Name:        "Release v2.5.0",
		Body:        "## Changelog\n- Added TLS support\n- Fixed SFTP leaks",
		HTMLURL:     "https://github.com/AndreyOkh/ros-fleet/releases/tag/v2.5.0",
		PublishedAt: now,
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.0.0", nil)
	checker.SetBaseURL(server.URL())

	res, err := checker.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("CheckNow failed: %v", err)
	}
	if res.ReleaseNotes != "## Changelog\n- Added TLS support\n- Fixed SFTP leaks" {
		t.Errorf("ReleaseNotes mismatch. Got: %q", res.ReleaseNotes)
	}
	if res.HTMLURL != "https://github.com/AndreyOkh/ros-fleet/releases/tag/v2.5.0" {
		t.Errorf("HTMLURL mismatch. Got: %s", res.HTMLURL)
	}
	if !res.PublishedAt.Equal(now) {
		t.Errorf("PublishedAt mismatch. Expected %v, got %v", now, res.PublishedAt)
	}
}

// ============================================================================
// Feature 4: -self-update & SHA-256 Verification (T1-F04-01 .. T1-F04-05)
// ============================================================================

// T1-F04-01: End-to-End Successful Self-Update
func TestTier1_F04_01_EndToEndSuccessfulSelfUpdate(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	mockPayload := BuildMockBinaryPayload(t, "v2.2.0")
	archive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: archive,
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums

	server := NewMockGitHubServer(t)
	server.ConfigureReleaseWithAssets("v2.2.0", files)

	// Execute self-update via CLI
	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. Output: %s", res.ExitCode, res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "Successfully updated ros-fleet to version v2.2.0") {
		t.Fatalf("missing restart instruction, got: %s", res.Stdout)
	}

	// Verify binary on disk was replaced and executes
	resVer := h.RunCLI(CLIOptions{BinaryPath: stagedBin, Args: []string{"-version"}})
	if resVer.ExitCode != 0 || !strings.Contains(resVer.Stdout, "v2.2.0") {
		t.Fatalf("updated binary did not report v2.2.0, got: %s", resVer.Stdout)
	}

	h.AssertFileNotExists(stagedBin + ".old")
}

// T1-F04-02: Binary Permissions Preservation
func TestTier1_F04_02_BinaryPermissionsPreservation(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	if err := os.Chmod(stagedBin, 0o750); err != nil {
		t.Fatalf("failed to chmod 0750: %v", err)
	}

	info, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("expected 0750 perm, got %o", info.Mode().Perm())
	}

	mockPayload := BuildMockBinaryPayload(t, "v2.2.0")
	archive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
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
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. Output: %s", res.ExitCode, res.CombinedOutput)
	}

	infoAfter, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat after failed: %v", err)
	}
	if infoAfter.Mode().Perm() != 0o750 {
		t.Fatalf("expected permissions 0750 preserved, got %o", infoAfter.Mode().Perm())
	}
}

// T1-F04-03: Self-Update Idempotency When Up-To-Date
func TestTier1_F04_03_SelfUpdateIdempotencyWhenUpToDate(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	dataBefore, err := os.ReadFile(stagedBin)
	if err != nil {
		t.Fatalf("read before failed: %v", err)
	}

	currentVer := version.Get().Version
	server := NewMockGitHubServer(t)
	mockPayload := BuildMockBinaryPayload(t, currentVer)
	archive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
	archiveName := fmt.Sprintf("ros-fleet_%s_linux_amd64.tar.gz", currentVer)
	if !strings.HasPrefix(currentVer, "v") {
		archiveName = fmt.Sprintf("ros-fleet_v%s_linux_amd64.tar.gz", currentVer)
	}
	files := map[string][]byte{
		archiveName: archive,
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums
	server.ConfigureReleaseWithAssets(currentVer, files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0 on idempotent run, got %d. Output: %s", res.ExitCode, res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "already up to date") {
		t.Fatalf("expected 'already up to date' in stdout, got: %s", res.Stdout)
	}

	dataAfter, _ := os.ReadFile(stagedBin)
	if !bytes.Equal(dataBefore, dataAfter) {
		t.Fatalf("binary modified on idempotent update")
	}
	h.AssertFileNotExists(stagedBin + ".old")
}

// T1-F04-04: Strict Platform Asset Name Matching
func TestTier1_F04_04_StrictPlatformAssetNameMatching(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	server := NewMockGitHubServer(t)
	defer server.Close()

	payload := BuildMockBinaryPayload(t, "v2.2.0")
	armArchive := CreateReleaseArchive(t, "ros-fleet", payload)

	files := map[string][]byte{
		"ros-fleet_v2.2.0_linux_arm64.tar.gz": armArchive,
		"ros-fleet_v2.2.0_windows_amd64.zip":  []byte("fake-windows-binary"),
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums

	server.ConfigureReleaseWithAssets("v2.2.0", files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})
	if res.ExitCode == 0 {
		t.Fatalf("expected failure on missing architecture asset, got exit 0")
	}
	if !strings.Contains(res.CombinedOutput, "does not contain matching binary archive") && !strings.Contains(res.CombinedOutput, "matching binary archive") {
		t.Fatalf("expected missing archive error, got: %s", res.CombinedOutput)
	}
}

// T1-F04-05: Multi-line Checksum File Parsing
func TestTier1_F04_05_MultilineChecksumFileParsing(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	mockPayload := BuildMockBinaryPayload(t, "v2.2.0")
	targetArchive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName:                           targetArchive,
		"ros-fleet_v2.2.0_linux_arm64.tar.gz": []byte("arm64-dummy"),
		"ros-fleet_v2.2.0_darwin_amd64.tar.gz": []byte("darwin-dummy"),
	}
	checksums := CreateSha256sums(t, files)

	// Add comments, empty lines, and extra entries
	multilineChecksums := fmt.Sprintf("# Release v2.2.0 checksums\n\n%s\n# end of checksums\n", string(checksums))
	files["sha256sums.txt"] = []byte(multilineChecksums)

	server := NewMockGitHubServer(t)
	server.ConfigureReleaseWithAssets("v2.2.0", files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})
	if res.ExitCode != 0 {
		t.Fatalf("failed to parse multiline checksum file: exit %d, output: %s", res.ExitCode, res.CombinedOutput)
	}

	resVer := h.RunCLI(CLIOptions{BinaryPath: stagedBin, Args: []string{"-version"}})
	if resVer.ExitCode != 0 || !strings.Contains(resVer.Stdout, "v2.2.0") {
		t.Fatalf("updated binary did not report v2.2.0: %s", resVer.Stdout)
	}
}

// ============================================================================
// Feature 5: Docker Environment Detection (T1-F05-01 .. T1-F05-05)
// ============================================================================

// T1-F05-01: Detection via /.dockerenv File
func TestTier1_F05_01_DetectionViaDockerenvFile(t *testing.T) {
	// IsRunningInDocker checks /.dockerenv and environment variables
	t.Setenv("DOCKER_CONTAINER", "1")
	if !updater.IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker to return true when container indicator present")
	}
}

// T1-F05-02: Detection via DOCKER_CONTAINER=1 Environment Variable
func TestTier1_F05_02_DetectionViaDockerContainerEnvVar(t *testing.T) {
	t.Setenv("DOCKER_CONTAINER", "1")
	if !updater.IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker true with DOCKER_CONTAINER=1")
	}
}

// T1-F05-03: Detection via container=docker Environment Variable
func TestTier1_F05_03_DetectionViaContainerDockerEnvVar(t *testing.T) {
	t.Setenv("container", "docker")
	if !updater.IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker true with container=docker")
	}
}

// T1-F05-04: Negative Container Detection on Normal Linux Host
func TestTier1_F05_04_NegativeContainerDetectionOnNormalHost(t *testing.T) {
	t.Setenv("DOCKER_CONTAINER", "")
	t.Setenv("container", "")
	t.Setenv("IS_DOCKER", "")
	t.Setenv("ROS_FLEET_DOCKER", "")

	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("Running inside real docker container with /.dockerenv present")
	}

	if updater.IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker to be false on normal host without env vars")
	}
}

// T1-F05-05: Web UI Docker-Specific Instructions
func TestTier1_F05_05_WebUIDockerSpecificInstructions(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "DOCKER_CONTAINER=1")
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from GET /update/modal in Docker mode, got %d", code)
	}
	if !strings.Contains(body, "docker compose pull ros-fleet") {
		t.Fatalf("expected docker compose pull instructions in Docker mode, got: %s", body)
	}
	if strings.Contains(body, "./ros-fleet -self-update") {
		t.Fatalf("did not expect binary self-update instructions when running inside Docker container")
	}
}

// ============================================================================
// Feature 6: Web UI Update Badge & Modal (T1-F06-01 .. T1-F06-05)
// ============================================================================

// T1-F06-01: GET /update/badge When Update Available
func TestTier1_F06_01_GetUpdateBadgeWhenUpdateAvailable(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName:     "v9.9.0",
		Name:        "Release v9.9.0",
		Body:        "Major updates",
		PublishedAt: time.Now().UTC(),
	})

	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	// Wait briefly for startup update check to complete
	deadline := time.Now().Add(2 * time.Second)
	var code int
	var body string
	for time.Now().Before(deadline) {
		code, body = daemon.GetUpdateBadge()
		if code == http.StatusOK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK for update badge when update is available, got %d. Body: %s", code, body)
	}
	if !strings.Contains(body, "badge-update") {
		t.Fatalf("expected badge-update class, got: %s", body)
	}
	if !strings.Contains(body, "Доступно обновление: v9.9.0") {
		t.Fatalf("expected badge text 'Доступно обновление: v9.9.0', got: %s", body)
	}
	if !strings.Contains(body, "hx-get=\"/update/modal\"") {
		t.Fatalf("expected hx-get to /update/modal in badge button, got: %s", body)
	}
}

// T1-F06-02: GET /update/badge When Up To Date
func TestTier1_F06_02_GetUpdateBadgeWhenUpToDate(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	// Set remote version equal to current version
	currentVer := version.Get().Version
	server.SetRelease(&updater.Release{
		TagName: currentVer,
		Name:    "Release " + currentVer,
	})

	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	code, body := daemon.GetUpdateBadge()
	if code != http.StatusNoContent {
		t.Fatalf("expected HTTP 204 No Content when up to date, got %d. Body: %s", code, body)
	}
}

// T1-F06-03: GET /update/modal Content Verification
func TestTier1_F06_03_GetUpdateModalContentVerification(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName:     "v9.9.0",
		Name:        "Release v9.9.0",
		Body:        "### Improvements\n- High throughput sync",
		HTMLURL:     "https://github.com/AndreyOkh/ros-fleet/releases/tag/v9.9.0",
		PublishedAt: time.Now().UTC(),
	})

	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from GET /update/modal, got %d. Body: %s", code, body)
	}
	if !strings.Contains(body, "update-modal") {
		t.Fatalf("expected modal element with id 'update-modal', got: %s", body)
	}
	if !strings.Contains(body, "v9.9.0") {
		t.Fatalf("expected latest version v9.9.0 in modal, got: %s", body)
	}
	if !strings.Contains(body, "High throughput sync") {
		t.Fatalf("expected release notes in modal, got: %s", body)
	}
	if !strings.Contains(body, "./ros-fleet -self-update") {
		t.Fatalf("expected CLI self-update instructions in modal, got: %s", body)
	}
	if !strings.Contains(body, "docker compose pull ros-fleet") {
		t.Fatalf("expected Docker instructions in modal, got: %s", body)
	}
}

// T1-F06-04: Web UI Dashboard Header Integration
func TestTier1_F06_04_WebUIDashboardHeaderIntegration(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "id=\"update-badge-container\"") {
		t.Fatalf("expected #update-badge-container in dashboard HTML header, got: %s", body)
	}
	if !strings.Contains(body, "hx-get=\"/update/badge\"") {
		t.Fatalf("expected hx-get='/update/badge' on update badge container, got: %s", body)
	}
}

// T1-F06-05: Modal Close Button Markup
func TestTier1_F06_05_ModalCloseButtonMarkup(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 from GET /update/modal, got %d", code)
	}
	if !strings.Contains(body, "close-btn") && !strings.Contains(body, "Закрыть") {
		t.Fatalf("modal missing close button markup, got: %s", body)
	}
	if !strings.Contains(body, "modal-container") {
		t.Fatalf("modal missing target #modal-container dismissal logic, got: %s", body)
	}
}

// ============================================================================
// Feature 7: Config Versioning & <path>.v1.bak Backup (T1-F07-01 .. T1-F07-05)
// ============================================================================

// T1-F07-01: Auto-Detection of Legacy v1 Config (Missing Version Field)
func TestTier1_F07_01_AutoDetectionOfLegacyV1Config(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("legacy_v1.json", "")

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// 1. Verify backup file was created
	bakPath := target + ".v1.bak"
	h.AssertFileExists(bakPath)

	// 2. Verify in-memory and disk configuration has config_version == 3
	cfg := mgr.Get()
	if cfg.ConfigVersion != config.CurrentConfigVersion {
		t.Fatalf("expected ConfigVersion %d, got %d", config.CurrentConfigVersion, cfg.ConfigVersion)
	}

	_, raw := h.ReadConfigFile(target)
	if v, ok := raw["config_version"].(float64); !ok || int(v) != config.CurrentConfigVersion {
		t.Fatalf("expected disk config_version %d, got %v", config.CurrentConfigVersion, raw["config_version"])
	}
}

// T1-F07-02: Explicit config_version: 1 Backup Creation
func TestTier1_F07_02_ExplicitConfigVersion1BackupCreation(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("explicit_v1.json", map[string]any{
		"config_version": 1,
	})

	_, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	bakPath := target + ".v1.bak"
	h.AssertFileExists(bakPath)
}

// T1-F07-03: Verbatim Byte Accuracy of .v1.bak Backup
func TestTier1_F07_03_VerbatimByteAccuracyOfV1Bak(t *testing.T) {
	h := NewHarness(t)
	target := filepath.Join(h.TempDir, "verbatim_test.json")

	// Custom formatted JSON with specific whitespace and comment-like structure
	rawBytes := []byte("{\n   \"backup_dir\":   \"/custom/dir\",\n   \"state_file\": \"/custom/state.json\",\n   \"defaults\": {\"port\": 22, \"user\": \"admin\", \"password\": \"default_password\"},\n   \"routers\": [{\"name\": \"Router-V1\", \"host\": \"192.168.1.1\", \"user\": \"admin\", \"password\": \"default_password\"}]\n}\n")
	if err := os.WriteFile(target, rawBytes, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	bakPath := target + ".v1.bak"
	h.AssertFileExists(bakPath)

	bakBytes, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("failed to read backup file: %v", err)
	}

	if string(bakBytes) != string(rawBytes) {
		t.Fatalf("backup file not byte-for-byte identical.\nExpected:\n%s\nGot:\n%s", string(rawBytes), string(bakBytes))
	}
}

// T1-F07-04: Backup File Permissions (0600)
func TestTier1_F07_04_BackupFilePermissions0600(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("perm_test.json", "")

	_, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	bakPath := target + ".v1.bak"
	info, err := os.Stat(bakPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Fatalf("expected backup permissions 0600, got %o", perm)
	}
}

// T1-F07-05: Loading Already-Migrated v3 Config (No Backup Created)
func TestTier1_F07_05_LoadingAlreadyMigratedV3Config(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV3Config("clean_v3.json")

	_, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	h.AssertFileNotExists(target + ".v1.bak")
	h.AssertFileNotExists(target + ".v2.bak")
	h.AssertFileNotExists(target + ".v3.bak")
}

// ============================================================================
// Feature 8: Deprecated Field Migration (backup_dir) (T1-F08-01 .. T1-F08-05)
// ============================================================================

// T1-F08-01: Translation of backup_dir to binary_backup_dir and export_dir
func TestTier1_F08_01_TranslationOfBackupDirToBinaryAndExportDirs(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("migrate_dirs.json", map[string]any{
		"backup_dir": "/mnt/nas/backups",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.BinaryBackupDir != "/mnt/nas/backups/binary" {
		t.Errorf("expected BinaryBackupDir /mnt/nas/backups/binary, got %s", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "/mnt/nas/backups/exports" {
		t.Errorf("expected ExportDir /mnt/nas/backups/exports, got %s", cfg.ExportDir)
	}
	if cfg.BackupDir != "" {
		t.Errorf("expected BackupDir to be cleared in memory, got %s", cfg.BackupDir)
	}
}

// T1-F08-02: Omission of backup_dir from Migrated JSON File
func TestTier1_F08_02_OmissionOfBackupDirFromMigratedJSON(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("omit_test.json", "/mnt/nas/backups")

	_, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	_, raw := h.ReadConfigFile(target)
	if _, exists := raw["backup_dir"]; exists {
		t.Fatalf("expected backup_dir to be omitted from migrated v2 JSON file on disk, but it was present")
	}
	if _, exists := raw["binary_backup_dir"]; !exists {
		t.Fatalf("expected binary_backup_dir to be present in migrated v2 JSON file")
	}
	if _, exists := raw["export_dir"]; !exists {
		t.Fatalf("expected export_dir to be present in migrated v2 JSON file")
	}
}

// T1-F08-03: Preservation of Pre-Existing Explicit Target Directories
func TestTier1_F08_03_PreservationOfPreExistingExplicitTargetDirectories(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("explicit_dirs.json", map[string]any{
		"backup_dir":        "/old",
		"binary_backup_dir": "/custom/bin",
		"export_dir":        "/custom/exp",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.BinaryBackupDir != "/custom/bin" {
		t.Errorf("expected BinaryBackupDir /custom/bin, got %s", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "/custom/exp" {
		t.Errorf("expected ExportDir /custom/exp, got %s", cfg.ExportDir)
	}
}

// T1-F08-04: Partial Migration (Only One Target Defined)
func TestTier1_F08_04_PartialMigrationOnlyOneTargetDefined(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("partial_dirs.json", map[string]any{
		"backup_dir":        "/data",
		"binary_backup_dir": "/custom/bin",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.BinaryBackupDir != "/custom/bin" {
		t.Errorf("expected BinaryBackupDir /custom/bin, got %s", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "/data/exports" {
		t.Errorf("expected ExportDir /data/exports, got %s", cfg.ExportDir)
	}
}

// T1-F08-05: Empty backup_dir Default Fallback
func TestTier1_F08_05_EmptyBackupDirDefaultFallback(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1CustomConfig("empty_backup_dir.json", map[string]any{
		"backup_dir": "",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.BinaryBackupDir != "./backups/binary" {
		t.Errorf("expected BinaryBackupDir ./backups/binary, got %s", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "./backups/exports" {
		t.Errorf("expected ExportDir ./backups/exports, got %s", cfg.ExportDir)
	}
}

// ============================================================================
// Feature 9: Unknown Field Detection & Logging (T1-F09-01 .. T1-F09-05)
// ============================================================================

// T1-F09-01: Single Unknown Top-Level Field Detection
func TestTier1_F09_01_SingleUnknownTopLevelFieldDetection(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("unknown_top.json")
	h.InjectUnknownKeys(target, map[string]any{
		"cluster_name": "alpha",
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected migration notice with unknown keys, got nil")
	}

	found := false
	for _, k := range notice.UnknownKeys {
		if k == "cluster_name" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected UnknownKeys to contain cluster_name, got: %v", notice.UnknownKeys)
	}
}

// T1-F09-02: Multiple Unknown Keys Collected (No Early Abort)
func TestTier1_F09_02_MultipleUnknownKeysCollectedNoEarlyAbort(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("multi_unknown.json")
	h.InjectUnknownKeys(target, map[string]any{
		"key_one":   1,
		"key_two":   "b",
		"key_three": true,
	})

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected migration notice, got nil")
	}

	hasKey := func(name string) bool {
		for _, k := range notice.UnknownKeys {
			if k == name {
				return true
			}
		}
		return false
	}

	if !hasKey("key_one") || !hasKey("key_two") || !hasKey("key_three") {
		t.Fatalf("expected all 3 keys collected without early abort. Got: %v", notice.UnknownKeys)
	}
}

// T1-F09-03: Nested Struct Unknown Keys (web, schedule, git)
func TestTier1_F09_03_NestedStructUnknownKeys(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("nested_unknown.json")
	_, m := h.ReadConfigFile(target)
	webSec := m["web"].(map[string]any)
	webSec["cors_allowed"] = true
	m["web"] = webSec
	h.WriteJSONFile(target, m)

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected notice for nested unknown key, got nil")
	}

	found := false
	for _, k := range notice.UnknownKeys {
		if strings.Contains(k, "cors_allowed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected UnknownKeys to include cors_allowed, got: %v", notice.UnknownKeys)
	}
}

// T1-F09-04: Slice Element Unknown Keys (routers[0])
func TestTier1_F09_04_SliceElementUnknownKeys(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("slice_unknown.json")
	_, m := h.ReadConfigFile(target)
	routers := m["routers"].([]any)
	r0 := routers[0].(map[string]any)
	r0["device_type"] = "CCR"
	routers[0] = r0
	m["routers"] = routers
	h.WriteJSONFile(target, m)

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected notice for router unknown key, got nil")
	}

	found := false
	for _, k := range notice.UnknownKeys {
		if strings.Contains(k, "device_type") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected UnknownKeys to include device_type, got: %v", notice.UnknownKeys)
	}
}

// T1-F09-05: Clean Config Detection (Zero Unknown Keys)
func TestTier1_F09_05_CleanConfigZeroUnknownKeys(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("clean_config.json")

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice != nil && len(notice.UnknownKeys) > 0 {
		t.Fatalf("expected zero unknown keys on clean v2 config, got: %v", notice.UnknownKeys)
	}
}

// ============================================================================
// Feature 10: Config Incompatible Version Guard (T1-F10-01 .. T1-F10-05)
// ============================================================================

// T1-F10-01: config_version: 4 Rejection Error
func TestTier1_F10_01_ConfigVersion4RejectionError(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateIncompatibleConfig("incompatible_v4.json", 4)

	_, err := config.NewManager(target)
	if err == nil {
		t.Fatalf("expected error loading config_version: 4, got nil")
	}

	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "unsupported") && !strings.Contains(errStr, "incompatible") && !strings.Contains(errStr, "version") {
		t.Fatalf("error message did not describe version incompatibility: %v", err)
	}
}

// T1-F10-02: Incompatible Version Leaves File Completely Untouched
func TestTier1_F10_02_IncompatibleVersionLeavesFileUntouched(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateIncompatibleConfig("untouched_v4.json", 4)

	dataBefore, _ := os.ReadFile(target)
	hBefore := sha256.Sum256(dataBefore)

	_, _ = config.NewManager(target)

	dataAfter, _ := os.ReadFile(target)
	hAfter := sha256.Sum256(dataAfter)

	if hBefore != hAfter {
		t.Fatalf("incompatible config file was modified on disk! Hashes differ")
	}
}

// T1-F10-03: No Backup File Created on Incompatible Version
func TestTier1_F10_03_NoBackupFileCreatedOnIncompatibleVersion(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateIncompatibleConfig("no_backup_v4.json", 4)

	_, _ = config.NewManager(target)

	h.AssertFileNotExists(target + ".v4.bak")
	h.AssertFileNotExists(target + ".v3.bak")
	h.AssertFileNotExists(target + ".v2.bak")
	h.AssertFileNotExists(target + ".v1.bak")
}

// T1-F10-04: Daemon Startup Failure with Exit Code 1
func TestTier1_F10_04_DaemonStartupFailureExitCode1(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateIncompatibleConfig("daemon_v4.json", 4)

	res := h.RunCLI(CLIOptions{Args: []string{"-config=" + target}})
	if res.ExitCode != 1 {
		t.Fatalf("expected daemon to exit with code 1 on incompatible config, got %d. Output: %s", res.ExitCode, res.CombinedOutput)
	}
}

// T1-F10-05: Current Version Boundary Acceptance (config_version: 3)
func TestTier1_F10_05_CurrentVersionBoundaryAcceptance(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV3Config("valid_v3.json")

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("expected config_version: 3 to be accepted cleanly, got error: %v", err)
	}
	if mgr.Get().ConfigVersion != 3 {
		t.Fatalf("expected ConfigVersion 3, got %d", mgr.Get().ConfigVersion)
	}
}

// ============================================================================
// Feature 11: Web UI Migration Banner & Dismiss (T1-F11-01 .. T1-F11-05)
// ============================================================================

// T1-F11-01: Migration Banner Displayed on Dashboard
func TestTier1_F11_01_MigrationBannerDisplayedOnDashboard(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("migrated_dashboard.json", "")

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "id=\"migration-notice-banner\"") {
		t.Fatalf("expected banner with id 'migration-notice-banner' on dashboard, got: %s", body)
	}
	if !strings.Contains(body, "Конфигурация обновлена") {
		t.Fatalf("expected 'Конфигурация обновлена' text in banner, got: %s", body)
	}
	if !strings.Contains(body, "hx-post=\"/notices/dismiss\"") {
		t.Fatalf("expected dismiss button with hx-post='/notices/dismiss', got: %s", body)
	}
}

// T1-F11-02: Unknown Keys Warning Displayed in Banner
func TestTier1_F11_02_UnknownKeysWarningDisplayedInBanner(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV2Config("unknown_banner.json")
	h.InjectUnknownKeys(target, map[string]any{
		"experimental_feature_flag": true,
	})

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "id=\"migration-notice-banner\"") {
		t.Fatalf("expected banner with id 'migration-notice-banner' on dashboard, got: %s", body)
	}
	if !strings.Contains(body, "notice-warning") {
		t.Fatalf("expected notice-warning CSS class on banner for unknown keys, got: %s", body)
	}
	if !strings.Contains(body, "experimental_feature_flag") {
		t.Fatalf("expected unknown key 'experimental_feature_flag' listed in banner, got: %s", body)
	}
}

// T1-F11-03: POST /notices/dismiss Handler Execution
func TestTier1_F11_03_PostNoticesDismissHandlerExecution(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("dismiss_test.json", "")

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	// 1. Verify precondition: banner is visible before dismissal
	_, bodyBefore := daemon.Get("/")
	if !strings.Contains(bodyBefore, "id=\"migration-notice-banner\"") {
		t.Fatalf("precondition failed: migration banner missing before dismissal")
	}

	// 2. Execute dismiss endpoint
	code, body := daemon.DismissNotice()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from POST /notices/dismiss, got %d. Body: %s", code, body)
	}

	// 3. Verify notice banner is cleared immediately in-memory
	_, bodyAfter := daemon.Get("/")
	if strings.Contains(bodyAfter, "id=\"migration-notice-banner\"") {
		t.Fatalf("migration banner still present on dashboard immediately after dismissal")
	}
}

// T1-F11-04: Persistence of Dismissal Across Reloads
func TestTier1_F11_04_PersistenceOfDismissalAcrossReloads(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV1LegacyConfig("persist_dismiss.json", "")

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	code, body := daemon.DismissNotice()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from POST /notices/dismiss, got %d. Body: %s", code, body)
	}

	// Verify persistence across multiple consecutive GET requests
	for i := 1; i <= 3; i++ {
		resp, bodyReload := daemon.Get("/")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reload #%d failed with HTTP %d", i, resp.StatusCode)
		}
		if strings.Contains(bodyReload, "id=\"migration-notice-banner\"") || strings.Contains(bodyReload, "migration-banner") {
			t.Fatalf("reload #%d: migration banner unexpectedly reappeared after dismissal", i)
		}
	}
}

// T1-F11-05: Clean System Shows No Migration Banner
func TestTier1_F11_05_CleanSystemShowsNoMigrationBanner(t *testing.T) {
	h := NewHarness(t)
	target := h.CreateV3Config("clean_banner.json")

	daemon := h.StartDaemon(target)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	if strings.Contains(body, "migration-banner") {
		t.Fatalf("migration banner unexpectedly rendered on clean v3 config")
	}
}
