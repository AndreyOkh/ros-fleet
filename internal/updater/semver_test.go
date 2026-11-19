package updater

import (
	"testing"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input       string
		expected    Semver
		expectError bool
	}{
		{input: "v1.2.3", expected: Semver{1, 2, 3}},
		{input: "V1.2.3", expected: Semver{1, 2, 3}},
		{input: "1.2.3", expected: Semver{1, 2, 3}},
		{input: "v0.0.1", expected: Semver{0, 0, 1}},
		{input: "v10.20.30", expected: Semver{10, 20, 30}},
		{input: "v2.1.0-rc1", expected: Semver{2, 1, 0}},
		{input: "v2.1.0-beta.2", expected: Semver{2, 1, 0}},
		{input: "v2.1.0+dirty", expected: Semver{2, 1, 0}},
		{input: "v2.1.0-rc1+build.123", expected: Semver{2, 1, 0}},
		{input: "v0.0.0-20261118061410-ef26dffbca9e", expected: Semver{0, 0, 0}},
		{input: "  v1.0.0  ", expected: Semver{1, 0, 0}},

		// Invalid cases
		{input: "", expectError: true},
		{input: "dev", expectError: true},
		{input: "v1.2", expectError: true},
		{input: "1", expectError: true},
		{input: "v1.2.3.4", expectError: true},
		{input: "va.b.c", expectError: true},
		{input: "v1.-2.3", expectError: true},
		{input: "v-1.2.3", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			res, err := ParseSemver(tt.input)
			if tt.expectError {
				if err == nil {
					t.Fatalf("ParseSemver(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSemver(%q) unexpected error: %v", tt.input, err)
			}
			if res != tt.expected {
				t.Fatalf("ParseSemver(%q) = %+v, expected %+v", tt.input, res, tt.expected)
			}
		})
	}
}

func TestSemverCompareAndString(t *testing.T) {
	s1 := Semver{1, 2, 3}
	s2 := Semver{1, 2, 4}
	s3 := Semver{1, 3, 0}
	s4 := Semver{2, 0, 0}
	s1Copy := Semver{1, 2, 3}

	if s1.String() != "v1.2.3" {
		t.Fatalf("String() = %q, expected %q", s1.String(), "v1.2.3")
	}

	if s1.Compare(s1Copy) != 0 {
		t.Fatalf("expected s1 == s1Copy (0), got %d", s1.Compare(s1Copy))
	}
	if s1.Compare(s2) != -1 {
		t.Fatalf("expected s1 < s2 (-1), got %d", s1.Compare(s2))
	}
	if s2.Compare(s1) != 1 {
		t.Fatalf("expected s2 > s1 (1), got %d", s2.Compare(s1))
	}
	if s1.Compare(s3) != -1 {
		t.Fatalf("expected s1 < s3 (-1), got %d", s1.Compare(s3))
	}
	if s3.Compare(s1) != 1 {
		t.Fatalf("expected s3 > s1 (1), got %d", s3.Compare(s1))
	}
	if s1.Compare(s4) != -1 {
		t.Fatalf("expected s1 < s4 (-1), got %d", s1.Compare(s4))
	}
	if s4.Compare(s1) != 1 {
		t.Fatalf("expected s4 > s1 (1), got %d", s4.Compare(s1))
	}
}

func TestIsUpdateAvailable(t *testing.T) {
	tests := []struct {
		current  string
		latest   string
		expected bool
	}{
		// Standard upgrades
		{current: "v1.0.0", latest: "v1.0.1", expected: true},
		{current: "v1.0.0", latest: "v1.1.0", expected: true},
		{current: "v1.0.0", latest: "v2.0.0", expected: true},
		{current: "1.0.0", latest: "1.0.1", expected: true},

		// Same version
		{current: "v1.0.0", latest: "v1.0.0", expected: false},
		{current: "1.0.0", latest: "v1.0.0", expected: false},

		// Newer local/current version
		{current: "v2.0.0", latest: "v1.9.9", expected: false},
		{current: "v1.1.0", latest: "v1.0.9", expected: false},

		// Build metadata & prerelease ignoring
		{current: "v2.1.0+dirty", latest: "v2.1.0", expected: false},
		{current: "v2.1.0-dev", latest: "v2.1.0", expected: false},
		{current: "v2.1.0-rc1", latest: "v2.2.0", expected: true},
		{current: "v2.2.0", latest: "v2.2.0+dirty", expected: false},

		// Dev builds: MUST ALWAYS BE FALSE
		{current: "dev", latest: "v2.1.0", expected: false},
		{current: "devel", latest: "v2.1.0", expected: false},
		{current: "", latest: "v2.1.0", expected: false},
		{current: "0.0.0", latest: "v2.1.0", expected: false},
		{current: "v0.0.0", latest: "v2.1.0", expected: false},
		{current: "v0.0.0-20261118061410-ef26dffbca9e", latest: "v2.1.0", expected: false},
		{current: "invalid-semver", latest: "v2.1.0", expected: false},

		// Invalid latest
		{current: "v1.0.0", latest: "invalid", expected: false},
		{current: "v1.0.0", latest: "0.0.0", expected: false},
		{current: "v1.0.0", latest: "dev", expected: false},
		{current: "v1.0.0", latest: "", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.current+"_vs_"+tt.latest, func(t *testing.T) {
			got := IsUpdateAvailable(tt.current, tt.latest)
			if got != tt.expected {
				t.Fatalf("IsUpdateAvailable(%q, %q) = %v, expected %v", tt.current, tt.latest, got, tt.expected)
			}
		})
	}
}
