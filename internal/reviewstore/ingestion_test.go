package reviewstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

func observationRequest(source, submission string) ingestion.Request {
	return ingestion.Request{
		ProtocolVersion: ingestion.ProtocolVersion, SubmissionID: submission,
		Metadata: ingestion.Metadata{SourceID: source, Hostname: "agent-host", RepositoryKey: "repo-key", RepositoryName: "my-repo", CheckoutKey: "checkout", Root: "/deleted/checkout", WorktreeName: "sandbox", Branch: "feature", CollectedAt: 123, Trigger: "agent-hook"},
		Scopes: []ingestion.Scope{
			{Snapshot: review.RepositoryDiff{Mode: review.DiffAll, Revision: "all-revision", Files: []review.ChangedFile{{ID: "file", Path: "file.txt", Fingerprint: "all-fingerprint"}}}, Patches: map[string]review.FilePatch{"file": {Patch: "all-patch", Contents: &review.FileContents{Before: "old", After: "new"}}}},
			{Snapshot: review.RepositoryDiff{Mode: review.DiffStaged, Revision: "staged-revision", Files: []review.ChangedFile{{ID: "file", Path: "file.txt", Fingerprint: "staged-fingerprint"}}}, Patches: map[string]review.FilePatch{"file": {Patch: "staged-patch"}}},
			{Snapshot: review.RepositoryDiff{Mode: review.DiffUnstaged, Revision: "unstaged-revision", Files: []review.ChangedFile{}}, Patches: map[string]review.FilePatch{}},
		},
	}
}

func TestObservationRetryAndIndependentSources(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "observations")
	request := observationRequest("container-a", "submission")
	first, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.Ingest(user.ID, request)
	if err != nil || retry.ContextID != first.ContextID || len(retry.DiffIDs) != 3 || retry.VersionID != "" {
		t.Fatalf("retry: %#v, %v", retry, err)
	}
	request.Metadata.Branch = "other"
	if _, err := store.Ingest(user.ID, request); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("conflict: %v", err)
	}
	request = observationRequest("container-b", "submission")
	second, err := store.Ingest(user.ID, request)
	if err != nil || second.ContextID == first.ContextID || first.RepositoryID == nil || second.RepositoryID == nil || *first.RepositoryID != *second.RepositoryID {
		t.Fatalf("source provenance: %#v, %v", second, err)
	}
	request.SubmissionID = "another"
	third, err := store.Ingest(user.ID, request)
	if err != nil || third.ContextID == second.ContextID {
		t.Fatalf("identical independent submission: %#v, %v", third, err)
	}
	other := testUser(t, store, "other-user")
	if _, err := store.Observation(other.ID, first.ContextID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user metadata: %v", err)
	}
	if _, err := store.ObservationSnapshot(other.ID, first.ContextID, review.DiffAll); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user snapshot: %v", err)
	}
	if _, err := store.ObservationPatch(other.ID, first.ContextID, review.DiffAll, "file"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user patch: %v", err)
	}
}

func TestObservationRollbackOnIncompleteScope(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "rollback")
	request := observationRequest("source", "submission")
	request.Scopes[1].Patches = nil
	if _, err := store.Ingest(user.ID, request); err == nil {
		t.Fatal("incomplete scope accepted")
	}
	for _, table := range []string{"observations", "observation_repositories", "observation_scopes", "contexts", "diffs", "diff_versions", "diff_files", "reviews"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial commit in %s: %d, %v", table, count, err)
		}
	}
}

func TestObservationReopensWithoutCheckoutAndDoesNotExpire(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "durable")
	request := observationRequest("source", "request")
	request.Metadata.Root = filepath.Join(dir, "checkout")
	if err := os.Mkdir(request.Metadata.Root, 0700); err != nil {
		t.Fatal(err)
	}
	binding, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(request.Metadata.Root); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PruneExpired(time.Now().Add(100 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	retry, err := store.Ingest(user.ID, request)
	if err != nil || retry.ContextID != binding.ContextID {
		t.Fatalf("durable replay: %#v, %v", retry, err)
	}
	item, err := store.Context(user.ID, binding.ContextID, time.Now().Add(100*24*time.Hour))
	if err != nil || item.Kind != "observation" || item.Metadata == nil || item.Metadata.SourceID != "source" || item.Root == nil || *item.Root != request.Metadata.Root || item.ExpiresAt != nil {
		t.Fatalf("metadata: %#v, %v", item, err)
	}
	snapshot, err := store.ObservationSnapshot(user.ID, binding.ContextID, review.DiffStaged)
	if err != nil || snapshot.ID != binding.DiffIDs[review.DiffStaged] || snapshot.Revision != "staged-revision" {
		t.Fatalf("snapshot: %#v, %v", snapshot, err)
	}
	patch, err := store.ObservationPatch(user.ID, binding.ContextID, review.DiffAll, "file")
	if err != nil || patch.Contents == nil || patch.Contents.After != "new" {
		t.Fatalf("contents: %#v, %v", patch, err)
	}
	rebound, err := store.ContextBinding(user.ID, binding.ContextID, time.Now())
	if err != nil || len(rebound.DiffIDs) != 3 || rebound.DiffIDs[review.DiffStaged] != snapshot.ID {
		t.Fatalf("binding: %#v, %v", rebound, err)
	}
}

func TestObservationReviewScopesAndMetadataSearch(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "search")
	binding, err := store.Ingest(user.ID, observationRequest("source", "request"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ObservationSnapshot(user.ID, binding.ContextID, review.DiffStaged)
	if err != nil {
		t.Fatal(err)
	}
	comment := review.ReviewComment{ID: "comment", DiffID: snapshot.ID, VersionID: snapshot.VersionID, Scope: review.DiffStaged, Path: "file.txt", Body: "review", CreatedAt: 1}
	if err := store.PutComment(binding.ContextID, comment); err != nil {
		t.Fatal(err)
	}
	comments, err := store.Comments(binding.ContextID)
	if err != nil || len(comments) != 1 || comments[0].VersionID != snapshot.VersionID {
		t.Fatalf("comments: %#v, %v", comments, err)
	}
	mark := review.ReviewMark{DiffID: snapshot.ID, VersionID: snapshot.VersionID, Scope: review.DiffStaged, FileID: "file", FileVersion: "staged-fingerprint"}
	if err := store.PutMark(binding.ContextID, mark); err != nil {
		t.Fatal(err)
	}
	marks, err := store.Marks(binding.ContextID, review.DiffStaged)
	if err != nil || len(marks) != 1 {
		t.Fatalf("marks: %#v, %v", marks, err)
	}
	filters := []ingestion.Filter{{Query: "AGENT-HOST"}, {Repository: "my-repo", Branch: "feature", Worktree: "sandbox", Hostname: "agent-host", SourceID: "source"}}
	for _, filter := range filters {
		items, err := store.ObservationContexts(user.ID, 100, 0, "", filter)
		if err != nil || len(items) != 1 || items[0].Metadata == nil {
			t.Fatalf("filter %#v: %#v, %v", filter, items, err)
		}
	}
	items, err := store.ObservationContexts(user.ID, 100, 0, "", ingestion.Filter{Branch: "absent"})
	if err != nil || len(items) != 0 {
		t.Fatalf("nonmatching filter: %#v, %v", items, err)
	}
	if err := store.ClearMarks(binding.ContextID, review.DiffStaged); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteComment(binding.ContextID, "comment"); err != nil || !deleted {
		t.Fatalf("delete comment: %v,%v", deleted, err)
	}
}

func TestSchemaThreeMigrationPreservesCaptures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "schema3")
	old, err := store.Capture(user.ID, "old patch", review.RepositoryDiff{Mode: review.DiffAll, Revision: "old", Files: []review.ChangedFile{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DROP TABLE observation_scopes", "DROP TABLE observations", "DROP TABLE observation_repositories", "PRAGMA user_version = 3"} {
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
	raw, reopened, err := store.ReopenCapture(user.ID, old.ContextID, time.Now())
	if err != nil || raw != "old patch" || reopened.VersionID != old.VersionID {
		t.Fatalf("legacy capture: %#v, %v", reopened, err)
	}
	if _, err := store.Ingest(user.ID, observationRequest("source", "request")); err != nil {
		t.Fatal(err)
	}
}
