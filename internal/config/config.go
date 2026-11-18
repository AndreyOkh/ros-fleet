package config

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config represents the application configuration.
type Config struct {
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
}

// TelegramConfig holds settings for Telegram notifications.
type TelegramConfig struct {
	Enabled         bool   `json:"enabled"`
	BotToken        string `json:"bot_token"`
	ChatID          string `json:"chat_id"`
	NotifyOnSuccess bool   `json:"notify_on_success"`
	TimeoutSec      int    `json:"timeout_sec,omitempty"`
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

// Load reads and parses a JSON configuration file from disk.
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

func (c *Config) applyDefaults() {
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

// Manager coordinates thread-safe access and persistence of the configuration.
type Manager struct {
	path string
	mu   sync.RWMutex
	cfg  *Config
}

// NewManager loads configuration and initializes a thread-safe Manager.
func NewManager(path string) (*Manager, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Manager{
		path: filepath.Clean(path),
		cfg:  cfg,
	}, nil
}

// Get returns a deep copy of the current configuration.
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c := *m.cfg
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
	data, err := json.Marshal(m.cfg, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("failed to encode config to JSON: %w", err)
	}
	data = append(data, '\n')

	tmpFile := m.path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp config file: %w", err)
	}

	if err := os.Rename(tmpFile, m.path); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to atomically replace config file: %w", err)
	}

	return nil
}
