package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
)

// RemoteValues is deployment configuration, independent of personal CLI settings.
type RemoteValues struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	State         string `json:"state"`
	Account       string `json:"account"`
	RetentionDays int    `json:"retentionDays"`
}

// LoadRemoteFile resolves defaults, an explicitly selected file, then environment.
// It never discovers personal config, creates files, or acquires a config lock.
func LoadRemoteFile(path string) (RemoteValues, error) {
	values := RemoteValues{Host: "127.0.0.1", Port: 7981, State: "data/state.db", Account: "admin", RetentionDays: 7}
	if path == "" {
		path = os.Getenv("DIFFX_CONFIG_PATH")
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return values, fmt.Errorf("read server config: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		document := &values
		if err := decoder.Decode(&document); err != nil {
			return values, fmt.Errorf("invalid server config: %w", err)
		}
		if document == nil {
			return values, errors.New("server config must be a JSON object")
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return values, errors.New("server config must contain one JSON object")
		}
	}
	for _, field := range []struct {
		name   string
		target *string
	}{
		{"DIFFX_HOST", &values.Host},
		{"DIFFX_STATE", &values.State},
		{"DIFFX_ACCOUNT", &values.Account},
	} {
		if value, exists := os.LookupEnv(field.name); exists {
			*field.target = value
		}
	}
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"DIFFX_PORT", &values.Port},
		{"DIFFX_RETENTION_DAYS", &values.RetentionDays},
	} {
		if raw, exists := os.LookupEnv(field.name); exists {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return values, fmt.Errorf("invalid %s: %w", field.name, err)
			}
			*field.target = value
		}
	}
	return values, nil
}

// ListenAddress validates the configured listener when no flag overrides it.
func (values RemoteValues) ListenAddress() (string, error) {
	if net.ParseIP(values.Host) == nil {
		return "", errors.New("server host must be an IP address")
	}
	if values.Port < 0 || values.Port > 65535 {
		return "", errors.New("server port must be between 0 and 65535")
	}
	return net.JoinHostPort(values.Host, strconv.Itoa(values.Port)), nil
}
