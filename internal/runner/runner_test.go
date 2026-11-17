package runner

import (
	"context"
	"log/slog"
	"testing"

	"github.com/username/real-mikrotik-backup/internal/config"
)

func TestRunnerContextCancellation(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &config.Config{
		BackupDir:        tempDir,
		Concurrency:      2,
		TimeoutSec:       5,
		ScheduleInterval: "1h",
		Routers: []config.RouterConfig{
			{Name: "R1", Host: "127.0.0.1", User: "admin", Password: "pwd"},
			{Name: "R2", Host: "127.0.0.2", User: "admin", Password: "pwd"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately before run

	r := New(cfg, slog.Default())
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
