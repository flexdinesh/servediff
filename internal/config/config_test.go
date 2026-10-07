package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{"SERVEDIFF_SERVER_URL", "ftp://example.com"},
		{"SERVEDIFF_SERVER_URL", "https://example.com/api"},
		{"SERVEDIFF_RETENTION_DAYS", ""},
		{"SERVEDIFF_RETENTION_DAYS", "0"},
		{"SERVEDIFF_RETENTION_DAYS", "-1"},
		{"SERVEDIFF_RETENTION_DAYS", "106752"},
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

func TestDestinationAndRetentionPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for key, value := range map[string]string{"server": "https://reviews.example.com", "token": "saved-token", "retentionDays": "14"} {
		if err := EditFile(path, key, &value); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SERVEDIFF_SERVER_URL", "http://127.0.0.1:9000")
	t.Setenv("SERVEDIFF_TOKEN", "env-token")
	t.Setenv("SERVEDIFF_RETENTION_DAYS", "3")
	values, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if values.Server != "http://127.0.0.1:9000" || values.Token != "env-token" || values.RetentionDays != 3 {
		t.Fatalf("environment precedence: %#v", values)
	}
	settings, err := values.Settings()
	if err != nil || settings.RetentionDays != 3 {
		t.Fatalf("server retention: %#v %v", settings, err)
	}
	saved, err := ReadFile(path)
	if err != nil || saved.Server != "https://reviews.example.com" || saved.Token != "saved-token" || saved.RetentionDays != 14 {
		t.Fatalf("environment persisted: %#v %v", saved, err)
	}
	for _, key := range []string{"server", "token", "retentionDays"} {
		if err := EditFile(path, key, nil); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := ReadFile(path)
	if err != nil || restored.Server != "" || restored.Token != "" || restored.RetentionDays != 7 {
		t.Fatalf("remove restores defaults: %#v %v", restored, err)
	}
	info, err := os.Stat(path)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("token config is not private: %v", err)
	}
}

func TestLegacyConfigAndInvalidCollectorSettings(t *testing.T) {
	defaults, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Merge(defaults, `{"host":"127.0.0.1","state":"memory"}`)
	if err != nil || legacy.RetentionDays != 7 {
		t.Fatalf("legacy retention: %#v %v", legacy, err)
	}
	for _, raw := range []string{
		`{"server":null}`, `{"token":null}`, `{"retentionDays":null}`,
		`{"server":"https://user:pass@example.com"}`, `{"server":"https://example.com?query"}`,
		`{"server":"https://example.com#fragment"}`, `{"retentionDays":0}`, `{"retentionDays":1.5}`,
	} {
		if _, err := Merge(defaults, raw); err == nil {
			t.Fatalf("accepted invalid settings: %s", raw)
		} else if strings.Contains(err.Error(), "user:pass") {
			t.Fatalf("error exposes credentials: %v", err)
		}
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
