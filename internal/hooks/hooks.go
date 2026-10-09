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
	"github.com/flexdinesh/servediff/internal/submission"
)

var ErrUnchanged = errors.New("checkout unchanged")
var ErrCooldown = errors.New("service startup cooling down")
var ErrUnavailable = errors.New("collection source unavailable")

const defaultTimeout = 30 * time.Second
const acknowledgementTTL = 24 * time.Hour
const pendingTTL = 7 * 24 * time.Hour
const maxPendingBytes = 256 << 20

type Event struct {
	Path        string `json:"path"`
	InputPath   string `json:"inputPath,omitempty"`
	Identity    string `json:"identity,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Base        string `json:"base,omitempty"`
	Resolved    bool   `json:"resolved,omitempty"`
	Retry       bool   `json:"-"`
	Agent       string `json:"agent"`
	RunID       string `json:"runId,omitempty"`
	SessionName string `json:"sessionName,omitempty"`
	ConfigFile  string `json:"configFile,omitempty"`
	Routing     string `json:"routing,omitempty"`
}

// Identity identifies the server's persistent database (or memory instance).
// An unknown identity disables suppression and prevents uncertain replay.
type Target = submission.Target

type Engine struct {
	Directory        string
	Expand           func(context.Context, Event) ([]Event, error)
	ValidateLocation func(Event, Event) error
	Normalize        func(context.Context, Event) (Event, error)
	Resolve          func(context.Context, Event) (Target, error)
	Collect          func(context.Context, Event, string) (ingestion.Request, string, error)
	Deliver          func(context.Context, Target, ingestion.Request) (string, error)
	// Confirm checks cached context freshness only after collection reports
	// unchanged. Production callers provide it to detect other producers.
	Confirm func(context.Context, Target, string) (bool, error)
	// SuppressInitialEmpty proves a clean capture has no retained stream history.
	// Errors keep the conservative publication path.
	SuppressInitialEmpty func(context.Context, Target, ingestion.Request) (bool, error)
	Launch               func(string) error
	Timeout              time.Duration
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
		return filepath.Join(runtime, "hooks-v3"), nil
	}
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(root, "servediff", "hooks-v3"), nil
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
	if event.InputPath == "" {
		event.InputPath = event.Path
	}
	identity := path
	if event.Identity != "" {
		identity = event.Identity
		locationPath := filepath.Join(engine.Directory, "locations", key(identity)+".json")
		locationLock, err := processlock.Acquire(locationPath + ".lock")
		if err != nil {
			return err
		}
		var previous Event
		if readJSON(locationPath, &previous) == nil && engine.ValidateLocation != nil {
			if err := engine.ValidateLocation(previous, event); err != nil {
				_ = locationLock.Close()
				engine.Record(Activity{Stage: "identity", Status: "waiting", Path: path, Error: err.Error()})
				return err
			}
		}
		err = writeJSON(locationPath, event)
		_ = locationLock.Close()
		if err != nil {
			return err
		}
	}
	job := key(identity, event.ConfigFile, event.Routing, event.Branch, event.Base, event.Agent, event.RunID, event.SessionName)
	// A stable branch identity already carries its branch; its label may change.
	if event.Identity != "" {
		job = key(identity, event.ConfigFile, event.Routing, event.Base, event.Agent, event.RunID, event.SessionName)
	}
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
	engine.RecordJob(job, event, Activity{Stage: "scheduled", Status: "queued"})
	if engine.Launch == nil {
		return errors.New("hook worker launcher unavailable")
	}
	if err := engine.Launch(job); err != nil {
		engine.RecordJob(job, event, Activity{Stage: "launch", Status: "failed", Error: err.Error()})
		return err
	}
	return nil
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
		engine.Record(Activity{Job: job, Stage: "coalesced", Status: "running"})
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
			var activity Activity
			_ = readJSON(filepath.Join(directory, "status.json"), &activity)
			activity.Time = time.Now().UTC()
			activity.Stage, activity.Status = "worker", "waiting"
			activity.Error, activity.Reason = syncErr.Error(), "retry on next completion or collector retry"
			engine.RecordJob(job, request.Event, activity)
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
	if err := file.Sync(); err != nil {
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
