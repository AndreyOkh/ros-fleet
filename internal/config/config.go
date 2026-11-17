package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config represents the application configuration.
type Config struct {
	BackupDir             string         `json:"backup_dir"`
	RetentionDays         int            `json:"retention_days"`
	Concurrency           int            `json:"concurrency"`
	TimeoutSec            int            `json:"timeout_sec"`
	ScheduleInterval      string         `json:"schedule_interval"`
	InsecureIgnoreHostKey bool           `json:"insecure_ignore_host_key"`
	Telegram              TelegramConfig `json:"telegram"`
	Routers               []RouterConfig `json:"routers"`
}

// TelegramConfig holds settings for Telegram notifications.
type TelegramConfig struct {
	Enabled         bool   `json:"enabled"`
	BotToken        string `json:"bot_token"`
	ChatID          string `json:"chat_id"`
	NotifyOnSuccess bool   `json:"notify_on_success"`
	TimeoutSec      int    `json:"timeout_sec,omitempty"`
}

// RouterConfig contains connection and backup settings for a single MikroTik device.
type RouterConfig struct {
	Name                  string `json:"name"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	User                  string `json:"user"`
	Password              string `json:"password,omitempty"`
	PrivateKeyPath        string `json:"private_key_path,omitempty"`
	PrivateKeyPassphrase  string `json:"private_key_passphrase,omitempty"`
	InsecureIgnoreHostKey *bool  `json:"insecure_ignore_host_key,omitempty"`
	ExportSensitive       bool   `json:"export_sensitive"`
	EncryptBackup         bool   `json:"encrypt_backup"`
	BackupPassword        string `json:"backup_password,omitempty"`
}

// ShouldIgnoreHostKey returns router-level host key setting if defined, or global fallback.
func (r *RouterConfig) ShouldIgnoreHostKey(globalFallback bool) bool {
	if r.InsecureIgnoreHostKey != nil {
		return *r.InsecureIgnoreHostKey
	}
	return globalFallback
}

// Load reads and parses a JSON configuration file from disk, applying defaults and validation.
func Load(path string) (*Config, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", cleanPath, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// applyDefaults fills in reasonable default values for missing configuration fields.
func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.BackupDir) == "" {
		c.BackupDir = "./backups"
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 5
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 120
	}
	if strings.TrimSpace(c.ScheduleInterval) == "" {
		c.ScheduleInterval = "24h"
	}
	if c.Telegram.TimeoutSec <= 0 {
		c.Telegram.TimeoutSec = 15
	}

	for i := range c.Routers {
		if c.Routers[i].Port <= 0 {
			c.Routers[i].Port = 22
		}
	}
}

// Validate checks configuration sanity and returns descriptive errors for any issues.
func (c *Config) Validate() error {
	var errs []string

	if len(c.Routers) == 0 {
		errs = append(errs, "at least one router must be defined in 'routers'")
	}

	if _, err := time.ParseDuration(c.ScheduleInterval); err != nil {
		errs = append(errs, fmt.Sprintf("invalid 'schedule_interval' %q: %v", c.ScheduleInterval, err))
	}

	if c.Telegram.Enabled {
		if strings.TrimSpace(c.Telegram.BotToken) == "" {
			errs = append(errs, "telegram.bot_token is required when telegram.enabled is true")
		}
		if strings.TrimSpace(c.Telegram.ChatID) == "" {
			errs = append(errs, "telegram.chat_id is required when telegram.enabled is true")
		}
	}

	names := make(map[string]struct{})
	for i, r := range c.Routers {
		prefix := fmt.Sprintf("routers[%d]", i)
		if strings.TrimSpace(r.Name) == "" {
			errs = append(errs, fmt.Sprintf("%s: name is required", prefix))
		} else {
			if _, exists := names[r.Name]; exists {
				errs = append(errs, fmt.Sprintf("%s: duplicate router name %q", prefix, r.Name))
			}
			names[r.Name] = struct{}{}
		}

		if strings.TrimSpace(r.Host) == "" {
			errs = append(errs, fmt.Sprintf("%s: host is required", prefix))
		}

		if strings.TrimSpace(r.User) == "" {
			errs = append(errs, fmt.Sprintf("%s: user is required", prefix))
		}

		if strings.TrimSpace(r.Password) == "" && strings.TrimSpace(r.PrivateKeyPath) == "" {
			errs = append(errs, fmt.Sprintf("%s: either 'password' or 'private_key_path' must be provided", prefix))
		}

		if r.EncryptBackup && strings.TrimSpace(r.BackupPassword) == "" {
			errs = append(errs, fmt.Sprintf("%s: 'backup_password' is required when 'encrypt_backup' is true", prefix))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	return nil
}

// ParsedScheduleInterval returns the duration parsed from ScheduleInterval.
func (c *Config) ParsedScheduleInterval() time.Duration {
	d, err := time.ParseDuration(c.ScheduleInterval)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}
