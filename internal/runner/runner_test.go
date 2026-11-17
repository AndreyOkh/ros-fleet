package runner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/state"
)

func TestRunnerContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	statePath := filepath.Join(tempDir, "state.json")

	cfgJSON := `{
		"backup_dir": "` + tempDir + `",
		"state_file": "` + statePath + `",
		"concurrency": 2,
		"timeout_sec": 5,
		"defaults": {
			"port": 22,
			"user": "admin",
			"password": "pwd"
		},
		"routers": [
			{"name": "R1", "host": "127.0.0.1"},
			{"name": "R2", "host": "127.0.0.2"}
		]
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgMgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	stateMgr, err := state.NewManager(statePath, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	r := New(cfgMgr, stateMgr, slog.Default())
	summary, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}

	if summary.TotalRouters != 2 {
		t.Errorf("TotalRouters = %d, want 2", summary.TotalRouters)
	}
	if summary.FailedCount != 2 {
		t.Errorf("FailedCount = %d, want 2", summary.FailedCount)
	}
}
