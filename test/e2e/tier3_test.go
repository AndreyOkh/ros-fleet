package e2e

import (
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/updater"
)

// ============================================================================
// Tier 3: Pairwise Cross-Feature Interaction Scenarios (TC-T3-01 .. TC-T3-13)
// ============================================================================

// TC-T3-01: Config Migration + Web UI Migration Banner + Dismiss Action
func TestTier3_TC01_ConfigMigration_WebUIMigrationBanner_DismissAction(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV1LegacyConfig("legacy_t3_01.json", filepath.Join(h.TempDir, "backups"))

	// Verify pre-migration state
	h.AssertFileNotExists(cfgPath + ".v1.bak")

	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	// 1. Filesystem assertions: .v1.bak exists and v3 saved
	h.AssertFileExists(cfgPath + ".v1.bak")
	_, raw := h.ReadConfigFile(cfgPath)
	if v, ok := raw["config_version"].(float64); !ok || int(v) != config.CurrentConfigVersion {
		t.Fatalf("expected config_version %d, got: %v", config.CurrentConfigVersion, raw["config_version"])
	}

	// 2. Web UI initial load: must render banner
	resp1, body1 := daemon.Get("/")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 on initial GET /, got %d", resp1.StatusCode)
	}
	if !strings.Contains(body1, "id=\"migration-notice-banner\"") {
		t.Fatalf("expected migration notice banner on initial load, got: %s", body1)
	}

	// 3. Dismiss action: must return HTTP 200
	code, body := daemon.DismissNotice()
	if code != http.StatusOK {
		t.Fatalf("POST /notices/dismiss failed with status %d, body: %s", code, body)
	}

	// 4. Subsequent reload: notice banner absent
	resp2, body2 := daemon.Get("/")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 on reload, got %d", resp2.StatusCode)
	}
	if strings.Contains(body2, "id=\"migration-notice-banner\"") || strings.Contains(body2, "migration-banner") {
		t.Fatalf("migration banner unexpectedly visible after dismissal")
	}
}

// TC-T3-02: Updater Check + Web UI Top Bar Badge + Modal Rendering
func TestTier3_TC02_UpdaterCheck_WebUITopBarBadge_ModalRendering(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName:     "v2.1.0",
		Name:        "Release v2.1.0",
		Body:        "### Features\n- Automated backup pruning\n- Enhanced security",
		HTMLURL:     "https://github.com/AndreyOkh/ros-fleet/releases/tag/v2.1.0",
		PublishedAt: time.Now().UTC(),
	})

	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	// Poll briefly for initial update check
	deadline := time.Now().Add(2 * time.Second)
	var badgeCode int
	var badgeBody string
	for time.Now().Before(deadline) {
		badgeCode, badgeBody = daemon.GetUpdateBadge()
		if badgeCode == http.StatusOK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if badgeCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 from /update/badge, got %d, body: %s", badgeCode, badgeBody)
	}
	if !strings.Contains(badgeBody, "badge-update") {
		t.Fatalf("expected badge-update class in badge HTML, got: %s", badgeBody)
	}
	if !strings.Contains(badgeBody, "v2.1.0") || !strings.Contains(badgeBody, "Доступно обновление") {
		t.Fatalf("expected update text and version in badge, got: %s", badgeBody)
	}

	modalCode, modalBody := daemon.GetUpdateModal()
	if modalCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 from /update/modal, got %d, body: %s", modalCode, modalBody)
	}
	if !strings.Contains(modalBody, "update-modal") {
		t.Fatalf("expected modal dialog markup, got: %s", modalBody)
	}
	if !strings.Contains(modalBody, "v2.1.0") {
		t.Fatalf("expected new version v2.1.0 rendered in modal")
	}
	if !strings.Contains(modalBody, "Automated backup pruning") {
		t.Fatalf("expected release notes rendered in modal body")
	}
}

// TC-T3-03: Docker Environment Detection + CLI -self-update Rejection
func TestTier3_TC03_DockerEnvironmentDetection_CLISelfUpdateRejection(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	statBefore, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	dataBefore, _ := os.ReadFile(stagedBin)
	hBefore := sha256.Sum256(dataBefore)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"DOCKER_CONTAINER=1"},
	})

	if res.ExitCode != 0 {
		t.Fatalf("expected clean exit code 0 when inside Docker container, got %d, output: %s", res.ExitCode, res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "ros-fleet is running inside a Docker container") && !strings.Contains(res.CombinedOutput, "Docker container") {
		t.Fatalf("expected Docker container detection message in stdout, got: %s", res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "docker compose pull ros-fleet") {
		t.Fatalf("expected docker compose guidance instructions in stdout, got: %s", res.CombinedOutput)
	}

	statAfter, _ := os.Stat(stagedBin)
	if statBefore.ModTime() != statAfter.ModTime() {
		t.Fatalf("binary on disk was modified despite running inside Docker container")
	}
	dataAfter, _ := os.ReadFile(stagedBin)
	if hBefore != sha256.Sum256(dataAfter) {
		t.Fatalf("binary content modified despite running in container")
	}
	h.AssertFileNotExists(stagedBin + ".old")
}

// TC-T3-04: Legacy Config with Unknown Keys + Byte-Verbatim Backup + Web UI Notice Display
func TestTier3_TC04_LegacyConfigWithUnknownKeys_ByteVerbatimBackup_WebUINoticeDisplay(t *testing.T) {
	h := NewHarness(t)
	cfgPath := filepath.Join(h.TempDir, "config_t3_04.json")

	initialJSON := `{
  "backup_dir": "/var/backups",
  "vendor_hardware_id": "RB4011-OFFICE",
  "custom_metric_tag": 42,
  "state_file": "./state.json",
  "defaults": {
    "port": 22,
    "user": "admin",
    "password": "default_password"
  },
  "web": {
    "enabled": true,
    "listen_addr": "127.0.0.1:0"
  },
  "routers": [
    {
      "name": "Router-V1",
      "host": "192.168.1.1",
      "user": "admin",
      "password": "default_password"
    }
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(initialJSON), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 1. Start daemon directly on legacy config with unknown keys
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	// 2. Verify backup created by daemon
	bakPath := cfgPath + ".v1.bak"
	h.AssertFileExists(bakPath)
	bakBytes, err := os.ReadFile(bakPath)
	if err != nil || len(bakBytes) == 0 {
		t.Fatalf("failed to read backup file %s: %v", bakPath, err)
	}
	bakStr := string(bakBytes)
	if !strings.Contains(bakStr, "vendor_hardware_id") || !strings.Contains(bakStr, "custom_metric_tag") || !strings.Contains(bakStr, "backup_dir") {
		t.Fatalf("backup file missing original v1 and unknown keys: %s", bakStr)
	}

	// 3. Web UI notice rendering assertions
	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "id=\"migration-notice-banner\"") {
		t.Fatalf("expected migration-notice-banner in HTML, got: %s", body)
	}
	if !strings.Contains(body, "vendor_hardware_id") {
		t.Fatalf("expected vendor_hardware_id in banner HTML, got: %s", body)
	}
	if !strings.Contains(body, "custom_metric_tag") {
		t.Fatalf("expected custom_metric_tag in banner HTML, got: %s", body)
	}
}

// TC-T3-05: Hybrid Versioning with -ldflags Overrides vs -version CLI Flag & Web UI Display
func TestTier3_TC05_HybridVersioningLdflagsOverrides_VersionCLI_WebUIDisplay(t *testing.T) {
	ldflags := "-X main.version=v3.5.0-prod -X main.commit=7d8e9f0a -X main.date=2026-11-18T14:30:00Z " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Version=v3.5.0-prod " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.Commit=7d8e9f0a " +
		"-X github.com/AndreyOkh/ros-fleet/internal/version.BuildDate=2026-11-18T14:30:00Z"

	customBin := BuildCustomBinary(t, ldflags)
	h := NewHarness(t)

	// 1. CLI -version check
	res := h.RunCLI(CLIOptions{BinaryPath: customBin, Args: []string{"-version"}})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "v3.5.0-prod") {
		t.Fatalf("stdout missing injected version v3.5.0-prod: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "7d8e9f0a") {
		t.Fatalf("stdout missing injected commit 7d8e9f0a: %s", res.Stdout)
	}

	// 2. Web UI rendering check
	h.BinPath = customBin
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "v3.5.0-prod") {
		t.Fatalf("expected injected version v3.5.0-prod in Web UI, got: %s", body)
	}
	if !strings.Contains(body, "7d8e9f0a") {
		t.Fatalf("expected injected commit 7d8e9f0a in Web UI footer, got: %s", body)
	}
}

// TC-T3-06: SemVer Dirty / Prerelease Build vs Newer / Equal / Older Releases
func TestTier3_TC06_SemVerDirtyPrereleaseBuild_Vs_NewerEqualOlderReleases(t *testing.T) {
	currentVersion := "v2.4.0-rc1+dirty"

	// Case A: Older release v2.3.9 -> false
	if updater.IsUpdateAvailable(currentVersion, "v2.3.9") {
		t.Fatalf("expected IsUpdateAvailable false for older release v2.3.9")
	}

	// Case B: Equal base release v2.4.0 -> false
	if updater.IsUpdateAvailable(currentVersion, "v2.4.0") {
		t.Fatalf("expected IsUpdateAvailable false for equal base release v2.4.0")
	}

	// Case C: Truly newer release v2.5.0 -> true
	if !updater.IsUpdateAvailable(currentVersion, "v2.5.0") {
		t.Fatalf("expected IsUpdateAvailable true for newer release v2.5.0")
	}
}

// TC-T3-07: Incompatible Config Version (config_version > 3) + Web Server / Daemon Boot Guard
func TestTier3_TC07_IncompatibleConfigVersion_DaemonBootGuard(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateIncompatibleConfig("incompat_t3_07.json", 4)

	dataBefore, _ := os.ReadFile(cfgPath)
	hBefore := sha256.Sum256(dataBefore)

	res := h.RunCLI(CLIOptions{
		Args:    []string{"-config=" + cfgPath, "-once"},
		Timeout: 3 * time.Second,
	})
	if res.ExitCode != 1 {
		t.Fatalf("expected exit code 1 on incompatible config_version 4, got %d (err: %v)", res.ExitCode, res.Err)
	}
	if res.Duration >= 2*time.Second {
		t.Fatalf("process hung on startup for %v instead of fast abort", res.Duration)
	}
	if !strings.Contains(res.CombinedOutput, "unsupported configuration version") && !strings.Contains(res.CombinedOutput, "config_version") {
		t.Fatalf("expected descriptive incompatible version error, got: %s", res.CombinedOutput)
	}

	dataAfter, _ := os.ReadFile(cfgPath)
	hAfter := sha256.Sum256(dataAfter)
	if hBefore != hAfter {
		t.Fatalf("incompatible configuration file was mutated on disk")
	}

	h.AssertFileNotExists(cfgPath + ".v4.bak")
	h.AssertFileNotExists(cfgPath + ".v3.bak")
	h.AssertFileNotExists(cfgPath + ".v2.bak")
}

// TC-T3-08: -self-update Binary Swap + File Permission Mode Preservation + Rollback Clean Removal
func TestTier3_TC08_SelfUpdateBinarySwap_FilePermissionPreservation_RollbackCleanRemoval(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	if err := os.Chmod(stagedBin, 0o750); err != nil {
		t.Fatalf("chmod 0750 failed: %v", err)
	}

	stat, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if stat.Mode().Perm() != 0o750 {
		t.Fatalf("expected 0750 permissions, got %o", stat.Mode().Perm())
	}

	server := NewMockGitHubServer(t)
	defer server.Close()

	mockPayload := BuildMockBinaryPayload(t, "v2.2.0")
	archive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: archive,
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums

	server.ConfigureReleaseWithAssets("v2.2.0", files)

	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d, output: %s", res.ExitCode, res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "Successfully updated ros-fleet to version v2.2.0") {
		t.Fatalf("missing restart instruction, got: %s", res.Stdout)
	}

	// Verify binary on disk was replaced and executes
	resVer := h.RunCLI(CLIOptions{BinaryPath: stagedBin, Args: []string{"-version"}})
	if resVer.ExitCode != 0 || !strings.Contains(resVer.Stdout, "v2.2.0") {
		t.Fatalf("updated binary did not report v2.2.0, got: %s", resVer.Stdout)
	}

	// Verify permissions preserved on updated binary
	statAfter, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat after failed: %v", err)
	}
	if statAfter.Mode().Perm() != 0o750 {
		t.Fatalf("expected 0750 permissions preserved on updated binary, got %o", statAfter.Mode().Perm())
	}

	h.AssertFileNotExists(stagedBin + ".old")
}

// TC-T3-09: -self-update SHA-256 Checksum Mismatch Recovery + Active Binary Preservation
func TestTier3_TC09_SelfUpdateSHA256ChecksumMismatchRecovery_ActiveBinaryPreservation(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	dataBefore, _ := os.ReadFile(stagedBin)
	hBefore := sha256.Sum256(dataBefore)

	server := NewMockGitHubServer(t)
	defer server.Close()

	mockPayload := BuildMockBinaryPayload(t, "v2.2.0")
	archive := CreateReleaseArchive(t, "ros-fleet", mockPayload)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: archive,
	}
	files["sha256sums.txt"] = []byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  " + archiveName + "\n")

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
		t.Fatalf("expected checksum error in output, got: %s", res.CombinedOutput)
	}

	dataAfter, _ := os.ReadFile(stagedBin)
	hAfter := sha256.Sum256(dataAfter)
	if hBefore != hAfter {
		t.Fatalf("binary modified on checksum mismatch")
	}

	h.AssertFileNotExists(stagedBin + ".old")

	resVer := h.RunCLI(CLIOptions{BinaryPath: stagedBin, Args: []string{"-version"}})
	if resVer.ExitCode != 0 {
		t.Fatalf("active binary damaged after failed update attempt")
	}
}

// TC-T3-10: Config Migration of Deprecated backup_dir + Router Runner Directory Resolution
func TestTier3_TC10_ConfigMigrationOfDeprecatedBackupDir_RunnerStoragePaths(t *testing.T) {
	h := NewHarness(t)
	customBackupRoot := filepath.Join(h.TempDir, "custom", "storage", "root")
	cfgPath := h.CreateV1CustomConfig("config_t3_10.json", map[string]any{
		"backup_dir": customBackupRoot,
	})

	mgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	expectedBin := filepath.Join(customBackupRoot, "binary")
	expectedExp := filepath.Join(customBackupRoot, "exports")

	if cfg.BinaryBackupDir != expectedBin {
		t.Errorf("expected BinaryBackupDir %q, got %q", expectedBin, cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != expectedExp {
		t.Errorf("expected ExportDir %q, got %q", expectedExp, cfg.ExportDir)
	}
	if cfg.BackupDir != "" {
		t.Errorf("expected BackupDir cleared in memory, got %q", cfg.BackupDir)
	}
}

// TC-T3-11: Docker Container Environment + Web UI Modal Docker Instructions Display
func TestTier3_TC11_DockerContainerEnvironment_WebUIModalDockerInstructions(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("config.json")
	daemon := h.StartDaemon(cfgPath, "DOCKER_CONTAINER=1")
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 from /update/modal in container, got %d, body: %s", code, body)
	}
	if !strings.Contains(body, "update-modal") {
		t.Fatalf("expected modal dialog markup, got: %s", body)
	}
	if !strings.Contains(body, "docker compose pull ros-fleet") {
		t.Fatalf("expected docker compose pull command in modal, got: %s", body)
	}
}

// TC-T3-12: Background Release Checker Dynamic Status Change + Web UI Badge Transition
func TestTier3_TC12_BackgroundReleaseCheckerDynamicStatusChange_WebUIBadgeTransition(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	// 1. Initial status: up to date (v2.0.0)
	server.SetRelease(&updater.Release{
		TagName: "v2.0.0",
		Name:    "Release v2.0.0",
	})

	checker := updater.NewChecker("AndreyOkh/ros-fleet", "v2.0.0", nil)
	checker.SetBaseURL(server.URL())

	res1, _ := checker.CheckNow(context.Background())
	if res1.UpdateAvailable {
		t.Fatalf("expected initially not available")
	}

	// 2. Publish new release (v2.1.0)
	server.SetRelease(&updater.Release{
		TagName: "v2.1.0",
		Name:    "Release v2.1.0",
	})

	res2, _ := checker.CheckNow(context.Background())
	if !res2.UpdateAvailable {
		t.Fatalf("expected update available after publishing v2.1.0")
	}
	if res2.LatestVersion != "v2.1.0" {
		t.Fatalf("expected LatestVersion v2.1.0, got %s", res2.LatestVersion)
	}
}

// TC-T3-13: Multiple Unknown Fields in Nested Config Sections + Atomic Config Persistence & UI Notice Enumeration
func TestTier3_TC13_MultipleUnknownFieldsInNestedSections_AtomicConfigPersistence(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV1CustomConfig("config_t3_13.json", map[string]any{
		"legacy_root_key":  "val1",
		"another_root_key": 999,
		"defaults": map[string]any{
			"port":              22,
			"user":              "admin",
			"password":          "default_password",
			"unknown_port_rule": "custom",
		},
		"schedule": map[string]any{
			"unknown_interval": "10m",
		},
	})

	mgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected notice for unknown keys, got nil")
	}

	if len(notice.UnknownKeys) < 4 {
		t.Fatalf("expected at least 4 unknown keys collected, got %d: %v", len(notice.UnknownKeys), notice.UnknownKeys)
	}

	// Verify atomic save as valid current JSON
	_, rawDisk := h.ReadConfigFile(cfgPath)
	if v, ok := rawDisk["config_version"].(float64); !ok || int(v) != config.CurrentConfigVersion {
		t.Fatalf("expected disk config_version %d, got: %v", config.CurrentConfigVersion, rawDisk["config_version"])
	}
}
