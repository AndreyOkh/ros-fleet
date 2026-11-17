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
	if !strings.Contains(rrGit.Body.String(), "git-status-badge") {
		t.Errorf("expected git status HTML to contain 'git-status-badge'")
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
