package e2e

import (
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
// Tier 4: Real-World Application Workflows (TC-T4-01 .. TC-T4-07)
// ============================================================================

// TC-T4-01: Full Upgrade Lifecycle (End-to-End Legacy Deployment to Self-Update & Restart)
func TestTier4_TC01_FullUpgradeLifecycle(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()
	cfgPath := h.CreateV1LegacyConfig("legacy_lifecycle.json", filepath.Join(h.TempDir, "backups"))

	// 1. Phase 1 & 2: Boot Legacy Deployment & Verify Config Auto-Migration
	daemon := h.StartDaemon(cfgPath)

	h.AssertFileExists(cfgPath + ".v1.bak")
	_, rawDisk := h.ReadConfigFile(cfgPath)
	if v, ok := rawDisk["config_version"].(float64); !ok || int(v) != config.CurrentConfigVersion {
		t.Fatalf("expected migrated config_version %d, got: %v", config.CurrentConfigVersion, rawDisk["config_version"])
	}

	// 2. Phase 3: Web UI Dashboard Notice & Dismissal
	resp, body1 := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if !strings.Contains(body1, "id=\"migration-notice-banner\"") {
		t.Fatalf("expected migration banner on initial load, got: %s", body1)
	}

	code, dismissBody := daemon.DismissNotice()
	if code != http.StatusOK {
		t.Fatalf("dismiss notice failed: %d, body: %s", code, dismissBody)
	}

	resp2, body2 := daemon.Get("/")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("reload failed with %d", resp2.StatusCode)
	}
	if strings.Contains(body2, "id=\"migration-notice-banner\"") || strings.Contains(body2, "migration-banner") {
		t.Fatalf("migration banner unexpectedly visible after dismissal")
	}

	// Stop daemon before updating binary
	daemon.Stop()

	// 3. Phase 4: Publish Mock Release v2.2.0
	server := NewMockGitHubServer(t)
	defer server.Close()

	payloadV2 := BuildRealBinaryPayload(t, "v2.2.0")
	archive := CreateReleaseArchive(t, "ros-fleet", payloadV2)
	archiveName := "ros-fleet_v2.2.0_linux_amd64.tar.gz"

	files := map[string][]byte{
		archiveName: archive,
	}
	checksums := CreateSha256sums(t, files)
	files["sha256sums.txt"] = checksums

	server.ConfigureReleaseWithAssets("v2.2.0", files)

	// 4. Phase 5: Execute genuine CLI -self-update
	resUpdate := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})
	if resUpdate.ExitCode != 0 {
		t.Fatalf("self-update failed with exit code %d, output: %s", resUpdate.ExitCode, resUpdate.CombinedOutput)
	}
	if !strings.Contains(resUpdate.Stdout, "Successfully updated ros-fleet to version v2.2.0") {
		t.Fatalf("missing restart instruction in stdout: %s", resUpdate.Stdout)
	}
	h.AssertFileNotExists(stagedBin + ".old")

	// Verify updated binary executes and reports v2.2.0
	resVer := h.RunCLI(CLIOptions{BinaryPath: stagedBin, Args: []string{"-version"}})
	if resVer.ExitCode != 0 || !strings.Contains(resVer.Stdout, "v2.2.0") {
		t.Fatalf("updated binary failed -version check: %s", resVer.CombinedOutput)
	}

	// 5. Phase 6: Service Restart on updated binary is idempotent
	h.BinPath = stagedBin
	daemon2 := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon2.Stop()

	h.AssertFileNotExists(cfgPath + ".v2.bak")

	bCode, bBody := daemon2.GetUpdateBadge()
	if bCode != http.StatusNoContent {
		t.Fatalf("expected HTTP 204 No Content for up-to-date system, got %d, body: %s", bCode, bBody)
	}
}

// TC-T4-02: Container Deployment Lifecycle (Docker Environment Constraints & UI Guidance)
func TestTier4_TC02_ContainerDeploymentLifecycle(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	statBefore, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat before failed: %v", err)
	}

	// In container, self-update is disabled
	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"DOCKER_CONTAINER=1"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0 inside container, got %d, output: %s", res.ExitCode, res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "ros-fleet is running inside a Docker container") && !strings.Contains(res.CombinedOutput, "Docker container") {
		t.Fatalf("missing Docker container guidance message: %s", res.CombinedOutput)
	}
	if !strings.Contains(res.Stdout, "docker compose pull ros-fleet") {
		t.Fatalf("missing docker compose command in stdout: %s", res.CombinedOutput)
	}

	statAfter, err := os.Stat(stagedBin)
	if err != nil {
		t.Fatalf("stat after failed: %v", err)
	}
	if statBefore.ModTime() != statAfter.ModTime() {
		t.Fatalf("binary modified inside container environment")
	}
	h.AssertFileNotExists(stagedBin + ".old")

	// Start daemon with DOCKER_CONTAINER=1
	cfgPath := h.CreateV2Config("docker_config.json")
	daemon := h.StartDaemon(cfgPath, "DOCKER_CONTAINER=1")
	defer daemon.Stop()

	code, body := daemon.GetUpdateModal()
	if code != http.StatusOK {
		t.Fatalf("expected HTTP 200 from update modal in container, got %d", code)
	}
	if !strings.Contains(body, "docker compose pull ros-fleet") {
		t.Fatalf("expected docker compose pull command in container modal, got: %s", body)
	}
	if strings.Contains(body, "./ros-fleet -self-update") {
		t.Fatalf("did not expect binary self-update instructions inside container modal")
	}
}

// TC-T4-03: Corrupt Update Attempt Recovery (Checksum Failure & Service Resilience)
func TestTier4_TC03_CorruptUpdateAttemptRecovery(t *testing.T) {
	h := NewHarness(t)
	stagedBin := h.StagedBinaryCopy()

	dataBefore, err := os.ReadFile(stagedBin)
	if err != nil {
		t.Fatalf("read before failed: %v", err)
	}
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

	// Execute update attempt with corrupted checksum
	res := h.RunCLI(CLIOptions{
		BinaryPath: stagedBin,
		Args:       []string{"-self-update"},
		Env:        []string{"GITHUB_API_URL=" + server.URL()},
	})
	if res.ExitCode == 0 {
		t.Fatalf("expected update to fail on corrupted checksum, got exit code 0")
	}
	if !strings.Contains(res.CombinedOutput, "checksum") && !strings.Contains(res.CombinedOutput, "mismatch") && !strings.Contains(res.CombinedOutput, "failed") {
		t.Fatalf("expected checksum error in output, got: %s", res.CombinedOutput)
	}

	// Verify that binary on disk is untouched
	h.AssertFileNotExists(stagedBin + ".old")

	dataAfter, err := os.ReadFile(stagedBin)
	if err != nil {
		t.Fatalf("read after failed: %v", err)
	}
	hAfter := sha256.Sum256(dataAfter)
	if hBefore != hAfter {
		t.Fatalf("binary on disk modified after corrupted update attempt")
	}

	// Service can boot normally
	cfgPath := h.CreateV2Config("resilience.json")
	daemon := h.StartDaemon(cfgPath)
	defer daemon.Stop()

	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected daemon to boot normally after failed update, got %d, body: %s", resp.StatusCode, body)
	}
}

// TC-T4-04: Multi-Version Config Evolution & Idempotent Restart Cycle
func TestTier4_TC04_MultiVersionConfigEvolution_IdempotentRestartCycle(t *testing.T) {
	h := NewHarness(t)
	disabled := false
	cfgPath := h.CreateV1CustomConfig("config_evolution.json", map[string]any{
		"vendor_datacenter_region": "eu-north-1",
		"custom_inventory_id":      10928,
		"routers": []map[string]any{
			{
				"name":     "Router-Disabled",
				"host":     "127.0.0.1",
				"user":     "admin",
				"password": "default_password",
				"enabled":  &disabled,
			},
		},
	})

	// Run 1: Initial migration
	res1 := h.RunCLI(CLIOptions{Args: []string{"-config=" + cfgPath, "-once"}})
	if res1.ExitCode != 0 {
		t.Fatalf("Run 1 failed: %v, output: %s", res1.Err, res1.CombinedOutput)
	}

	bakPath := cfgPath + ".v1.bak"
	h.AssertFileExists(bakPath)
	bakInfo1, err := os.Stat(bakPath)
	if err != nil {
		t.Fatalf("stat bakPath failed: %v", err)
	}

	_, raw1 := h.ReadConfigFile(cfgPath)
	if v, ok := raw1["config_version"].(float64); !ok || int(v) != config.CurrentConfigVersion {
		t.Fatalf("expected config_version %d after Run 1, got: %v", config.CurrentConfigVersion, raw1["config_version"])
	}

	// Run 2: Idempotent reboot
	res2 := h.RunCLI(CLIOptions{Args: []string{"-config=" + cfgPath, "-once"}})
	if res2.ExitCode != 0 {
		t.Fatalf("Run 2 failed: %v, output: %s", res2.Err, res2.CombinedOutput)
	}

	h.AssertFileNotExists(cfgPath + ".v3.bak")
	h.AssertFileNotExists(cfgPath + ".v2.bak")
	bakInfo2, err := os.Stat(bakPath)
	if err != nil {
		t.Fatalf("stat bakPath after Run 2 failed: %v", err)
	}
	if bakInfo1.ModTime() != bakInfo2.ModTime() {
		t.Fatalf(".v1.bak modified on second boot")
	}

	// Run 3: Future version rejection
	h.CreateIncompatibleConfig("config_evolution.json", 4)
	res3 := h.RunCLI(CLIOptions{Args: []string{"-config=" + cfgPath, "-once"}, Timeout: 3 * time.Second})
	if res3.ExitCode != 1 {
		t.Fatalf("expected Run 3 exit code 1 on config_version 4, got %d (err: %v)", res3.ExitCode, res3.Err)
	}
	if res3.Duration >= 2*time.Second {
		t.Fatalf("process hung for %v instead of fast abort (< 2s)", res3.Duration)
	}
	if !strings.Contains(res3.CombinedOutput, "unsupported configuration version") && !strings.Contains(res3.CombinedOutput, "config_version") {
		t.Fatalf("missing descriptive error: %s", res3.CombinedOutput)
	}
	h.AssertFileNotExists(cfgPath + ".v4.bak")
	h.AssertFileNotExists(cfgPath + ".v3.bak")
}

// TC-T4-05: Offline / Air-Gapped Operation with Network Fault Tolerance
func TestTier4_TC05_OfflineAirGappedOperation_NetworkFaultTolerance(t *testing.T) {
	h := NewHarness(t)
	cfgPath := h.CreateV2Config("offline_config.json")

	// Point updater to an unreachable port
	unreachableURL := "http://127.0.0.1:59999"
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+unreachableURL)
	defer daemon.Stop()

	// Wait 300ms for initial background update check to fail
	time.Sleep(300 * time.Millisecond)

	// Web UI remains fully responsive
	resp, body := daemon.Get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on air-gapped web dashboard, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "ros-fleet") {
		t.Fatalf("expected dashboard HTML content, got: %s", body)
	}

	// Update badge returns 204 No Content (no update alert)
	badgeCode, badgeBody := daemon.GetUpdateBadge()
	if badgeCode != http.StatusNoContent {
		t.Fatalf("expected HTTP 204 No Content when offline/air-gapped, got %d, body: %s", badgeCode, badgeBody)
	}
}

// TC-T4-06: Fast Concurrent HTTP Requests During Active Background Checker Operations
func TestTier4_TC06_FastConcurrentHTTPRequests_BackgroundChecker(t *testing.T) {
	server := NewMockGitHubServer(t)
	defer server.Close()

	server.SetRelease(&updater.Release{
		TagName: "v2.3.0",
		Name:    "Release v2.3.0",
	})

	h := NewHarness(t)
	cfgPath := h.CreateV1LegacyConfig("concurrent_load.json", "")
	daemon := h.StartDaemon(cfgPath, "GITHUB_API_URL="+server.URL())
	defer daemon.Stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 60)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := daemon.GetUpdateBadge()
			if code != http.StatusOK && code != http.StatusNoContent {
				errCh <- fmt.Errorf("unexpected badge status %d: %s", code, body)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := daemon.GetUpdateModal()
			if code != http.StatusOK && code != http.StatusNoContent {
				errCh <- fmt.Errorf("unexpected modal status %d: %s", code, body)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := daemon.DismissNotice()
			if code != http.StatusOK {
				errCh <- fmt.Errorf("unexpected dismiss status %d: %s", code, body)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// TC-T4-07: Resilient Atomic Rollback on Configuration Write Failure / Interrupt
func TestTier4_TC07_ResilientAtomicRollback_ConfigWriteFailure(t *testing.T) {
	h := NewHarness(t)
	dir := filepath.Join(h.TempDir, "readonly_atomic_test")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	target := filepath.Join(dir, "config.json")
	originalJSON := fmt.Sprintf(`{"config_version": %d, "binary_backup_dir": "/var/backups/bin", "export_dir": "/var/backups/exp", "state_file": "./state.json", "retention_days": 30, "defaults": {"port": 22, "user": "admin", "password": "default_password"}, "routers": [{"name": "r1", "host": "192.168.1.1", "user": "admin", "password": "default_password"}]}`, config.CurrentConfigVersion)
	if err := os.WriteFile(target, []byte(originalJSON), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	hBefore := sha256.Sum256([]byte(originalJSON))

	mgr, err := config.NewManager(target)
	if err != nil {
		t.Fatalf("initial NewManager failed: %v", err)
	}

	// Inject disk error / filesystem write fault: make directory read-only
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir 0555 failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
	})

	// Attempt save under read-only conditions
	saveErr := mgr.Save()
	if saveErr == nil {
		t.Fatalf("expected Save() to fail on read-only directory, but got nil")
	}

	// Verify original config file is not truncated and unchanged
	dataAfter, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("failed to read target config: %v", readErr)
	}
	if len(dataAfter) == 0 {
		t.Fatalf("target config file was truncated to 0 bytes during failed write!")
	}
	if hBefore != sha256.Sum256(dataAfter) {
		t.Fatalf("target config file corrupted on disk during failed write")
	}

	// Assert no lingering temporary file
	h.AssertFileNotExists(target + ".tmp")

	// Recovery Phase: restore permissions
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("restore chmod 0755 failed: %v", err)
	}

	recoverErr := mgr.Save()
	if recoverErr != nil {
		t.Fatalf("subsequent Save() failed after restoring write permissions: %v", recoverErr)
	}

	dataRecover, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after recover failed: %v", err)
	}
	if len(dataRecover) == 0 {
		t.Fatalf("target config empty after recover")
	}
}

