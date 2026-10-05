package config

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestRuntimeEnvironmentOverridesFileWithoutPersisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "config.json")
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(t.TempDir(), "unused.json"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"host":"127.0.0.2","port":4123,"state":"file.db","webDir":"file-assets"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_HOST", "::1")
	t.Setenv("SERVEDIFF_PORT", "5123")
	t.Setenv("SERVEDIFF_STATE", "memory")
	t.Setenv("SERVEDIFF_WEB_DIR", "")
	values, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if values.Host != "::1" || values.Port == nil || *values.Port != 5123 || values.State != "memory" || values.WebDir != "" {
		t.Fatalf("environment precedence: %#v", values)
	}
	if err := EditFile(path, "host", nil); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Host != "127.0.0.1" || saved.Port == nil || *saved.Port != 4123 || saved.State != "file.db" || saved.WebDir != "file-assets" {
		t.Fatalf("environment persisted during edit: %#v", saved)
	}
	if _, err := os.Stat(os.Getenv("SERVEDIFF_CONFIG_PATH")); !os.IsNotExist(err) {
		t.Fatalf("explicit file touched default: %v", err)
	}
}

func TestMissingRuntimeFileCreatesPrivateDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "nested", "config.json")
	values, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if values.Host != "127.0.0.1" || values.Port != nil || values.State == "" || values.WebDir != "" {
		t.Fatalf("defaults: %#v", values)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{path, filepath.Dir(path)} {
			info, err := os.Stat(name)
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("non-private config path %s: %v", name, err)
			}
		}
	}
}

func TestInvalidEnvironmentAndFileFail(t *testing.T) {
	for _, field := range []struct{ name, value string }{
		{"SERVEDIFF_HOST", "localhost"},
		{"SERVEDIFF_PORT", ""},
		{"SERVEDIFF_PORT", "65536"},
		{"SERVEDIFF_PORT", "-1"},
		{"SERVEDIFF_STATE", ""},
	} {
		t.Run(field.name+"/"+field.value, func(t *testing.T) {
			t.Setenv(field.name, field.value)
			if _, err := LoadFile(filepath.Join(t.TempDir(), "config.json")); err == nil {
				t.Fatal("accepted invalid environment")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"unknown":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("accepted invalid file")
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
