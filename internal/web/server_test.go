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
	"github.com/AndreyOkh/ros-fleet/internal/state"
)

func setupTestServer(t *testing.T, authUser, authPass string) (*Server, string) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	cfgJSON := `{
		"backup_dir": "` + tempDir + `/backups",
		"web": {
			"enabled": true,
			"listen_addr": ":8080",
			"auth_user": "` + authUser + `",
			"auth_password": "` + authPass + `"
		},
		"routers": [
			{
				"name": "Router-Test",
				"host": "192.168.1.1",
				"user": "admin",
				"password": "secret"
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

	reg := state.NewRegistry()
	reg.InitFromConfig(cfgMgr.Get().Routers)

	run := runner.New(cfgMgr, reg, slog.Default())

	srv, err := NewServer(cfgMgr, run, reg, slog.Default())
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
	if !strings.Contains(rrCSS.Body.String(), "--bg-color") {
		t.Errorf("expected style.css content")
	}

	// 3. Test GET /static/htmx.min.js
	reqJS := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	rrJS := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrJS, reqJS)

	if rrJS.Code != http.StatusOK {
		t.Errorf("GET /static/htmx.min.js returned status %d, want %d", rrJS.Code, http.StatusOK)
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

	// Test Add router
	form := url.Values{}
	form.Set("name", "Router-New")
	form.Set("host", "10.0.0.1")
	form.Set("port", "22")
	form.Set("user", "admin")
	form.Set("password", "pass123")

	reqAdd := httptest.NewRequest(http.MethodPost, "/routers", strings.NewReader(form.Encode()))
	reqAdd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrAdd := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrAdd, reqAdd)

	if rrAdd.Code != http.StatusOK {
		t.Errorf("add router returned code %d, want %d", rrAdd.Code, http.StatusOK)
	}
	if !strings.Contains(rrAdd.Body.String(), "Router-New") {
		t.Errorf("expected table to include 'Router-New'")
	}

	// Test Delete router
	reqDel := httptest.NewRequest(http.MethodDelete, "/routers/Router-New", nil)
	rrDel := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrDel, reqDel)

	if rrDel.Code != http.StatusOK {
		t.Errorf("delete router returned code %d, want %d", rrDel.Code, http.StatusOK)
	}
	if strings.Contains(rrDel.Body.String(), "Router-New") {
		t.Errorf("expected table to omit deleted router")
	}
}

func TestServerBasicAuth(t *testing.T) {
	srv, _ := setupTestServer(t, "admin", "securepassword")

	// Without auth
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", rr.Code)
	}

	// With correct auth
	reqAuth := httptest.NewRequest(http.MethodGet, "/", nil)
	reqAuth.SetBasicAuth("admin", "securepassword")
	rrAuth := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rrAuth, reqAuth)

	if rrAuth.Code != http.StatusOK {
		t.Errorf("expected status 200 OK with valid credentials, got %d", rrAuth.Code)
	}
}
