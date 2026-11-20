package config

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CurrentConfigVersion is the current configuration schema version.
const CurrentConfigVersion = 3

// Config represents the application configuration.
type Config struct {
	ConfigVersion   int            `json:"config_version"`
	BinaryBackupDir string         `json:"binary_backup_dir"`
	ExportDir       string         `json:"export_dir"`
	BackupDir       string         `json:"backup_dir,omitempty"`
	StateFile       string         `json:"state_file"`
	RetentionDays   int            `json:"retention_days"`
	Concurrency     int            `json:"concurrency"`
	TimeoutSec      int            `json:"timeout_sec"`
	Schedule        ScheduleConfig `json:"schedule"`
	Defaults        DefaultsConfig `json:"defaults"`
	Web             WebConfig      `json:"web"`
	Git             GitConfig      `json:"git"`
	Telegram        TelegramConfig `json:"telegram"`
	Updates         UpdatesConfig  `json:"updates"`
	CheckUpdates    *bool          `json:"check_updates,omitempty"`
	Routers         []RouterConfig `json:"routers"`
}

// ScheduleConfig defines time-of-day execution and timezone.
type ScheduleConfig struct {
	Times    []string `json:"times"`
	Timezone string   `json:"timezone"`
}

// DefaultsConfig specifies fallback connection and backup parameters.
type DefaultsConfig struct {
	Port                  int    `json:"port"`
	User                  string `json:"user"`
	Password              string `json:"password,omitempty"`
	PrivateKeyPath        string `json:"private_key_path,omitempty"`
	PrivateKeyPassphrase  string `json:"private_key_passphrase,omitempty"`
	InsecureIgnoreHostKey bool   `json:"insecure_ignore_host_key"`
	ExportSensitive       bool   `json:"export_sensitive"`
	EncryptBackup         bool   `json:"encrypt_backup"`
	BackupPassword        string `json:"backup_password,omitempty"`
}

// GitConfig holds settings for versioning .rsc configurations in a local Git repository.
type GitConfig struct {
	Enabled     bool   `json:"enabled"`
	RemoteURL   string `json:"remote_url,omitempty"`
	Branch      string `json:"branch"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	AutoPush    bool   `json:"auto_push"`
	AccessToken string `json:"access_token,omitempty"`
}

// WebConfig holds settings for the built-in HTTP dashboard.
type WebConfig struct {
	Enabled      bool   `json:"enabled"`
	ListenAddr   string `json:"listen_addr"`
	AuthUser     string `json:"auth_user,omitempty"`
	AuthPassword string `json:"auth_password,omitempty"`
	SSLEnabled   *bool  `json:"ssl_enabled,omitempty"`
	SSLCertFile  string `json:"ssl_cert_file,omitempty"`
	SSLKeyFile   string `json:"ssl_key_file,omitempty"`
}

// IsSSLEnabled reports whether SSL/TLS encryption is enabled for the web dashboard (defaults to true).
func (w WebConfig) IsSSLEnabled() bool {
	if w.SSLEnabled == nil {
		return true
	}
	return *w.SSLEnabled
}

// TelegramConfig holds settings for Telegram notifications.
type TelegramConfig struct {
	Enabled         bool   `json:"enabled"`
	BotToken        string `json:"bot_token"`
	ChatID          string `json:"chat_id"`
	NotifyOnSuccess bool   `json:"notify_on_success"`
	TimeoutSec      int    `json:"timeout_sec,omitempty"`
}

// UpdatesConfig specifies settings for automatic update checks.
type UpdatesConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// IsUpdateCheckEnabled reports whether automatic update checks are enabled.
// By default, update checks are enabled unless explicitly disabled via
// updates.enabled = false or check_updates = false.
func (c Config) IsUpdateCheckEnabled() bool {
	if c.CheckUpdates != nil && !*c.CheckUpdates {
		return false
	}
	if c.Updates.Enabled != nil && !*c.Updates.Enabled {
		return false
	}
	return true
}

// IsSSLEnabled reports whether SSL/TLS encryption is enabled for the web dashboard (defaults to true).
func (c Config) IsSSLEnabled() bool {
	return c.Web.IsSSLEnabled()
}

// RouterConfig contains connection settings for a single MikroTik device.
// Pointer fields allow distinguishing between explicitly false and unset (inherited from defaults).
type RouterConfig struct {
	Enabled               *bool  `json:"enabled,omitempty"`
	Name                  string `json:"name"`
	Host                  string `json:"host"`
	Port                  int    `json:"port,omitempty"`
	User                  string `json:"user,omitempty"`
	Password              string `json:"password,omitempty"`
	PrivateKeyPath        string `json:"private_key_path,omitempty"`
	PrivateKeyPassphrase  string `json:"private_key_passphrase,omitempty"`
	InsecureIgnoreHostKey *bool  `json:"insecure_ignore_host_key,omitempty"`
	ExportSensitive       *bool  `json:"export_sensitive,omitempty"`
	EncryptBackup         *bool  `json:"encrypt_backup,omitempty"`
	BackupPassword        string `json:"backup_password,omitempty"`
}

// EffectiveRouterConfig represents a router configuration with defaults resolved.
type EffectiveRouterConfig struct {
	Enabled               bool
	Name                  string
	Host                  string
	Port                  int
	User                  string
	Password              string
	PrivateKeyPath        string
	PrivateKeyPassphrase  string
	InsecureIgnoreHostKey bool
	ExportSensitive       bool
	EncryptBackup         bool
	BackupPassword        string
}

// IsEnabled returns whether backup is enabled for this router (defaults to true).
func (r RouterConfig) IsEnabled() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

// UsesDefaults returns true if the router overrides none of the default connection/backup settings.
func (r RouterConfig) UsesDefaults() bool {
	return r.Port == 0 &&
		r.User == "" &&
		r.Password == "" &&
		r.PrivateKeyPath == "" &&
		r.PrivateKeyPassphrase == "" &&
		r.InsecureIgnoreHostKey == nil &&
		r.ExportSensitive == nil &&
		r.EncryptBackup == nil &&
		r.BackupPassword == ""
}

// SetEnabled updates the enabled state of the router.
func (r *RouterConfig) SetEnabled(val bool) {
	r.Enabled = &val
}

// Clone returns a deep copy of the RouterConfig including all pointer fields.
func (r RouterConfig) Clone() RouterConfig {
	c := r
	if r.Enabled != nil {
		v := *r.Enabled
		c.Enabled = &v
	}
	if r.InsecureIgnoreHostKey != nil {
		v := *r.InsecureIgnoreHostKey
		c.InsecureIgnoreHostKey = &v
	}
	if r.ExportSensitive != nil {
		v := *r.ExportSensitive
		c.ExportSensitive = &v
	}
	if r.EncryptBackup != nil {
		v := *r.EncryptBackup
		c.EncryptBackup = &v
	}
	return c
}

// Effective resolves router fields with defaults.
func (r RouterConfig) Effective(def DefaultsConfig) EffectiveRouterConfig {
	eff := EffectiveRouterConfig{
		Enabled: r.IsEnabled(),
		Name:    r.Name,
		Host:    r.Host,
		Port:    22,
	}

	// Port resolution
	if r.Port > 0 {
		eff.Port = r.Port
	} else if def.Port > 0 {
		eff.Port = def.Port
	}

	// User
	if strings.TrimSpace(r.User) != "" {
		eff.User = strings.TrimSpace(r.User)
	} else {
		eff.User = strings.TrimSpace(def.User)
	}

	// Password
	if r.Password != "" {
		eff.Password = r.Password
	} else {
		eff.Password = def.Password
	}

	// SSH Private Key
	if r.PrivateKeyPath != "" {
		eff.PrivateKeyPath = r.PrivateKeyPath
		eff.PrivateKeyPassphrase = r.PrivateKeyPassphrase
	} else {
		eff.PrivateKeyPath = def.PrivateKeyPath
		eff.PrivateKeyPassphrase = def.PrivateKeyPassphrase
	}

	// Insecure Ignore Host Key
	if r.InsecureIgnoreHostKey != nil {
		eff.InsecureIgnoreHostKey = *r.InsecureIgnoreHostKey
	} else {
		eff.InsecureIgnoreHostKey = def.InsecureIgnoreHostKey
	}

	// Export Sensitive
	if r.ExportSensitive != nil {
		eff.ExportSensitive = *r.ExportSensitive
	} else {
		eff.ExportSensitive = def.ExportSensitive
	}

	// Encrypt Backup
	if r.EncryptBackup != nil {
		eff.EncryptBackup = *r.EncryptBackup
	} else {
		eff.EncryptBackup = def.EncryptBackup
	}

	// Backup Password
	if r.BackupPassword != "" {
		eff.BackupPassword = r.BackupPassword
	} else {
		eff.BackupPassword = def.BackupPassword
	}

	return eff
}

// Load reads and parses a JSON configuration file from disk, performing auto-migration if needed.
func Load(path string) (*Config, error) {
	cfg, _, err := loadAndMigrate(path)
	return cfg, err
}

func loadAndMigrate(path string) (*Config, *MigrationNotice, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read config file %q: %w", cleanPath, err)
	}

	var probe struct {
		ConfigVersion *int `json:"config_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	if probe.ConfigVersion != nil && *probe.ConfigVersion < 0 {
		return nil, nil, fmt.Errorf("invalid config_version %d in %q", *probe.ConfigVersion, cleanPath)
	}

	version := 1
	if probe.ConfigVersion != nil && *probe.ConfigVersion > 0 {
		version = *probe.ConfigVersion
	}

	if version > CurrentConfigVersion {
		return nil, nil, fmt.Errorf("unsupported configuration version %d (current supported version is %d); please upgrade ros-fleet", version, CurrentConfigVersion)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(data, &raw)

	var notice *MigrationNotice

	if version < CurrentConfigVersion {
		backupPath := fmt.Sprintf("%s.v%d.bak", cleanPath, version)
		if err := os.WriteFile(backupPath, data, 0o600); err != nil {
			return nil, nil, fmt.Errorf("failed to create config backup %q: %w", backupPath, err)
		}

		// Sequential schema migrations
		if version < 2 {
			migrateV1ToV2(&cfg)
		}
		if version < 3 {
			migrateV2ToV3(&cfg)
		}

		cfg.ConfigVersion = CurrentConfigVersion
		cfg.applyDefaults()

		// Unknown field detection (exclude backup_dir during v1 migration)
		unknownKeys := findUnknownKeys(raw, reflect.TypeOf(Config{}), "", version < 2)
		sort.Strings(unknownKeys)

		if len(unknownKeys) > 0 {
			slog.Warn("Unknown configuration keys detected in config",
				slog.Any("unknown_keys", unknownKeys),
				slog.Int("count", len(unknownKeys)),
			)
		}

		if err := cfg.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid configuration: %w", err)
		}

		// Atomically persist updated config to disk
		if err := saveConfigToDisk(cleanPath, &cfg); err != nil {
			return nil, nil, fmt.Errorf("failed to save migrated configuration to %q: %w", cleanPath, err)
		}

		slog.Info("Configuration migrated to new version",
			slog.Int("from_version", version),
			slog.Int("to_version", CurrentConfigVersion),
			slog.String("backup", backupPath),
		)

		msg := fmt.Sprintf("Конфигурация автоматически обновлена с версии %d до %d. Резервная копия: %s.", version, CurrentConfigVersion, filepath.Base(backupPath))
		if version < 3 {
			msg += " Включено безопасное шифрование веб-трафика (web.ssl_enabled = true)."
		}
		notice = &MigrationNotice{
			Migrated:    true,
			OldVersion:  version,
			NewVersion:  CurrentConfigVersion,
			BackupPath:  backupPath,
			UnknownKeys: unknownKeys,
			Message:     msg,
		}
	} else {
		cfg.applyDefaults()

		// Unknown field check on v2
		unknownKeys := findUnknownKeys(raw, reflect.TypeOf(Config{}), "", false)
		sort.Strings(unknownKeys)

		if len(unknownKeys) > 0 {
			slog.Warn("Unknown configuration keys detected in config",
				slog.Any("unknown_keys", unknownKeys),
				slog.Int("count", len(unknownKeys)),
			)
			notice = &MigrationNotice{
				Migrated:    false,
				UnknownKeys: unknownKeys,
				Message:     fmt.Sprintf("Обнаружены неизвестные параметры конфигурации (%d шт.)", len(unknownKeys)),
			}
		}

		if err := cfg.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid configuration: %w", err)
		}
	}

	return &cfg, notice, nil
}

func findUnknownKeys(raw any, t reflect.Type, prefix string, isV1Migration bool) []string {
	var unknown []string
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		rawMap, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		known := make(map[string]reflect.Type)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = f.Name
			}
			// In v2 schema, backup_dir is deprecated and not considered a valid v2 key
			if t == reflect.TypeOf(Config{}) && name == "backup_dir" {
				continue
			}
			known[name] = f.Type
		}

		keys := make([]string, 0, len(rawMap))
		for k := range rawMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			// In v1 migration, backup_dir is a recognized deprecated field, not an unknown key
			if isV1Migration && prefix == "" && k == "backup_dir" {
				continue
			}

			keyPath := k
			if prefix != "" {
				keyPath = prefix + "." + k
			}

			fieldType, exists := known[k]
			if !exists {
				unknown = append(unknown, keyPath)
			} else {
				unknown = append(unknown, findUnknownKeys(rawMap[k], fieldType, keyPath, false)...)
			}
		}

	case reflect.Slice:
		rawSlice, ok := raw.([]any)
		if !ok {
			return nil
		}
		elemType := t.Elem()
		for i, item := range rawSlice {
			itemPrefix := fmt.Sprintf("%s[%d]", prefix, i)
			unknown = append(unknown, findUnknownKeys(item, elemType, itemPrefix, false)...)
		}
	}

	return unknown
}

func (c *Config) applyDefaults() {
	if c.ConfigVersion <= 0 {
		c.ConfigVersion = CurrentConfigVersion
	}
	if strings.TrimSpace(c.BinaryBackupDir) == "" {
		if strings.TrimSpace(c.BackupDir) != "" {
			c.BinaryBackupDir = filepath.Join(c.BackupDir, "binary")
		} else {
			c.BinaryBackupDir = "./backups/binary"
		}
	}
	if strings.TrimSpace(c.ExportDir) == "" {
		if strings.TrimSpace(c.BackupDir) != "" {
			c.ExportDir = filepath.Join(c.BackupDir, "exports")
		} else {
			c.ExportDir = "./backups/exports"
		}
	}
	if strings.TrimSpace(c.StateFile) == "" {
		c.StateFile = "./state.json"
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 5
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 120
	}
	if len(c.Schedule.Times) == 0 {
		c.Schedule.Times = []string{"03:00"}
	}
	if strings.TrimSpace(c.Schedule.Timezone) == "" {
		c.Schedule.Timezone = "Local"
	}
	if c.Defaults.Port <= 0 {
		c.Defaults.Port = 22
	}
	if strings.TrimSpace(c.Web.ListenAddr) == "" {
		c.Web.ListenAddr = ":8080"
	}
	if c.Web.SSLEnabled == nil {
		defaultSSL := true
		c.Web.SSLEnabled = &defaultSSL
	}
	if strings.TrimSpace(c.Git.Branch) == "" {
		c.Git.Branch = "main"
	}
	if strings.TrimSpace(c.Git.AuthorName) == "" {
		c.Git.AuthorName = "ros-fleet"
	}
	if strings.TrimSpace(c.Git.AuthorEmail) == "" {
		c.Git.AuthorEmail = "ros-fleet@local"
	}
	if strings.TrimSpace(c.Git.AccessToken) == "" {
		if token := os.Getenv("GIT_ACCESS_TOKEN"); token != "" {
			c.Git.AccessToken = token
		} else if token := os.Getenv("ROS_FLEET_GIT_TOKEN"); token != "" {
			c.Git.AccessToken = token
		}
	}
	if strings.TrimSpace(c.Git.RemoteURL) == "" {
		if remote := os.Getenv("GIT_REMOTE_URL"); remote != "" {
			c.Git.RemoteURL = remote
		}
	}
	if c.Telegram.TimeoutSec <= 0 {
		c.Telegram.TimeoutSec = 15
	}

	for i := range c.Routers {
		if c.Routers[i].Enabled == nil {
			defaultEnabled := true
			c.Routers[i].Enabled = &defaultEnabled
		}
	}
}

// Validate checks configuration sanity.
func (c *Config) Validate() error {
	var errs []string

	if strings.TrimSpace(c.BinaryBackupDir) == "" {
		errs = append(errs, "'binary_backup_dir' must not be empty")
	}
	if strings.TrimSpace(c.ExportDir) == "" {
		errs = append(errs, "'export_dir' must not be empty")
	}

	if c.IsSSLEnabled() {
		hasCert := strings.TrimSpace(c.Web.SSLCertFile) != ""
		hasKey := strings.TrimSpace(c.Web.SSLKeyFile) != ""
		if (hasCert && !hasKey) || (!hasCert && hasKey) {
			errs = append(errs, "both 'web.ssl_cert_file' and 'web.ssl_key_file' must be specified when custom SSL certificates are configured")
		}
	}

	if len(c.Routers) == 0 {
		errs = append(errs, "at least one router must be defined in 'routers'")
	}

	// Validate Schedule
	tz := strings.TrimSpace(c.Schedule.Timezone)
	if tz != "" && !strings.EqualFold(tz, "Local") && !strings.EqualFold(tz, "UTC") {
		if _, err := time.LoadLocation(tz); err != nil {
			errs = append(errs, fmt.Sprintf("invalid schedule.timezone %q: %v", tz, err))
		}
	}

	for _, t := range c.Schedule.Times {
		parts := strings.Split(strings.TrimSpace(t), ":")
		if len(parts) != 2 {
			errs = append(errs, fmt.Sprintf("invalid schedule time %q (must be HH:MM)", t))
			continue
		}
		h, errH := strconv.Atoi(parts[0])
		m, errM := strconv.Atoi(parts[1])
		if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			errs = append(errs, fmt.Sprintf("invalid schedule time %q (hour 00-23, minute 00-59)", t))
		}
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

		eff := r.Effective(c.Defaults)
		if eff.User == "" {
			errs = append(errs, fmt.Sprintf("%s: user is required (set in router or defaults)", prefix))
		}

		if eff.Password == "" && eff.PrivateKeyPath == "" {
			errs = append(errs, fmt.Sprintf("%s: either password or private_key_path must be provided (set in router or defaults)", prefix))
		}

		if eff.EncryptBackup && eff.BackupPassword == "" {
			errs = append(errs, fmt.Sprintf("%s: backup_password is required when encrypt_backup is true", prefix))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	return nil
}

// MigrationNotice holds details about configuration migration or unknown keys detected.
type MigrationNotice struct {
	Migrated    bool     `json:"migrated"`
	OldVersion  int      `json:"old_version,omitempty"`
	NewVersion  int      `json:"new_version,omitempty"`
	BackupPath  string   `json:"backup_path,omitempty"`
	UnknownKeys []string `json:"unknown_keys,omitempty"`
	Message     string   `json:"message"`
}

// ConfigManager is an alias for Manager to allow interchangeable usage.
type ConfigManager = Manager

// Manager coordinates thread-safe access and persistence of the configuration.
type Manager struct {
	path            string
	mu              sync.RWMutex
	cfg             *Config
	migrationNotice *MigrationNotice
}

// NewManager loads configuration, performs auto-migration if needed, and initializes a thread-safe Manager.
func NewManager(path string) (*Manager, error) {
	cfg, notice, err := loadAndMigrate(path)
	if err != nil {
		return nil, err
	}
	return &Manager{
		path:            filepath.Clean(path),
		cfg:             cfg,
		migrationNotice: notice,
	}, nil
}

// Path returns the configuration file path on disk.
func (m *Manager) Path() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.path
}

// OverrideSSLEnabled overrides the SSL enabled setting in memory (e.g. from CLI flag).
func (m *Manager) OverrideSSLEnabled(val bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := val
	m.cfg.Web.SSLEnabled = &b
}

// UpdateWebCertFiles updates SSL certificate and private key paths and saves config to disk.
func (m *Manager) UpdateWebCertFiles(certFile, keyFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Web.SSLCertFile = certFile
	m.cfg.Web.SSLKeyFile = keyFile
	return m.saveLocked()
}

// GetMigrationNotice returns a deep copy of the migration notice, or nil if none.
func (m *Manager) GetMigrationNotice() *MigrationNotice {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.migrationNotice == nil {
		return nil
	}
	cp := *m.migrationNotice
	if m.migrationNotice.UnknownKeys != nil {
		cp.UnknownKeys = append([]string(nil), m.migrationNotice.UnknownKeys...)
	}
	return &cp
}

// ClearMigrationNotice clears the current migration notice in memory.
func (m *Manager) ClearMigrationNotice() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.migrationNotice = nil
}

// Get returns a deep copy of the current configuration.
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c := *m.cfg
	if m.cfg.Updates.Enabled != nil {
		b := *m.cfg.Updates.Enabled
		c.Updates.Enabled = &b
	}
	if m.cfg.CheckUpdates != nil {
		b := *m.cfg.CheckUpdates
		c.CheckUpdates = &b
	}
	if m.cfg.Web.SSLEnabled != nil {
		b := *m.cfg.Web.SSLEnabled
		c.Web.SSLEnabled = &b
	}
	c.Routers = make([]RouterConfig, len(m.cfg.Routers))
	for i, r := range m.cfg.Routers {
		c.Routers[i] = r.Clone()
	}
	c.Schedule.Times = make([]string, len(m.cfg.Schedule.Times))
	copy(c.Schedule.Times, m.cfg.Schedule.Times)
	return c
}

// ListRouters returns a deep copy of the router slice.
func (m *Manager) ListRouters() []RouterConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]RouterConfig, len(m.cfg.Routers))
	for i, r := range m.cfg.Routers {
		res[i] = r.Clone()
	}
	return res
}

// GetRouter retrieves a deep copy of a router configuration by name.
func (m *Manager) GetRouter(name string) (RouterConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, r := range m.cfg.Routers {
		if r.Name == name {
			return r.Clone(), true
		}
	}
	return RouterConfig{}, false
}

// ToggleRouter flips the enabled status of a router and saves config.
func (m *Manager) ToggleRouter(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.cfg.Routers {
		if m.cfg.Routers[i].Name == name {
			newState := !m.cfg.Routers[i].IsEnabled()
			m.cfg.Routers[i].SetEnabled(newState)
			if err := m.saveLocked(); err != nil {
				return false, err
			}
			return newState, nil
		}
	}
	return false, fmt.Errorf("router %q not found", name)
}

// UpsertRouter adds or updates a router and saves config.
func (m *Manager) UpsertRouter(router RouterConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.TrimSpace(router.Name) == "" {
		return errors.New("router name is required")
	}
	if strings.TrimSpace(router.Host) == "" {
		return errors.New("router host is required")
	}
	if router.Enabled == nil {
		enabled := true
		router.Enabled = &enabled
	}

	eff := router.Effective(m.cfg.Defaults)
	if eff.User == "" {
		return errors.New("user is required (provide in router or configure defaults)")
	}
	if eff.Password == "" && eff.PrivateKeyPath == "" {
		return errors.New("password or private key path is required")
	}
	if eff.EncryptBackup && eff.BackupPassword == "" {
		return errors.New("backup password is required when encrypt backup is enabled")
	}

	found := false
	for i := range m.cfg.Routers {
		if m.cfg.Routers[i].Name == router.Name {
			m.cfg.Routers[i] = router
			found = true
			break
		}
	}

	if !found {
		m.cfg.Routers = append(m.cfg.Routers, router)
	}

	return m.saveLocked()
}

// DeleteRouter removes a router by name and saves config.
func (m *Manager) DeleteRouter(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, r := range m.cfg.Routers {
		if r.Name == name {
			idx = i
			break
		}
	}

	if idx == -1 {
		return fmt.Errorf("router %q not found", name)
	}

	m.cfg.Routers = append(m.cfg.Routers[:idx], m.cfg.Routers[idx+1:]...)
	return m.saveLocked()
}

// UpdateDefaults updates global defaults and saves config.
func (m *Manager) UpdateDefaults(def DefaultsConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if def.Port <= 0 {
		def.Port = 22
	}
	m.cfg.Defaults = def
	return m.saveLocked()
}

// UpdateSchedule updates schedule times and timezone and saves config.
func (m *Manager) UpdateSchedule(sched ScheduleConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg.Schedule = sched
	return m.saveLocked()
}

// Save atomically writes the current configuration back to disk.
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	return saveConfigToDisk(m.path, m.cfg)
}

func saveConfigToDisk(path string, cfg *Config) error {
	data, err := json.Marshal(cfg, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("failed to encode config to JSON: %w", err)
	}
	data = append(data, '\n')

	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp config file: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		// Fallback for Docker single-file bind mounts (EBUSY / EXDEV):
		// When path is a bind-mounted file, rename() fails because the target is a mount point.
		// In this case, truncate and write directly to update the mounted file.
		_ = os.Remove(tmpFile)
		if writeErr := writeDirect(path, data); writeErr != nil {
			return fmt.Errorf("failed to write config file: %w (rename error: %v)", writeErr, err)
		}
		return nil
	}

	return nil
}

func writeDirect(path string, data []byte) error {
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open file %q for writing: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write data to %q: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync data to %q: %w", path, err)
	}
	return nil
}
