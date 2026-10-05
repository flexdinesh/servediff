// Package hooks schedules finite, detached checkout synchronization workers.
package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/processlock"
)

var ErrUnchanged = errors.New("checkout unchanged")
var ErrCooldown = errors.New("service startup cooling down")

const defaultTimeout = 30 * time.Second
const acknowledgementTTL = 24 * time.Hour
const pendingTTL = 7 * 24 * time.Hour
const maxPendingBytes = 256 << 20

type Event struct {
	Path       string `json:"path"`
	Agent      string `json:"agent"`
	RunID      string `json:"runId,omitempty"`
	ConfigFile string `json:"configFile,omitempty"`
	Routing    string `json:"routing,omitempty"`
}

// Identity identifies the server's persistent database (or memory instance).
// An unknown identity disables suppression and prevents uncertain replay.
type Target struct {
	Destination string
	Identity    string
	SourceID    string
}

type Engine struct {
	Directory string
	Normalize func(context.Context, Event) (Event, error)
	Resolve   func(context.Context, Event) (Target, error)
	Collect   func(context.Context, Event, string) (ingestion.Request, string, error)
	Deliver   func(context.Context, Target, ingestion.Request) (string, error)
	// Confirm checks cached context freshness only after collection reports
	// unchanged. Production callers provide it to detect other producers.
	Confirm func(context.Context, Target, string) (bool, error)
	Launch  func(string) error
	Timeout time.Duration
}

type queued struct {
	Event Event  `json:"event"`
	ID    string `json:"id"`
}

type state struct {
	Target      Target    `json:"target"`
	Fingerprint string    `json:"fingerprint"`
	ContextID   string    `json:"contextId,omitempty"`
	At          time.Time `json:"at"`
}

type pending struct {
	state
	Request ingestion.Request `json:"request"`
}

func StateDirectory() (string, error) {
	if runtime := os.Getenv("SERVEDIFF_RUNTIME_DIR"); runtime != "" {
		return filepath.Join(runtime, "hooks"), nil
	}
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(root, "servediff", "hooks"), nil
}

func key(parts ...string) string {
	data, _ := json.Marshal(parts)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (engine Engine) jobPath(job string) (string, error) {
	decoded, err := hex.DecodeString(job)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("invalid hook job")
	}
	return filepath.Join(engine.Directory, "jobs", job), nil
}

// Schedule writes only a small request. Collection and transport never run here.
func (engine Engine) Schedule(event Event) error {
	path, err := filepath.Abs(event.Path)
	if err != nil {
		return err
	}
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		path = canonical
	}
	event.Path = path
	if event.ConfigFile != "" {
		event.ConfigFile, err = filepath.Abs(event.ConfigFile)
		if err != nil {
			return err
		}
	}
	job := key(path, event.ConfigFile, event.Routing)
	directory, _ := engine.jobPath(job)
	lock, err := processlock.Acquire(filepath.Join(directory, "queue.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	request := queued{Event: event, ID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())}
	if err := writeJSON(filepath.Join(directory, "request.json"), request); err != nil {
		return err
	}
	if engine.Launch == nil {
		return errors.New("hook worker launcher unavailable")
	}
	return engine.Launch(job)
}

// Run owns one checkout until all observed requests are processed. Queue writes
// and worker exit share a short lock, preventing a trigger losing its worker.
func (engine Engine) Run(ctx context.Context, job string) error {
	directory, err := engine.jobPath(job)
	if err != nil {
		return err
	}
	worker, err := processlock.TryAcquire(filepath.Join(directory, "worker.lock"))
	if errors.Is(err, processlock.ErrLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer engine.prune()
	defer worker.Close()
	timeout := engine.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	engine.prune()
	for {
		var request queued
		if err := readJSON(filepath.Join(directory, "request.json"), &request); err != nil {
			return err
		}
		syncErr := engine.sync(ctx, directory, request.Event)
		if syncErr != nil {
			engine.Log(syncErr)
		}
		queue, err := processlock.Acquire(filepath.Join(directory, "queue.lock"))
		if err != nil {
			return err
		}
		var latest queued
		err = readJSON(filepath.Join(directory, "request.json"), &latest)
		if err != nil || latest.ID == request.ID || ctx.Err() != nil {
			// Release ownership before scheduling can launch the next worker.
			_ = worker.Close()
			if err == nil && latest.ID != request.ID && engine.Launch != nil {
				// Deadline exhausted while a newer event arrived: hand it off.
				err = engine.Launch(job)
			}
			_ = queue.Close()
			return errors.Join(syncErr, err)
		}
		_ = queue.Close()
	}
}

func (engine Engine) sync(ctx context.Context, directory string, event Event) error {
	if engine.Resolve == nil || engine.Collect == nil || engine.Deliver == nil {
		return errors.New("hook worker callbacks unavailable")
	}
	if engine.Normalize != nil {
		normalized, err := engine.Normalize(ctx, event)
		if errors.Is(err, ErrUnchanged) {
			return nil
		}
		if err != nil {
			return err
		}
		if path, err := filepath.EvalSymlinks(normalized.Path); err == nil {
			normalized.Path = path
		}
		if normalized.Path != event.Path {
			return engine.Schedule(normalized)
		}
		event = normalized
	}
	target, err := engine.Resolve(ctx, event)
	if err != nil {
		return err
	}
	stream := key(target.Destination, target.SourceID)
	ackPath := filepath.Join(directory, stream+".ack.json")
	pendingPath := filepath.Join(directory, stream+".pending.json")
	var ack state
	previous := ""
	if readJSON(ackPath, &ack) == nil && target.Identity != "" && ack.Target == target && (engine.Confirm == nil || ack.ContextID != "") && time.Since(ack.At) < acknowledgementTTL {
		previous = ack.Fingerprint
	}
	var saved pending
	hasPending := readJSON(pendingPath, &saved) == nil && target.Identity != "" && saved.Target == target && time.Since(saved.At) < pendingTTL
	if hasPending && saved.Fingerprint == "" {
		contextID, err := engine.Deliver(ctx, target, saved.Request)
		if err != nil {
			return err
		}
		if err := writeJSON(ackPath, state{Target: target, ContextID: contextID, At: time.Now()}); err != nil {
			return err
		}
		if err := os.Remove(pendingPath); err != nil {
			return err
		}
		// Without a verified full-content fingerprint, the checkout may have
		// changed since capture. Retry first, then capture its latest state.
		hasPending = false
		previous = ""
	}
	request, fingerprint, err := engine.Collect(ctx, event, previous)
	if errors.Is(err, ErrUnchanged) {
		current := true
		if engine.Confirm != nil {
			current, err = engine.Confirm(ctx, target, ack.ContextID)
			if err != nil {
				return err
			}
		}
		if current {
			// A failed observation is now obsolete (e.g. reverted edits).
			_ = os.Remove(pendingPath)
			return nil
		}
		// Another collector advanced the stream or retention removed our
		// context. Recollect even though local files still match our cache.
		request, fingerprint, err = engine.Collect(ctx, event, "")
	}
	if err != nil {
		return err
	}
	replayed := hasPending && fingerprint != "" && saved.Fingerprint == fingerprint
	if replayed {
		request = saved.Request
	} else {
		saved = pending{state: state{Target: target, Fingerprint: fingerprint, At: time.Now()}, Request: request}
		if err := writeJSON(pendingPath, saved); err != nil {
			return err
		}
	}
	contextID, err := engine.Deliver(ctx, target, request)
	if err != nil {
		return err
	}
	if replayed && engine.Confirm != nil {
		current, err := engine.Confirm(ctx, target, contextID)
		if err != nil {
			return err
		}
		if !current {
			// Replays retain capture time and cannot supersede a newer external
			// observation. Capture once more with a fresh submission identity.
			request, fingerprint, err = engine.Collect(ctx, event, "")
			if err != nil {
				return err
			}
			saved = pending{state: state{Target: target, Fingerprint: fingerprint, At: time.Now()}, Request: request}
			if err := writeJSON(pendingPath, saved); err != nil {
				return err
			}
			contextID, err = engine.Deliver(ctx, target, request)
			if err != nil {
				return err
			}
		}
	}
	if err := writeJSON(ackPath, state{Target: target, ContextID: contextID, Fingerprint: fingerprint, At: time.Now()}); err != nil {
		return err
	}
	return os.Remove(pendingPath)
}

func writeJSON(path string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > ingestion.MaxRequestBytes+(1<<20) {
		return errors.New("hook state exceeds size limit")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".hook-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func readJSON(path string, value interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > ingestion.MaxRequestBytes+(1<<20) {
		return errors.New("hook state exceeds size limit")
	}
	return json.NewDecoder(file).Decode(value)
}
