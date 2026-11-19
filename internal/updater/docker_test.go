package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckRunningInDocker(t *testing.T) {
	nonExistentFile := filepath.Join(t.TempDir(), ".nonexistent_dockerenv")

	tests := []struct {
		name     string
		envPath  string
		envMap   map[string]string
		expected bool
	}{
		{
			name:     "no docker indicators",
			envPath:  nonExistentFile,
			envMap:   map[string]string{},
			expected: false,
		},
		{
			name:     "dockerenv file present",
			envPath:  "", // will be set dynamically
			envMap:   map[string]string{},
			expected: true,
		},
		{
			name:     "DOCKER_CONTAINER set",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"DOCKER_CONTAINER": "1"},
			expected: true,
		},
		{
			name:     "container=docker set",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"container": "docker"},
			expected: true,
		},
		{
			name:     "container=DOCKER case-insensitive",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"container": "DOCKER"},
			expected: true,
		},
		{
			name:     "IS_DOCKER=true set",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"IS_DOCKER": "true"},
			expected: true,
		},
		{
			name:     "IS_DOCKER=1 set",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"IS_DOCKER": "1"},
			expected: true,
		},
		{
			name:     "ROS_FLEET_DOCKER set",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"ROS_FLEET_DOCKER": "yes"},
			expected: true,
		},
		{
			name:     "unrelated env vars",
			envPath:  nonExistentFile,
			envMap:   map[string]string{"OTHER_VAR": "value", "container": "podman", "IS_DOCKER": "false"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.envPath
			if tt.name == "dockerenv file present" {
				tmpDir := t.TempDir()
				path = filepath.Join(tmpDir, ".dockerenv")
				if err := os.WriteFile(path, []byte(""), 0644); err != nil {
					t.Fatalf("failed to create temp .dockerenv: %v", err)
				}
			}

			lookup := func(key string) string {
				return tt.envMap[key]
			}

			got := checkRunningInDocker(path, lookup)
			if got != tt.expected {
				t.Fatalf("checkRunningInDocker() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestIsRunningInDockerHook(t *testing.T) {
	origEnvPath := dockerEnvPath
	origLookup := envLookupFunc
	t.Cleanup(func() {
		dockerEnvPath = origEnvPath
		envLookupFunc = origLookup
	})

	// Case 1: False by default when mock env is clean
	dockerEnvPath = filepath.Join(t.TempDir(), "not_docker")
	envLookupFunc = func(key string) string { return "" }
	if IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker to return false")
	}

	// Case 2: True when mock env sets DOCKER_CONTAINER
	envLookupFunc = func(key string) string {
		if key == "DOCKER_CONTAINER" {
			return "1"
		}
		return ""
	}
	if !IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker to return true")
	}

	// Case 3: True when dockerEnvPath exists
	mockEnvFile := filepath.Join(t.TempDir(), ".dockerenv")
	if err := os.WriteFile(mockEnvFile, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create mock env file: %v", err)
	}
	dockerEnvPath = mockEnvFile
	envLookupFunc = func(key string) string { return "" }
	if !IsRunningInDocker() {
		t.Fatalf("expected IsRunningInDocker to return true when file exists")
	}
}
