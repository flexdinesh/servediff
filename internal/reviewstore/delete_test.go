package reviewstore

import (
	"testing"
	"time"
)

func TestDeleteLatestObservationKeepsOlderSnapshotsStale(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "owner")
	olderRequest := observationRequest("source", "older")
	olderRequest.Metadata.CollectedAt = 100
	older, err := store.Ingest(owner.ID, olderRequest)
	if err != nil {
		t.Fatal(err)
	}
	latestRequest := observationRequest("source", "latest")
	latestRequest.Metadata.CollectedAt = 200
	latest, err := store.Ingest(owner.ID, latestRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContext(owner.ID, latest.ContextID); err != nil {
		t.Fatal(err)
	}
	item, err := store.Context(owner.ID, older.ContextID, time.Now())
	if err != nil || !item.Stale {
		t.Fatalf("older snapshot promoted after deletion: %#v, %v", item, err)
	}
	lateRequest := observationRequest("source", "late")
	lateRequest.Metadata.CollectedAt = 150
	late, err := store.Ingest(owner.ID, lateRequest)
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Context(owner.ID, late.ContextID, time.Now())
	if err != nil || !item.Stale {
		t.Fatalf("late snapshot promoted after deletion: %#v, %v", item, err)
	}
}

func TestDeleteContextRollsBackWhenAnchorDeletionFails(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "owner")
	binding, err := store.Ingest(owner.ID, observationRequest("source", "request"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_anchor BEFORE DELETE ON diffs WHEN OLD.mode='all' BEGIN SELECT RAISE(ABORT,'anchor deletion failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContext(owner.ID, binding.ContextID); err == nil {
		t.Fatal("anchor failure did not abort deletion")
	}
	for mode := range binding.DiffIDs {
		if _, err := store.ObservationSnapshot(owner.ID, binding.ContextID, mode); err != nil {
			t.Fatalf("scope %s removed by failed deletion: %v", mode, err)
		}
	}
}

func TestDirectReplayCannotResurrectDeletedObservation(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "owner")
	input := observationRequest("source", "request")
	binding, err := store.Ingest(owner.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContext(owner.ID, binding.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ingest(owner.ID, input); err == nil {
		t.Fatal("exact replay resurrected deleted observation")
	}
	input.SubmissionID = "fresh"
	fresh, err := store.Ingest(owner.ID, input)
	if err != nil || fresh.ContextID == binding.ContextID {
		t.Fatalf("fresh submission: %+v %v", fresh, err)
	}
}
