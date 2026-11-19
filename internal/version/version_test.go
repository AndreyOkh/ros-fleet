package version

import (
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveInfo_AllLDFlagsPresent(t *testing.T) {
	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.0.0-vcs"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "vcsrev1234567890"},
			{Key: "vcs.time", Value: "2026-01-01T00:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}

	info := resolveInfo(bi, "v2.5.0", "custom-commit-abc", "2026-11-18T12:00:00Z")

	if info.Version != "v2.5.0" {
		t.Errorf("expected Version 'v2.5.0', got %q", info.Version)
	}
	if info.Commit != "custom-commit-abc" {
		t.Errorf("expected Commit 'custom-commit-abc', got %q", info.Commit)
	}
	if info.BuildDate != "2026-11-18T12:00:00Z" {
		t.Errorf("expected BuildDate '2026-11-18T12:00:00Z', got %q", info.BuildDate)
	}
	if info.Modified {
		t.Errorf("expected Modified to be false, got true")
	}
}

func TestResolveInfo_NoLDFlags_ValidVCSSettings(t *testing.T) {
	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "v2.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.time", Value: "2026-11-18T10:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}

	info := resolveInfo(bi, "", "", "")

	if info.Version != "v2.1.0" {
		t.Errorf("expected Version 'v2.1.0', got %q", info.Version)
	}
	if info.Commit != "abcdef1234567890" {
		t.Errorf("expected Commit 'abcdef1234567890', got %q", info.Commit)
	}
	if info.BuildDate != "2026-11-18T10:00:00Z" {
		t.Errorf("expected BuildDate '2026-11-18T10:00:00Z', got %q", info.BuildDate)
	}
	if info.Modified {
		t.Errorf("expected Modified to be false, got true")
	}
}

func TestResolveInfo_DirtyWorkingTree(t *testing.T) {
	t.Run("with VCS tag", func(t *testing.T) {
		bi := &debug.BuildInfo{
			Main: debug.Module{Version: "v2.1.0"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-11-18T10:00:00Z"},
				{Key: "vcs.modified", Value: "true"},
			},
		}

		info := resolveInfo(bi, "", "", "")

		if info.Version != "v2.1.0+dirty" {
			t.Errorf("expected Version 'v2.1.0+dirty', got %q", info.Version)
		}
		if !info.Modified {
			t.Errorf("expected Modified to be true, got false")
		}
	})

	t.Run("without VCS tag (dev)", func(t *testing.T) {
		bi := &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-11-18T10:00:00Z"},
				{Key: "vcs.modified", Value: "true"},
			},
		}

		info := resolveInfo(bi, "", "", "")

		if info.Version != "dev+dirty" {
			t.Errorf("expected Version 'dev+dirty', got %q", info.Version)
		}
		if !info.Modified {
			t.Errorf("expected Modified to be true, got false")
		}
	})

	t.Run("already has +dirty suffix in Main.Version", func(t *testing.T) {
		bi := &debug.BuildInfo{
			Main: debug.Module{Version: "v2.1.0+dirty"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.modified", Value: "true"},
			},
		}

		info := resolveInfo(bi, "", "", "")

		if info.Version != "v2.1.0+dirty" {
			t.Errorf("expected Version 'v2.1.0+dirty', got %q", info.Version)
		}
		if !info.Modified {
			t.Errorf("expected Modified to be true")
		}
	})

	t.Run("explicit ldVersion preserves without double dirty", func(t *testing.T) {
		bi := &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.modified", Value: "true"},
			},
		}

		info := resolveInfo(bi, "v3.0.0", "", "")

		if info.Version != "v3.0.0" {
			t.Errorf("expected Version 'v3.0.0', got %q", info.Version)
		}
		if !info.Modified {
			t.Errorf("expected Modified to be true")
		}
	})
}

func TestResolveInfo_NilBuildInfo(t *testing.T) {
	info := resolveInfo(nil, "", "", "")

	if info.Version != "dev" {
		t.Errorf("expected Version 'dev', got %q", info.Version)
	}
	if info.Commit != "unknown" {
		t.Errorf("expected Commit 'unknown', got %q", info.Commit)
	}
	if info.BuildDate != "unknown" {
		t.Errorf("expected BuildDate 'unknown', got %q", info.BuildDate)
	}
	if info.Modified {
		t.Errorf("expected Modified to be false, got true")
	}
}

func TestResolveInfo_DevelMainVersion(t *testing.T) {
	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "fedcba0987654321"},
			{Key: "vcs.time", Value: "2026-11-18T11:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}

	info := resolveInfo(bi, "", "", "")

	if info.Version != "dev" {
		t.Errorf("expected Version 'dev', got %q", info.Version)
	}
	if info.Commit != "fedcba0987654321" {
		t.Errorf("expected Commit 'fedcba0987654321', got %q", info.Commit)
	}
	if info.BuildDate != "2026-11-18T11:00:00Z" {
		t.Errorf("expected BuildDate '2026-11-18T11:00:00Z', got %q", info.BuildDate)
	}
	if info.Modified {
		t.Errorf("expected Modified to be false, got true")
	}
}

func TestResolveInfo_PlaceholderLDFlagsFallbacks(t *testing.T) {
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "rev-from-vcs"},
			{Key: "vcs.time", Value: "time-from-vcs"},
		},
	}

	t.Run("none commit falls back to vcs", func(t *testing.T) {
		info := resolveInfo(bi, "", "none", "")
		if info.Commit != "rev-from-vcs" {
			t.Errorf("expected 'rev-from-vcs', got %q", info.Commit)
		}
	})

	t.Run("unknown commit falls back to vcs", func(t *testing.T) {
		info := resolveInfo(bi, "", "unknown", "")
		if info.Commit != "rev-from-vcs" {
			t.Errorf("expected 'rev-from-vcs', got %q", info.Commit)
		}
	})

	t.Run("unknown build date falls back to vcs", func(t *testing.T) {
		info := resolveInfo(bi, "", "", "unknown")
		if info.BuildDate != "time-from-vcs" {
			t.Errorf("expected 'time-from-vcs', got %q", info.BuildDate)
		}
	})

	t.Run("empty bi settings with placeholder ldflags falls back to unknown", func(t *testing.T) {
		emptyBi := &debug.BuildInfo{}
		info := resolveInfo(emptyBi, "", "none", "unknown")
		if info.Commit != "unknown" {
			t.Errorf("expected 'unknown', got %q", info.Commit)
		}
		if info.BuildDate != "unknown" {
			t.Errorf("expected 'unknown', got %q", info.BuildDate)
		}
	})
}

func TestGet(t *testing.T) {
	info := Get()

	if info.Version == "" {
		t.Errorf("expected non-empty Version from Get()")
	}
	if info.Commit == "" {
		t.Errorf("expected non-empty Commit from Get()")
	}
	if info.BuildDate == "" {
		t.Errorf("expected non-empty BuildDate from Get()")
	}
}

func TestGet_WithPackageVariables(t *testing.T) {
	origVersion, origCommit, origDate := Version, Commit, BuildDate
	defer func() {
		Version, Commit, BuildDate = origVersion, origCommit, origDate
	}()

	Version = "v1.5.0"
	Commit = "varcommit123"
	BuildDate = "2026-11-18T15:00:00Z"

	info := Get()
	if info.Version != "v1.5.0" {
		t.Errorf("expected Version 'v1.5.0', got %q", info.Version)
	}
	if info.Commit != "varcommit123" {
		t.Errorf("expected Commit 'varcommit123', got %q", info.Commit)
	}
	if info.BuildDate != "2026-11-18T15:00:00Z" {
		t.Errorf("expected BuildDate '2026-11-18T15:00:00Z', got %q", info.BuildDate)
	}
}

func TestInfo_String(t *testing.T) {
	info := Info{
		Version:   "v1.2.3",
		Commit:    "abcdef123456",
		BuildDate: "2026-11-18T12:00:00Z",
		Modified:  false,
	}

	expected := "ros-fleet version v1.2.3 (commit abcdef123456, built at 2026-11-18T12:00:00Z)"
	if got := info.String(); got != expected {
		t.Errorf("String() = %q; want %q", got, expected)
	}
}

func TestInfo_JSONMarshaling(t *testing.T) {
	t.Run("unmodified omits modified field", func(t *testing.T) {
		info := Info{
			Version:   "v2.0.0",
			Commit:    "abcdef",
			BuildDate: "2026-11-18",
			Modified:  false,
		}

		data, err := json.Marshal(info)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		jsonStr := string(data)
		if strings.Contains(jsonStr, "modified") {
			t.Errorf("expected json to omit 'modified' when false, got %s", jsonStr)
		}

		var decoded Info
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}
		if decoded != info {
			t.Errorf("decoded %+v != original %+v", decoded, info)
		}
	})

	t.Run("modified includes modified field", func(t *testing.T) {
		info := Info{
			Version:   "v2.0.0+dirty",
			Commit:    "abcdef",
			BuildDate: "2026-11-18",
			Modified:  true,
		}

		data, err := json.Marshal(info)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		jsonStr := string(data)
		if !strings.Contains(jsonStr, `"modified":true`) {
			t.Errorf("expected json to contain '\"modified\":true', got %s", jsonStr)
		}

		var decoded Info
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}
		if decoded != info {
			t.Errorf("decoded %+v != original %+v", decoded, info)
		}
	})
}
