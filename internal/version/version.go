package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	// Version is injected via -ldflags.
	Version = ""
	// Commit is injected via -ldflags.
	Commit = ""
	// BuildDate is injected via -ldflags.
	BuildDate = ""
)

// Info holds version and build metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Modified  bool   `json:"modified,omitempty"`
}

// readBuildInfo returns debug.ReadBuildInfo() or nil if unavailable.
func readBuildInfo() *debug.BuildInfo {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi
	}
	return nil
}

// Get returns the resolved build and version information.
func Get() Info {
	return resolveInfo(readBuildInfo(), Version, Commit, BuildDate)
}

// resolveInfo resolves build metadata combining ldflags and debug.BuildInfo.
func resolveInfo(bi *debug.BuildInfo, ldVersion, ldCommit, ldDate string) Info {
	ldVersion = strings.TrimSpace(ldVersion)
	ldCommit = strings.TrimSpace(ldCommit)
	ldDate = strings.TrimSpace(ldDate)

	var vcsRevision, vcsTime string
	var vcsModified bool

	if bi != nil {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				vcsRevision = s.Value
			case "vcs.time":
				vcsTime = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					vcsModified = true
				}
			}
		}
	}

	var v string
	if ldVersion != "" {
		v = ldVersion
	} else if bi != nil && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		v = bi.Main.Version
		if vcsModified && !strings.HasSuffix(v, "+dirty") {
			v += "+dirty"
		}
	} else {
		v = "dev"
		if vcsModified && !strings.HasSuffix(v, "+dirty") {
			v += "+dirty"
		}
	}

	var c string
	if ldCommit != "" && ldCommit != "none" && ldCommit != "unknown" {
		c = ldCommit
	} else if vcsRevision != "" {
		c = vcsRevision
	} else {
		c = "unknown"
	}

	var d string
	if ldDate != "" && ldDate != "unknown" {
		d = ldDate
	} else if vcsTime != "" {
		d = vcsTime
	} else {
		d = "unknown"
	}

	modified := vcsModified
	if strings.HasSuffix(v, "+dirty") {
		modified = true
	}

	return Info{
		Version:   v,
		Commit:    c,
		BuildDate: d,
		Modified:  modified,
	}
}

// String returns formatted human-readable version information.
func (i Info) String() string {
	return fmt.Sprintf("ros-fleet version %s (commit %s, built at %s)", i.Version, i.Commit, i.BuildDate)
}
