// Package submission preserves immutable manual uploads across CLI invocations.
package submission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/processlock"
)

type Pending struct {
	Request ingestion.Request `json:"request"`
	StateID string            `json:"stateId,omitempty"`
}

type Outbox struct {
	directory string
	lock      *processlock.Lock
}

func Open(ctx context.Context, root, route string) (*Outbox, error) {
	sum := sha256.Sum256([]byte(route))
	directory := filepath.Join(root, "sync-v4", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	for {
		lock, err := processlock.TryAcquire(filepath.Join(directory, "upload.lock"))
		if err == nil {
			return &Outbox{directory: directory, lock: lock}, nil
		}
		if !errors.Is(err, processlock.ErrLocked) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (box *Outbox) Close() error { return box.lock.Close() }
func (box *Outbox) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(box.directory, hex.EncodeToString(sum[:])+".json")
}
func (box *Outbox) Save(pending Pending) error {
	raw, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(box.directory, ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
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
	return os.Rename(file.Name(), box.path(pending.Request.SubmissionID))
}
func (box *Outbox) Remove(id string) error { return os.Remove(box.path(id)) }
func (box *Outbox) Pending() ([]Pending, error) {
	files, err := filepath.Glob(filepath.Join(box.directory, "*.json"))
	if err != nil {
		return nil, err
	}
	pending := make([]Pending, 0, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var item Pending
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		if err := ingestion.Validate(item.Request); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, nil
}
