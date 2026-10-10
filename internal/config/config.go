// Package config owns persisted settings, separate from private runtime credentials.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/flexdinesh/diffx/internal/processlock"
	"github.com/flexdinesh/diffx/internal/reviewstore"
)

type ServerSettings struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	State         string `json:"state"`
	WebDir        string `json:"webDir"`
	RetentionDays int    `json:"retentionDays"`
}

type Values struct {
	Host          string `json:"host"`
	Port          *int   `json:"port"`
	State         string `json:"state"`
	WebDir        string `json:"webDir"`
	Server        string `json:"server"`
	Token         string `json:"token"`
	RetentionDays int    `json:"retentionDays"`
}

func Default() (Values, error) {
	state, err := reviewstore.DefaultPath()
	return Values{Host: "127.0.0.1", State: state, RetentionDays: 7}, err
}

func Path() (string, error) {
	if path := os.Getenv("DIFFX_CONFIG_PATH"); path != "" {
		return filepath.Abs(path)
	}
	if runtime := os.Getenv("DIFFX_RUNTIME_DIR"); runtime != "" {
		return filepath.Abs(filepath.Join(runtime, "config.json"))
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".config", "diffx", "config.json"), err
}

// Load creates defaults once. The lock also serializes first-use and config edits.
func Load() (Values, error) {
	return ReadFile("")
}

// ReadFile reads persisted values only. An empty path selects the machine config.
func ReadFile(path string) (Values, error) {
	path, err := resolvePath(path)
	if err != nil {
		return Values{}, err
	}
	lock, err := processlock.Acquire(path + ".lock")
	if err != nil {
		return Values{}, err
	}
	defer lock.Close()
	return load(path)
}

func resolvePath(path string) (string, error) {
	if path == "" {
		return Path()
	}
	return filepath.Abs(path)
}

// LoadFile resolves defaults, a JSON file, then environment overrides. It never
// persists environment values. An empty path selects the machine config.
func LoadFile(path string) (Values, error) {
	values, err := ReadFile(path)
	if err != nil {
		return values, err
	}
	for _, field := range []struct {
		name   string
		target *string
	}{
		{"DIFFX_HOST", &values.Host},
		{"DIFFX_STATE", &values.State},
		{"DIFFX_WEB_DIR", &values.WebDir},
		{"DIFFX_SERVER_URL", &values.Server},
		{"DIFFX_TOKEN", &values.Token},
	} {
		if value, exists := os.LookupEnv(field.name); exists {
			*field.target = value
		}
	}
	if raw, exists := os.LookupEnv("DIFFX_PORT"); exists {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return values, fmt.Errorf("invalid DIFFX_PORT: %w", err)
		}
		values.Port = &port
	}
	if raw, exists := os.LookupEnv("DIFFX_RETENTION_DAYS"); exists {
		days, err := strconv.Atoi(raw)
		if err != nil {
			return values, fmt.Errorf("invalid DIFFX_RETENTION_DAYS: %w", err)
		}
		values.RetentionDays = days
	}
	if err := values.Validate(); err != nil {
		return values, fmt.Errorf("invalid environment config: %w", err)
	}
	return values, nil
}

func load(path string) (Values, error) {
	values, err := Default()
	if err != nil {
		return values, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, save(path, values)
	}
	if err != nil {
		return values, err
	}
	return Merge(values, string(raw))
}

func Merge(values Values, raw string) (Values, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	if err := decoder.Decode(&fields); err != nil {
		return values, fmt.Errorf("invalid config JSON: %w", err)
	}
	if fields == nil {
		return values, errors.New("config must be a JSON object")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return values, errors.New("config must contain one JSON object")
	}
	for key, field := range fields {
		var target interface{}
		switch key {
		case "host":
			target = &values.Host
		case "port":
			target = &values.Port
		case "state":
			target = &values.State
		case "webDir":
			target = &values.WebDir
		case "server":
			target = &values.Server
		case "token":
			target = &values.Token
		case "retentionDays":
			target = &values.RetentionDays
		default:
			return values, fmt.Errorf("unknown config key %q", key)
		}
		if string(field) == "null" && key != "port" {
			return values, fmt.Errorf("%s cannot be null", key)
		}
		if err := json.Unmarshal(field, target); err != nil {
			return values, fmt.Errorf("invalid %s: %w", key, err)
		}
	}
	if err := values.Validate(); err != nil {
		return values, err
	}
	return values, nil
}

func (values Values) Validate() error {
	if net.ParseIP(values.Host) == nil {
		return errors.New("host must be an IP address")
	}
	if values.Port != nil && (*values.Port < 0 || *values.Port > 65535) {
		return errors.New("port must be null (automatic) or between 0 and 65535")
	}
	if values.State == "" {
		return errors.New("state must be a database path or memory")
	}
	if values.Server != "" {
		endpoint, err := url.Parse(values.Server)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
			return errors.New("server must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
	}
	if values.RetentionDays <= 0 || values.RetentionDays > 106751 {
		return errors.New("retentionDays must be between 1 and 106751")
	}
	return nil
}

func (values Values) Settings() (ServerSettings, error) {
	if err := values.Validate(); err != nil {
		return ServerSettings{}, err
	}
	settings := ServerSettings{Host: net.ParseIP(values.Host).String(), Port: -1, State: values.State, WebDir: values.WebDir, RetentionDays: values.RetentionDays}
	if values.Port != nil {
		settings.Port = *values.Port
	}
	var err error
	if settings.State != "memory" {
		settings.State, err = filepath.Abs(settings.State)
		if err != nil {
			return settings, err
		}
	}
	if settings.WebDir != "" {
		settings.WebDir, err = filepath.Abs(settings.WebDir)
	}
	return settings, err
}

func Edit(key string, value *string) error {
	return EditFile("", key, value)
}

// EditFile edits only persisted values, independent of environment overrides.
func EditFile(path, key string, value *string) error {
	path, err := resolvePath(path)
	if err != nil {
		return err
	}
	lock, err := processlock.Acquire(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	values, err := load(path)
	if err != nil {
		return err
	}
	defaults, err := Default()
	if err != nil {
		return err
	}
	fields := map[string]interface{}{"host": defaults.Host, "port": defaults.Port, "state": defaults.State, "webDir": defaults.WebDir, "server": defaults.Server, "token": defaults.Token, "retentionDays": defaults.RetentionDays}
	field, ok := fields[key]
	if !ok {
		return fmt.Errorf("unknown config key %q", key)
	}
	if value != nil {
		if key == "retentionDays" {
			days, err := strconv.Atoi(*value)
			if err != nil {
				return fmt.Errorf("invalid retentionDays: %w", err)
			}
			field = days
		} else if key == "port" {
			var port *int
			if err := json.Unmarshal([]byte(*value), &port); err != nil {
				return fmt.Errorf("invalid port: %w", err)
			}
			field = port
		} else {
			field = *value
		}
	}
	raw, err := json.Marshal(map[string]interface{}{key: field})
	if err != nil {
		return err
	}
	values, err = Merge(values, string(raw))
	if err != nil {
		return err
	}
	return save(path, values)
}

func save(path string, values Values) error {
	raw, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(append(raw, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
