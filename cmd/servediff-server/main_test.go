package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteConfigMappingAndPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"::1","port":4123,"state":"file.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_CONFIG_PATH", "")
	t.Setenv("SERVEDIFF_RUNTIME_DIR", t.TempDir())
	settings, _, err := parseSettings(nil, io.Discard)
	if err != nil || settings.Listen != "0.0.0.0:7981" {
		t.Fatalf("remote default changed: %#v %v", settings, err)
	}
	settings, _, err = parseSettings([]string{"--config-file", path}, io.Discard)
	if err != nil || settings.Listen != "[::1]:4123" || filepath.Base(settings.State) != "file.db" {
		t.Fatalf("config mapping: %#v %v", settings, err)
	}
	t.Setenv("SERVEDIFF_HOST", "127.0.0.2")
	t.Setenv("SERVEDIFF_PORT", "5123")
	t.Setenv("SERVEDIFF_STATE", "environment.db")
	settings, _, err = parseSettings([]string{"--config-file", path}, io.Discard)
	if err != nil || settings.Listen != "127.0.0.2:5123" || filepath.Base(settings.State) != "environment.db" {
		t.Fatalf("environment mapping: %#v %v", settings, err)
	}
	settings, _, err = parseSettings([]string{"--config-file", path, "--listen", "0.0.0.0:6123", "--state", "flags.db"}, io.Discard)
	if err != nil || settings.Listen != "0.0.0.0:6123" || settings.State != "flags.db" {
		t.Fatalf("flags mapping: %#v %v", settings, err)
	}
}

func TestRemoteConfigAutomaticPortAndVersion(t *testing.T) {
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	settings, _, err := parseSettings(nil, io.Discard)
	if err != nil || settings.Listen != "127.0.0.1:7981" {
		t.Fatalf("automatic config port: %#v %v", settings, err)
	}
	if _, showVersion, err := parseSettings([]string{"--version", "--config-file", "/missing/config.json"}, io.Discard); err != nil || !showVersion {
		t.Fatalf("version resolved config: %v", err)
	}
}
