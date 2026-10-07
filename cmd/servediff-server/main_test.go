package main

import (
	"bytes"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestCreateUserPersistsCredentialAndRejectsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	var stdout, stderr bytes.Buffer
	args := []string{"create", "--name", "alice", "--state", path}
	if err := createUser(args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(stdout.String(), "Bearer token: ")
	if len(parts) != 2 || stderr.Len() != 0 {
		t.Fatalf("provisioning output: %q %q", stdout.String(), stderr.String())
	}
	credential := strings.TrimSpace(parts[1])
	store, err := reviewstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.AuthenticateToken(credential)
	if err != nil || user.Name != "alice" {
		t.Fatalf("provisioned account: %+v %v", user, err)
	}
	stdout.Reset()
	if err := createUser([]string{"create", "--name", "bob", "--state", path}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("provisioning bypassed server lock: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatal("credential printed after failure")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := createUser(args, &stdout, &stderr); err == nil {
		t.Fatal("duplicate account accepted")
	}
	if stdout.Len() != 0 {
		t.Fatal("credential printed for existing user")
	}
}

func TestCreateUserValidation(t *testing.T) {
	for _, arguments := range [][]string{nil, {"delete"}, {"create"}, {"create", "--name", "alice", "extra"}, {"create", "--name", "alice", "--state", ":memory:"}} {
		if err := createUser(arguments, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid user command: %v", arguments)
		}
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

func TestRemoteRetentionConfigPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"retentionDays":14}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, _, err := parseSettings([]string{"--config-file", path}, io.Discard)
	if err != nil || settings.RetentionDays != 14 {
		t.Fatalf("retention config: %+v %v", settings, err)
	}
	t.Setenv("SERVEDIFF_RETENTION_DAYS", "21")
	settings, _, err = parseSettings([]string{"--config-file", path}, io.Discard)
	if err != nil || settings.RetentionDays != 21 {
		t.Fatalf("retention environment: %+v %v", settings, err)
	}
	settings, _, err = parseSettings([]string{"--config-file", path, "--retention-days", "3"}, io.Discard)
	if err != nil || settings.RetentionDays != 3 {
		t.Fatalf("retention flag: %+v %v", settings, err)
	}
	for _, value := range []string{"0", "-1", "1000000000"} {
		if _, _, err := parseSettings([]string{"--config-file", path, "--retention-days", value}, io.Discard); err == nil {
			t.Fatalf("invalid retention accepted: %s", value)
		}
	}
}
