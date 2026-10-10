package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/diffx/internal/controlapi"
	"github.com/flexdinesh/diffx/internal/processlock"
)

var ErrUnavailable = errors.New("service unavailable; ownership held but control endpoint is unreachable")

type Client struct {
	RuntimeDirectory string
}

func NewClient() (*Client, error) {
	dir, err := RuntimeDir()
	if err != nil {
		return nil, err
	}
	return &Client{RuntimeDirectory: dir}, nil
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
			status.Settings = normalizeSettings(status.Settings)
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

func (client *Client) Stop(ctx context.Context) error {
	lock, err := client.lifecycleLock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	return client.stop(ctx)
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
