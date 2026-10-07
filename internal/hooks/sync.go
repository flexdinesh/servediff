package hooks

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
)

// Collection is durable before health checks or local startup. A saved payload
// can outlive its checkout; once attempted, it remains bound to that database.
func (engine Engine) sync(ctx context.Context, directory string, event Event) error {
	job := filepath.Base(directory)
	capturePath := filepath.Join(directory, "capture.pending.json")
	captured, hasCapture := engine.latestCapture(directory)
	priorAttempt := hasCapture && captured.Target != (Target{})
	// Older workers persisted only a destination-specific pending file and did
	// not mark resolved jobs. Recover those payloads before attempting discovery.
	if hasCapture && !event.Resolved {
		event.Resolved = true
	}
	record := func(activity Activity) {
		activity.InputPath = event.InputPath
		if activity.Branch == "" {
			activity.Branch = event.Branch
		}
		if activity.Base == "" {
			activity.Base = event.Base
		}
		engine.RecordJob(job, event, activity)
	}
	if engine.Expand != nil && !event.Resolved {
		record(Activity{Stage: "discovery", Status: "running"})
		sources, err := engine.Expand(ctx, event)
		if err != nil {
			return err
		}
		var failures []error
		for _, source := range sources {
			if err := engine.Schedule(source); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) > 0 {
			return errors.Join(failures...)
		}
		record(Activity{Stage: "discovery", Status: "complete", FileCount: len(sources), Reason: "sources scheduled"})
		return nil
	}
	if engine.Resolve == nil || engine.Collect == nil || engine.Deliver == nil {
		return errors.New("hook worker callbacks unavailable")
	}
	if event.Identity != "" {
		var location Event
		if readJSON(filepath.Join(engine.Directory, "locations", key(event.Identity)+".json"), &location) == nil && location.Identity == event.Identity {
			event.Path, event.Branch = location.Path, location.Branch
		}
	}
	var unavailable error
	if engine.Normalize != nil {
		normalized, err := engine.Normalize(ctx, event)
		if errors.Is(err, ErrUnchanged) {
			record(Activity{Stage: "collection", Status: "skipped", Reason: err.Error()})
			return nil
		}
		if err != nil {
			if !errors.Is(err, ErrUnavailable) || !hasCapture {
				return err
			}
			unavailable = err
		} else {
			if path, err := filepath.EvalSymlinks(normalized.Path); err == nil {
				normalized.Path = path
			}
			if normalized.Path != event.Path && event.Identity == "" {
				return engine.Schedule(normalized)
			}
			event = normalized
		}
	}
	// Route-specific jobs permit an optimistic content check without contacting
	// the server. The acknowledgement is validated against health before skipping.
	cached := engine.latestAcknowledgement(directory)
	previous := ""
	if cached.Target.Identity != "" && time.Since(cached.At) < acknowledgementTTL && (engine.Confirm == nil || cached.ContextID != "") {
		previous = cached.Fingerprint
	}
	var fresh ingestion.Request
	fingerprint := ""
	unchanged := false
	if unavailable == nil {
		record(Activity{Stage: "collection", Status: "running", Branch: event.Branch, Base: event.Base})
		var err error
		fresh, fingerprint, err = engine.Collect(ctx, event, previous)
		unchanged = errors.Is(err, ErrUnchanged)
		if err != nil && !unchanged {
			if !errors.Is(err, ErrUnavailable) || !hasCapture {
				return err
			}
			unavailable = err
		}
		if err == nil {
			next := pending{state: state{Fingerprint: fingerprint, At: time.Now()}, Request: fresh}
			if hasCapture && fingerprint != "" && captured.Fingerprint == fingerprint {
				next = captured
			}
			if err := writeJSON(capturePath, next); err != nil {
				return err
			}
			captured, hasCapture = next, true
			record(requestActivity("captured", "pending", captured.Request))
		}
	}
	if unavailable != nil {
		fresh, fingerprint = captured.Request, captured.Fingerprint
		if err := writeJSON(capturePath, captured); err != nil {
			return err
		}
		record(Activity{Stage: "recovery", Status: "pending", SubmissionID: fresh.SubmissionID, Reason: "using saved payload", Error: unavailable.Error()})
	}
	record(Activity{Stage: "destination", Status: "running"})
	target, err := engine.Resolve(ctx, event)
	if err != nil {
		return err
	}
	stream := key(target.Destination, target.SourceID)
	ackPath := filepath.Join(directory, stream+".ack.json")
	pendingPath := filepath.Join(directory, stream+".pending.json")
	var ack state
	ackValid := readJSON(ackPath, &ack) == nil && target.Identity != "" && ack.Target == target && time.Since(ack.At) < acknowledgementTTL && (engine.Confirm == nil || ack.ContextID != "")
	if !ackValid && !priorAttempt && !hasCaptureBound(captured) && engine.SuppressInitialEmpty != nil && emptyCapture(fresh) {
		skip, reconcileErr := engine.SuppressInitialEmpty(ctx, target, fresh)
		if reconcileErr == nil && skip {
			_ = os.Remove(capturePath)
			record(Activity{Stage: "collection", Status: "skipped", Destination: target.Destination, Reason: "initial clean checkout has no retained stream history"})
			return nil
		}
	}
	if unchanged {
		current := ackValid && fingerprint == ack.Fingerprint
		if current && engine.Confirm != nil {
			current, err = engine.Confirm(ctx, target, ack.ContextID)
			if err != nil {
				return err
			}
		}
		if current {
			_ = os.Remove(pendingPath)
			_ = os.Remove(capturePath)
			record(Activity{Stage: "collection", Status: "unchanged", ContextID: ack.ContextID, Destination: target.Destination, Reason: "acknowledged context remains current"})
			return nil
		}
		fresh, fingerprint, err = engine.Collect(ctx, event, "")
		if err != nil {
			return err
		}
		captured = pending{state: state{Fingerprint: fingerprint, At: time.Now()}, Request: fresh}
		hasCapture = true
		if err := writeJSON(capturePath, captured); err != nil {
			return err
		}
	}
	if unavailable != nil && captured.Target != (Target{}) && (target.Identity == "" || captured.Target != target) {
		return errors.New("saved payload belongs to another destination or database; awaiting its original destination")
	}
	var saved pending
	hasPending := readJSON(pendingPath, &saved) == nil && target.Identity != "" && saved.Target == target && time.Since(saved.At) < pendingTTL
	if hasPending && saved.Fingerprint == "" {
		if _, err := engine.deliverSaved(ctx, job, event, target, saved, ackPath); err != nil {
			return err
		}
		if err := os.Remove(pendingPath); err != nil {
			return err
		}
		if unavailable != nil {
			return removeIfPresent(capturePath)
		}
		// Unknown complete identity cannot suppress the newly captured state.
		hasPending = false
	}
	replayed := hasPending && fingerprint != "" && saved.Fingerprint == fingerprint
	if replayed {
		captured = saved
	} else if !hasCapture {
		return errors.New("collection produced no durable payload")
	} else if captured.Target != (Target{}) && captured.Target != target {
		// A fresh capture can supersede a pending request bound to an old server.
		captured = pending{state: state{Fingerprint: fingerprint, At: time.Now()}, Request: fresh}
	}
	captured.Target = target
	if err := writeJSON(capturePath, captured); err != nil {
		return err
	}
	if err := writeJSON(pendingPath, captured); err != nil {
		return err
	}
	contextID, err := engine.deliverSaved(ctx, job, event, target, captured, ackPath)
	if err != nil {
		return err
	}
	if replayed && unavailable == nil && engine.Confirm != nil {
		current, err := engine.Confirm(ctx, target, contextID)
		if err != nil {
			return err
		}
		if !current {
			fresh, fingerprint, err = engine.Collect(ctx, event, "")
			if err != nil {
				return err
			}
			captured = pending{state: state{Target: target, Fingerprint: fingerprint, At: time.Now()}, Request: fresh}
			if err := writeJSON(capturePath, captured); err != nil {
				return err
			}
			if err := writeJSON(pendingPath, captured); err != nil {
				return err
			}
			if _, err := engine.deliverSaved(ctx, job, event, target, captured, ackPath); err != nil {
				return err
			}
		}
	}
	return errors.Join(removeIfPresent(pendingPath), removeIfPresent(capturePath))
}

func (engine Engine) latestAcknowledgement(directory string) state {
	var latest state
	paths, _ := filepath.Glob(filepath.Join(directory, "*.ack.json"))
	for _, path := range paths {
		var ack state
		if readJSON(path, &ack) == nil && ack.At.After(latest.At) {
			latest = ack
		}
	}
	return latest
}

func (engine Engine) latestCapture(directory string) (pending, bool) {
	var latest pending
	paths, _ := filepath.Glob(filepath.Join(directory, "*.pending.json"))
	for _, path := range paths {
		var saved pending
		if readJSON(path, &saved) == nil && saved.Request.SubmissionID != "" && time.Since(saved.At) < pendingTTL && saved.At.After(latest.At) {
			latest = saved
		}
	}
	return latest, latest.Request.SubmissionID != ""
}

func (engine Engine) deliverSaved(ctx context.Context, job string, event Event, target Target, saved pending, ackPath string) (string, error) {
	activity := requestActivity("ingestion", "running", saved.Request)
	activity.Destination = destinationLabel(target.Destination)
	engine.RecordJob(job, event, activity)
	contextID, err := engine.Deliver(ctx, target, saved.Request)
	if err != nil {
		activity.Status, activity.Error = "waiting", err.Error()
		activity.Reason = "immutable payload retained for retry"
		engine.RecordJob(job, event, activity)
		return "", err
	}
	if contextID == "" {
		return "", errors.New("ingestion receipt missing context identity")
	}
	if err := writeJSON(ackPath, state{Target: target, Fingerprint: saved.Fingerprint, ContextID: contextID, At: time.Now()}); err != nil {
		return "", err
	}
	activity.Status, activity.ContextID = "complete", contextID
	engine.RecordJob(job, event, activity)
	return contextID, nil
}

func requestActivity(stage, status string, request ingestion.Request) Activity {
	metadata := request.Metadata
	count := 0
	if len(request.Scopes) > 0 {
		count = len(request.Scopes[0].Snapshot.Files)
	}
	head := ""
	if metadata.Head != nil {
		head = *metadata.Head
	}
	return Activity{Stage: stage, Status: status, Path: metadata.Root, Branch: metadata.Branch, BranchID: metadata.BranchID, RepositoryKey: metadata.RepositoryKey,
		CheckoutKey: metadata.CheckoutKey, SubmissionID: request.SubmissionID, Head: head, FileCount: count}
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func destinationLabel(destination string) string {
	if endpoint, ok := strings.CutPrefix(destination, "remote:"); ok {
		if separator := strings.LastIndexByte(endpoint, ':'); separator >= 0 {
			if digest, err := hex.DecodeString(endpoint[separator+1:]); err == nil && len(digest) == 32 {
				return endpoint[:separator]
			}
		}
		return endpoint
	}
	return destination
}

// Retry is bounded, event-driven, and restricted to the invoking environment's
// route and config. Credentials are inherited, never read from persisted jobs.
func (engine Engine) Retry(event Event) error {
	entries, err := os.ReadDir(filepath.Join(engine.Directory, "jobs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory, err := engine.jobPath(entry.Name())
		if err != nil {
			continue
		}
		var saved queued
		if readJSON(filepath.Join(directory, "request.json"), &saved) != nil || saved.Event.Routing != event.Routing || saved.Event.ConfigFile != event.ConfigFile {
			continue
		}
		var status Activity
		if readJSON(filepath.Join(directory, "status.json"), &status) == nil && (status.Status == "complete" || status.Status == "unchanged" || status.Status == "skipped") {
			continue
		}
		if info, err := os.Stat(filepath.Join(directory, "request.json")); err != nil || time.Since(info.ModTime()) >= pendingTTL {
			continue
		}
		if count >= 128 {
			failures = append(failures, errors.New("collector retry limit reached; remaining jobs await another retry"))
			break
		}
		count++
		if engine.Launch == nil {
			return errors.New("hook worker launcher unavailable")
		}
		engine.Record(Activity{Stage: "retry", Status: "queued", Job: entry.Name(), Path: saved.Event.Path})
		if err := engine.Launch(entry.Name()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (engine Engine) Statuses() ([]Activity, error) {
	paths, err := filepath.Glob(filepath.Join(engine.Directory, "jobs", "*", "status.json"))
	if err != nil {
		return nil, err
	}
	statuses := make([]Activity, 0, len(paths))
	for _, path := range paths {
		var status Activity
		if err := readJSON(path, &status); err != nil {
			return nil, fmt.Errorf("read collector status %s: %w", path, err)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func hasCaptureBound(captured pending) bool { return captured.Target != (Target{}) }

func emptyCapture(request ingestion.Request) bool {
	return len(request.Scopes) > 0 && len(request.Scopes[0].Snapshot.Files) == 0
}
