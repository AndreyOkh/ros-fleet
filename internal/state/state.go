package state

import (
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

// BackupStatus represents the current/last backup state.
type BackupStatus string

const (
	StatusNeverRun BackupStatus = "never_run"
	StatusRunning  BackupStatus = "running"
	StatusSuccess  BackupStatus = "success"
	StatusError    BackupStatus = "error"
)

// DeviceState holds in-memory telemetry, online status, and backup history for a single router.
type DeviceState struct {
	Name              string       `json:"name"`
	Host              string       `json:"host"`
	Port              int          `json:"port"`
	Model             string       `json:"model"`
	Version           string       `json:"version"`
	Uptime            string       `json:"uptime,omitempty"`
	LastSeen          time.Time    `json:"last_seen,omitempty"`
	IsOnline          bool         `json:"is_online"`
	Status            BackupStatus `json:"status"`
	LastBackupTime    time.Time    `json:"last_backup_time,omitempty"`
	LastBackupSuccess bool         `json:"last_backup_success"`
	LastBackupError   string       `json:"last_backup_error,omitempty"`
	LastBackupRscSize int64        `json:"last_backup_rsc_size,omitempty"`
	LastBackupBinSize int64        `json:"last_backup_bin_size,omitempty"`
}

// Registry manages thread-safe in-memory state of all fleet devices.
type Registry struct {
	mu     sync.RWMutex
	states map[string]*DeviceState
}

// NewRegistry initializes an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		states: make(map[string]*DeviceState),
	}
}

// InitFromConfig ensures all routers defined in config exist in the state registry.
func (r *Registry) InitFromConfig(routers []config.RouterConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	activeNames := make(map[string]struct{})
	for _, rc := range routers {
		activeNames[rc.Name] = struct{}{}
		if _, exists := r.states[rc.Name]; !exists {
			r.states[rc.Name] = &DeviceState{
				Name:    rc.Name,
				Host:    rc.Host,
				Port:    rc.Port,
				Model:   "Unknown",
				Version: "Unknown",
				Status:  StatusNeverRun,
			}
		} else {
			// Update host/port if changed
			r.states[rc.Name].Host = rc.Host
			r.states[rc.Name].Port = rc.Port
		}
	}

	// Remove devices that no longer exist in config
	for name := range r.states {
		if _, exists := activeNames[name]; !exists {
			delete(r.states, name)
		}
	}
}

// Get returns a copy of a router's state.
func (r *Registry) Get(name string) (DeviceState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, exists := r.states[name]
	if !exists {
		return DeviceState{}, false
	}
	return *s, true
}

// GetAll returns a copy map of all device states.
func (r *Registry) GetAll() map[string]DeviceState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make(map[string]DeviceState, len(r.states))
	for k, v := range r.states {
		res[k] = *v
	}
	return res
}

// SetRunning marks that a backup or check is actively running for this device.
func (r *Registry) SetRunning(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, exists := r.states[name]; exists {
		s.Status = StatusRunning
	}
}

// UpdateDeviceInfo updates hardware model, firmware version, and online timestamp.
func (r *Registry) UpdateDeviceInfo(name string, model, version, uptime string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, exists := r.states[name]
	if !exists {
		s = &DeviceState{
			Name:   name,
			Status: StatusNeverRun,
		}
		r.states[name] = s
	}

	if model != "" {
		s.Model = model
	}
	if version != "" {
		s.Version = version
	}
	if uptime != "" {
		s.Uptime = uptime
	}
	s.IsOnline = true
	s.LastSeen = time.Now()
}

// SetOffline records an error when connecting to a device.
func (r *Registry) SetOffline(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, exists := r.states[name]; exists {
		s.IsOnline = false
		s.Status = StatusError
		if err != nil {
			s.LastBackupError = err.Error()
		}
	}
}

// UpdateBackupResult saves the outcome of a backup run.
func (r *Registry) UpdateBackupResult(name string, success bool, err error, rscSize, binSize int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, exists := r.states[name]
	if !exists {
		s = &DeviceState{Name: name}
		r.states[name] = s
	}

	s.LastBackupTime = time.Now()
	s.LastBackupSuccess = success

	if success {
		s.Status = StatusSuccess
		s.LastBackupError = ""
		s.LastBackupRscSize = rscSize
		s.LastBackupBinSize = binSize
		s.IsOnline = true
		s.LastSeen = s.LastBackupTime
	} else {
		s.Status = StatusError
		if err != nil {
			s.LastBackupError = err.Error()
		}
	}
}
