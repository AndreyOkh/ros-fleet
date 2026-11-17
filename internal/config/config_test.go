package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	validJSON := `{
		"defaults": {
			"port": 2222,
			"user": "backup_global",
			"password": "default_pass",
			"insecure_ignore_host_key": true
		},
		"schedule": {
			"times": ["04:00", "16:00"],
			"timezone": "UTC"
		},
		"routers": [
			{
				"name": "Router-Office",
				"host": "192.168.88.1"
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
	if cfg.BinaryBackupDir != "./backups/binary" {
		t.Errorf("expected BinaryBackupDir to default to './backups/binary', got %q", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "./backups/exports" {
		t.Errorf("expected ExportDir to default to './backups/exports', got %q", cfg.ExportDir)
	}
	if cfg.StateFile != "./state.json" {
		t.Errorf("expected StateFile to default to './state.json', got %q", cfg.StateFile)
	}
	if cfg.Concurrency != 5 {
		t.Errorf("expected Concurrency to default to 5, got %d", cfg.Concurrency)
	}
	if cfg.TimeoutSec != 120 {
		t.Errorf("expected TimeoutSec to default to 120, got %d", cfg.TimeoutSec)
	}
	if len(cfg.Schedule.Times) != 2 || cfg.Schedule.Times[0] != "04:00" {
		t.Errorf("unexpected Schedule.Times: %v", cfg.Schedule.Times)
	}

	// Verify effective router inheritance
	eff := cfg.Routers[0].Effective(cfg.Defaults)
	if eff.Port != 2222 {
		t.Errorf("expected inherited port 2222, got %d", eff.Port)
	}
	if eff.User != "backup_global" {
		t.Errorf("expected inherited user 'backup_global', got %q", eff.User)
	}
	if eff.Password != "default_pass" {
		t.Errorf("expected inherited password 'default_pass', got %q", eff.Password)
	}
	if !eff.InsecureIgnoreHostKey {
		t.Errorf("expected inherited InsecureIgnoreHostKey to be true")
	}
}

func TestRouterConfigEffectiveOverrides(t *testing.T) {
	port := 2200
	user := "custom_user"
	pass := "custom_pass"
	ignoreHostKey := false
	sensitive := true

	r := RouterConfig{
		Name:                  "CustomRouter",
		Host:                  "10.0.0.1",
		Port:                  port,
		User:                  user,
		Password:              pass,
		InsecureIgnoreHostKey: &ignoreHostKey,
		ExportSensitive:       &sensitive,
	}

	def := DefaultsConfig{
		Port:                  22,
		User:                  "default_user",
		Password:              "default_pass",
		InsecureIgnoreHostKey: true,
		ExportSensitive:       false,
	}

	eff := r.Effective(def)
	if eff.Port != 2200 {
		t.Errorf("expected override port 2200, got %d", eff.Port)
	}
	if eff.User != "custom_user" {
		t.Errorf("expected override user 'custom_user', got %q", eff.User)
	}
	if eff.Password != "custom_pass" {
		t.Errorf("expected override pass 'custom_pass', got %q", eff.Password)
	}
	if eff.InsecureIgnoreHostKey != false {
		t.Errorf("expected override InsecureIgnoreHostKey false, got true")
	}
	if eff.ExportSensitive != true {
		t.Errorf("expected override ExportSensitive true, got false")
	}
}
