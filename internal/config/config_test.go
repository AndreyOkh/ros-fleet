package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestRouterConfigCloneAndDeepCopy(t *testing.T) {
	enabled := true
	ignoreKey := false
	sensitive := true
	encrypt := false

	original := RouterConfig{
		Enabled:               &enabled,
		Name:                  "Router1",
		Host:                  "192.168.1.1",
		InsecureIgnoreHostKey: &ignoreKey,
		ExportSensitive:       &sensitive,
		EncryptBackup:         &encrypt,
	}

	cloned := original.Clone()

	// Modifying cloned pointers must not alter original
	*cloned.Enabled = false
	*cloned.InsecureIgnoreHostKey = true
	*cloned.ExportSensitive = false
	*cloned.EncryptBackup = true

	if *original.Enabled != true {
		t.Errorf("original Enabled was mutated: got %v", *original.Enabled)
	}
	if *original.InsecureIgnoreHostKey != false {
		t.Errorf("original InsecureIgnoreHostKey was mutated: got %v", *original.InsecureIgnoreHostKey)
	}
	if *original.ExportSensitive != true {
		t.Errorf("original ExportSensitive was mutated: got %v", *original.ExportSensitive)
	}
	if *original.EncryptBackup != false {
		t.Errorf("original EncryptBackup was mutated: got %v", *original.EncryptBackup)
	}
}

func TestActualConfigFile(t *testing.T) {
	if _, err := os.Stat("../../config.json"); os.IsNotExist(err) {
		t.Skip("config.json not present, skipping test")
	}

	cfg, err := Load("../../config.json")
	if err != nil {
		t.Fatalf("failed to load actual config.json: %v", err)
	}

	if len(cfg.Routers) == 0 {
		t.Fatalf("expected routers in config.json, got 0")
	}
	t.Logf("Successfully loaded and validated config.json with %d routers", len(cfg.Routers))
}

func TestGitConfigAccessTokenAndEnvFallback(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	rawJSON := `{
		"git": {
			"enabled": true,
			"remote_url": "https://github.com/myorg/configs.git",
			"access_token": "ghp_TestExplicitToken123"
		},
		"routers": [
			{"name": "R1", "host": "192.168.1.1", "user": "admin", "password": "pwd"}
		]
	}`

	if err := os.WriteFile(cfgFile, []byte(rawJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Git.AccessToken != "ghp_TestExplicitToken123" {
		t.Errorf("expected AccessToken 'ghp_TestExplicitToken123', got %q", cfg.Git.AccessToken)
	}

	// Test environment variable fallback
	cfgFileEnv := filepath.Join(tempDir, "config_env.json")
	rawJSONNoToken := `{
		"git": {
			"enabled": true
		},
		"routers": [
			{"name": "R1", "host": "192.168.1.1", "user": "admin", "password": "pwd"}
		]
	}`
	if err := os.WriteFile(cfgFileEnv, []byte(rawJSONNoToken), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_ACCESS_TOKEN", "ghp_EnvVarToken456")
	t.Setenv("GIT_REMOTE_URL", "https://gitlab.com/myorg/configs.git")

	cfgEnv, err := Load(cfgFileEnv)
	if err != nil {
		t.Fatalf("Load with env failed: %v", err)
	}
	if cfgEnv.Git.AccessToken != "ghp_EnvVarToken456" {
		t.Errorf("expected env fallback token 'ghp_EnvVarToken456', got %q", cfgEnv.Git.AccessToken)
	}
	if cfgEnv.Git.RemoteURL != "https://gitlab.com/myorg/configs.git" {
		t.Errorf("expected env fallback remote_url 'https://gitlab.com/myorg/configs.git', got %q", cfgEnv.Git.RemoteURL)
	}
}

func TestWriteDirect(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "direct_config.json")
	data := []byte(`{"test": "fallback"}` + "\n")

	if err := writeDirect(filePath, data); err != nil {
		t.Fatalf("writeDirect failed: %v", err)
	}

	read, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed reading direct file: %v", err)
	}
	if string(read) != string(data) {
		t.Errorf("got %q, want %q", string(read), string(data))
	}
}

func TestConfigMigration_V1toV2_ByteVerbatimBackup(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	// Raw v1 configuration with deprecated backup_dir, without config_version or section defaults
	rawV1 := `{
  "backup_dir": "/legacy/router_backups",
  "routers": [
    {
      "name": "Router-V1",
      "host": "192.168.1.1",
      "user": "admin",
      "password": "secret_v1_password"
    }
  ]
}`
	originalBytes := []byte(rawV1)
	if err := os.WriteFile(cfgFile, originalBytes, 0o600); err != nil {
		t.Fatalf("failed to write v1 config: %v", err)
	}

	mgr, err := NewManager(cfgFile)
	if err != nil {
		t.Fatalf("NewManager failed to migrate v1 config: %v", err)
	}

	// 1. Verify byte-verbatim backup exists
	backupPath := cfgFile + ".v1.bak"
	bakStat, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("backup file was not created at %q: %v", backupPath, err)
	}
	if bakStat.Mode().Perm() != 0o600 {
		t.Errorf("expected backup file permissions 0600, got %o", bakStat.Mode().Perm())
	}

	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed to read backup file: %v", err)
	}
	if !bytes.Equal(backupBytes, originalBytes) {
		t.Errorf("backup file content is not byte-verbatim identical to original:\ngot  %s\nwant %s", string(backupBytes), string(originalBytes))
	}

	// 2. Verify in-memory and on-disk v2 configuration
	cfg := mgr.Get()
	if cfg.ConfigVersion != CurrentConfigVersion {
		t.Errorf("expected ConfigVersion %d, got %d", CurrentConfigVersion, cfg.ConfigVersion)
	}
	if cfg.BinaryBackupDir != "/legacy/router_backups/binary" {
		t.Errorf("expected BinaryBackupDir '/legacy/router_backups/binary', got %q", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "/legacy/router_backups/exports" {
		t.Errorf("expected ExportDir '/legacy/router_backups/exports', got %q", cfg.ExportDir)
	}
	if cfg.BackupDir != "" {
		t.Errorf("expected BackupDir to be empty after migration, got %q", cfg.BackupDir)
	}

	// Verify section defaults were populated
	if len(cfg.Schedule.Times) == 0 || cfg.Schedule.Times[0] != "03:00" {
		t.Errorf("expected default schedule times, got %v", cfg.Schedule.Times)
	}
	if cfg.Web.ListenAddr != ":8080" {
		t.Errorf("expected default web listen_addr ':8080', got %q", cfg.Web.ListenAddr)
	}
	if cfg.Defaults.Port != 22 {
		t.Errorf("expected default port 22, got %d", cfg.Defaults.Port)
	}
	if cfg.Git.Branch != "main" {
		t.Errorf("expected default git branch 'main', got %q", cfg.Git.Branch)
	}

	// Verify disk file has v2 format and omits backup_dir
	diskBytes, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("failed to read migrated config file on disk: %v", err)
	}
	diskContent := string(diskBytes)
	if strings.Contains(diskContent, `"backup_dir"`) {
		t.Errorf("migrated config on disk should not contain 'backup_dir', but found it:\n%s", diskContent)
	}
	if !strings.Contains(diskContent, `"config_version": 2`) {
		t.Errorf("migrated config on disk should contain 'config_version': 2, but found:\n%s", diskContent)
	}

	// 3. Verify MigrationNotice
	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected MigrationNotice, got nil")
	}
	if !notice.Migrated {
		t.Errorf("expected notice.Migrated to be true")
	}
	if notice.OldVersion != 1 {
		t.Errorf("expected OldVersion 1, got %d", notice.OldVersion)
	}
	if notice.NewVersion != CurrentConfigVersion {
		t.Errorf("expected NewVersion %d, got %d", CurrentConfigVersion, notice.NewVersion)
	}
	if notice.BackupPath != backupPath {
		t.Errorf("expected BackupPath %q, got %q", backupPath, notice.BackupPath)
	}
	if len(notice.UnknownKeys) != 0 {
		t.Errorf("expected no unknown keys, got %v", notice.UnknownKeys)
	}
	if notice.Message == "" {
		t.Errorf("expected non-empty notice Message")
	}
}

func TestConfigMigration_DeprecatedBackupDirExplicitOverride(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	// Raw v1 with backup_dir and explicit binary_backup_dir
	raw := `{
  "backup_dir": "/legacy",
  "binary_backup_dir": "/custom/binary",
  "routers": [
    {"name": "R1", "host": "1.1.1.1", "user": "u", "password": "p"}
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgFile)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.BinaryBackupDir != "/custom/binary" {
		t.Errorf("expected preserved BinaryBackupDir '/custom/binary', got %q", cfg.BinaryBackupDir)
	}
	if cfg.ExportDir != "/legacy/exports" {
		t.Errorf("expected ExportDir derived from backup_dir '/legacy/exports', got %q", cfg.ExportDir)
	}
	if cfg.BackupDir != "" {
		t.Errorf("expected cleared BackupDir, got %q", cfg.BackupDir)
	}
}

func TestConfigIncompatibleVersion(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	rawIncompatible := `{
  "config_version": 99,
  "routers": [
    {"name": "R1", "host": "1.1.1.1", "user": "admin", "password": "pwd"}
  ]
}`
	originalBytes := []byte(rawIncompatible)
	if err := os.WriteFile(cfgFile, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewManager(cfgFile)
	if err == nil {
		t.Fatalf("expected error for unsupported config_version 99, got nil")
	}

	expectedSubstr := "unsupported configuration version 99"
	if !strings.Contains(err.Error(), expectedSubstr) {
		t.Errorf("error %q does not contain %q", err.Error(), expectedSubstr)
	}

	// Verify no backup was created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".bak") {
			t.Errorf("no .bak file should be created for incompatible version, found: %s", f.Name())
		}
	}

	// Verify original file on disk was not modified
	readBytes, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readBytes, originalBytes) {
		t.Errorf("config file was modified despite incompatible version error")
	}
}

func TestConfigUnknownKeysCollection(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	// v2 config with multiple unknown keys across top-level and nested structs
	v2WithUnknown := `{
  "config_version": 2,
  "bogus_top": "unexpected",
  "another_bogus": 123,
  "backup_dir": "/legacy/should_be_flagged_in_v2",
  "schedule": {
    "times": ["03:00"],
    "unknown_sched_param": true
  },
  "web": {
    "listen_addr": ":8080",
    "invalid_web_key": 999
  },
  "routers": [
    {
      "name": "R1",
      "host": "10.0.0.1",
      "user": "admin",
      "password": "pwd",
      "bad_router_opt": "invalid"
    }
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(v2WithUnknown), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgFile)
	if err != nil {
		t.Fatalf("NewManager should succeed despite unknown keys, got error: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected MigrationNotice for unknown keys, got nil")
	}
	if notice.Migrated {
		t.Errorf("notice.Migrated should be false for version 2 config")
	}

	expectedKeys := []string{
		"another_bogus",
		"backup_dir",
		"bogus_top",
		"routers[0].bad_router_opt",
		"schedule.unknown_sched_param",
		"web.invalid_web_key",
	}

	if len(notice.UnknownKeys) != len(expectedKeys) {
		t.Fatalf("expected %d unknown keys (%v), got %d (%v)",
			len(expectedKeys), expectedKeys, len(notice.UnknownKeys), notice.UnknownKeys)
	}

	for i, expected := range expectedKeys {
		if notice.UnknownKeys[i] != expected {
			t.Errorf("unknown key [%d]: expected %q, got %q", i, expected, notice.UnknownKeys[i])
		}
	}
}

func TestConfigMigration_V1UnknownKeysExcludesBackupDir(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	// v1 config with deprecated backup_dir AND an unknown key
	v1JSON := `{
  "backup_dir": "/var/backups",
  "unexpected_v1_field": "test",
  "web": {
    "rogue_web_key": 42
  },
  "routers": [
    {"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(v1JSON), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgFile)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	notice := mgr.GetMigrationNotice()
	if notice == nil {
		t.Fatalf("expected MigrationNotice, got nil")
	}
	if !notice.Migrated {
		t.Errorf("expected notice.Migrated to be true")
	}

	// Verify unknown keys include unexpected_v1_field and web.rogue_web_key, but EXCLUDE backup_dir
	for _, k := range notice.UnknownKeys {
		if k == "backup_dir" {
			t.Errorf("backup_dir should NOT be flagged as unknown key during v1 migration")
		}
	}

	expectedKeys := []string{"unexpected_v1_field", "web.rogue_web_key"}
	if len(notice.UnknownKeys) != len(expectedKeys) {
		t.Fatalf("expected %d unknown keys (%v), got %d (%v)", len(expectedKeys), expectedKeys, len(notice.UnknownKeys), notice.UnknownKeys)
	}
	for i, expected := range expectedKeys {
		if notice.UnknownKeys[i] != expected {
			t.Errorf("unknown key [%d]: expected %q, got %q", i, expected, notice.UnknownKeys[i])
		}
	}
}

func TestMigrationNotice_ThreadSafetyAndDeepCopy(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	v1JSON := `{
  "unknown_key": "val",
  "routers": [
    {"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(v1JSON), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	// Test deep copy
	notice1 := mgr.GetMigrationNotice()
	if notice1 == nil || len(notice1.UnknownKeys) == 0 {
		t.Fatalf("expected notice with unknown keys")
	}
	notice1.UnknownKeys[0] = "mutated_key"

	notice2 := mgr.GetMigrationNotice()
	if notice2.UnknownKeys[0] == "mutated_key" {
		t.Errorf("GetMigrationNotice did not return deep copy; original UnknownKeys was mutated")
	}

	// Concurrent read and clear testing
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = mgr.GetMigrationNotice()
				_ = mgr.Get()
			}
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				mgr.ClearMigrationNotice()
			}
		}()
	}

	wg.Wait()

	// After clearing, notice should be nil
	if mgr.GetMigrationNotice() != nil {
		t.Errorf("expected MigrationNotice to be nil after ClearMigrationNotice")
	}
}

func TestConfigExplicitVersion0TreatedAsV1(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	raw := `{
  "config_version": 0,
  "routers": [
    {"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.ConfigVersion != CurrentConfigVersion {
		t.Errorf("expected ConfigVersion %d, got %d", CurrentConfigVersion, cfg.ConfigVersion)
	}

	backupPath := cfgFile + ".v1.bak"
	if _, err := os.Stat(backupPath); err != nil {
		t.Errorf("expected backup file %q for config_version 0: %v", backupPath, err)
	}
}

func TestConfigNegativeVersion(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")

	raw := `{
  "config_version": -1,
  "routers": [
    {"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgFile)
	if err == nil {
		t.Fatalf("expected error for negative config_version -1, got nil")
	}
	if !strings.Contains(err.Error(), "invalid config_version -1") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigBackupFailureAbortsMigration(t *testing.T) {
	tempDir := t.TempDir()
	readOnlyDir := filepath.Join(tempDir, "readonly")
	if err := os.Mkdir(readOnlyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(readOnlyDir, "config.json")
	raw := `{
  "backup_dir": "/legacy",
  "routers": [
    {"name": "R1", "host": "10.0.0.1", "user": "admin", "password": "pwd"}
  ]
}`
	if err := os.WriteFile(cfgFile, []byte(raw), 0o400); err != nil {
		t.Fatal(err)
	}
	// Make directory read-only so creating .v1.bak fails
	if err := os.Chmod(readOnlyDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o700)
	})

	_, err := Load(cfgFile)
	if err == nil {
		t.Fatalf("expected error when backup cannot be created, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create config backup") {
		t.Errorf("expected backup failure error, got: %v", err)
	}
}

func TestConfigUpdateCheckOptions(t *testing.T) {
	// 1. Default configuration: update check is enabled
	cfg := Config{}
	if !cfg.IsUpdateCheckEnabled() {
		t.Errorf("expected update check to be enabled by default")
	}

	// 2. Explicitly disabled via updates.enabled = false
	f := false
	cfgDisabled1 := Config{
		Updates: UpdatesConfig{
			Enabled: &f,
		},
	}
	if cfgDisabled1.IsUpdateCheckEnabled() {
		t.Errorf("expected update check to be disabled via updates.enabled = false")
	}

	// 3. Explicitly disabled via check_updates = false
	cfgDisabled2 := Config{
		CheckUpdates: &f,
	}
	if cfgDisabled2.IsUpdateCheckEnabled() {
		t.Errorf("expected update check to be disabled via check_updates = false")
	}

	// 4. Explicitly enabled via updates.enabled = true
	tr := true
	cfgEnabled := Config{
		Updates: UpdatesConfig{
			Enabled: &tr,
		},
	}
	if !cfgEnabled.IsUpdateCheckEnabled() {
		t.Errorf("expected update check to be enabled via updates.enabled = true")
	}

	// 5. Test loading from JSON and unknown keys check
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	jsonContent := `{
		"config_version": 2,
		"binary_backup_dir": "./backups/binary",
		"export_dir": "./backups/exports",
		"defaults": {
			"user": "admin",
			"password": "secret"
		},
		"updates": {
			"enabled": false
		},
		"check_updates": false,
		"routers": [
			{"name": "r1", "host": "192.168.1.1"}
		]
	}`
	if err := os.WriteFile(cfgPath, []byte(jsonContent), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	loaded := mgr.Get()
	if loaded.IsUpdateCheckEnabled() {
		t.Errorf("expected update check to be disabled after loading from JSON")
	}
	if mgr.GetMigrationNotice() != nil && len(mgr.GetMigrationNotice().UnknownKeys) > 0 {
		t.Errorf("updates or check_updates flagged as unknown keys: %v", mgr.GetMigrationNotice().UnknownKeys)
	}
}




