package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/processlock"
)

var ErrUnavailable = errors.New("service unavailable; ownership held but control endpoint is unreachable")
var ErrIncompatible = errors.New("incompatible service protocol; run servediff service restart")

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
	d, readErr := ReadDescriptor(client.RuntimeDirectory)
	if readErr == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		status, err := controlapi.NewClient(d.Endpoint, d.Token).Status(probeCtx)
		if err == nil && status.InstanceID == d.Status.InstanceID {
			return d, status, nil
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

func (client *Client) Connection(ctx context.Context) (*controlapi.Client, error) {
	d, status, err := client.probe(ctx)
	if err != nil {
		return nil, err
	}
	if status.State != "running" {
		return nil, errors.New("service stopped")
	}
	if status.ProtocolVersion != controlapi.ProtocolVersion {
		return nil, ErrIncompatible
	}
	return controlapi.NewClient(d.Endpoint, d.Token), nil
}

func (client *Client) Ensure(ctx context.Context, requested Settings, explicit Explicit) (Status, error) {
	_, status, err := client.probe(ctx)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return client.Start(ctx, requested, explicit)
		}
		return status, err
	}
	if status.State == "running" {
		return status, checkSettings(status, requested, explicit)
	}
	if status.State != "stopped" {
		return status, errors.New("service stopping; retry after shutdown")
	}
	return client.Start(ctx, requested, explicit)
}

func (client *Client) Start(ctx context.Context, requested Settings, explicit Explicit) (Status, error) {
	lock, err := client.lifecycleLock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	_, status, err := client.probe(ctx)
	if err != nil {
		return status, err
	}
	if status.State == "running" {
		return status, checkSettings(status, requested, explicit)
	}
	if status.State != "stopped" {
		return status, errors.New("service stopping; retry after shutdown")
	}
	return client.spawn(ctx, requested)
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
	var flags []string
	if explicit.Host && requested.Host != current.Host {
		flags = append(flags, "--host "+requested.Host)
	}
	if explicit.Port && requested.Port > 0 && requested.Port != current.Port {
		flags = append(flags, "--port "+strconv.Itoa(requested.Port))
	}
	if explicit.State && requested.State != current.State {
		flags = append(flags, "--state "+requested.State)
	}
	if explicit.WebDir && requested.WebDir != current.WebDir {
		flags = append(flags, "--web-dir "+requested.WebDir)
	}
	if len(flags) != 0 {
		return fmt.Errorf("service running at %s with different settings; run: servediff service restart %s", status.URL, strings.Join(flags, " "))
	}
	return nil
}

func (client *Client) lifecycleLock(ctx context.Context) (*processlock.Lock, error) {
	if _, err := prepareRuntime(client.RuntimeDirectory); err != nil {
		return nil, err
	}
	for {
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
		return Status{}, err
	}
	defer null.Close()
	if err := rotateLog(client.RuntimeDirectory); err != nil {
		return Status{}, err
	}
	log, err := os.OpenFile(LogPath(client.RuntimeDirectory), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return Status{}, err
	}
	defer log.Close()
	command.Stdin, command.Stdout, command.Stderr = null, log, log
	if err := command.Start(); err != nil {
		return Status{}, err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	for {
		select {
		case err := <-exited:
			return Status{}, fmt.Errorf("service failed to start (%v): %s", err, logTail(client.RuntimeDirectory))
		default:
		}
		_, status, err := client.probe(startupCtx)
		if err == nil && status.State == "running" {
			if status.ProtocolVersion != controlapi.ProtocolVersion {
				return status, ErrIncompatible
			}
			return status, nil
		}
		if err := pause(startupCtx); err != nil {
			return Status{}, fmt.Errorf("service startup timed out: %w; %s", err, logTail(client.RuntimeDirectory))
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
