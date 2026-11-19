package state

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

// BackupStatus represents the current/last backup state.
type BackupStatus string

const (
	StatusNeverRun BackupStatus = "never"
	StatusRunning  BackupStatus = "running"
	StatusSuccess  BackupStatus = "success"
	StatusError    BackupStatus = "error"
)

// DeviceState holds persistent telemetry and backup status for a router.
type DeviceState struct {
	Name              string       `json:"name"`
	Host              string       `json:"host"`
	Port              int          `json:"port"`
	Model             string       `json:"model"`
	Firmware          string       `json:"firmware"`
	Uptime            string       `json:"uptime,omitempty"`
	LastSeen          time.Time    `json:"last_seen,omitempty"`
	IsOnline          bool         `json:"is_online"`
	Status            BackupStatus `json:"last_status"`
	LastBackupAt      time.Time    `json:"last_backup_at,omitempty"`
	LastError         string       `json:"last_error,omitempty"`
	LastBackupRscSize int64        `json:"last_rsc_size,omitempty"`
	LastBackupBinSize int64        `json:"last_bin_size,omitempty"`
}

// StateManager coordinates thread-safe access and atomic disk persistence of device state.
type StateManager struct {
	path   string
	mu     sync.RWMutex
	states map[string]*DeviceState
	logger *slog.Logger
}

// NewManager loads the state from disk (or creates empty) and returns a StateManager.
func NewManager(path string, logger *slog.Logger) (*StateManager, error) {
	cleanPath := filepath.Clean(path)
	sm := &StateManager{
		path:   cleanPath,
		states: make(map[string]*DeviceState),
		logger: logger.With(slog.String("component", "state_manager")),
	}

	if data, err := os.ReadFile(cleanPath); err == nil && len(data) > 0 {
		var loaded map[string]*DeviceState
		if jsonErr := json.Unmarshal(data, &loaded); jsonErr == nil {
			sm.states = loaded
			sm.logger.Info("Loaded device state from disk",
				slog.String("file", cleanPath),
				slog.Int("devices", len(loaded)),
			)
		} else {
			sm.logger.Warn("Failed to parse state file, starting fresh", slog.Any("error", jsonErr))
		}
	} else if !os.IsNotExist(err) && err != nil {
		return nil, fmt.Errorf("failed reading state file %q: %w", cleanPath, err)
	}

	return sm, nil
}

// InitFromConfig ensures that all routers configured in config exist in state.
func (sm *StateManager) InitFromConfig(routers []config.RouterConfig, def config.DefaultsConfig) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	active := make(map[string]struct{})
	modified := false

	for _, rc := range routers {
		active[rc.Name] = struct{}{}
		eff := rc.Effective(def)

		s, exists := sm.states[rc.Name]
		if !exists {
			sm.states[rc.Name] = &DeviceState{
				Name:     rc.Name,
				Host:     eff.Host,
				Port:     eff.Port,
				Model:    "Unknown",
				Firmware: "Unknown",
				Status:   StatusNeverRun,
			}
			modified = true
		} else {
			if s.Host != eff.Host || s.Port != eff.Port {
				s.Host = eff.Host
				s.Port = eff.Port
				modified = true
			}
		}
	}

	// Clean up devices removed from config
	for name := range sm.states {
		if _, exists := active[name]; !exists {
			delete(sm.states, name)
			modified = true
		}
	}

	if modified {
		_ = sm.saveLocked()
	}
}

// Get returns a copy of a router's state.
func (sm *StateManager) Get(name string) (DeviceState, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	s, exists := sm.states[name]
	if !exists {
		return DeviceState{}, false
	}
	return *s, true
}

// GetAll returns a copy map of all device states.
func (sm *StateManager) GetAll() map[string]DeviceState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make(map[string]DeviceState, len(sm.states))
	for k, v := range sm.states {
		res[k] = *v
	}
	return res
}

// SetRunning marks that a backup is actively running for this device.
func (sm *StateManager) SetRunning(name string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, exists := sm.states[name]; exists {
		s.Status = StatusRunning
	}
}

// UpdateDeviceInfo updates hardware model, firmware version, and online timestamp.
func (sm *StateManager) UpdateDeviceInfo(name string, model, firmware, uptime string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, exists := sm.states[name]
	if !exists {
		s = &DeviceState{
			Name:   name,
			Status: StatusNeverRun,
		}
		sm.states[name] = s
	}

	if model != "" {
		s.Model = model
	}
	if firmware != "" {
		s.Firmware = firmware
	}
	if uptime != "" {
		s.Uptime = uptime
	}
	s.IsOnline = true
	s.LastSeen = time.Now()

	_ = sm.saveLocked()
}

// SetOffline records an error when connecting to a device.
func (sm *StateManager) SetOffline(name string, err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, exists := sm.states[name]; exists {
		s.IsOnline = false
		s.Status = StatusError
		if err != nil {
			s.LastError = err.Error()
		}
		_ = sm.saveLocked()
	}
}

// UpdateBackupResult saves the outcome of a backup run and persists to disk.
func (sm *StateManager) UpdateBackupResult(name string, success bool, err error, rscSize, binSize int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, exists := sm.states[name]
	if !exists {
		s = &DeviceState{Name: name}
		sm.states[name] = s
	}

	s.LastBackupAt = time.Now()

	if success {
		s.Status = StatusSuccess
		s.LastError = ""
		s.LastBackupRscSize = rscSize
		s.LastBackupBinSize = binSize
		s.IsOnline = true
		s.LastSeen = s.LastBackupAt
	} else {
		s.Status = StatusError
		if err != nil {
			s.LastError = err.Error()
		}
	}

	_ = sm.saveLocked()
}

// saveLocked atomically persists state to disk using encoding/json/v2.
func (sm *StateManager) saveLocked() error {
	data, err := json.Marshal(sm.states, jsontext.WithIndent("  "))
	if err != nil {
		sm.logger.Error("Failed to marshal state to JSON", slog.Any("error", err))
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(sm.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed creating state dir %q: %w", dir, err)
	}

	tmpFile := sm.path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		sm.logger.Error("Failed to write temporary state file", slog.Any("error", err))
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	if err := os.Rename(tmpFile, sm.path); err != nil {
		// Fallback for Docker single-file bind mounts (EBUSY / EXDEV):
		// When sm.path is a bind-mounted file, rename() fails because the target is a mount point.
		_ = os.Remove(tmpFile)
		if writeErr := writeDirect(sm.path, data); writeErr != nil {
			sm.logger.Error("Failed to persist state file directly after rename failure", slog.Any("error", writeErr))
			return fmt.Errorf("failed writing state file: %w (rename error: %v)", writeErr, err)
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
