package reviewstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	for _, table := range []string{"observations", "observation_repositories", "observation_scopes", "observation_submissions", "observation_identities", "contexts", "diffs", "diff_versions", "diff_files", "reviews"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial commit in %s: %d, %v", table, count, err)
		}
	}
}

func TestObservationReopensWithoutCheckoutWithinRetention(t *testing.T) {
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
	if err := store.PruneExpired(time.Now().Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	retry, err := store.Ingest(user.ID, request)
	if err != nil || retry.ContextID != binding.ContextID {
		t.Fatalf("durable replay: %#v, %v", retry, err)
	}
	item, err := store.Context(user.ID, binding.ContextID, time.Now().Add(24*time.Hour))
	if err != nil || item.Kind != "observation" || item.Metadata == nil || item.Metadata.SourceID != "source" || item.Root == nil || *item.Root != request.Metadata.Root || item.ExpiresAt == nil {
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

func TestObservationDedupePreservesReviewAndRefreshesOnlyFreshSubmissions(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "dedupe")
	request := observationRequest("source", "first")
	request.ContentHash = strings.Repeat("a", 64)
	first, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ObservationSnapshot(user.ID, first.ContextID, review.DiffStaged)
	if err != nil {
		t.Fatal(err)
	}
	comment := review.ReviewComment{ID: "retained", DiffID: snapshot.ID, VersionID: snapshot.VersionID, Scope: review.DiffStaged, Path: "file.txt", Body: "check", CreatedAt: 1}
	if err := store.PutComment(first.ContextID, comment); err != nil {
		t.Fatal(err)
	}
	mark := review.ReviewMark{DiffID: snapshot.ID, VersionID: snapshot.VersionID, Scope: review.DiffStaged, FileID: "file", FileVersion: "staged-fingerprint"}
	if err := store.PutMark(first.ContextID, mark); err != nil {
		t.Fatal(err)
	}
	oldExpiry := time.Now().Add(time.Hour).UnixMilli()
	if _, err := store.db.Exec(`UPDATE diffs SET expires_at=? WHERE id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)`, oldExpiry, first.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE contexts SET last_submitted_at=1 WHERE id=?`, first.ContextID); err != nil {
		t.Fatal(err)
	}
	retry, err := store.Ingest(user.ID, request)
	if err != nil || retry.ContextID != first.ContextID {
		t.Fatalf("retry: %#v %v", retry, err)
	}
	item, err := store.Context(user.ID, first.ContextID, time.Now())
	if err != nil || item.ExpiresAt == nil || *item.ExpiresAt != oldExpiry || item.LastSubmittedAt != 1 {
		t.Fatalf("retry refreshed TTL: %#v %v", item, err)
	}

	fresh := request
	fresh.SubmissionID = "second"
	fresh.Metadata.CollectedAt++
	fresh.Scopes = append([]ingestion.Scope(nil), request.Scopes...)
	fresh.Scopes[0].Snapshot.Revision = "stat-only-change"
	reused, err := store.Ingest(user.ID, fresh)
	if err != nil || reused.ContextID != first.ContextID {
		t.Fatalf("dedupe: %#v %v", reused, err)
	}
	item, err = store.Context(user.ID, first.ContextID, time.Now())
	if err != nil || item.ExpiresAt == nil || *item.ExpiresAt-item.LastSubmittedAt != CaptureLifetime.Milliseconds() || *item.ExpiresAt <= oldExpiry {
		t.Fatalf("fresh TTL: %#v %v", item, err)
	}
	var scopes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM observation_scopes s JOIN diffs d ON d.id=s.diff_id WHERE s.context_id=? AND d.expires_at=?`, first.ContextID, *item.ExpiresAt).Scan(&scopes); err != nil || scopes != 3 {
		t.Fatalf("scope retention: %d %v", scopes, err)
	}
	comments, err := store.Comments(first.ContextID)
	if err != nil || len(comments) != 1 || comments[0].VersionID != snapshot.VersionID {
		t.Fatalf("review lost: %#v %v", comments, err)
	}
	marks, err := store.Marks(first.ContextID, review.DiffStaged)
	if err != nil || len(marks) != 1 {
		t.Fatalf("mark lost: %#v %v", marks, err)
	}
	original, err := store.ObservationSnapshot(user.ID, first.ContextID, review.DiffStaged)
	if err != nil || original.VersionID != snapshot.VersionID || original.Revision != snapshot.Revision {
		t.Fatalf("snapshot replaced: %#v %v", original, err)
	}
	for _, replay := range []ingestion.Request{request, fresh} {
		binding, err := store.Ingest(user.ID, replay)
		if err != nil || binding.ContextID != first.ContextID {
			t.Fatalf("alias replay: %#v %v", binding, err)
		}
	}
	fresh.Metadata.Branch = "conflict"
	if _, err := store.Ingest(user.ID, fresh); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("alias conflict: %v", err)
	}
}

func TestObservationIdentitySeparatesProvenanceHeadAndContent(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "identity")
	base := observationRequest("source", "base")
	base.ContentHash = strings.Repeat("a", 64)
	first, err := store.Ingest(user.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ingestion.Request){
		"content":    func(r *ingestion.Request) { r.ContentHash = strings.Repeat("b", 64) },
		"head":       func(r *ingestion.Request) { head := "new-head"; r.Metadata.Head = &head },
		"branch":     func(r *ingestion.Request) { r.Metadata.Branch = "other" },
		"checkout":   func(r *ingestion.Request) { r.Metadata.CheckoutKey = "other" },
		"repository": func(r *ingestion.Request) { r.Metadata.RepositoryKey = "other" },
		"source":     func(r *ingestion.Request) { r.Metadata.SourceID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			request.SubmissionID = name
			change(&request)
			binding, err := store.Ingest(user.ID, request)
			if err != nil || binding.ContextID == first.ContextID {
				t.Fatalf("identity merged: %#v %v", binding, err)
			}
		})
	}
	other := testUser(t, store, "other-owner")
	binding, err := store.Ingest(other.ID, base)
	if err != nil || binding.ContextID == first.ContextID {
		t.Fatalf("owner merged: %#v %v", binding, err)
	}
}

func TestEmptyObservationDedupeWithoutProducerHash(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "empty")
	request := observationRequest("source", "first")
	for i := range request.Scopes {
		request.Scopes[i].Snapshot.Files = []review.ChangedFile{}
		request.Scopes[i].Patches = map[string]review.FilePatch{}
	}
	first, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.SubmissionID = "second"
	request.Metadata.CollectedAt++
	request.Scopes[0], request.Scopes[2] = request.Scopes[2], request.Scopes[0]
	second, err := store.Ingest(user.ID, request)
	if err != nil || second.ContextID != first.ContextID {
		t.Fatalf("empty dedupe: %#v %v", second, err)
	}
}

func TestObservationDedupeConcurrentFreshSubmissions(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "concurrent")
	var group sync.WaitGroup
	ids := make(chan string, 8)
	for i := range 8 {
		group.Go(func() {
			request := observationRequest("source", string(rune('a'+i)))
			request.ContentHash = strings.Repeat("a", 64)
			binding, err := store.Ingest(user.ID, request)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- binding.ContextID
		})
	}
	group.Wait()
	close(ids)
	var id string
	for next := range ids {
		if id != "" && next != id {
			t.Fatalf("concurrent duplicates: %s %s", id, next)
		}
		id = next
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM observation_submissions`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("submission aliases: %d %v", count, err)
	}
}

func TestObservationExpiryHidesReadsAndPrunesEveryScope(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "expiry")
	request := observationRequest("source", "first")
	request.ContentHash = strings.Repeat("a", 64)
	first, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []review.DiffMode{review.DiffAll, review.DiffStaged} {
		snapshot, err := store.ObservationSnapshot(user.ID, first.ContextID, mode)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutComment(first.ContextID, review.ReviewComment{ID: string(mode), Scope: mode, VersionID: snapshot.VersionID, Path: "file.txt", Body: "check", CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if err := store.PutMark(first.ContextID, review.ReviewMark{Scope: mode, VersionID: snapshot.VersionID, FileID: "file"}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().Add(-time.Second)
	if _, err := store.db.Exec(`UPDATE diffs SET expires_at=? WHERE id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)`, cutoff.UnixMilli(), first.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Context(user.ID, first.ContextID, time.Now()); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired context: %v", err)
	}
	if _, err := store.Observation(user.ID, first.ContextID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired metadata: %v", err)
	}
	if _, err := store.ObservationSnapshot(user.ID, first.ContextID, review.DiffAll); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired snapshot: %v", err)
	}
	if _, err := store.ObservationPatch(user.ID, first.ContextID, review.DiffStaged, "file"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired patch: %v", err)
	}
	items, err := store.ObservationContexts(user.ID, 100, 0, "", ingestion.Filter{Repository: "my-repo"})
	if err != nil || len(items) != 0 {
		t.Fatalf("expired filtered catalog: %#v %v", items, err)
	}
	items, err = store.Contexts(user.ID, 100, 0, "", time.Now())
	if err != nil || len(items) != 0 {
		t.Fatalf("expired catalog: %#v %v", items, err)
	}
	comments, err := store.Comments(first.ContextID)
	if err != nil || len(comments) != 0 {
		t.Fatalf("expired comments visible: %#v %v", comments, err)
	}
	marks, err := store.Marks(first.ContextID, review.DiffStaged)
	if err != nil || len(marks) != 0 {
		t.Fatalf("expired marks visible: %#v %v", marks, err)
	}
	if _, err := store.Ingest(user.ID, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired replay resurrected: %v", err)
	}
	fresh := request
	fresh.SubmissionID = "fresh"
	second, err := store.Ingest(user.ID, fresh)
	if err != nil || second.ContextID == first.ContextID {
		t.Fatalf("fresh review after expiry: %#v %v", second, err)
	}
	if err := store.PruneExpired(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"diffs", "diff_versions", "reviews", "comments", "marks", "observation_scopes", "observation_submissions", "observation_identities", "observations", "contexts"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		expected := 1
		if table == "diffs" || table == "diff_versions" || table == "reviews" || table == "observation_scopes" {
			expected = 3
		}
		if table == "comments" || table == "marks" {
			expected = 0
		}
		if count != expected {
			t.Fatalf("orphaned %s: %d want %d", table, count, expected)
		}
	}
}

func TestSchemaFourMigrationPreservesObservationHistoryAndRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "migration")
	request := observationRequest("source", "original")
	first, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.SubmissionID = "duplicate"
	second, err := store.Ingest(user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContextID == second.ContextID {
		t.Fatal("legacy changed observations unexpectedly deduped")
	}
	if err := store.PutComment(first.ContextID, review.ReviewComment{ID: "first-comment", Scope: review.DiffAll, Path: "file.txt", Body: "retain", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	lastSubmitted := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := store.db.Exec(`UPDATE contexts SET last_submitted_at=?`, lastSubmitted); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"DROP TABLE observation_identities", "DROP TABLE observation_submissions", "UPDATE diffs SET expires_at=NULL", "PRAGMA user_version=4"} {
		if _, err := store.db.Exec(query); err != nil {
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
	for _, binding := range []Binding{first, second} {
		item, err := store.Context(user.ID, binding.ContextID, time.Now())
		if err != nil || item.ExpiresAt == nil || *item.ExpiresAt != lastSubmitted+CaptureLifetime.Milliseconds() {
			t.Fatalf("migration TTL: %#v %v", item, err)
		}
		var scopes int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM observation_scopes s JOIN diffs d ON d.id=s.diff_id WHERE s.context_id=? AND d.expires_at=?`, binding.ContextID, *item.ExpiresAt).Scan(&scopes); err != nil || scopes != 3 {
			t.Fatalf("migration scopes: %d %v", scopes, err)
		}
	}
	comments, err := store.Comments(first.ContextID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("migration review: %#v %v", comments, err)
	}
	request.SubmissionID = "original"
	retry, err := store.Ingest(user.ID, request)
	if err != nil || retry.ContextID != first.ContextID {
		t.Fatalf("migration replay: %#v %v", retry, err)
	}
}
