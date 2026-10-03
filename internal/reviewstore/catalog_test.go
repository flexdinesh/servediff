package reviewstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/processlock"
	"github.com/flexdinesh/servediff/internal/review"
)

func TestSchemaTwoMigrationPreservesReviewIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "migration")
	worktree, err := store.RegisterGit(user.ID, "/repo", "/repo/.git", "/repo/.git")
	if err != nil {
		t.Fatal(err)
	}
	capture, err := store.Capture(user.ID, "patch", review.RepositoryDiff{Source: "stdin", Mode: review.DiffAll, Revision: "revision", Files: []review.ChangedFile{}})
	if err != nil {
		t.Fatal(err)
	}
	comment := review.ReviewComment{ID: "comment", DiffID: capture.ContextID, VersionID: capture.VersionID, Path: "file", Scope: review.DiffAll, Body: "keep me"}
	if err := store.PutComment(capture.ContextID, comment); err != nil {
		t.Fatal(err)
	}
	mark := review.ReviewMark{DiffID: worktree.DiffIDs[review.DiffStaged], FileID: "file", FileVersion: "fingerprint", Scope: review.DiffStaged}
	if err := store.PutMark(worktree.ContextID, mark); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP TABLE submissions", "DROP TABLE contexts", "PRAGMA user_version = 2"} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	again := testUser(t, store, "migration")
	if again.ID != user.ID {
		t.Fatal("migration replaced user identity")
	}
	binding, err := store.ContextBinding(user.ID, worktree.ContextID, time.Now())
	if err != nil || binding.DiffIDs[review.DiffStaged] != worktree.DiffIDs[review.DiffStaged] {
		t.Fatalf("worktree changed: %#v, %v", binding, err)
	}
	_, reopened, err := store.ReopenCapture(user.ID, capture.ContextID, time.Now())
	if err != nil || reopened.VersionID != capture.VersionID {
		t.Fatalf("capture changed: %#v, %v", reopened, err)
	}
	comments, err := store.Comments(capture.ContextID)
	if err != nil || len(comments) != 1 || comments[0].ID != comment.ID || comments[0].Body != comment.Body {
		t.Fatalf("comments changed: %#v, %v", comments, err)
	}
	marks, err := store.Marks(worktree.ContextID, review.DiffStaged)
	if err != nil || len(marks) != 1 || marks[0].FileVersion != mark.FileVersion {
		t.Fatalf("marks changed: %#v, %v", marks, err)
	}
	contexts, err := store.Contexts(user.ID, 100, 0, "", time.Now())
	if err != nil || len(contexts) != 2 {
		t.Fatalf("backfill: %#v, %v", contexts, err)
	}
	snapshot, err := store.StoredVersion(user.ID, capture.ContextID, capture.VersionID, time.Now())
	if err != nil || snapshot.Revision != "revision" {
		t.Fatalf("version changed: %#v, %v", snapshot, err)
	}
}

func TestSubmissionRetrySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "dedup")
	snapshot := review.RepositoryDiff{Mode: review.DiffAll, Revision: "r", Files: []review.ChangedFile{}}
	first, err := store.CaptureSubmission(user.ID, "request", "hash", "raw", "/repo", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	retry, err := store.CaptureSubmission(user.ID, "request", "hash", "raw", "/repo", snapshot)
	if err != nil || retry.ContextID != first.ContextID || retry.VersionID != first.VersionID {
		t.Fatalf("retry: %#v, %v", retry, err)
	}
	if _, err := store.CaptureSubmission(user.ID, "request", "different", "other", "/repo", snapshot); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("different payload accepted: %v", err)
	}
	if _, err := store.RegisterGitSubmission(user.ID, "request", "hash", "/repo", "/repo/.git", "/repo/.git"); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("different kind accepted: %v", err)
	}
	second, err := store.CaptureSubmission(user.ID, "independent", "hash", "raw", "/repo", snapshot)
	if err != nil || second.ContextID == first.ContextID {
		t.Fatalf("independent capture deduplicated: %#v, %v", second, err)
	}
	item, err := store.Context(user.ID, first.ContextID, time.Now())
	if err != nil || item.SubmittedFrom == nil || *item.SubmittedFrom != "/repo" {
		t.Fatalf("provenance: %#v, %v", item, err)
	}
}

func TestExpiredSubmissionCanBeReused(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "reuse")
	snapshot := review.RepositoryDiff{Mode: review.DiffAll, Revision: "r", Files: []review.ChangedFile{}}
	first, err := store.CaptureSubmission(user.ID, "request", "hash", "raw", "", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE submissions SET created_at=?", time.Now().Add(-SubmissionLifetime-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	second, err := store.CaptureSubmission(user.ID, "request", "new", "other", "", snapshot)
	if err != nil || second.ContextID == first.ContextID {
		t.Fatalf("expired request not replaced: %#v, %v", second, err)
	}
}

func TestDatabaseOwnershipLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, processlock.ErrLocked) {
		t.Fatalf("concurrent database owner accepted: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("ownership not released: %v", err)
	}
	_ = second.Close()
}

func TestConcurrentCaptureRetriesCommitOneTarget(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "concurrent")
	snapshot := review.RepositoryDiff{Mode: review.DiffAll, Revision: "r", Files: []review.ChangedFile{}}
	const clients = 8
	results := make(chan Binding, clients)
	failures := make(chan error, clients)
	for range clients {
		go func() {
			binding, err := store.CaptureSubmission(user.ID, "request", "hash", "raw", "", snapshot)
			if err != nil {
				failures <- err
				return
			}
			results <- binding
		}()
	}
	var id string
	for range clients {
		select {
		case err := <-failures:
			t.Fatal(err)
		case binding := <-results:
			if id == "" {
				id = binding.ContextID
			}
			if binding.ContextID != id {
				t.Fatal("concurrent retries created different targets")
			}
		}
	}
	_, captures, err := store.ContextCounts(user.ID, time.Now())
	if err != nil || captures != 1 {
		t.Fatalf("retry count %d, %v", captures, err)
	}
}

func TestExpiryRemovesCatalogAndRetryRecords(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "expiry")
	snapshot := review.RepositoryDiff{Mode: review.DiffAll, Revision: "r", Files: []review.ChangedFile{}}
	captured, err := store.CaptureSubmission(user.ID, "request", "hash", "raw", "", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := store.db.Exec("UPDATE diffs SET expires_at=? WHERE id=?", now.UnixMilli(), captured.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Context(user.ID, captured.ContextID, now); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired context accessible: %v", err)
	}
	items, err := store.Contexts(user.ID, 100, 0, "", now)
	if err != nil || len(items) != 0 {
		t.Fatalf("expired capture listed: %#v, %v", items, err)
	}
	if err := store.PruneExpired(now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Context(user.ID, captured.ContextID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned context retained: %v", err)
	}
	var retries int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM submissions").Scan(&retries); err != nil || retries != 0 {
		t.Fatalf("expired retry identity retained: %d, %v", retries, err)
	}
}

func TestUnsupportedLegacyFilePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw := []byte(`{"version":1,"sessions":{"old":{}}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("unsupported legacy state opened")
	}
	preserved, err := os.ReadFile(path)
	if err != nil || string(preserved) != string(raw) {
		t.Fatalf("legacy state changed: %q, %v", preserved, err)
	}
}
