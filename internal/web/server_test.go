package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/runner"
	"github.com/AndreyOkh/ros-fleet/internal/scheduler"
	"github.com/AndreyOkh/ros-fleet/internal/state"
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

	srv, err := NewServer(cfgMgr, run, stateMgr, sched, slog.Default())
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

