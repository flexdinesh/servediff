package version

import (
	"runtime/debug"
	"testing"
)

func TestStringDefaultsToDevelopmentVersion(t *testing.T) {
	setBuildMetadata(t, "dev", "", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true)
	if got, want := String(), "servediff dev"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringIncludesReleaseMetadata(t *testing.T) {
	setBuildMetadata(t, "0.1.0", "abc123", "2026-09-17T00:00:00Z", nil, false)
	if got, want := String(), "servediff 0.1.0 abc123 2026-09-17T00:00:00Z"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringUsesTaggedGoInstallMetadata(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.2.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "def456"},
			{Key: "vcs.time", Value: "2026-09-17T01:00:00Z"},
		},
	}
	setBuildMetadata(t, "dev", "", "", info, true)
	if got, want := String(), "servediff 0.2.0 def456 2026-09-17T01:00:00Z"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func setBuildMetadata(t *testing.T, version, commit, date string, info *debug.BuildInfo, ok bool) {
	t.Helper()
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	oldReadBuildInfo := readBuildInfo
	Version, Commit, Date = version, commit, date
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	t.Cleanup(func() {
		Version, Commit, Date = oldVersion, oldCommit, oldDate
		readBuildInfo = oldReadBuildInfo
	})
}
