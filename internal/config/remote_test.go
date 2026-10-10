package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteConfigIgnoresPersonalSettingsAndDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	runtime := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DIFFX_RUNTIME_DIR", runtime)
	t.Setenv("DIFFX_CONFIG_PATH", "")
	path := filepath.Join(home, ".config", "diffx", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"state":"personal.db","port":9000}`), 0o400); err != nil {
		t.Fatal(err)
	}
	// Producer-only environment must not affect or invalidate server startup.
	t.Setenv("DIFFX_SERVER_URL", "not-a-url")
	t.Setenv("DIFFX_WEB_DIR", "/missing/assets")
	values, err := LoadRemoteFile("")
	if err != nil || values.State != "data/state.db" || values.Port != 7981 {
		t.Fatalf("personal config affected server: %+v %v", values, err)
	}
	for _, directory := range []string{runtime, filepath.Dir(path)} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if directory == runtime || entry.Name() != "config.json" {
				t.Fatalf("server created config artifact %s", filepath.Join(directory, entry.Name()))
			}
		}
	}
}

func TestRemoteConfigReadsOnlyExplicitFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	t.Setenv("DIFFX_CONFIG_PATH", path)
	if _, err := LoadRemoteFile(""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing config did not fail: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created missing config: %v", err)
	}
	raw := []byte(`{"host":"::1","port":4123,"state":"configured.db","retentionDays":14}`)
	if err := os.WriteFile(path, raw, 0o400); err != nil {
		t.Fatal(err)
	}
	values, err := LoadRemoteFile("")
	if err != nil || values.Host != "::1" || values.Port != 4123 || values.State != "configured.db" || values.RetentionDays != 14 {
		t.Fatalf("explicit config: %+v %v", values, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("read-only config created artifacts: %v %v", entries, err)
	}
	other := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(other, []byte(`{"port":5000}`), 0o400); err != nil {
		t.Fatal(err)
	}
	values, err = LoadRemoteFile(other)
	if err != nil || values.Port != 5000 {
		t.Fatalf("explicit path did not override env path: %+v %v", values, err)
	}
}

func TestRemoteConfigRejectsMalformedFiles(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"port":"7981"}`, `{"unknown":true}`, `{"server":"https://example.com"}`, `{} {}`, `{} trailing`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.json")
			if err := os.WriteFile(path, []byte(raw), 0o400); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadRemoteFile(path); err == nil {
				t.Fatal("accepted invalid server config")
			}
		})
	}
}
