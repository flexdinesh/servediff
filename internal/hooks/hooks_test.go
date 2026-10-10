package hooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/processlock"
	"github.com/flexdinesh/diffx/internal/review"
)

func fixture(t *testing.T) (Engine, *string) {
	t.Helper()
	var job string
	engine := Engine{
		Directory: t.TempDir(),
		Resolve: func(context.Context, Event) (Target, error) {
			return Target{Destination: "local", Identity: "database", SourceID: "machine"}, nil
		},
		Launch: func(key string) error { job = key; return nil },
	}
	return engine, &job
}

func trigger(t *testing.T, engine Engine) {
	t.Helper()
	if err := engine.Schedule(Event{Path: "/checkout", Agent: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestAcknowledgedFingerprintAndCleanTransition(t *testing.T) {
	engine, job := fixture(t)
	current := "dirty"
	var sent []string
	engine.Collect = func(_ context.Context, _ Event, previous string) (ingestion.Request, string, error) {
		if previous == current {
			return ingestion.Request{}, current, ErrUnchanged
		}
		return ingestion.Request{SubmissionID: current}, current, nil
	}
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		sent = append(sent, request.SubmissionID)
		return "context", nil
	}
	for _, state := range []string{"dirty", "dirty", "clean", "clean"} {
		current = state
		trigger(t, engine)
		if err := engine.Run(context.Background(), *job); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 2 || sent[0] != "dirty" || sent[1] != "clean" {
		t.Fatalf("deliveries = %v", sent)
	}
}

func TestAmbiguousRetryPreservesSubmissionAndNewStateSupersedes(t *testing.T) {
	engine, job := fixture(t)
	current := "one"
	count := 0
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		count++
		return ingestion.Request{SubmissionID: current + time.Now().String()}, current, nil
	}
	var sent []ingestion.Request
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		sent = append(sent, request)
		if len(sent) < 3 {
			return "", errors.New("receipt lost")
		}
		return "context", nil
	}
	for i := 0; i < 3; i++ {
		if i == 2 {
			current = "two"
		}
		trigger(t, engine)
		_ = engine.Run(context.Background(), *job)
	}
	if sent[0].SubmissionID != sent[1].SubmissionID || sent[1].SubmissionID == sent[2].SubmissionID || count != 3 {
		t.Fatalf("retry/supersession = %v", sent)
	}
}

func TestTargetReplacementInvalidatesAcknowledgementAndPending(t *testing.T) {
	engine, job := fixture(t)
	target := Target{Destination: "local", Identity: "old", SourceID: "machine"}
	engine.Resolve = func(context.Context, Event) (Target, error) { return target, nil }
	var previous []string
	count := 0
	engine.Collect = func(_ context.Context, _ Event, fingerprint string) (ingestion.Request, string, error) {
		previous = append(previous, fingerprint)
		count++
		return ingestion.Request{SubmissionID: time.Now().String()}, "same", nil
	}
	var sent []ingestion.Request
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		sent = append(sent, request)
		if len(sent) == 2 {
			return "", errors.New("receipt lost")
		}
		return "context", nil
	}
	for i := 0; i < 3; i++ {
		if i == 2 {
			target.Identity = "replacement"
		}
		trigger(t, engine)
		_ = engine.Run(context.Background(), *job)
	}
	// The content check now precedes health. Only the delivery may reuse a
	// pending submission, and replacing the database must prevent that replay.
	if len(sent) != 3 || previous[0] != "" || previous[1] != "same" || sent[1].SubmissionID == sent[2].SubmissionID {
		t.Fatalf("replacement previous=%v sent=%v", previous, sent)
	}
}

func TestUnknownIdentityAndExpiredAcknowledgementNeverSkip(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired", true: "unknown"}[unknown], func(t *testing.T) {
			engine, job := fixture(t)
			target, _ := engine.Resolve(context.Background(), Event{})
			if unknown {
				target.Identity = ""
			}
			engine.Resolve = func(context.Context, Event) (Target, error) { return target, nil }
			engine.Collect = func(_ context.Context, _ Event, previous string) (ingestion.Request, string, error) {
				if previous != "" {
					t.Fatalf("unexpected suppression %q", previous)
				}
				return ingestion.Request{SubmissionID: "new"}, "new", nil
			}
			engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { return "context", nil }
			trigger(t, engine)
			directory, _ := engine.jobPath(*job)
			ack := state{Target: target, Fingerprint: "old", At: time.Now().Add(-acknowledgementTTL - time.Minute)}
			if unknown {
				ack.At = time.Now()
			}
			if err := writeJSON(filepath.Join(directory, key(target.Destination, target.SourceID)+".ack.json"), ack); err != nil {
				t.Fatal(err)
			}
			if err := engine.Run(context.Background(), *job); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTriggerDuringUploadCoalescesWithoutLosingNewState(t *testing.T) {
	engine, job := fixture(t)
	started, resume := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var events []Event
	engine.Collect = func(_ context.Context, event Event, _ string) (ingestion.Request, string, error) {
		return ingestion.Request{SubmissionID: event.InputPath}, event.InputPath, nil
	}
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		mu.Lock()
		events = append(events, Event{RunID: request.SubmissionID})
		first := len(events) == 1
		mu.Unlock()
		if first {
			close(started)
			<-resume
		}
		return "context", nil
	}
	if err := engine.Schedule(Event{Path: "/checkout", RunID: "session", InputPath: "first"}); err != nil {
		t.Fatal(err)
	}
	workerJob := *job
	done := make(chan error, 1)
	go func() { done <- engine.Run(context.Background(), workerJob) }()
	<-started
	for _, id := range []string{"middle", "latest"} {
		if err := engine.Schedule(Event{Path: "/checkout", RunID: "session", InputPath: id}); err != nil {
			t.Fatal(err)
		}
		if err := engine.Run(context.Background(), workerJob); err != nil {
			t.Fatal(err)
		}
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].RunID != "first" || events[1].RunID != "latest" {
		t.Fatalf("events = %v", events)
	}
}

func TestDeadlineHandsOffNewTrigger(t *testing.T) {
	engine, job := fixture(t)
	engine.Timeout = 40 * time.Millisecond
	started := make(chan struct{})
	engine.Collect = func(ctx context.Context, _ Event, _ string) (ingestion.Request, string, error) {
		close(started)
		<-ctx.Done()
		return ingestion.Request{}, "", ctx.Err()
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { return "context", nil }
	trigger(t, engine)
	workerJob := *job
	var launches atomic.Int32
	engine.Launch = func(string) error { launches.Add(1); return nil }
	done := make(chan error, 1)
	go func() { done <- engine.Run(context.Background(), workerJob) }()
	<-started
	trigger(t, engine)
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if launches.Load() != 2 {
		t.Fatalf("handoff launches = %d", launches.Load())
	}
}

func TestStartupCooldownSharedAcrossCheckouts(t *testing.T) {
	engine, _ := fixture(t)
	attempts := 0
	start := func(context.Context) error { attempts++; return errors.New("broken binary") }
	if err := engine.Start(context.Background(), "local", start); err == nil {
		t.Fatal("expected failure")
	}
	if err := engine.Start(context.Background(), "local", start); !errors.Is(err, ErrCooldown) || attempts != 1 {
		t.Fatalf("cooldown err=%v attempts=%d", err, attempts)
	}
	if err := engine.Start(context.Background(), "another", start); err == nil || attempts != 2 {
		t.Fatalf("independent destination err=%v attempts=%d", err, attempts)
	}
}

func TestConcurrentStartupWaitsForSharedSuccess(t *testing.T) {
	engine, _ := fixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- engine.Start(context.Background(), "local", func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	var secondCalls atomic.Int32
	second := make(chan error, 1)
	go func() {
		second <- engine.Start(context.Background(), "local", func(context.Context) error {
			secondCalls.Add(1)
			return nil
		})
	}()
	select {
	case err := <-second:
		t.Fatalf("second worker dropped before startup completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil || secondCalls.Load() != 1 {
		t.Fatalf("second worker did not continue: %v calls=%d", err, secondCalls.Load())
	}
}

func TestConcurrentStartupFailureAttemptsOnce(t *testing.T) {
	engine, _ := fixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- engine.Start(context.Background(), "local", func(context.Context) error {
			close(started)
			<-release
			return errors.New("failed startup")
		})
	}()
	<-started
	var secondCalls atomic.Int32
	second := make(chan error, 1)
	go func() {
		second <- engine.Start(context.Background(), "local", func(context.Context) error {
			secondCalls.Add(1)
			return nil
		})
	}()
	select {
	case err := <-second:
		t.Fatalf("second worker did not wait for outcome: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-first; err == nil {
		t.Fatal("startup succeeded unexpectedly")
	}
	if err := <-second; !errors.Is(err, ErrCooldown) || secondCalls.Load() != 0 {
		t.Fatalf("second worker retried failed startup: %v calls=%d", err, secondCalls.Load())
	}
}

func TestStartupLockWaitHonorsDeadline(t *testing.T) {
	engine, _ := fixture(t)
	path := filepath.Join(engine.Directory, "startup", key("local"), "startup.lock")
	lock, err := processlock.TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := engine.Start(ctx, "local", func(context.Context) error {
		t.Fatal("startup callback called without ownership")
		return nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup wait error = %v", err)
	}
}

func TestPrunePendingRetentionAndPrivateFiles(t *testing.T) {
	engine, job := fixture(t)
	trigger(t, engine)
	directory, _ := engine.jobPath(*job)
	path := filepath.Join(directory, "old.pending.json")
	if err := writeJSON(path, pending{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private file = %v %v", info, err)
	}
	old := time.Now().Add(-pendingTTL - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	engine.prune()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired payload = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "worker.lock")); err != nil {
		t.Fatalf("lock inode removed: %v", err)
	}
}

func TestRuntimeIsolationAndSafeJobNames(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("DIFFX_RUNTIME_DIR", runtime)
	directory, err := StateDirectory()
	if err != nil || directory != filepath.Join(runtime, "hooks-v4") {
		t.Fatalf("directory = %q %v", directory, err)
	}
	engine := Engine{Directory: directory}
	if err := engine.Run(context.Background(), "../../outside"); err == nil {
		t.Fatal("unsafe job accepted")
	}
}

func TestNestedCheckoutReschedulesBeforeCollection(t *testing.T) {
	engine, job := fixture(t)
	engine.Normalize = func(_ context.Context, event Event) (Event, error) {
		event.Path = "/checkout"
		return event, nil
	}
	collected := 0
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		collected++
		return ingestion.Request{SubmissionID: "latest"}, "latest", nil
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { return "context", nil }
	if err := engine.Schedule(Event{Path: "/checkout/nested"}); err != nil {
		t.Fatal(err)
	}
	nestedJob := *job
	if err := engine.Run(context.Background(), nestedJob); err != nil {
		t.Fatal(err)
	}
	if *job == nestedJob || collected != 0 {
		t.Fatalf("nested job was collected: %d", collected)
	}
	if err := engine.Run(context.Background(), *job); err != nil || collected != 1 {
		t.Fatalf("canonical job collected=%d err=%v", collected, err)
	}
}

func TestUnknownFingerprintRetryAlsoUploadsLatestState(t *testing.T) {
	engine, job := fixture(t)
	collections, uploads := 0, 0
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		collections++
		return ingestion.Request{SubmissionID: fmt.Sprintf("observation-%d", collections)}, "", nil
	}
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		uploads++
		if (uploads <= 2 && request.SubmissionID != "observation-1") || (uploads == 3 && request.SubmissionID != "observation-2") {
			t.Fatalf("retry/latest observation: %v", request)
		}
		if uploads == 1 {
			return "", errors.New("receipt lost")
		}
		return "context", nil
	}
	trigger(t, engine)
	_ = engine.Run(context.Background(), *job)
	trigger(t, engine)
	if err := engine.Run(context.Background(), *job); err != nil {
		t.Fatal(err)
	}
	if collections != 2 || uploads != 3 {
		t.Fatalf("collections=%d uploads=%d", collections, uploads)
	}
}

func TestPendingPruningSkipsActiveWorker(t *testing.T) {
	engine, job := fixture(t)
	trigger(t, engine)
	directory, _ := engine.jobPath(*job)
	path := filepath.Join(directory, "old.pending.json")
	if err := writeJSON(path, pending{}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-pendingTTL - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	lock, err := processlock.TryAcquire(filepath.Join(directory, "worker.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	engine.prune()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active pending removed: %v", err)
	}
}

func TestPendingBudgetRemovesOldestPayload(t *testing.T) {
	engine, job := fixture(t)
	trigger(t, engine)
	directory, _ := engine.jobPath(*job)
	for i, name := range []string{"first.pending.json", "second.pending.json"} {
		path := filepath.Join(directory, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		// Sparse files exercise the disk budget without allocating payloads.
		if err := file.Truncate(maxPendingBytes/2 + 1); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		at := time.Now().Add(time.Duration(i-2) * time.Hour)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	engine.prune()
	if _, err := os.Stat(filepath.Join(directory, "first.pending.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "second.pending.json")); err != nil {
		t.Fatalf("newest removed: %v", err)
	}
}

func TestLogsStayBounded(t *testing.T) {
	engine, _ := fixture(t)
	for i := 0; i < 80; i++ {
		engine.Log(errors.New(strings.Repeat("x", 6000)))
	}
	info, err := os.Stat(filepath.Join(engine.Directory, "hooks.log"))
	if err != nil || info.Size() > maxLogBytes+4200 || info.Mode().Perm() != 0o600 {
		t.Fatalf("log bound/privacy = %v %v", info, err)
	}
}

func TestAcknowledgedContextMustRemainCurrentBeforeSkipping(t *testing.T) {
	for _, reason := range []string{"another collector", "expired", "missing"} {
		t.Run(reason, func(t *testing.T) {
			engine, job := fixture(t)
			latest := ""
			collections, uploads, confirmations := 0, 0, 0
			engine.Collect = func(_ context.Context, _ Event, previous string) (ingestion.Request, string, error) {
				collections++
				if previous == "state-A" {
					return ingestion.Request{}, previous, ErrUnchanged
				}
				return ingestion.Request{SubmissionID: fmt.Sprintf("capture-%d", collections)}, "state-A", nil
			}
			engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
				uploads++
				latest = request.SubmissionID
				return latest, nil
			}
			engine.Confirm = func(_ context.Context, _ Target, contextID string) (bool, error) {
				confirmations++
				return contextID == latest, nil
			}
			trigger(t, engine)
			if err := engine.Run(context.Background(), *job); err != nil {
				t.Fatal(err)
			}
			// The cached observation disappears or another collector publishes B.
			latest = reason
			trigger(t, engine)
			if err := engine.Run(context.Background(), *job); err != nil {
				t.Fatal(err)
			}
			if latest != "capture-3" || uploads != 2 || confirmations != 1 {
				t.Fatalf("latest=%q uploads=%d confirmations=%d", latest, uploads, confirmations)
			}
			// Once A is latest again, another identical trigger can skip safely.
			trigger(t, engine)
			if err := engine.Run(context.Background(), *job); err != nil || uploads != 2 || confirmations != 2 {
				t.Fatalf("current cache uploads=%d confirmations=%d err=%v", uploads, confirmations, err)
			}
		})
	}
}

func TestChangedCheckoutDoesNotConfirmPriorObservation(t *testing.T) {
	engine, job := fixture(t)
	current := "A"
	engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
		return ingestion.Request{SubmissionID: current}, current, nil
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { return current, nil }
	engine.Confirm = func(context.Context, Target, string) (bool, error) {
		t.Fatal("changed checkout performed unnecessary confirmation")
		return false, nil
	}
	for _, value := range []string{"A", "B"} {
		current = value
		trigger(t, engine)
		if err := engine.Run(context.Background(), *job); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfirmationFailurePreservesAcknowledgement(t *testing.T) {
	engine, job := fixture(t)
	engine.Collect = func(_ context.Context, _ Event, previous string) (ingestion.Request, string, error) {
		if previous == "A" {
			return ingestion.Request{}, previous, ErrUnchanged
		}
		return ingestion.Request{SubmissionID: "A"}, "A", nil
	}
	uploads := 0
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) {
		uploads++
		return "A", nil
	}
	engine.Confirm = func(context.Context, Target, string) (bool, error) {
		return false, errors.New("confirmation offline")
	}
	trigger(t, engine)
	if err := engine.Run(context.Background(), *job); err != nil {
		t.Fatal(err)
	}
	directory, _ := engine.jobPath(*job)
	target, _ := engine.Resolve(context.Background(), Event{})
	ackPath := filepath.Join(directory, key(target.Destination, target.SourceID)+".ack.json")
	before, err := os.ReadFile(ackPath)
	if err != nil {
		t.Fatal(err)
	}
	trigger(t, engine)
	if err := engine.Run(context.Background(), *job); err == nil {
		t.Fatal("confirmation failure ignored")
	}
	after, err := os.ReadFile(ackPath)
	if err != nil || string(before) != string(after) || uploads != 1 {
		t.Fatalf("ack advanced despite failure: %v uploads=%d", err, uploads)
	}
}

func TestAcknowledgementWithoutContextRecollects(t *testing.T) {
	engine, job := fixture(t)
	target, _ := engine.Resolve(context.Background(), Event{})
	engine.Collect = func(_ context.Context, _ Event, previous string) (ingestion.Request, string, error) {
		if previous != "" {
			t.Fatalf("unverifiable legacy acknowledgement suppressed collection: %q", previous)
		}
		return ingestion.Request{SubmissionID: "fresh"}, "A", nil
	}
	engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { return "fresh", nil }
	engine.Confirm = func(context.Context, Target, string) (bool, error) {
		t.Fatal("confirmation called without context identity")
		return false, nil
	}
	trigger(t, engine)
	directory, _ := engine.jobPath(*job)
	path := filepath.Join(directory, key(target.Destination, target.SourceID)+".ack.json")
	if err := writeJSON(path, state{Target: target, Fingerprint: "A", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Run(context.Background(), *job); err != nil {
		t.Fatal(err)
	}
}

func TestStalePendingReplayCapturesAndPromotesLatestInSameSync(t *testing.T) {
	engine, job := fixture(t)
	latest := ""
	collections, confirmations := 0, 0
	engine.Collect = func(_ context.Context, _ Event, previous string) (ingestion.Request, string, error) {
		collections++
		if previous != "" {
			t.Fatalf("unexpected previous fingerprint: %q", previous)
		}
		return ingestion.Request{SubmissionID: fmt.Sprintf("capture-%d", collections)}, "state-A", nil
	}
	var submissions []string
	engine.Deliver = func(_ context.Context, _ Target, request ingestion.Request) (string, error) {
		submissions = append(submissions, request.SubmissionID)
		if len(submissions) == 1 {
			latest = "A"
			return "", errors.New("durable receipt lost")
		}
		if request.SubmissionID != "capture-1" {
			latest = "A"
		}
		return "context-A", nil
	}
	engine.Confirm = func(_ context.Context, _ Target, contextID string) (bool, error) {
		confirmations++
		if contextID != "context-A" {
			t.Fatalf("wrong replay context: %q", contextID)
		}
		return latest == "A", nil
	}
	trigger(t, engine)
	if err := engine.Run(context.Background(), *job); err == nil {
		t.Fatal("lost receipt ignored")
	}
	// An external producer advances the stream while A's retry is pending.
	latest = "B"
	trigger(t, engine)
	if err := engine.Run(context.Background(), *job); err != nil {
		t.Fatal(err)
	}
	if latest != "A" || collections != 3 || confirmations != 1 || strings.Join(submissions, ",") != "capture-1,capture-1,capture-3" {
		t.Fatalf("latest=%q collections=%d confirmations=%d submissions=%v", latest, collections, confirmations, submissions)
	}
}

func TestSessionsQueueIndependently(t *testing.T) {
	engine, job := fixture(t)
	sessions := []Event{{Path: "/checkout", Agent: "codex", RunID: "A"}, {Path: "/checkout", Agent: "codex", RunID: "B"}, {Path: "/checkout", Agent: "claude", RunID: "A"}, {Path: "/checkout", Agent: "codex", RunID: "A", SessionName: "renamed"}}
	jobs := make(map[string]bool)
	for _, event := range sessions {
		if err := engine.Schedule(event); err != nil {
			t.Fatal(err)
		}
		if jobs[*job] {
			t.Fatal("distinct session association coalesced")
		}
		jobs[*job] = true
		directory, _ := engine.jobPath(*job)
		var saved queued
		if err := readJSON(filepath.Join(directory, "request.json"), &saved); err != nil || saved.Event != event && saved.Event.InputPath != event.Path {
			t.Fatalf("saved session: %#v %v", saved, err)
		}
	}
}

func TestInitialEmptyReconciliationAndConservativePublication(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		skip       bool
		err        error
		deliveries int
	}{{"never dirty", true, nil, 0}, {"retained history", false, nil, 1}, {"query unavailable", false, errors.New("offline"), 1}} {
		t.Run(scenario.name, func(t *testing.T) {
			engine, job := fixture(t)
			engine.Collect = func(context.Context, Event, string) (ingestion.Request, string, error) {
				return ingestion.Request{SubmissionID: "empty", Scopes: []ingestion.Scope{{Snapshot: review.RepositoryDiff{}}}}, "empty", nil
			}
			queries, delivered := 0, 0
			engine.SuppressInitialEmpty = func(context.Context, Target, ingestion.Request) (bool, error) {
				queries++
				return scenario.skip, scenario.err
			}
			engine.Deliver = func(context.Context, Target, ingestion.Request) (string, error) { delivered++; return "context", nil }
			trigger(t, engine)
			if err := engine.Run(t.Context(), *job); err != nil {
				t.Fatal(err)
			}
			if queries != 1 || delivered != scenario.deliveries {
				t.Fatalf("queries=%d deliveries=%d", queries, delivered)
			}
		})
	}
}
