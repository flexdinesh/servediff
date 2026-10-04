package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstUseOverridesAndRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SERVEDIFF_CONFIG_PATH", path)
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	host := "0.0.0.0"
	if err := Edit("host", &host); err != nil {
		t.Fatal(err)
	}
	saved, err := Load()
	if err != nil || saved.Host != host {
		t.Fatalf("saved config: %#v, %v", saved, err)
	}
	override, err := Merge(saved, `{"host":"127.0.0.1","port":4000,"state":"memory","webDir":"/assets"}`)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := override.Settings()
	if err != nil || settings.Host != "127.0.0.1" || settings.Port != 4000 || settings.State != "memory" || settings.WebDir != "/assets" {
		t.Fatalf("override: %#v, %v", settings, err)
	}
	unchanged, err := Load()
	if err != nil || unchanged.Host != host || unchanged.Port != nil {
		t.Fatalf("override persisted: %#v, %v", unchanged, err)
	}
	if err := Edit("host", nil); err != nil {
		t.Fatal(err)
	}
	restored, err := Load()
	if err != nil || restored.Host != defaults.Host {
		t.Fatalf("remove: %#v, %v", restored, err)
	}
}

func TestInvalidConfigNeverChangesSavedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SERVEDIFF_CONFIG_PATH", path)
	values, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `{"unknown":1}`, `{"host":null}`, `{"state":""}`, `{"host":"example.com"}`, `{"port":-1}`, `{"port":65536}`, `{"port":"4000"}`, `{} {}`} {
		if _, err := Merge(values, raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := "-1"
	if err := Edit("port", &invalid); err == nil {
		t.Fatal("saved invalid port")
	}
	after, _ := os.ReadFile(path)
	if string(raw) != string(after) {
		t.Fatal("invalid edit altered file")
	}
}
