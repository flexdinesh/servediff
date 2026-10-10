package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestServerConfigPrecedenceAndSnapshotIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.2","port":4123,"state":"file.db","webDir":"file-assets"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIFFX_CONFIG_PATH", filepath.Join(t.TempDir(), "unused.json"))
	t.Setenv("DIFFX_HOST", "::1")
	t.Setenv("DIFFX_PORT", "5123")
	t.Setenv("DIFFX_STATE", "memory")
	t.Setenv("DIFFX_WEB_DIR", "env-assets")
	values, err := parseOptionsMode([]string{"--config-file", path}, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := resolvedServerSettings(values)
	if err != nil || settings.Host != "::1" || settings.Port != 5123 || settings.State != "memory" || filepath.Base(settings.WebDir) != "env-assets" {
		t.Fatalf("environment resolution: %#v %v", settings, err)
	}
	values, err = parseOptionsMode([]string{"--config-file", path, "--host", "127.0.0.1", "--port", "7123", "--state", "flags.db", "--web-dir", "flags-assets"}, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = resolvedServerSettings(values)
	if err != nil || settings.Host != "127.0.0.1" || settings.Port != 7123 || filepath.Base(settings.State) != "flags.db" || filepath.Base(settings.WebDir) != "flags-assets" {
		t.Fatalf("flag resolution: %#v %v", settings, err)
	}

}

func TestConfigCommandExplicitFileAndEnvironmentIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom", "config.json")
	t.Setenv("DIFFX_CONFIG_PATH", filepath.Join(t.TempDir(), "unused.json"))
	t.Setenv("DIFFX_HOST", "::1")
	var output bytes.Buffer
	if err := runConfig([]string{"set", "host", "127.0.0.2", "--config-file", path}, &output); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := runConfig([]string{"--config-file", path, "get", "host"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "\"127.0.0.2\"\n" {
		t.Fatalf("get read environment: %s", output.String())
	}
	if _, err := os.Stat(os.Getenv("DIFFX_CONFIG_PATH")); !os.IsNotExist(err) {
		t.Fatalf("custom edit touched default file: %v", err)
	}
}
