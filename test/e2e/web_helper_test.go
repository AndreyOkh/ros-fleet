package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

// GetFreePort allocates an ephemeral free TCP port on localhost.
func GetFreePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("GetFreePort: failed to listen on ephemeral port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type RunningDaemon struct {
	T          *testing.T
	Cmd        *exec.Cmd
	Port       int
	BaseURL    string
	Stdout     *bytes.Buffer
	Stderr     *bytes.Buffer
	ConfigPath string
	Cancel     context.CancelFunc
	Stopped    bool
	mu         sync.Mutex
}

// StartDaemon launches ros-fleet in daemon mode with web server enabled.
func (h *Harness) StartDaemon(configPath string, extraEnv ...string) *RunningDaemon {
	port := GetFreePort(h.T)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Update config file's web.listen_addr to 127.0.0.1:<port>
	_, rawMap := h.ReadConfigFile(configPath)
	webSection, ok := rawMap["web"].(map[string]any)
	if !ok {
		webSection = make(map[string]any)
	}
	webSection["enabled"] = true
	webSection["listen_addr"] = fmt.Sprintf("127.0.0.1:%d", port)
	rawMap["web"] = webSection
	h.WriteJSONFile(configPath, rawMap)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, h.BinPath, "-config", configPath)
	cmd.Dir = h.TempDir

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	env := append(h.Env, extraEnv...)
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		cancel()
		h.T.Fatalf("StartDaemon: cmd.Start failed: %v", err)
	}

	daemon := &RunningDaemon{
		T:          h.T,
		Cmd:        cmd,
		Port:       port,
		BaseURL:    baseURL,
		Stdout:     &stdoutBuf,
		Stderr:     &stderrBuf,
		ConfigPath: configPath,
		Cancel:     cancel,
	}

	h.Daemon = daemon

	// Poll readiness endpoint
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !ready {
		daemon.Stop()
		h.T.Fatalf("StartDaemon: server failed to become ready at %s within 5s.\nStdout: %s\nStderr: %s",
			baseURL, stdoutBuf.String(), stderrBuf.String())
	}

	return daemon
}

func (d *RunningDaemon) Stop() {
	d.mu.Lock()
	if d.Stopped {
		d.mu.Unlock()
		return
	}
	d.Stopped = true
	d.mu.Unlock()

	if d.Cmd != nil && d.Cmd.Process != nil {
		// Send graceful SIGINT / SIGTERM
		_ = d.Cmd.Process.Signal(syscall.SIGINT)

		done := make(chan error, 1)
		go func() {
			done <- d.Cmd.Wait()
		}()

		select {
		case <-done:
			// Terminated cleanly
		case <-time.After(300 * time.Millisecond):
			// Force kill
			_ = d.Cmd.Process.Kill()
			<-done
		}
	}
	if d.Cancel != nil {
		d.Cancel()
	}
}

func (d *RunningDaemon) Client() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func (d *RunningDaemon) Get(path string) (*http.Response, string) {
	client := d.Client()
	resp, err := client.Get(d.BaseURL + path)
	if err != nil {
		d.T.Fatalf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (d *RunningDaemon) GetAuth(path, user, pass string) (*http.Response, string) {
	client := d.Client()
	req, err := http.NewRequest(http.MethodGet, d.BaseURL+path, nil)
	if err != nil {
		d.T.Fatalf("GetAuth new request failed: %v", err)
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		d.T.Fatalf("GET auth %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (d *RunningDaemon) Post(path string, contentType string, body io.Reader) (*http.Response, string) {
	client := d.Client()
	resp, err := client.Post(d.BaseURL+path, contentType, body)
	if err != nil {
		d.T.Fatalf("POST %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, string(respBody)
}

func (d *RunningDaemon) PostForm(path string, form url.Values) (*http.Response, string) {
	client := d.Client()
	resp, err := client.PostForm(d.BaseURL+path, form)
	if err != nil {
		d.T.Fatalf("POST form %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, string(respBody)
}

func (d *RunningDaemon) GetUpdateBadge() (int, string) {
	resp, body := d.Get("/update/badge")
	return resp.StatusCode, body
}

func (d *RunningDaemon) GetUpdateModal() (int, string) {
	resp, body := d.Get("/update/modal")
	return resp.StatusCode, body
}

func (d *RunningDaemon) DismissNotice() (int, string) {
	resp, body := d.Post("/notices/dismiss", "application/x-www-form-urlencoded", nil)
	return resp.StatusCode, body
}
