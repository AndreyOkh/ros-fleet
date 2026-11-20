package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/runner"
	"github.com/AndreyOkh/ros-fleet/internal/scheduler"
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/internal/updater"
	"github.com/AndreyOkh/ros-fleet/internal/version"
)

func setupTestServer(t *testing.T, authUser, authPass string) (*Server, string) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	stateFile := filepath.Join(tempDir, "state.json")

	cfgJSON := `{
		"backup_dir": "` + tempDir + `/backups",
		"state_file": "` + stateFile + `",
		"defaults": {
			"port": 22,
			"user": "admin",
			"password": "default_pass"
		},
		"schedule": {
			"times": ["03:00", "15:00"],
			"timezone": "UTC"
		},
		"web": {
			"enabled": true,
			"listen_addr": ":8080",
			"auth_user": "` + authUser + `",
			"auth_password": "` + authPass + `"
		},
		"routers": [
			{
				"name": "Router-Test",
				"host": "192.168.1.1"
			}
		]
	}`

	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgMgr, err := config.NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	stateMgr, err := state.NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	stateMgr.InitFromConfig(cfgMgr.Get().Routers, cfgMgr.Get().Defaults)

	run := runner.New(cfgMgr, stateMgr, slog.Default())

	sched, err := scheduler.New(cfgMgr.Get().Schedule.Times, cfgMgr.Get().Schedule.Timezone, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(cfgMgr, run, stateMgr, sched, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	return srv, cfgFile
}

func TestServerDashboardAndStatic(t *testing.T) {
	srv, _ := setupTestServer(t, "", "")

	// 1. Test GET /
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET / returned status %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "ros-fleet") {
		t.Errorf("expected dashboard HTML to contain 'ros-fleet', got:\n%s", body)
	}
	if !strings.Contains(body, "Router-Test") {
		t.Errorf("expected dashboard HTML to list 'Router-Test'")
	}
	if !strings.Contains(body, "Устройства") {
		t.Errorf("expected dashboard HTML to contain section title 'Устройства'")
	}
	if !strings.Contains(body, "filters-bar") {
		t.Errorf("expected dashboard HTML to contain 'filters-bar'")
	}
	if !strings.Contains(body, "devices-table") {
		t.Errorf("expected dashboard HTML to contain 'devices-table'")
	}

	// 2. Test GET /static/style.css
	reqCSS := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	rrCSS := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrCSS, reqCSS)

	if rrCSS.Code != http.StatusOK {
		t.Errorf("GET /static/style.css returned status %d, want %d", rrCSS.Code, http.StatusOK)
	}

	// 3. Test Defaults Modal GET /settings/defaults
	reqDef := httptest.NewRequest(http.MethodGet, "/settings/defaults", nil)
	rrDef := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDef, reqDef)

	if rrDef.Code != http.StatusOK {
		t.Errorf("GET /settings/defaults returned status %d, want %d", rrDef.Code, http.StatusOK)
	}
	if !strings.Contains(rrDef.Body.String(), "Глобальные настройки подключения") {
		t.Errorf("expected defaults modal to contain title")
	}

	// 4. Test Git Status GET /dashboard/git-status
	reqGit := httptest.NewRequest(http.MethodGet, "/dashboard/git-status", nil)
	rrGit := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrGit, reqGit)

	if rrGit.Code != http.StatusOK {
		t.Errorf("GET /dashboard/git-status returned status %d, want %d", rrGit.Code, http.StatusOK)
	}
	if !strings.Contains(rrGit.Body.String(), "git-status-card") {
		t.Errorf("expected git status HTML to contain 'git-status-card'")
	}

	// 5. Test Git Push POST /dashboard/git-push
	reqPush := httptest.NewRequest(http.MethodPost, "/dashboard/git-push", nil)
	rrPush := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrPush, reqPush)

	if rrPush.Code != http.StatusOK {
		t.Errorf("POST /dashboard/git-push returned status %d, want %d", rrPush.Code, http.StatusOK)
	}
	if !strings.Contains(rrPush.Body.String(), "git-status-card") {
		t.Errorf("expected git push response to return 'git-status-card' HTML")
	}
}

func TestServerToggleAndEndpoints(t *testing.T) {
	srv, _ := setupTestServer(t, "", "")

	// Test Toggle
	reqToggle := httptest.NewRequest(http.MethodPost, "/routers/Router-Test/toggle", nil)
	rrToggle := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrToggle, reqToggle)

	if rrToggle.Code != http.StatusOK {
		t.Errorf("toggle returned code %d, want %d", rrToggle.Code, http.StatusOK)
	}
	if !strings.Contains(rrToggle.Body.String(), "Отключен") {
		t.Errorf("expected toggled row to have 'Отключен' badge")
	}

	// Test New Router Modal
	reqNew := httptest.NewRequest(http.MethodGet, "/routers/new", nil)
	rrNew := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrNew, reqNew)
	if rrNew.Code != http.StatusOK {
		t.Errorf("GET /routers/new returned %d", rrNew.Code)
	}
	if !strings.Contains(rrNew.Body.String(), "tri-state-toggle") {
		t.Errorf("expected new router modal to contain tri-state-toggle")
	}

	// Test Add router (inheriting user & password from defaults, overriding insecure_ignore_host_key)
	form := url.Values{}
	form.Set("name", "Router-New")
	form.Set("host", "10.0.0.1")
	form.Set("insecure_ignore_host_key", "true")
	form.Set("export_sensitive", "inherit")
	form.Set("encrypt_backup", "inherit")
	form.Set("backup_password", "should_be_cleared_on_inherit")

	reqAdd := httptest.NewRequest(http.MethodPost, "/routers", strings.NewReader(form.Encode()))
	reqAdd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrAdd := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrAdd, reqAdd)

	if rrAdd.Code != http.StatusOK {
		t.Errorf("add router returned code %d, want %d, body: %s", rrAdd.Code, http.StatusOK, rrAdd.Body.String())
	}
	if !strings.Contains(rrAdd.Body.String(), "Router-New") {
		t.Errorf("expected table to include 'Router-New'")
	}

	rc, found := srv.cfgMgr.GetRouter("Router-New")
	if !found {
		t.Fatalf("expected router Router-New to exist in config")
	}
	if rc.InsecureIgnoreHostKey == nil || !*rc.InsecureIgnoreHostKey {
		t.Errorf("expected InsecureIgnoreHostKey to be true")
	}
	if rc.ExportSensitive != nil {
		t.Errorf("expected ExportSensitive to be nil (inherited from defaults), got %v", *rc.ExportSensitive)
	}
	if rc.BackupPassword != "" {
		t.Errorf("expected BackupPassword to be empty when encrypt_backup is inherit, got %q", rc.BackupPassword)
	}

	// Test Add router with explicit encryption enabled and custom password
	formEnc := url.Values{}
	formEnc.Set("name", "Router-Encrypted")
	formEnc.Set("host", "10.0.0.2")
	formEnc.Set("encrypt_backup", "true")
	formEnc.Set("backup_password", "custom_secret_123")

	reqAddEnc := httptest.NewRequest(http.MethodPost, "/routers", strings.NewReader(formEnc.Encode()))
	reqAddEnc.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrAddEnc := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrAddEnc, reqAddEnc)

	if rrAddEnc.Code != http.StatusOK {
		t.Errorf("add encrypted router returned code %d", rrAddEnc.Code)
	}

	rcEnc, foundEnc := srv.cfgMgr.GetRouter("Router-Encrypted")
	if !foundEnc {
		t.Fatalf("expected router Router-Encrypted to exist in config")
	}
	if rcEnc.EncryptBackup == nil || !*rcEnc.EncryptBackup {
		t.Errorf("expected EncryptBackup to be true")
	}
	if rcEnc.BackupPassword != "custom_secret_123" {
		t.Errorf("expected BackupPassword to be custom_secret_123, got %q", rcEnc.BackupPassword)
	}

	// Test POST /backup/all kicks off background run and returns table immediately
	reqBackupAll := httptest.NewRequest(http.MethodPost, "/backup/all", nil)
	rrBackupAll := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrBackupAll, reqBackupAll)

	if rrBackupAll.Code != http.StatusOK {
		t.Errorf("POST /backup/all returned status %d, want 200", rrBackupAll.Code)
	}
	if !strings.Contains(rrBackupAll.Body.String(), "card-table") {
		t.Errorf("expected table HTML in response to POST /backup/all")
	}

	// Wait for background backup to finish before tempDir cleanup
	for srv.runner.IsRunning() {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerBasicAuth(t *testing.T) {
	srv, _ := setupTestServer(t, "admin", "securepassword")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", rr.Code)
	}

	reqAuth := httptest.NewRequest(http.MethodGet, "/", nil)
	reqAuth.SetBasicAuth("admin", "securepassword")
	rrAuth := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrAuth, reqAuth)

	if rrAuth.Code != http.StatusOK {
		t.Errorf("expected status 200 OK with valid credentials, got %d", rrAuth.Code)
	}
}

func TestServerBackupsEndpoints(t *testing.T) {
	srv, _ := setupTestServer(t, "", "")
	cfg := srv.cfgMgr.Get()

	// 1. Initially empty backups modal
	reqModal := httptest.NewRequest(http.MethodGet, "/routers/Router-Test/backups", nil)
	rrModal := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrModal, reqModal)

	if rrModal.Code != http.StatusOK {
		t.Fatalf("GET /routers/Router-Test/backups failed with code %d", rrModal.Code)
	}
	bodyEmpty := rrModal.Body.String()
	if !strings.Contains(bodyEmpty, "Бэкапы для этого устройства еще не создавались") {
		t.Errorf("expected empty state message in backups modal, got:\n%s", bodyEmpty)
	}

	// 2. Place sample backup files
	rscDir := cfg.ExportDir
	binDir := filepath.Join(cfg.BinaryBackupDir, "Router-Test")
	_ = os.MkdirAll(rscDir, 0o755)
	_ = os.MkdirAll(binDir, 0o755)

	rscContent := []byte("/system identity set name=Test\n")
	_ = os.WriteFile(filepath.Join(rscDir, "Router-Test.rsc"), rscContent, 0o644)

	binContent := []byte("binary-backup-sample-data")
	binFile := filepath.Join(binDir, "Router-Test_2026-11-18_12-00-00.backup")
	_ = os.WriteFile(binFile, binContent, 0o644)

	// 3. Request modal again
	rrModal2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrModal2, reqModal)
	if rrModal2.Code != http.StatusOK {
		t.Fatalf("GET /routers/Router-Test/backups failed with code %d", rrModal2.Code)
	}
	bodyFiles := rrModal2.Body.String()
	if !strings.Contains(bodyFiles, "Router-Test.rsc") {
		t.Errorf("expected modal to list Router-Test.rsc")
	}
	if !strings.Contains(bodyFiles, "Router-Test_2026-11-18_12-00-00.backup") {
		t.Errorf("expected modal to list binary backup file")
	}

	// 4. Download RSC
	reqDlRsc := httptest.NewRequest(http.MethodGet, "/routers/Router-Test/backups/download?kind=export&file=Router-Test.rsc", nil)
	rrDlRsc := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDlRsc, reqDlRsc)

	if rrDlRsc.Code != http.StatusOK {
		t.Errorf("download RSC returned code %d, want 200", rrDlRsc.Code)
	}
	if disp := rrDlRsc.Header().Get("Content-Disposition"); !strings.Contains(disp, "attachment") || !strings.Contains(disp, "Router-Test.rsc") {
		t.Errorf("expected attachment Content-Disposition header, got %q", disp)
	}
	if rrDlRsc.Body.String() != string(rscContent) {
		t.Errorf("downloaded RSC content mismatch: got %q, want %q", rrDlRsc.Body.String(), string(rscContent))
	}

	// 5. Download Binary
	relBinPath := "Router-Test/Router-Test_2026-11-18_12-00-00.backup"
	reqDlBin := httptest.NewRequest(http.MethodGet, "/routers/Router-Test/backups/download?kind=binary&file="+relBinPath, nil)
	rrDlBin := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDlBin, reqDlBin)

	if rrDlBin.Code != http.StatusOK {
		t.Errorf("download binary returned code %d, want 200", rrDlBin.Code)
	}
	if rrDlBin.Body.String() != string(binContent) {
		t.Errorf("downloaded binary content mismatch: got %q, want %q", rrDlBin.Body.String(), string(binContent))
	}

	// 6. Download Latest
	reqLatest := httptest.NewRequest(http.MethodGet, "/routers/Router-Test/backups/latest?kind=binary", nil)
	rrLatest := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrLatest, reqLatest)

	if rrLatest.Code != http.StatusOK {
		t.Errorf("download latest binary returned code %d, want 200", rrLatest.Code)
	}
	if rrLatest.Body.String() != string(binContent) {
		t.Errorf("downloaded latest content mismatch")
	}

	// 7. Security: Path Traversal
	reqAttack := httptest.NewRequest(http.MethodGet, "/routers/Router-Test/backups/download?kind=export&file=../../etc/passwd", nil)
	rrAttack := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrAttack, reqAttack)

	if rrAttack.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for path traversal attempt, got %d", rrAttack.Code)
	}
}

type mockUpdateChecker struct {
	mu     sync.RWMutex
	status *updater.CheckResult
}

func (m *mockUpdateChecker) GetStatus() *updater.CheckResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.status == nil {
		return nil
	}
	cp := *m.status
	return &cp
}

func (m *mockUpdateChecker) setStatus(s *updater.CheckResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = s
}

func TestServerVersionInDashboard(t *testing.T) {
	srv, _ := setupTestServer(t, "", "")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET / returned status %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	// Version badge in header
	if !strings.Contains(body, "badge-version") {
		t.Errorf("expected dashboard HTML to contain version badge 'badge-version', got:\n%s", body)
	}

	// Active version in footer
	if !strings.Contains(body, "<footer class=\"footer\">") {
		t.Errorf("expected dashboard HTML to contain semantic footer")
	}

	currVer := version.Get().Version
	if !strings.Contains(body, currVer) {
		t.Errorf("expected dashboard to render active version %q", currVer)
	}
}

func TestServerUpdateEndpoints(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	stateFile := filepath.Join(tempDir, "state.json")

	cfgJSON := `{
		"config_version": 2,
		"binary_backup_dir": "` + tempDir + `/backups/binary",
		"export_dir": "` + tempDir + `/backups/exports",
		"state_file": "` + stateFile + `",
		"defaults": {"port": 22, "user": "admin", "password": "pwd"},
		"web": {"enabled": true, "listen_addr": ":8080"},
		"routers": [{"name": "R1", "host": "10.0.0.1"}]
	}`
	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgMgr, err := config.NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	stateMgr, err := state.NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	run := runner.New(cfgMgr, stateMgr, slog.Default())
	sched, err := scheduler.New(nil, "", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	checker := &mockUpdateChecker{}
	srv, err := NewServer(cfgMgr, run, stateMgr, sched, checker, slog.Default())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET /update/badge when no update available (returns 204 No Content)
	reqBadge1 := httptest.NewRequest(http.MethodGet, "/update/badge", nil)
	rrBadge1 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrBadge1, reqBadge1)
	if rrBadge1.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content when no update, got %d", rrBadge1.Code)
	}

	// 2. Set update available
	checker.setStatus(&updater.CheckResult{
		UpdateAvailable: true,
		CurrentVersion:  "v2.1.0",
		LatestVersion:   "v2.2.0",
		ReleaseNotes:    "Release v2.2.0 includes major improvements and bugfixes.",
		PublishedAt:     time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
		HTMLURL:         "https://github.com/AndreyOkh/ros-fleet/releases/tag/v2.2.0",
	})

	// 3. GET /update/badge returns 200 with badge markup
	reqBadge2 := httptest.NewRequest(http.MethodGet, "/update/badge", nil)
	rrBadge2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrBadge2, reqBadge2)
	if rrBadge2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for update badge, got %d", rrBadge2.Code)
	}
	badgeHTML := rrBadge2.Body.String()
	if !strings.Contains(badgeHTML, "badge-update") || !strings.Contains(badgeHTML, "v2.2.0") {
		t.Errorf("unexpected badge markup: %s", badgeHTML)
	}

	// 4. GET /update/modal returns 200 with modal and commands
	reqModal := httptest.NewRequest(http.MethodGet, "/update/modal", nil)
	rrModal := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrModal, reqModal)
	if rrModal.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for update modal, got %d", rrModal.Code)
	}
	modalHTML := rrModal.Body.String()
	if !strings.Contains(modalHTML, "update-modal") {
		t.Errorf("expected modal to contain update-modal id, got: %s", modalHTML)
	}
	if !strings.Contains(modalHTML, "v2.2.0") {
		t.Errorf("expected modal to mention latest version v2.2.0")
	}
	if !strings.Contains(modalHTML, "Release v2.2.0 includes major improvements") {
		t.Errorf("expected modal to contain release notes")
	}
	if !strings.Contains(modalHTML, "docker compose pull ros-fleet") {
		t.Errorf("expected modal to contain Docker update command")
	}
	if !strings.Contains(modalHTML, "./ros-fleet -self-update") {
		t.Errorf("expected modal to contain CLI self-update command")
	}
}

func TestServerMigrationBannerAndDismiss(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	stateFile := filepath.Join(tempDir, "state.json")

	// Create legacy v1 config with backup_dir and unknown key
	cfgJSON := `{
		"backup_dir": "` + tempDir + `/backups",
		"state_file": "` + stateFile + `",
		"unknown_custom_key": "some_value",
		"defaults": {"port": 22, "user": "admin", "password": "pwd"},
		"web": {"enabled": true, "listen_addr": ":8080"},
		"routers": [{"name": "R1", "host": "10.0.0.1"}]
	}`
	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgMgr, err := config.NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfgMgr.GetMigrationNotice() == nil {
		t.Fatalf("expected non-nil MigrationNotice after loading v1 config")
	}

	stateMgr, err := state.NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	run := runner.New(cfgMgr, stateMgr, slog.Default())
	sched, err := scheduler.New(nil, "", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(cfgMgr, run, stateMgr, sched, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET / renders migration banner
	reqIndex := httptest.NewRequest(http.MethodGet, "/", nil)
	rrIndex := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrIndex, reqIndex)
	if rrIndex.Code != http.StatusOK {
		t.Fatalf("GET / returned %d, want 200", rrIndex.Code)
	}
	bodyIndex := rrIndex.Body.String()
	if !strings.Contains(bodyIndex, "migration-notice-banner") {
		t.Errorf("expected dashboard to contain migration-notice-banner")
	}
	if !strings.Contains(bodyIndex, "unknown_custom_key") {
		t.Errorf("expected dashboard banner to mention unknown_custom_key")
	}

	// 2. POST /notices/dismiss dismisses notice
	reqDismiss := httptest.NewRequest(http.MethodPost, "/notices/dismiss", nil)
	rrDismiss := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDismiss, reqDismiss)
	if rrDismiss.Code != http.StatusOK {
		t.Fatalf("POST /notices/dismiss returned %d, want 200", rrDismiss.Code)
	}

	if cfgMgr.GetMigrationNotice() != nil {
		t.Errorf("expected MigrationNotice to be cleared after dismissal")
	}

	// 3. GET / again does not render banner
	rrIndex2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrIndex2, reqIndex)
	if rrIndex2.Code != http.StatusOK {
		t.Fatalf("GET / returned %d, want 200", rrIndex2.Code)
	}
	if strings.Contains(rrIndex2.Body.String(), "migration-notice-banner") {
		t.Errorf("expected migration banner to be absent after dismissal")
	}

	// 4. POST /notices/dismiss again is idempotent
	rrDismiss2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDismiss2, reqDismiss)
	if rrDismiss2.Code != http.StatusOK {
		t.Errorf("expected 200 OK on idempotent dismissal, got %d", rrDismiss2.Code)
	}
}

func TestServerConcurrentUpdatesAndDismissal(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	stateFile := filepath.Join(tempDir, "state.json")

	cfgJSON := `{
		"config_version": 2,
		"binary_backup_dir": "` + tempDir + `/backups/binary",
		"export_dir": "` + tempDir + `/backups/exports",
		"state_file": "` + stateFile + `",
		"defaults": {"port": 22, "user": "admin", "password": "pwd"},
		"web": {"enabled": true, "listen_addr": ":8080"},
		"routers": [{"name": "R1", "host": "10.0.0.1"}]
	}`
	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgMgr, err := config.NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	stateMgr, err := state.NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	run := runner.New(cfgMgr, stateMgr, slog.Default())
	sched, err := scheduler.New(nil, "", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	checker := &mockUpdateChecker{
		status: &updater.CheckResult{
			UpdateAvailable: true,
			LatestVersion:   "v3.0.0",
		},
	}

	srv, err := NewServer(cfgMgr, run, stateMgr, sched, checker, slog.Default())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/update/badge", nil)
			rr := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rr, req)
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/update/modal", nil)
			rr := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rr, req)
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/notices/dismiss", nil)
			rr := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rr, req)
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rr := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rr, req)
		}()
	}

	wg.Wait()
}

func TestServerDisabledUpdateChecker(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	cfgJSON := `{
		"config_version": 2,
		"binary_backup_dir": "./backups/binary",
		"export_dir": "./backups/exports",
		"defaults": {
			"user": "admin",
			"password": "pwd"
		},
		"routers": [
			{"name": "router1", "host": "192.168.1.1"}
		]
	}`
	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgMgr, err := config.NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	stateMgr, err := state.NewManager(filepath.Join(tempDir, "state.json"), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	run := runner.New(cfgMgr, stateMgr, slog.Default())
	sched, err := scheduler.New(nil, "", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	// Pass checker as nil (representing disabled update checks)
	srv, err := NewServer(cfgMgr, run, stateMgr, sched, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET / - index should render without update-badge-container
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /, got %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "id=\"update-badge-container\"") {
		t.Errorf("expected update-badge-container to be absent when update checker is disabled")
	}

	// 2. GET /update/badge - should return 204 No Content
	req = httptest.NewRequest(http.MethodGet, "/update/badge", nil)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content for /update/badge, got %d", rr.Code)
	}

	// 3. GET /update/modal - should return 404 Not Found
	req = httptest.NewRequest(http.MethodGet, "/update/modal", nil)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for /update/modal, got %d", rr.Code)
	}
}

func TestSecretsNotLeakedInWebUIAndPreservedOnEdit(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	stateFile := filepath.Join(tempDir, "state.json")

	secretDefaultPassword := "secret_def_device_pwd_000"
	secretDefaultPassphrase := "secret_def_pass_123"
	secretDefaultBackupPwd := "secret_def_backup_pwd_456"

	secretRouterPassword := "secret_r_device_pwd_111"
	secretRouterPassphrase := "secret_r_pass_789"
	secretRouterBackupPwd := "secret_r_backup_pwd_abc"

	cfgJSON := `{
		"config_version": 3,
		"binary_backup_dir": "` + tempDir + `/backups/binary",
		"export_dir": "` + tempDir + `/backups/exports",
		"state_file": "` + stateFile + `",
		"defaults": {
			"port": 22,
			"user": "admin",
			"password": "` + secretDefaultPassword + `",
			"private_key_path": "/path/to/def.key",
			"private_key_passphrase": "` + secretDefaultPassphrase + `",
			"encrypt_backup": true,
			"backup_password": "` + secretDefaultBackupPwd + `"
		},
		"web": {
			"enabled": true,
			"listen_addr": ":8080",
			"ssl_enabled": false
		},
		"routers": [
			{
				"name": "Router-Secured",
				"host": "10.0.0.99",
				"password": "` + secretRouterPassword + `",
				"private_key_path": "/path/to/custom.key",
				"private_key_passphrase": "` + secretRouterPassphrase + `",
				"encrypt_backup": true,
				"backup_password": "` + secretRouterBackupPwd + `"
			}
		]
	}`

	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgMgr, err := config.NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	stateMgr, err := state.NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	stateMgr.InitFromConfig(cfgMgr.Get().Routers, cfgMgr.Get().Defaults)
	run := runner.New(cfgMgr, stateMgr, slog.Default())
	sched, err := scheduler.New([]string{"03:00"}, "UTC", slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(cfgMgr, run, stateMgr, sched, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	// 1. Verify GET /settings/defaults does NOT leak any secrets in HTML
	reqDefModal := httptest.NewRequest(http.MethodGet, "/settings/defaults", nil)
	rrDefModal := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDefModal, reqDefModal)
	if rrDefModal.Code != http.StatusOK {
		t.Fatalf("GET /settings/defaults returned status %d", rrDefModal.Code)
	}
	bodyDef := rrDefModal.Body.String()
	if strings.Contains(bodyDef, secretDefaultPassword) {
		t.Errorf("GET /settings/defaults leaked default password in HTML!")
	}
	if strings.Contains(bodyDef, secretDefaultPassphrase) {
		t.Errorf("GET /settings/defaults leaked default private_key_passphrase in HTML!")
	}
	if strings.Contains(bodyDef, secretDefaultBackupPwd) {
		t.Errorf("GET /settings/defaults leaked default backup_password in HTML!")
	}
	if !strings.Contains(bodyDef, "•••••••• (сохранен)") {
		t.Errorf("expected placeholder '•••••••• (сохранен)' in defaults modal")
	}

	// 2. Submit POST /settings/defaults with empty password, passphrase and backup_password -> must preserve secrets!
	formDef := url.Values{}
	formDef.Set("port", "2222")
	formDef.Set("user", "admin_updated")
	formDef.Set("password", "")               // left empty on edit
	formDef.Set("private_key_path", "/path/to/def.key")
	formDef.Set("private_key_passphrase", "") // left empty on edit
	formDef.Set("encrypt_backup", "true")
	formDef.Set("backup_password", "")        // left empty on edit

	reqSaveDef := httptest.NewRequest(http.MethodPost, "/settings/defaults", strings.NewReader(formDef.Encode()))
	reqSaveDef.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrSaveDef := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrSaveDef, reqSaveDef)
	if rrSaveDef.Code != http.StatusOK {
		t.Fatalf("POST /settings/defaults failed with status %d: %s", rrSaveDef.Code, rrSaveDef.Body.String())
	}

	updatedDefaults := srv.cfgMgr.Get().Defaults
	if updatedDefaults.User != "admin_updated" {
		t.Errorf("expected user 'admin_updated', got %q", updatedDefaults.User)
	}
	if updatedDefaults.Password != secretDefaultPassword {
		t.Errorf("expected preserved Password %q, got %q", secretDefaultPassword, updatedDefaults.Password)
	}
	if updatedDefaults.PrivateKeyPassphrase != secretDefaultPassphrase {
		t.Errorf("expected preserved PrivateKeyPassphrase %q, got %q", secretDefaultPassphrase, updatedDefaults.PrivateKeyPassphrase)
	}
	if updatedDefaults.BackupPassword != secretDefaultBackupPwd {
		t.Errorf("expected preserved BackupPassword %q, got %q", secretDefaultBackupPwd, updatedDefaults.BackupPassword)
	}

	// 3. Verify GET /routers/new does NOT leak any secrets and indicates inherited defaults
	reqNewModal := httptest.NewRequest(http.MethodGet, "/routers/new", nil)
	rrNewModal := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrNewModal, reqNewModal)
	if rrNewModal.Code != http.StatusOK {
		t.Fatalf("GET /routers/new returned status %d", rrNewModal.Code)
	}
	bodyNew := rrNewModal.Body.String()
	if strings.Contains(bodyNew, secretDefaultPassword) || strings.Contains(bodyNew, secretDefaultPassphrase) || strings.Contains(bodyNew, secretDefaultBackupPwd) {
		t.Errorf("GET /routers/new leaked defaults secrets in HTML!")
	}
	if !strings.Contains(bodyNew, "•••••••• (из defaults)") {
		t.Errorf("expected placeholder '•••••••• (из defaults)' in new router modal")
	}

	// 4. Verify GET /routers/Router-Secured/edit does NOT leak router secrets
	reqEditModal := httptest.NewRequest(http.MethodGet, "/routers/Router-Secured/edit", nil)
	rrEditModal := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrEditModal, reqEditModal)
	if rrEditModal.Code != http.StatusOK {
		t.Fatalf("GET /routers/Router-Secured/edit returned status %d", rrEditModal.Code)
	}
	bodyRouter := rrEditModal.Body.String()
	if strings.Contains(bodyRouter, secretRouterPassword) {
		t.Errorf("GET /routers/Router-Secured/edit leaked router password in HTML!")
	}
	if strings.Contains(bodyRouter, secretRouterPassphrase) {
		t.Errorf("GET /routers/Router-Secured/edit leaked router private_key_passphrase in HTML!")
	}
	if strings.Contains(bodyRouter, secretRouterBackupPwd) {
		t.Errorf("GET /routers/Router-Secured/edit leaked router backup_password in HTML!")
	}
	if !strings.Contains(bodyRouter, "•••••••• (сохранен)") {
		t.Errorf("expected placeholder '•••••••• (сохранен)' in router edit modal")
	}

	// 5. Submit POST /routers (edit Router-Secured) with empty secrets -> must preserve existing!
	formRouter := url.Values{}
	formRouter.Set("name", "Router-Secured")
	formRouter.Set("host", "10.0.0.100") // updated host
	formRouter.Set("password", "")       // left empty
	formRouter.Set("private_key_path", "/path/to/custom.key")
	formRouter.Set("private_key_passphrase", "") // left empty
	formRouter.Set("encrypt_backup", "true")
	formRouter.Set("backup_password", "")        // left empty

	reqSaveRouter := httptest.NewRequest(http.MethodPost, "/routers", strings.NewReader(formRouter.Encode()))
	reqSaveRouter.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrSaveRouter := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrSaveRouter, reqSaveRouter)
	if rrSaveRouter.Code != http.StatusOK {
		t.Fatalf("POST /routers failed with status %d: %s", rrSaveRouter.Code, rrSaveRouter.Body.String())
	}

	savedRouter, found := srv.cfgMgr.GetRouter("Router-Secured")
	if !found {
		t.Fatalf("Router-Secured not found after update")
	}
	if savedRouter.Host != "10.0.0.100" {
		t.Errorf("expected host 10.0.0.100, got %s", savedRouter.Host)
	}
	if savedRouter.Password != secretRouterPassword {
		t.Errorf("expected preserved router Password %q, got %q", secretRouterPassword, savedRouter.Password)
	}
	if savedRouter.PrivateKeyPassphrase != secretRouterPassphrase {
		t.Errorf("expected preserved router PrivateKeyPassphrase %q, got %q", secretRouterPassphrase, savedRouter.PrivateKeyPassphrase)
	}
	if savedRouter.BackupPassword != secretRouterBackupPwd {
		t.Errorf("expected preserved router BackupPassword %q, got %q", secretRouterBackupPwd, savedRouter.BackupPassword)
	}

	// 6. Submit POST /routers with a NEW backup password and new device password -> must update!
	newDevicePwd := "brand_new_device_pwd_888"
	newBackupPwd := "brand_new_backup_pwd_999"
	formRouter.Set("password", newDevicePwd)
	formRouter.Set("backup_password", newBackupPwd)
	reqSaveRouter2 := httptest.NewRequest(http.MethodPost, "/routers", strings.NewReader(formRouter.Encode()))
	reqSaveRouter2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrSaveRouter2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrSaveRouter2, reqSaveRouter2)
	if rrSaveRouter2.Code != http.StatusOK {
		t.Fatalf("POST /routers with new password failed with status %d", rrSaveRouter2.Code)
	}

	savedRouter2, _ := srv.cfgMgr.GetRouter("Router-Secured")
	if savedRouter2.Password != newDevicePwd {
		t.Errorf("expected updated Password %q, got %q", newDevicePwd, savedRouter2.Password)
	}
	if savedRouter2.BackupPassword != newBackupPwd {
		t.Errorf("expected updated BackupPassword %q, got %q", newBackupPwd, savedRouter2.BackupPassword)
	}

	// Check that the new passwords are NOT rendered in the edit modal either
	reqEditModal2 := httptest.NewRequest(http.MethodGet, "/routers/Router-Secured/edit", nil)
	rrEditModal2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrEditModal2, reqEditModal2)
	if strings.Contains(rrEditModal2.Body.String(), newDevicePwd) {
		t.Errorf("new Password leaked in edit modal!")
	}
	if strings.Contains(rrEditModal2.Body.String(), newBackupPwd) {
		t.Errorf("new BackupPassword leaked in edit modal!")
	}

	// 7. Reset router password to defaults using "-"
	formRouter.Set("password", "-")
	reqSaveRouter3 := httptest.NewRequest(http.MethodPost, "/routers", strings.NewReader(formRouter.Encode()))
	reqSaveRouter3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrSaveRouter3 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrSaveRouter3, reqSaveRouter3)
	if rrSaveRouter3.Code != http.StatusOK {
		t.Fatalf("POST /routers with '-' password failed with status %d", rrSaveRouter3.Code)
	}

	savedRouter3, _ := srv.cfgMgr.GetRouter("Router-Secured")
	if savedRouter3.Password != "" {
		t.Errorf("expected cleared router Password, got %q", savedRouter3.Password)
	}

	// Modal now shows fallback to defaults
	reqEditModal3 := httptest.NewRequest(http.MethodGet, "/routers/Router-Secured/edit", nil)
	rrEditModal3 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrEditModal3, reqEditModal3)
	bodyEdit3 := rrEditModal3.Body.String()
	if !strings.Contains(bodyEdit3, "•••••••• (из defaults)") {
		t.Errorf("expected placeholder '•••••••• (из defaults)' after resetting router password with '-'")
	}

	// 8. Verify Table and Backups Modal never leak secrets
	reqTable := httptest.NewRequest(http.MethodGet, "/routers/table", nil)
	rrTable := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrTable, reqTable)
	bodyTable := rrTable.Body.String()
	for _, sec := range []string{secretDefaultPassword, secretDefaultPassphrase, secretDefaultBackupPwd, newDevicePwd, newBackupPwd} {
		if strings.Contains(bodyTable, sec) {
			t.Errorf("Table leaked secret %q in HTML!", sec)
		}
	}

	reqBackups := httptest.NewRequest(http.MethodGet, "/routers/Router-Secured/backups", nil)
	rrBackups := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrBackups, reqBackups)
	bodyBackups := rrBackups.Body.String()
	for _, sec := range []string{secretDefaultPassword, secretDefaultPassphrase, secretDefaultBackupPwd, newDevicePwd, newBackupPwd} {
		if strings.Contains(bodyBackups, sec) {
			t.Errorf("Backups modal leaked secret %q in HTML!", sec)
		}
	}
}



