package updater

import (
	"os"
	"strings"
)

// dockerEnvPath is the path to the standard docker environment file,
// mockable in unit tests.
var dockerEnvPath = "/.dockerenv"

// envLookupFunc is the environment lookup function, mockable in unit tests.
var envLookupFunc = os.Getenv

// IsRunningInDocker returns true if the process is running inside a Docker container.
// It checks for the presence of /.dockerenv and container environment variables
// (DOCKER_CONTAINER, container=docker, IS_DOCKER=true/1, ROS_FLEET_DOCKER).
func IsRunningInDocker() bool {
	return checkRunningInDocker(dockerEnvPath, envLookupFunc)
}

func checkRunningInDocker(envPath string, lookup func(string) string) bool {
	if lookup == nil {
		lookup = os.Getenv
	}

	if _, err := os.Stat(envPath); err == nil {
		return true
	}

	if lookup("DOCKER_CONTAINER") != "" {
		return true
	}

	if strings.ToLower(lookup("container")) == "docker" {
		return true
	}

	isDocker := strings.ToLower(strings.TrimSpace(lookup("IS_DOCKER")))
	if isDocker == "true" || isDocker == "1" || isDocker == "yes" {
		return true
	}

	if lookup("ROS_FLEET_DOCKER") != "" {
		return true
	}

	return false
}
