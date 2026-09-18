// Package version reports servediff build metadata.
package version

import (
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = ""
	Date    = ""

	readBuildInfo = debug.ReadBuildInfo
)

// String returns the CLI name and available build metadata.
func String() string {
	version, commit, date := metadata()
	parts := []string{"servediff", version}
	if strings.TrimSpace(commit) != "" {
		parts = append(parts, commit)
	}
	if strings.TrimSpace(date) != "" {
		parts = append(parts, date)
	}
	return strings.Join(parts, " ")
}

// Number returns the resolved release or development version.
func Number() string {
	version, _, _ := metadata()
	return version
}

func metadata() (string, string, string) {
	version := Version
	commit := Commit
	date := Date
	if version == "dev" {
		if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = strings.TrimPrefix(info.Main.Version, "v")
			commit = buildSetting(info, "vcs.revision", commit)
			date = buildSetting(info, "vcs.time", date)
		}
	}
	return version, commit, date
}

func buildSetting(info *debug.BuildInfo, key, fallback string) string {
	if fallback != "" {
		return fallback
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}
