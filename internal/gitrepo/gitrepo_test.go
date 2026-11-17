package gitrepo

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

func TestNormalizeRSC(t *testing.T) {
	input := []byte(`# 2026-11-17 14:30:15 by RouterOS 7.16.1
# software id = 1234
/interface bridge add name=bridge1
`)

	normalized := string(NormalizeRSC(input))
	if strings.Contains(normalized, "2026-11-17") {
		t.Errorf("expected date/time to be stripped, got:\n%s", normalized)
	}
	if !strings.Contains(normalized, "# RouterOS 7.16.1") {
		t.Errorf("expected version to be preserved, got:\n%s", normalized)
	}
}

func TestGitCommitAndPushCycle(t *testing.T) {
	tempDir := t.TempDir()
	exportDir := filepath.Join(tempDir, "exports")

	cfg := config.GitConfig{
		Enabled:     true,
		Branch:      "main",
		AuthorName:  "Test Bot",
		AuthorEmail: "test@local",
		AutoPush:    false,
	}

	repo := New(exportDir, cfg, slog.Default())
	ctx := context.Background()

	// 1. Initial write of Router1.rsc (normalized)
	rsc1 := NormalizeRSC([]byte("# 2026-11-17 01:00:00 by RouterOS 7.16.1\n/system identity set name=Router1\n"))
	_ = os.MkdirAll(exportDir, 0o755)
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), rsc1, 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err := repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("first CommitAndPush failed: %v", err)
	}
	if !committed {
		t.Error("expected first cycle to commit")
	}

	status := repo.GetStatus()
	if status.LastCommitHash == "" {
		t.Error("expected non-empty commit hash")
	}
	if !strings.Contains(status.LastCommitMsg, "Router1") {
		t.Errorf("expected commit message to mention Router1, got %q", status.LastCommitMsg)
	}

	// 2. Second cycle: write identical config with different date/time in header
	rsc2 := NormalizeRSC([]byte("# 2026-11-17 02:00:00 by RouterOS 7.16.1\n/system identity set name=Router1\n"))
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), rsc2, 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err = repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("second CommitAndPush failed: %v", err)
	}
	if committed {
		t.Error("expected no commit when normalized content is unchanged")
	}

	// 3. Third cycle: genuine configuration change
	rsc3 := NormalizeRSC([]byte("# 2026-11-17 03:00:00 by RouterOS 7.16.1\n/system identity set name=Router1-Renamed\n"))
	if err := os.WriteFile(filepath.Join(exportDir, "Router1.rsc"), rsc3, 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err = repo.CommitAndPush(ctx)
	if err != nil {
		t.Fatalf("third CommitAndPush failed: %v", err)
	}
	if !committed {
		t.Error("expected commit on configuration change")
	}
}
