package reviewstore

import (
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
)

func branchIdentityRequest(submission, label, id, content string, collected int64) ingestion.Request {
	request := observationRequest("source", submission)
	request.Metadata.Branch, request.Metadata.BranchID = label, id
	request.Metadata.CollectedAt = collected
	request.ContentHash = strings.Repeat(content, 64)
	return request
}

func TestBranchIdentityAdoptsLegacyReviewWithoutChangingReplay(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "branch-adoption")
	legacy := branchIdentityRequest("legacy", "feature", "", "a", 100)
	first, err := store.Ingest(user.ID, legacy)
	if err != nil {
		t.Fatal(err)
	}
	adopted := branchIdentityRequest("stable", "feature", "branch-id", "a", 200)
	second, err := store.Ingest(user.ID, adopted)
	if err != nil || second.ContextID != first.ContextID {
		t.Fatalf("adoption lost review history: %#v, %v", second, err)
	}
	renamed := branchIdentityRequest("renamed", "new-name", "branch-id", "a", 300)
	third, err := store.Ingest(user.ID, renamed)
	if err != nil || third.ContextID != first.ContextID {
		t.Fatalf("rename lost review history: %#v, %v", third, err)
	}
	metadata, err := store.Observation(user.ID, first.ContextID)
	if err != nil || metadata.Branch != "feature" || metadata.BranchID != "" {
		t.Fatalf("immutable observation changed: %#v, %v", metadata, err)
	}
	replayed, err := store.Ingest(user.ID, legacy)
	if err != nil || replayed.ContextID != first.ContextID {
		t.Fatalf("adoption broke immutable replay: %#v, %v", replayed, err)
	}
	newContent := branchIdentityRequest("new-content", "new-name", "branch-id", "b", 400)
	latest, err := store.Ingest(user.ID, newContent)
	if err != nil || latest.ContextID == first.ContextID {
		t.Fatalf("new content did not advance stream: %#v, %v", latest, err)
	}
	oldContext, err := store.Context(user.ID, first.ContextID, time.Now())
	if err != nil || !oldContext.Stale {
		t.Fatalf("legacy alias failed freshness: %#v, %v", oldContext, err)
	}
	current, err := store.Context(user.ID, latest.ContextID, time.Now())
	if err != nil || current.Stale {
		t.Fatalf("renamed current context stale: %#v, %v", current, err)
	}
	// Restoring pre-adoption content after a rename must recover its review too.
	restored, err := store.Ingest(user.ID, branchIdentityRequest("restored", "new-name", "branch-id", "a", 500))
	if err != nil || restored.ContextID != first.ContextID {
		t.Fatalf("restored legacy content lost review: %#v, %v", restored, err)
	}
}

func TestReusedBranchNameKeepsIndependentReviewAndFreshness(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "branch-reuse")
	first, err := store.Ingest(user.ID, branchIdentityRequest("original", "feature", "original-id", "a", 100))
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := store.Ingest(user.ID, branchIdentityRequest("renamed", "renamed", "original-id", "b", 200))
	if err != nil {
		t.Fatal(err)
	}
	reused, err := store.Ingest(user.ID, branchIdentityRequest("reused", "feature", "fresh-id", "a", 300))
	if err != nil || reused.ContextID == first.ContextID {
		t.Fatalf("reused branch inherited old review: %#v, %v", reused, err)
	}
	for _, check := range []struct {
		id    string
		stale bool
	}{{first.ContextID, true}, {renamed.ContextID, false}, {reused.ContextID, false}} {
		context, err := store.Context(user.ID, check.id, time.Now())
		if err != nil || context.Stale != check.stale {
			t.Fatalf("independent branch freshness %s: %#v, %v", check.id, context, err)
		}
	}
}

func TestLateBranchAdoptionPreservesNewerLegacyHead(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "late-branch-adoption")
	newer, err := store.Ingest(user.ID, branchIdentityRequest("newer-legacy", "feature", "", "a", 300))
	if err != nil {
		t.Fatal(err)
	}
	older, err := store.Ingest(user.ID, branchIdentityRequest("older-adoption", "feature", "branch-id", "b", 200))
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		id    string
		stale bool
	}{{newer.ContextID, false}, {older.ContextID, true}} {
		context, err := store.Context(user.ID, check.id, time.Now())
		if err != nil || context.Stale != check.stale {
			t.Fatalf("late adoption regressed stream: %#v, %v", context, err)
		}
	}
}
