package gitrepo

import (
	"context"
	"log/slog"
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

func TestGitSyncConfig(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "git-configs")

	cfg := config.GitConfig{
		Enabled:     true,
		RepoDir:     repoDir,
		Branch:      "main",
		AuthorName:  "Test Bot",
		AuthorEmail: "test@local",
	}

	repo := New(cfg, slog.Default())
	ctx := context.Background()

	// 1. First sync -> should commit
	rsc1 := []byte("# 2026-11-17 01:00:00 by RouterOS 7.16.1\n/system identity set name=Router1\n")
	committed, err := repo.SyncConfig(ctx, "Router1", rsc1)
	if err != nil {
		t.Fatalf("first SyncConfig failed: %v", err)
	}
	if !committed {
		t.Error("expected first sync to create commit")
	}

	// 2. Second sync with identical config but DIFFERENT timestamp -> should NOT commit
	rsc2 := []byte("# 2026-11-17 02:00:00 by RouterOS 7.16.1\n/system identity set name=Router1\n")
	committed, err = repo.SyncConfig(ctx, "Router1", rsc2)
	if err != nil {
		t.Fatalf("second SyncConfig failed: %v", err)
	}
	if committed {
		t.Error("expected second sync with identical config not to commit")
	}

	// 3. Third sync with genuine change -> should commit
	rsc3 := []byte("# 2026-11-17 03:00:00 by RouterOS 7.16.1\n/system identity set name=Router1-Updated\n")
	committed, err = repo.SyncConfig(ctx, "Router1", rsc3)
	if err != nil {
		t.Fatalf("third SyncConfig failed: %v", err)
	}
	if !committed {
		t.Error("expected third sync with changed config to commit")
	}
}
