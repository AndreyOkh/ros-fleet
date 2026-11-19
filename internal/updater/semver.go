package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Semver represents a parsed semantic version (MAJOR.MINOR.PATCH).
type Semver struct {
	Major int
	Minor int
	Patch int
}

// String formats Semver as "vMAJOR.MINOR.PATCH".
func (s Semver) String() string {
	return fmt.Sprintf("v%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// Compare returns:
//
//	-1 if s < other
//	 0 if s == other
//	 1 if s > other
func (s Semver) Compare(other Semver) int {
	if s.Major != other.Major {
		if s.Major > other.Major {
			return 1
		}
		return -1
	}
	if s.Minor != other.Minor {
		if s.Minor > other.Minor {
			return 1
		}
		return -1
	}
	if s.Patch != other.Patch {
		if s.Patch > other.Patch {
			return 1
		}
		return -1
	}
	return 0
}

// ParseSemver parses a version string into Semver.
// It trims leading 'v' or 'V', strips build metadata (+...) and prereleases (-...).
// Returns an error if the remaining string does not match MAJOR.MINOR.PATCH.
func ParseSemver(s string) (Semver, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Semver{}, fmt.Errorf("empty version string")
	}

	// Strip leading 'v' or 'V'
	if strings.HasPrefix(trimmed, "v") || strings.HasPrefix(trimmed, "V") {
		trimmed = trimmed[1:]
	}

	// Strip build metadata (+...)
	if idx := strings.IndexByte(trimmed, '+'); idx != -1 {
		trimmed = trimmed[:idx]
	}

	// Strip prerelease (-...)
	if idx := strings.IndexByte(trimmed, '-'); idx != -1 {
		trimmed = trimmed[:idx]
	}

	trimmed = strings.TrimSpace(trimmed)
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("invalid semver format %q: expected 3 parts (MAJOR.MINOR.PATCH)", s)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return Semver{}, fmt.Errorf("invalid major version in %q: %w", s, err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return Semver{}, fmt.Errorf("invalid minor version in %q: %w", s, err)
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil || patch < 0 {
		return Semver{}, fmt.Errorf("invalid patch version in %q: %w", s, err)
	}

	return Semver{
		Major: major,
		Minor: minor,
		Patch: patch,
	}, nil
}

// IsUpdateAvailable returns true if latest is strictly greater than current.
// Dev builds ("dev", "", "0.0.0", or non-semver) must return false (never trigger update alert on dev).
func IsUpdateAvailable(current, latest string) bool {
	currTrimmed := strings.TrimSpace(current)
	if currTrimmed == "" || strings.EqualFold(currTrimmed, "dev") || strings.EqualFold(currTrimmed, "devel") {
		return false
	}

	currSemver, err := ParseSemver(currTrimmed)
	if err != nil {
		return false
	}

	// 0.0.0 is treated as a dev build
	if currSemver.Major == 0 && currSemver.Minor == 0 && currSemver.Patch == 0 {
		return false
	}

	latestSemver, err := ParseSemver(latest)
	if err != nil {
		return false
	}

	if latestSemver.Major == 0 && latestSemver.Minor == 0 && latestSemver.Patch == 0 {
		return false
	}

	return latestSemver.Compare(currSemver) > 0
}
