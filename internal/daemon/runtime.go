package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/flexdinesh/servediff/internal/controlapi"
)

type Settings = controlapi.Settings
type Status = controlapi.Status

type Explicit struct{ Host, Port, State, WebDir, RetentionDays bool }

type Descriptor struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	Status   Status `json:"status"`
}

func DefaultSettings() Settings { return Settings{Host: "127.0.0.1", Port: -1, RetentionDays: 7} }

func normalizeSettings(settings Settings) Settings {
	if settings.RetentionDays == 0 {
		settings.RetentionDays = 7
	}
	return settings
}

func RuntimeDir() (string, error) {
	if dir := os.Getenv("SERVEDIFF_RUNTIME_DIR"); dir != "" {
		return prepareRuntime(dir)
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return prepareRuntime(filepath.Join(dir, "servediff"))
	}
	state := os.Getenv("XDG_STATE_HOME")
	if runtime.GOOS == "windows" && state == "" {
		state = os.Getenv("LOCALAPPDATA")
	}
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return prepareRuntime(filepath.Join(state, "servediff", "runtime"))
}

func prepareRuntime(dir string) (string, error) {
	path, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	if err := protectRuntime(path); err != nil {
		return "", err
	}
	return path, nil
}

func OwnershipPath(dir string) string  { return filepath.Join(dir, "daemon.lock") }
func LifecyclePath(dir string) string  { return filepath.Join(dir, "lifecycle.lock") }
func DescriptorPath(dir string) string { return filepath.Join(dir, "daemon.json") }
func LogPath(dir string) string        { return filepath.Join(dir, "daemon.log") }

func validateDescriptor(d Descriptor) error {
	endpoint, err := url.Parse(d.Endpoint)
	if err != nil {
		return errors.New("invalid daemon endpoint")
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil || port < 1 || port > 65535 || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("invalid daemon endpoint")
	}
	ip := net.ParseIP(endpoint.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("daemon endpoint must be loopback")
	}
	if len(d.Token) < 32 || d.Status.InstanceID == "" {
		return errors.New("invalid daemon credentials")
	}
	return nil
}

func PublishDescriptor(dir string, d Descriptor) error {
	if _, err := prepareRuntime(dir); err != nil {
		return err
	}
	if err := validateDescriptor(d); err != nil {
		return err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".daemon-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), DescriptorPath(dir))
}

func ReadDescriptor(dir string) (Descriptor, error) {
	var d Descriptor
	if err := validatePrivateFile(DescriptorPath(dir)); err != nil {
		return d, err
	}
	file, err := os.Open(DescriptorPath(dir))
	if err != nil {
		return d, err
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 64<<10)).Decode(&d); err != nil {
		return d, fmt.Errorf("read daemon descriptor: %w", err)
	}
	return d, validateDescriptor(d)
}

func RemoveDescriptor(dir, instanceID string) error {
	d, err := ReadDescriptor(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Status.InstanceID != instanceID {
		return nil
	}
	return os.Remove(DescriptorPath(dir))
}
