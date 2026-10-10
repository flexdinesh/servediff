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

// Acknowledgement permits upload suppression only while its server context
// remains current. Fingerprints are supplied by the collector composition.
type Acknowledgement struct {
	Fingerprint string    `json:"fingerprint"`
	StateID     string    `json:"stateId"`
	ContextID   string    `json:"contextId"`
	At          time.Time `json:"at"`
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
	return box.write(box.path(pending.Request.SubmissionID), pending)
}

func (box *Outbox) acknowledgementPath(request ingestion.Request) string {
	metadata := request.Metadata
	branch := metadata.BranchID
	if branch == "" {
		branch = metadata.Branch
	}
	data, _ := json.Marshal([]string{metadata.SourceID, metadata.RepositoryKey, metadata.CheckoutKey, branch, ingestion.ComparisonPolicy(metadata), metadata.Agent, metadata.RunID})
	sum := sha256.Sum256(data)
	return filepath.Join(box.directory, "acknowledgements", hex.EncodeToString(sum[:])+".json")
}

func (box *Outbox) Acknowledgement(request ingestion.Request) (Acknowledgement, error) {
	raw, err := os.ReadFile(box.acknowledgementPath(request))
	if errors.Is(err, os.ErrNotExist) {
		return Acknowledgement{}, nil
	}
	if err != nil {
		return Acknowledgement{}, err
	}
	var ack Acknowledgement
	if err := json.Unmarshal(raw, &ack); err != nil {
		return Acknowledgement{}, err
	}
	if time.Since(ack.At) >= 24*time.Hour {
		return Acknowledgement{}, nil
	}
	return ack, nil
}

func (box *Outbox) Acknowledge(request ingestion.Request, fingerprint, stateID, contextID string) error {
	if fingerprint == "" {
		return nil
	}
	return box.write(box.acknowledgementPath(request), Acknowledgement{Fingerprint: fingerprint, StateID: stateID, ContextID: contextID, At: time.Now()})
}

func (box *Outbox) write(path string, value interface{}) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
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
	return os.Rename(file.Name(), path)
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
