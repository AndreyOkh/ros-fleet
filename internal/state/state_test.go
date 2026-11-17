package state

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

func TestStateManagerPersistence(t *testing.T) {
	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "state.json")

	// 1. Initialize empty
	sm, err := NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	routers := []config.RouterConfig{
		{Name: "Router1", Host: "192.168.1.1"},
	}
	defaults := config.DefaultsConfig{Port: 22, User: "admin"}
	sm.InitFromConfig(routers, defaults)

	// Check device initialized
	st, found := sm.Get("Router1")
	if !found {
		t.Fatal("expected Router1 in state")
	}
	if st.Status != StatusNeverRun {
		t.Errorf("expected status 'never', got %s", st.Status)
	}

	// 2. Update telemetry and backup result
	sm.UpdateDeviceInfo("Router1", "RB4011iGS+", "7.16.1", "2w3d")
	sm.UpdateBackupResult("Router1", true, nil, 1024, 2048)

	// Verify state file was written to disk
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("failed reading state file from disk: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("state file on disk is empty")
	}

	// 3. Re-instantiate manager and verify persistence
	sm2, err := NewManager(stateFile, slog.Default())
	if err != nil {
		t.Fatalf("NewManager reload failed: %v", err)
	}

	st2, found := sm2.Get("Router1")
	if !found {
		t.Fatal("Router1 not found after reload")
	}
	if st2.Model != "RB4011iGS+" || st2.Firmware != "7.16.1" {
		t.Errorf("unexpected model/firmware after reload: %s / %s", st2.Model, st2.Firmware)
	}
	if st2.Status != StatusSuccess {
		t.Errorf("unexpected status: %s", st2.Status)
	}
	if st2.LastBackupRscSize != 1024 || st2.LastBackupBinSize != 2048 {
		t.Errorf("unexpected sizes: %d, %d", st2.LastBackupRscSize, st2.LastBackupBinSize)
	}
}
