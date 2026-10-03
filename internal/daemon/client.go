package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/processlock"
)

var ErrUnavailable = errors.New("service unavailable; ownership held but control endpoint is unreachable")
var ErrIncompatible = errors.New("incompatible service protocol; run servediff service restart")
var ErrStateChanged = errors.New("service database identity changed; submission not replayed")
var ErrSettingsChanged = errors.New("service settings changed; submission not replayed")
var ErrMemoryRestarted = errors.New("memory service restarted; submission not replayed")

// Connection binds accepted settings and authenticated status to one endpoint.
// Its credentials remain private and submitting never rediscovers a daemon.
type Connection struct {
	status  Status
	control *controlapi.Client
}

func (connection *Connection) Status() Status { return connection.status }

func (connection *Connection) Register(ctx context.Context, id, path string) (contextservice.Submission, error) {
	return connection.control.Register(ctx, id, path)
}

func (connection *Connection) Capture(ctx context.Context, id string, raw []byte, from string) (contextservice.Submission, error) {
	return connection.control.Capture(ctx, id, raw, from)
}

func (connection *Connection) OpenCapture(ctx context.Context, id string) (contextservice.Submission, error) {
	return connection.control.OpenCapture(ctx, id)
}

func boundConnection(descriptor Descriptor, status Status) (*Connection, error) {
	if status.ProtocolVersion != controlapi.ProtocolVersion || status.StateID == "" {
		return nil, ErrIncompatible
	}
	return &Connection{status: status, control: controlapi.NewClient(descriptor.Endpoint, descriptor.Token)}, nil
}

type Client struct {
	RuntimeDirectory string
	Executable       string
}

func NewClient() (*Client, error) {
	dir, err := RuntimeDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Client{RuntimeDirectory: dir, Executable: executable}, nil
}

func (client *Client) probe(ctx context.Context) (Descriptor, Status, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, Status{State: "unavailable"}, err
	}
	d, readErr := ReadDescriptor(client.RuntimeDirectory)
	if readErr == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		status, err := controlapi.NewClient(d.Endpoint, d.Token).Status(probeCtx)
		if err == nil && status.InstanceID == d.Status.InstanceID {
			return d, status, nil
		}
		if err := ctx.Err(); err != nil {
			return d, Status{State: "unavailable"}, err
		}
	}
	// Another discovery probe can hold ownership briefly while proving stopped.
	// Confirm contention persists before treating it as an unresponsive daemon.
	var lock *processlock.Lock
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		lock, err = processlock.TryAcquire(OwnershipPath(client.RuntimeDirectory))
		if !errors.Is(err, processlock.ErrLocked) {
			break
		}
		select {
		case <-ctx.Done():
			return d, Status{State: "unavailable"}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	if errors.Is(err, processlock.ErrLocked) {
		return d, Status{State: "unavailable"}, ErrUnavailable
	}
	if err != nil {
		return d, Status{State: "unavailable"}, err
	}
	if err := lock.Close(); err != nil {
		return d, Status{State: "unavailable"}, err
	}
	return Descriptor{}, Status{State: "stopped"}, nil
}

func (client *Client) Status(ctx context.Context) (Status, error) {
	_, status, err := client.probe(ctx)
	return status, err
}

func (client *Client) Ensure(ctx context.Context, requested Settings, explicit Explicit) (Status, error) {
	connection, err := client.EnsureConnection(ctx, requested, explicit)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return Status{State: "unavailable"}, err
		}
		return Status{}, err
	}
	return connection.Status(), nil
}

func (client *Client) EnsureConnection(ctx context.Context, requested Settings, explicit Explicit) (*Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	d, status, err := client.probe(ctx)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return client.startConnection(ctx, requested, explicit)
		}
		return nil, err
	}
	if status.State == "running" {
		if err := checkSettings(status, requested, explicit); err != nil {
			return nil, err
		}
		return boundConnection(d, status)
	}
	if status.State != "stopped" {
		return nil, errors.New("service stopping; retry after shutdown")
	}
	return client.startConnection(ctx, requested, explicit)
}

func (client *Client) Start(ctx context.Context, requested Settings, explicit Explicit) (Status, error) {
	connection, err := client.startConnection(ctx, requested, explicit)
	if err != nil {
		return Status{}, err
	}
	return connection.Status(), nil
}

func (client *Client) startConnection(ctx context.Context, requested Settings, explicit Explicit) (*Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock, err := client.lifecycleLock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	d, status, err := client.probe(ctx)
	if err != nil {
		return nil, err
	}
	if status.State == "running" {
		if err := checkSettings(status, requested, explicit); err != nil {
			return nil, err
		}
		return boundConnection(d, status)
	}
	if status.State != "stopped" {
		return nil, errors.New("service stopping; retry after shutdown")
	}
	return client.spawnConnection(ctx, requested)
}

// RecoverConnection may restore the accepted persistent service, but never
// redirects an uncertain submission into different state or settings.
func (client *Client) RecoverConnection(ctx context.Context, previous *Connection) (*Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock, err := client.lifecycleLock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	for {
		d, status, err := client.probe(ctx)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return nil, err
		}
		if err == nil && status.State == "running" {
			connection, err := boundConnection(d, status)
			if err != nil {
				return nil, err
			}
			return verifyRecovery(previous, connection)
		}
		if err == nil && status.State == "stopped" {
			if previous.status.Settings.State == "memory" {
				return nil, ErrMemoryRestarted
			}
			connection, err := client.spawnConnection(ctx, previous.status.Settings)
			if err != nil {
				return nil, err
			}
			return verifyRecovery(previous, connection)
		}
		if err := pause(ctx); err != nil {
			return nil, fmt.Errorf("service recovery timed out: %w", err)
		}
	}
}

func verifyRecovery(previous, current *Connection) (*Connection, error) {
	if previous.status.Settings != current.status.Settings {
		return nil, ErrSettingsChanged
	}
	if previous.status.Settings.State == "memory" && previous.status.InstanceID != current.status.InstanceID {
		return nil, ErrMemoryRestarted
	}
	if previous.status.StateID != current.status.StateID {
		return nil, ErrStateChanged
	}
	return current, nil
}

func (client *Client) Stop(ctx context.Context) error {
	lock, err := client.lifecycleLock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	return client.stop(ctx)
}

func (client *Client) Restart(ctx context.Context, requested Settings, explicit Explicit) (Status, error) {
	lock, err := client.lifecycleLock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	_, status, err := client.probe(ctx)
	if err != nil {
		return status, err
	}
	settings := requested
	if status.State == "running" {
		settings = status.Settings
		if explicit.Host {
			settings.Host = requested.Host
		}
		if explicit.Port {
			settings.Port = requested.Port
		}
		if explicit.State {
			settings.State = requested.State
		}
		if explicit.WebDir {
			settings.WebDir = requested.WebDir
		}
	}
	if err := client.stop(ctx); err != nil {
		return Status{}, err
	}
	return client.spawn(ctx, settings)
}

func checkSettings(status Status, requested Settings, explicit Explicit) error {
	if status.ProtocolVersion != controlapi.ProtocolVersion {
		return ErrIncompatible
	}
	current := status.Settings

	settings := map[string]interface{}{}
	if explicit.Host && requested.Host != current.Host {
		settings["host"] = requested.Host
	}
	if explicit.Port && requested.Port > 0 && requested.Port != current.Port {
		settings["port"] = requested.Port
	}
	if explicit.State && requested.State != current.State {
		settings["state"] = requested.State
	}
	if explicit.WebDir && requested.WebDir != current.WebDir {
		settings["webDir"] = requested.WebDir
	}
	if len(settings) != 0 {
		raw, _ := json.Marshal(settings)
		return fmt.Errorf("service running at %s with different settings; update service config or run: servediff service restart --config '%s'", status.URL, raw)
	}
	return nil
}

func (client *Client) lifecycleLock(ctx context.Context) (*processlock.Lock, error) {
	if _, err := prepareRuntime(client.RuntimeDirectory); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lock, err := processlock.TryAcquire(LifecyclePath(client.RuntimeDirectory))
		if !errors.Is(err, processlock.ErrLocked) {
			return lock, err
		}
		if err := pause(ctx); err != nil {
			return nil, err
		}
	}
}

func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func (client *Client) stop(ctx context.Context) error {
	d, status, err := client.probe(ctx)
	if err != nil {
		return err
	}
	if status.State == "stopped" {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := controlapi.NewClient(d.Endpoint, d.Token).Shutdown(stopCtx); err != nil {
		return err
	}
	for {
		lock, err := processlock.TryAcquire(OwnershipPath(client.RuntimeDirectory))
		if err == nil {
			defer lock.Close()
			return RemoveDescriptor(client.RuntimeDirectory, status.InstanceID)
		}
		if !errors.Is(err, processlock.ErrLocked) {
			return err
		}
		if err := pause(stopCtx); err != nil {
			return fmt.Errorf("service shutdown timed out: %w", err)
		}
	}
}

func (client *Client) spawn(ctx context.Context, settings Settings) (Status, error) {
	connection, err := client.spawnConnection(ctx, settings)
	if err != nil {
		return Status{}, err
	}
	return connection.Status(), nil
}

func (client *Client) spawnConnection(ctx context.Context, settings Settings) (*Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if settings.Host == "" {
		settings.Host = "127.0.0.1"
	}
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"__daemon", "--host", settings.Host, "--port", strconv.Itoa(settings.Port), "--state", settings.State, "--runtime-dir", client.RuntimeDirectory}
	if settings.WebDir != "" {
		args = append(args, "--web-dir", settings.WebDir)
	}
	command := exec.Command(client.Executable, args...)
	detach(command)
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	if err := rotateLog(client.RuntimeDirectory); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(LogPath(client.RuntimeDirectory), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	command.Stdin, command.Stdout, command.Stderr = null, log, log
	if err := startupCtx.Err(); err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	for {
		select {
		case err := <-exited:
			return nil, fmt.Errorf("service failed to start (%v): %s", err, logTail(client.RuntimeDirectory))
		default:
		}
		d, status, err := client.probe(startupCtx)
		if err == nil && status.State == "running" {
			if err := checkSettings(status, settings, Explicit{Host: true, Port: true, State: settings.State != "", WebDir: true}); err != nil {
				return nil, err
			}
			return boundConnection(d, status)
		}
		if err := pause(startupCtx); err != nil {
			return nil, fmt.Errorf("service startup timed out: %w; %s", err, logTail(client.RuntimeDirectory))
		}
	}
}

func logTail(dir string) string {
	file, err := os.Open(filepath.Join(dir, "daemon.log"))
	if err != nil {
		return "see daemon log"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "see daemon log"
	}
	if info.Size() > 4096 {
		_, _ = file.Seek(-4096, io.SeekEnd)
	}
	data, _ := io.ReadAll(file)
	return strings.TrimSpace(string(data))
}

func (client *Client) RunningConnection(ctx context.Context) (*Connection, error) {
	descriptor, status, err := client.probe(ctx)
	if err != nil {
		return nil, err
	}
	if status.State != "running" {
		return nil, errors.New("service is stopped; run servediff to register a repository first")
	}
	return boundConnection(descriptor, status)
}
func (connection *Connection) Change(ctx context.Context, input contextservice.ChangeInput) (contextservice.ChangeEvent, error) {
	return connection.control.Change(ctx, input)
}
