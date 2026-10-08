package reviewstore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/ingestionqueue"
)

func queuedRequest(id string) ingestion.Request {
	request := observationRequest("source", id)
	for i := range request.Scopes {
		request.Scopes[i].Snapshot.Source = "local"
		request.Scopes[i].Snapshot.Root = request.Metadata.Root
		request.Scopes[i].Snapshot.Branch = request.Metadata.Branch
	}
	return request
}

func TestQueueRestartReplayAndLeaseFencing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner := testUser(t, store, "queue")
	request := queuedRequest("first")
	job, err := store.Queue().Accept(t.Context(), owner.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.Queue().Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.Ingest(owner.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Context(owner.ID, binding.ContextID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after commit, before queue acknowledgement.
	if _, err := store.db.Exec("UPDATE ingestion_jobs SET lease_until=0"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recovered, err := store.Queue().Claim(t.Context(), time.Minute)
	if err != nil || recovered.ID != job.ID || recovered.Attempt != 2 {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	replay, err := store.Ingest(owner.ID, recovered.Request)
	if err != nil || replay.ContextID != binding.ContextID {
		t.Fatalf("commit replay: %+v %v", replay, err)
	}
	after, err := store.Context(owner.ID, binding.ContextID, time.Now())
	if err != nil || *after.ExpiresAt != *before.ExpiresAt || after.LastSubmittedAt != before.LastSubmittedAt {
		t.Fatalf("retry extended retention: %v", err)
	}
	if err := store.Queue().Complete(t.Context(), original, binding.ContextID); !errors.Is(err, ingestionqueue.ErrLeaseLost) {
		t.Fatalf("old lease: %v", err)
	}
	if err := store.Queue().Complete(t.Context(), recovered, binding.ContextID); err != nil {
		t.Fatal(err)
	}
	same, err := store.Queue().Accept(t.Context(), owner.ID, request)
	if err != nil || same.ID != job.ID || same.State != "succeeded" {
		t.Fatalf("duplicate acceptance: %+v %v", same, err)
	}
	request.Metadata.Hostname = "different"
	if _, err := store.Queue().Accept(t.Context(), owner.ID, request); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	other := testUser(t, store, "other")
	if _, err := store.Queue().Get(t.Context(), other.ID, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ownership: %v", err)
	}
}

func TestQueueAcceptanceOrdersEqualCollectionTimesAndPreventsResurrection(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "queue-order")
	first, second := queuedRequest("first"), queuedRequest("second")
	for _, request := range []ingestion.Request{first, second} {
		if _, err := store.Queue().Accept(t.Context(), owner.ID, request); err != nil {
			t.Fatal(err)
		}
	}
	newer, err := store.Ingest(owner.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	older, err := store.Ingest(owner.ID, first)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Context(owner.ID, older.ContextID, time.Now())
	if err != nil || !old.Stale {
		t.Fatalf("worker order changed freshness: %+v %v", old, err)
	}
	latest, err := store.Context(owner.ID, newer.ContextID, time.Now())
	if err != nil || latest.Stale {
		t.Fatalf("accepted last must be latest: %+v %v", latest, err)
	}
	if err := store.DeleteContext(owner.ID, newer.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ingest(owner.ID, second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted queued submission recreated: %v", err)
	}
}

func TestQueueBackoffAndTerminalFailure(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "queue-failures")
	job, err := store.Queue().Accept(t.Context(), owner.ID, queuedRequest("job"))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := store.Queue().Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Queue().Retry(t.Context(), delivery, time.Now().Add(time.Hour), "temporary"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue().Claim(t.Context(), time.Minute); !errors.Is(err, ingestionqueue.ErrEmpty) {
		t.Fatalf("backoff ignored: %v", err)
	}
	if _, err := store.db.Exec("UPDATE ingestion_jobs SET available_at=0"); err != nil {
		t.Fatal(err)
	}
	delivery, err = store.Queue().Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Queue().Fail(t.Context(), delivery, "invalid"); err != nil {
		t.Fatal(err)
	}
	status, err := store.Queue().Get(t.Context(), owner.ID, job.ID)
	if err != nil || status.State != "failed" || status.Detail != "invalid" {
		t.Fatalf("failure status: %+v %v", status, err)
	}
	if _, err := store.Queue().Claim(t.Context(), time.Minute); !errors.Is(err, ingestionqueue.ErrEmpty) {
		t.Fatalf("failed job retried: %v", err)
	}
}
