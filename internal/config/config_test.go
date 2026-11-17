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
	if cfg.ParsedScheduleInterval() != 24*time.Hour {
		t.Errorf("expected ParsedScheduleInterval to be 24h, got %v", cfg.ParsedScheduleInterval())
	}
	if cfg.Routers[0].Port != 22 {
		t.Errorf("expected Router Port to default to 22, got %d", cfg.Routers[0].Port)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "empty routers",
			json:    `{"routers": []}`,
			wantErr: true,
		},
		{
			name: "invalid schedule interval",
			json: `{
				"schedule_interval": "invalid_interval",
				"routers": [{"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}]
			}`,
			wantErr: true,
		},
		{
			name: "duplicate router names",
			json: `{
				"routers": [
					{"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"},
					{"name": "R1", "host": "10.0.0.2", "user": "admin", "password": "pwd"}
				]
			}`,
			wantErr: true,
		},
		{
			name: "missing router auth",
			json: `{
				"routers": [
					{"name": "R1", "host": "10.0.0.1", "user": "admin"}
				]
			}`,
			wantErr: true,
		},
		{
			name: "telegram enabled but missing token",
			json: `{
				"telegram": {"enabled": true, "chat_id": "12345"},
				"routers": [{"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}]
			}`,
			wantErr: true,
		},
		{
			name: "encrypt backup true but missing backup password",
			json: `{
				"routers": [
					{"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd", "encrypt_backup": true}
				]
			}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			cfgPath := filepath.Join(tempDir, "test.json")
			_ = os.WriteFile(cfgPath, []byte(tt.json), 0o600)

			_, err := Load(cfgPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestHostKeyResolution(t *testing.T) {
	trueVal := true
	falseVal := false

	r1 := RouterConfig{InsecureIgnoreHostKey: &trueVal}
	if !r1.ShouldIgnoreHostKey(false) {
		t.Errorf("expected router override true to win over global false")
	}

	r2 := RouterConfig{InsecureIgnoreHostKey: &falseVal}
	if r2.ShouldIgnoreHostKey(true) {
		t.Errorf("expected router override false to win over global true")
	}

	r3 := RouterConfig{InsecureIgnoreHostKey: nil}
	if !r3.ShouldIgnoreHostKey(true) {
		t.Errorf("expected global true when router override is nil")
	}
	if r3.ShouldIgnoreHostKey(false) {
		t.Errorf("expected global false when router override is nil")
	}
}
