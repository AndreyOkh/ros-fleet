package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateV2ToV3(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")

	// Raw v2 configuration without ssl_enabled
	v2JSON := `{
  "config_version": 2,
  "binary_backup_dir": "./backups/binary",
  "export_dir": "./backups/exports",
  "state_file": "./state.json",
  "retention_days": 30,
  "concurrency": 5,
  "timeout_sec": 120,
  "schedule": {
    "times": ["03:00"],
    "timezone": "Local"
  },
  "defaults": {
    "port": 22,
    "user": "admin",
    "password": "default_password"
  },
  "web": {
    "enabled": true,
    "listen_addr": ":8080"
  },
  "routers": [
    {
      "name": "Router1",
      "host": "192.168.88.1"
    }
  ]
}`

	if err := os.WriteFile(cfgPath, []byte(v2JSON), 0o600); err != nil {
		t.Fatalf("failed to write test v2 config: %v", err)
	}

	mgr, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed to migrate v2 config: %v", err)
	}

	// 1. Verify backup .v2.bak was created
	bakPath := cfgPath + ".v2.bak"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("expected backup file %q: %v", bakPath, err)
	}
	if string(bakData) != v2JSON {
		t.Errorf("backup file content does not match original v2 json")
	}

	// 2. Verify in-memory config
	cfg := mgr.Get()
	if cfg.ConfigVersion != 3 {
		t.Errorf("expected ConfigVersion 3, got %d", cfg.ConfigVersion)
	}
	if !cfg.IsSSLEnabled() {
		t.Errorf("expected ssl_enabled to be true by default in v3, got false")
	}
	if cfg.Web.SSLEnabled == nil || !*cfg.Web.SSLEnabled {
		t.Errorf("expected cfg.Web.SSLEnabled pointer to be true")
	}

	// 3. Verify disk config
	diskData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read migrated config from disk: %v", err)
	}
	diskStr := string(diskData)
	if !strings.Contains(diskStr, `"config_version": 3`) {
		t.Errorf("expected disk config to have config_version 3, got:\n%s", diskStr)
	}
	if !strings.Contains(diskStr, `"ssl_enabled": true`) {
		t.Errorf("expected disk config to contain 'ssl_enabled': true, got:\n%s", diskStr)
	}

	// 4. Verify MigrationNotice
	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected MigrationNotice to be present")
	}
	if !notice.Migrated {
		t.Errorf("expected Migrated to be true")
	}
	if notice.OldVersion != 2 {
		t.Errorf("expected OldVersion 2, got %d", notice.OldVersion)
	}
	if notice.NewVersion != 3 {
		t.Errorf("expected NewVersion 3, got %d", notice.NewVersion)
	}
	if !strings.Contains(notice.Message, "ssl_enabled") {
		t.Errorf("expected MigrationNotice message to mention ssl_enabled, got %q", notice.Message)
	}
}

func TestMigrateV1ToV3(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config_v1.json")

	// Raw v1 config without config_version and with legacy backup_dir
	v1JSON := `{
  "backup_dir": "/var/backups",
  "state_file": "./state.json",
  "schedule": {
    "times": ["04:00"]
  },
  "defaults": {
    "user": "admin",
    "password": "secret"
  },
  "web": {
    "enabled": true
  },
  "routers": [
    {
      "name": "r1",
      "host": "10.0.0.1"
    }
  ]
}`

	if err := os.WriteFile(cfgPath, []byte(v1JSON), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed on v1 config: %v", err)
	}

	// 1. Verify backup .v1.bak
	bakPath := cfgPath + ".v1.bak"
	if _, err := os.Stat(bakPath); err != nil {
		t.Fatalf("expected .v1.bak backup file: %v", err)
	}

	// 2. Verify v1 migration (backup_dir -> binary & export) + v3 migration (ssl_enabled)
	cfg := mgr.Get()
	if cfg.ConfigVersion != 3 {
		t.Errorf("expected ConfigVersion 3, got %d", cfg.ConfigVersion)
	}
	if cfg.BinaryBackupDir != "/var/backups/binary" {
		t.Errorf("expected BinaryBackupDir '/var/backups/binary', got %q", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "/var/backups/exports" {
		t.Errorf("expected ExportDir '/var/backups/exports', got %q", cfg.ExportDir)
	}
	if !cfg.IsSSLEnabled() {
		t.Errorf("expected IsSSLEnabled to be true after migration")
	}
}

func TestSSLValidation(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "invalid_ssl.json")

	// Only ssl_cert_file without ssl_key_file
	invalidJSON := `{
  "config_version": 3,
  "binary_backup_dir": "./bin",
  "export_dir": "./exp",
  "state_file": "./state.json",
  "defaults": {"user": "admin", "password": "123"},
  "web": {
    "enabled": true,
    "ssl_enabled": true,
    "ssl_cert_file": "/path/to/cert.pem",
    "ssl_key_file": ""
  },
  "routers": [{"name": "r1", "host": "10.0.0.1"}]
}`

	if err := os.WriteFile(cfgPath, []byte(invalidJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewManager(cfgPath)
	if err == nil {
		t.Fatalf("expected error when only ssl_cert_file is specified, got nil")
	}
	if !strings.Contains(err.Error(), "both 'web.ssl_cert_file' and 'web.ssl_key_file' must be specified") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify file was NOT overwritten
	content, _ := os.ReadFile(cfgPath)
	if string(content) != invalidJSON {
		t.Errorf("config file was modified despite validation failure")
	}

	// Only ssl_key_file without ssl_cert_file
	invalidKeyJSON := strings.Replace(invalidJSON, `"ssl_cert_file": "/path/to/cert.pem"`, `"ssl_cert_file": ""`, 1)
	invalidKeyJSON = strings.Replace(invalidKeyJSON, `"ssl_key_file": ""`, `"ssl_key_file": "/path/to/key.pem"`, 1)
	if err := os.WriteFile(cfgPath, []byte(invalidKeyJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = NewManager(cfgPath)
	if err == nil {
		t.Fatalf("expected error when only ssl_key_file is specified, got nil")
	}
	if !strings.Contains(err.Error(), "both 'web.ssl_cert_file' and 'web.ssl_key_file' must be specified") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExplicitSSLDisabledPreserved(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "ssl_disabled.json")

	disabledJSON := `{
  "config_version": 3,
  "binary_backup_dir": "./bin",
  "export_dir": "./exp",
  "state_file": "./state.json",
  "defaults": {"user": "admin", "password": "123"},
  "web": {
    "enabled": true,
    "ssl_enabled": false
  },
  "routers": [{"name": "r1", "host": "10.0.0.1"}]
}`

	if err := os.WriteFile(cfgPath, []byte(disabledJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.IsSSLEnabled() {
		t.Errorf("expected IsSSLEnabled to be false when explicitly disabled in JSON")
	}
	if cfg.Web.SSLEnabled == nil || *cfg.Web.SSLEnabled != false {
		t.Errorf("expected SSLEnabled to remain false, got %v", cfg.Web.SSLEnabled)
	}

	// Test OverrideSSLEnabled
	mgr.OverrideSSLEnabled(true)
	if !mgr.Get().IsSSLEnabled() {
		t.Errorf("expected OverrideSSLEnabled(true) to enable SSL")
	}
}

func TestUpdateWebCertFiles(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")

	initialJSON := `{
  "config_version": 3,
  "binary_backup_dir": "./bin",
  "export_dir": "./exp",
  "state_file": "./state.json",
  "defaults": {"user": "admin", "password": "123"},
  "web": {
    "enabled": true,
    "ssl_enabled": true
  },
  "routers": [{"name": "r1", "host": "10.0.0.1"}]
}`

	if err := os.WriteFile(cfgPath, []byte(initialJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	certPath := filepath.Join(tempDir, "certs", "ros-fleet.crt")
	keyPath := filepath.Join(tempDir, "certs", "ros-fleet.key")

	if err := mgr.UpdateWebCertFiles(certPath, keyPath); err != nil {
		t.Fatalf("UpdateWebCertFiles failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Web.SSLCertFile != certPath || cfg.Web.SSLKeyFile != keyPath {
		t.Errorf("in-memory paths not updated: cert=%q, key=%q", cfg.Web.SSLCertFile, cfg.Web.SSLKeyFile)
	}

	// Verify on disk
	diskData, _ := os.ReadFile(cfgPath)
	diskStr := string(diskData)
	if !strings.Contains(diskStr, certPath) || !strings.Contains(diskStr, keyPath) {
		t.Errorf("disk config does not contain updated paths:\n%s", diskStr)
	}
}
