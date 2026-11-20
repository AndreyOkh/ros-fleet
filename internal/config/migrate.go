package config

import (
	"path/filepath"
	"strings"
)

// migrateV1ToV2 migrates the deprecated backup_dir field to binary_backup_dir and export_dir.
func migrateV1ToV2(cfg *Config) {
	if strings.TrimSpace(cfg.BackupDir) != "" {
		if strings.TrimSpace(cfg.BinaryBackupDir) == "" {
			cfg.BinaryBackupDir = filepath.Join(cfg.BackupDir, "binary")
		}
		if strings.TrimSpace(cfg.ExportDir) == "" {
			cfg.ExportDir = filepath.Join(cfg.BackupDir, "exports")
		}
		cfg.BackupDir = ""
	}
}

// migrateV2ToV3 migrates configuration from schema version 2 to version 3
// by enabling built-in SSL/TLS for web traffic by default.
func migrateV2ToV3(cfg *Config) {
	if cfg.Web.SSLEnabled == nil {
		enabled := true
		cfg.Web.SSLEnabled = &enabled
	}
}
