package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
)

func TestCaptureSurvivesDestinationFailureAndSourceRemoval(t *testing.T) {
	engine, job := fixture(t)
	available, healthy := true, false
	collections, deliveries := 0, 0
	engine.Normalize = func(_ context.Context, event Event) (Event, error) {
		if !available {
			return event, ErrUnavailable
		}
		return event, nil
	}
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		collections++
		return ingestion.Request{SubmissionID: "immutable"}, "contents", nil
	}
	engine.Resolve = func(context.Context, Event) (Target, error) {
		directory, _ := engine.jobPath(*job)
		var captured pending
		if err := readJSON(filepath.Join(directory, "capture.pending.json"), &captured); err != nil || captured.Request.SubmissionID != "immutable" {
			t.Fatalf("health contacted before durable capture: %#v %v", captured, err)
		}
		if !healthy {
			return Target{}, errors.New("server unavailable")
		}
		return Target{Destination: "local", Identity: "database", SourceID: "machine"}, nil
	}
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		deliveries++
		if request.SubmissionID != "immutable" {
			t.Fatal("retry replaced immutable submission")
		}
		return "context", nil
	}
	trigger(t, engine)
	if err := engine.Run(t.Context(), *job); err == nil {
		t.Fatal("destination failure ignored")
	}
	available, healthy = false, true
	if err := engine.Run(t.Context(), *job); err != nil {
		t.Fatal(err)
	}
	if collections != 1 || deliveries != 1 {
		t.Fatalf("collections=%d deliveries=%d", collections, deliveries)
	}
	statuses, err := engine.Statuses()
	if err != nil || len(statuses) != 1 || statuses[0].Status != "complete" || statuses[0].ContextID != "context" {
		t.Fatalf("delivery status: %#v %v", statuses, err)
	}
}

func TestRemovedSourceCannotReplayIntoReplacementDatabase(t *testing.T) {
	engine, job := fixture(t)
	available := true
	identity := "original"
	deliveries := 0
	engine.Normalize = func(_ context.Context, event Event) (Event, error) {
		if !available {
			return event, ErrUnavailable
		}
		return event, nil
	}
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		return ingestion.Request{SubmissionID: "possibly-committed"}, "contents", nil
	}
	engine.Resolve = func(context.Context, Event) (Target, error) {
		return Target{Destination: "local", Identity: identity, SourceID: "machine"}, nil
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) {
		deliveries++
		return "", errors.New("receipt lost")
	}
	trigger(t, engine)
	_ = engine.Run(t.Context(), *job)
	available, identity = false, "replacement"
	if err := engine.Run(t.Context(), *job); err == nil || deliveries != 1 {
		t.Fatalf("replacement replayed ambiguous request: deliveries=%d error=%v", deliveries, err)
	}
}

func TestStableJobFollowsMoveAndBranchRename(t *testing.T) {
	engine, job := fixture(t)
	old := filepath.Join(t.TempDir(), "old")
	current := filepath.Join(t.TempDir(), "moved")
	first := Event{Path: old, InputPath: old, Identity: "stable-source", Branch: "feature", Base: "auto", Resolved: true}
	if err := engine.Schedule(first); err != nil {
		t.Fatal(err)
	}
	originalJob := *job
	renamed := first
	renamed.Path, renamed.Branch = current, "renamed"
	if err := engine.Schedule(renamed); err != nil || *job != originalJob {
		t.Fatalf("move changed job: %s %s %v", originalJob, *job, err)
	}
	engine.Normalize = func(_ context.Context, event Event) (Event, error) {
		if event.Path != current || event.Branch != "renamed" {
			t.Fatalf("stale source selected: %#v", event)
		}
		return event, nil
	}
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		return ingestion.Request{SubmissionID: "current"}, "contents", nil
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { return "context", nil }
	// A worker already holding the pre-move event must use the current registry.
	directory, _ := engine.jobPath(originalJob)
	if err := engine.sync(t.Context(), directory, first); err != nil {
		t.Fatal(err)
	}
}

func TestRetryIsRouteScopedAndSkipsCompletedJobs(t *testing.T) {
	engine, _ := fixture(t)
	var launches []string
	engine.Launch = func(key string) error { launches = append(launches, key); return nil }
	for _, route := range []string{"first", "second"} {
		if err := engine.Schedule(Event{Path: "/checkout", Routing: route}); err != nil {
			t.Fatal(err)
		}
	}
	launches = nil
	if err := engine.Retry(Event{Routing: "first"}); err != nil || len(launches) != 1 {
		t.Fatalf("retry routes: %v %v", launches, err)
	}
	var request queued
	directory, _ := engine.jobPath(launches[0])
	if err := readJSON(filepath.Join(directory, "request.json"), &request); err != nil || request.Event.Routing != "first" {
		t.Fatalf("wrong route replayed: %#v %v", request, err)
	}
	engine.RecordJob(launches[0], request.Event, Activity{Status: "complete"})
	launches = nil
	if err := engine.Retry(Event{Routing: "first"}); err != nil || len(launches) != 0 {
		t.Fatalf("completed job relaunched: %v %v", launches, err)
	}
}

func TestLegacyPendingReplayDoesNotRequireDiscoveryOrCheckout(t *testing.T) {
	engine, job := fixture(t)
	engine.Expand = func(context.Context, Event) ([]Event, error) {
		t.Fatal("legacy immutable payload was sent through workspace discovery")
		return nil, nil
	}
	engine.Normalize = func(_ context.Context, event Event) (Event, error) { return event, ErrUnavailable }
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		t.Fatal("legacy removed source was collected")
		return ingestion.Request{}, "", nil
	}
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		if request.SubmissionID != "legacy" {
			t.Fatalf("legacy request replaced: %#v", request)
		}
		return "context", nil
	}
	trigger(t, engine)
	directory, _ := engine.jobPath(*job)
	target, _ := engine.Resolve(t.Context(), Event{})
	saved := pending{state: state{Target: target, Fingerprint: "legacy-state", At: time.Now()}, Request: ingestion.Request{SubmissionID: "legacy"}}
	if err := writeJSON(filepath.Join(directory, key(target.Destination, target.SourceID)+".pending.json"), saved); err != nil {
		t.Fatal(err)
	}
	if err := engine.Run(t.Context(), *job); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableSourceWithNoCaptureRemainsWaiting(t *testing.T) {
	engine, job := fixture(t)
	engine.Normalize = func(_ context.Context, event Event) (Event, error) { return event, ErrUnavailable }
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		t.Fatal("unavailable source was collected")
		return ingestion.Request{}, "", nil
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) {
		t.Fatal("uncaptured edits were fabricated")
		return "", nil
	}
	trigger(t, engine)
	if err := engine.Run(t.Context(), *job); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing source error: %v", err)
	}
	directory, _ := engine.jobPath(*job)
	if _, err := os.Stat(filepath.Join(directory, "capture.pending.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fabricated capture: %v", err)
	}
}
