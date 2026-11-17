package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigLoadAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	validJSON := `{
		"routers": [
			{
				"name": "Router-Office",
				"host": "192.168.88.1",
				"user": "admin",
				"password": "secretpassword"
			}
		]
	}`

	if err := os.WriteFile(cfgFile, []byte(validJSON), 0o600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	// Verify defaults
	if cfg.BackupDir != "./backups" {
		t.Errorf("expected BackupDir to default to './backups', got %q", cfg.BackupDir)
	}
	if cfg.Concurrency != 5 {
		t.Errorf("expected Concurrency to default to 5, got %d", cfg.Concurrency)
	}
	if cfg.TimeoutSec != 120 {
		t.Errorf("expected TimeoutSec to default to 120, got %d", cfg.TimeoutSec)
	}
	if cfg.ScheduleInterval != "24h" {
		t.Errorf("expected ScheduleInterval to default to '24h', got %q", cfg.ScheduleInterval)
	}
	if cfg.Web.ListenAddr != ":8080" {
		t.Errorf("expected Web.ListenAddr to default to ':8080', got %q", cfg.Web.ListenAddr)
	}
	if cfg.ParsedScheduleInterval() != 24*time.Hour {
		t.Errorf("expected ParsedScheduleInterval to be 24h, got %v", cfg.ParsedScheduleInterval())
	}
	if cfg.Routers[0].Port != 22 {
		t.Errorf("expected Router Port to default to 22, got %d", cfg.Routers[0].Port)
	}
	if !cfg.Routers[0].IsEnabled() {
		t.Errorf("expected Router Enabled to default to true")
	}
}

func TestConfigManagerOperations(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	initialJSON := `{
		"routers": [
			{
				"name": "Router1",
				"host": "192.168.1.1",
				"user": "admin",
				"password": "pwd"
			}
		]
	}`

	if err := os.WriteFile(cfgFile, []byte(initialJSON), 0o600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	mgr, err := NewManager(cfgFile)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Test Toggle
	newState, err := mgr.ToggleRouter("Router1")
	if err != nil {
		t.Fatalf("ToggleRouter failed: %v", err)
	}
	if newState != false {
		t.Errorf("expected toggled state to be false, got %v", newState)
	}

	// Verify reload from disk
	reloadedCfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("failed to reload config from disk: %v", err)
	}
	if reloadedCfg.Routers[0].IsEnabled() != false {
		t.Errorf("persisted config did not reflect toggled state")
	}

	// Test Upsert (add new router)
	newRouter := RouterConfig{
		Name:     "Router2",
		Host:     "192.168.2.1",
		User:     "backup",
		Password: "secret",
	}
	if err := mgr.UpsertRouter(newRouter); err != nil {
		t.Fatalf("UpsertRouter failed: %v", err)
	}

	routers := mgr.ListRouters()
	if len(routers) != 2 {
		t.Fatalf("expected 2 routers, got %d", len(routers))
	}

	// Test Delete
	if err := mgr.DeleteRouter("Router1"); err != nil {
		t.Fatalf("DeleteRouter failed: %v", err)
	}
	if len(mgr.ListRouters()) != 1 {
		t.Fatalf("expected 1 router after deletion, got %d", len(mgr.ListRouters()))
	}
}
