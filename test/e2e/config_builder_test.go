package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (h *Harness) WriteJSONFile(path string, data any) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		h.T.Fatalf("WriteJSONFile: marshal failed: %v", err)
	}
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		h.T.Fatalf("WriteJSONFile: write failed: %v", err)
	}
}

func (h *Harness) ReadConfigFile(path string) (string, map[string]any) {
	data, err := os.ReadFile(path)
	if err != nil {
		h.T.Fatalf("ReadConfigFile: read failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		h.T.Fatalf("ReadConfigFile: unmarshal failed: %v", err)
	}
	return string(data), m
}

// CreateV1LegacyConfig generates a legacy v1 configuration without config_version and with backup_dir.
func (h *Harness) CreateV1LegacyConfig(name string, backupDir string) string {
	if name == "" {
		name = "config.json"
	}
	if backupDir == "" {
		backupDir = filepath.Join(h.TempDir, "backups")
	}
	target := filepath.Join(h.TempDir, name)

	cfg := map[string]any{
		"backup_dir": backupDir,
		"state_file": filepath.Join(h.TempDir, "state.json"),
		"defaults": map[string]any{
			"port":     22,
			"user":     "admin",
			"password": "default_password",
		},
		"web": map[string]any{
			"enabled":     true,
			"listen_addr": "127.0.0.1:0",
		},
		"routers": []map[string]any{
			{
				"name":     "Router-V1",
				"host":     "192.168.1.1",
				"user":     "admin",
				"password": "default_password",
			},
		},
	}
	h.WriteJSONFile(target, cfg)
	return target
}

// CreateV1CustomConfig generates a valid v1 configuration with custom overrides.
func (h *Harness) CreateV1CustomConfig(name string, overrides map[string]any) string {
	if name == "" {
		name = "config.json"
	}
	target := filepath.Join(h.TempDir, name)

	cfg := map[string]any{
		"backup_dir": filepath.Join(h.TempDir, "backups"),
		"state_file": filepath.Join(h.TempDir, "state.json"),
		"defaults": map[string]any{
			"port":     22,
			"user":     "admin",
			"password": "default_password",
		},
		"web": map[string]any{
			"enabled":     true,
			"listen_addr": "127.0.0.1:0",
		},
		"routers": []map[string]any{
			{
				"name":     "Router-V1",
				"host":     "192.168.1.1",
				"user":     "admin",
				"password": "default_password",
			},
		},
	}
	for k, v := range overrides {
		cfg[k] = v
	}
	h.WriteJSONFile(target, cfg)
	return target
}

// CreateV2Config generates a valid v2 configuration with config_version = 2.
func (h *Harness) CreateV2Config(name string) string {
	if name == "" {
		name = "config.json"
	}
	target := filepath.Join(h.TempDir, name)

	cfg := map[string]any{
		"config_version":    2,
		"binary_backup_dir": filepath.Join(h.TempDir, "backups", "binary"),
		"export_dir":        filepath.Join(h.TempDir, "backups", "exports"),
		"state_file":        filepath.Join(h.TempDir, "state.json"),
		"defaults": map[string]any{
			"port":     22,
			"user":     "admin",
			"password": "default_password",
		},
		"schedule": map[string]any{
			"times":    []string{"03:00"},
			"timezone": "UTC",
		},
		"web": map[string]any{
			"enabled":     true,
			"listen_addr": "127.0.0.1:0",
		},
		"routers": []map[string]any{
			{
				"name":     "Router-V2",
				"host":     "192.168.2.1",
				"user":     "admin",
				"password": "default_password",
			},
		},
	}
	h.WriteJSONFile(target, cfg)
	return target
}

// CreateV3Config generates a valid v3 configuration with config_version = 3.
func (h *Harness) CreateV3Config(name string) string {
	if name == "" {
		name = "config.json"
	}
	target := filepath.Join(h.TempDir, name)

	cfg := map[string]any{
		"config_version":    3,
		"binary_backup_dir": filepath.Join(h.TempDir, "backups", "binary"),
		"export_dir":        filepath.Join(h.TempDir, "backups", "exports"),
		"state_file":        filepath.Join(h.TempDir, "state.json"),
		"defaults": map[string]any{
			"port":     22,
			"user":     "admin",
			"password": "default_password",
		},
		"schedule": map[string]any{
			"times":    []string{"03:00"},
			"timezone": "UTC",
		},
		"web": map[string]any{
			"enabled":     true,
			"listen_addr": "127.0.0.1:0",
			"ssl_enabled": true,
		},
		"routers": []map[string]any{
			{
				"name":     "Router-V3",
				"host":     "192.168.3.1",
				"user":     "admin",
				"password": "default_password",
			},
		},
	}
	h.WriteJSONFile(target, cfg)
	return target
}

// CreateIncompatibleConfig generates a configuration with config_version > CurrentConfigVersion (e.g. 4).
func (h *Harness) CreateIncompatibleConfig(name string, version int) string {
	if name == "" {
		name = "config.json"
	}
	target := filepath.Join(h.TempDir, name)

	cfg := map[string]any{
		"config_version":    version,
		"binary_backup_dir": filepath.Join(h.TempDir, "backups", "binary"),
		"export_dir":        filepath.Join(h.TempDir, "backups", "exports"),
		"state_file":        filepath.Join(h.TempDir, "state.json"),
		"defaults": map[string]any{
			"port":     22,
			"user":     "admin",
			"password": "default_password",
		},
		"web": map[string]any{
			"enabled":     true,
			"listen_addr": "127.0.0.1:0",
		},
		"routers": []map[string]any{
			{
				"name":     "Router-Incompatible",
				"host":     "192.168.1.1",
				"user":     "admin",
				"password": "default_password",
			},
		},
	}
	h.WriteJSONFile(target, cfg)
	return target
}

// InjectUnknownKeys adds unknown fields to an existing config file.
func (h *Harness) InjectUnknownKeys(path string, unknownFields map[string]any) {
	_, m := h.ReadConfigFile(path)
	for k, v := range unknownFields {
		m[k] = v
	}
	h.WriteJSONFile(path, m)
}

func (h *Harness) AssertFileExists(path string) {
	if _, err := os.Stat(path); err != nil {
		h.T.Fatalf("AssertFileExists: file does not exist: %s (%v)", path, err)
	}
}

func (h *Harness) AssertFileNotExists(path string) {
	if _, err := os.Stat(path); err == nil {
		h.T.Fatalf("AssertFileNotExists: file unexpectedly exists: %s", path)
	}
}
