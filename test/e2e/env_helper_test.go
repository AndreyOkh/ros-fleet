package e2e

import (
	"os"
	"strings"
)

// SanitizedEnv returns the current os.Environ() stripped of container or mock settings
// to ensure a completely clean, deterministic baseline test environment.
func SanitizedEnv() []string {
	var clean []string
	excludedPrefixes := []string{
		"DOCKER_CONTAINER=",
		"container=",
		"IS_DOCKER=",
		"ROS_FLEET_DOCKER=",
		"GITHUB_API_URL=",
	}

	for _, e := range os.Environ() {
		exclude := false
		for _, prefix := range excludedPrefixes {
			if strings.HasPrefix(strings.ToLower(e), strings.ToLower(prefix)) {
				exclude = true
				break
			}
		}
		if !exclude {
			clean = append(clean, e)
		}
	}
	return clean
}

// WithDockerEnv adds container simulation environment variables.
func WithDockerEnv(base []string) []string {
	return append(base, "DOCKER_CONTAINER=1")
}

// WithGitHubURL adds the mock GitHub API server URL.
func WithGitHubURL(base []string, url string) []string {
	return append(base, "GITHUB_API_URL="+url)
}
