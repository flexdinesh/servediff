package reviewstore

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

func TestCurrentVersionMigrationPreservesIdentityAndPreviews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "1001")
	binding, err := store.RegisterGitSubmission(user.ID, "register", "input", "/repo", "/repo/.git", "/repo/.git")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := review.RepositoryDiff{ID: binding.DiffIDs[review.DiffAll], LocationID: binding.LocationID, Revision: "old", Mode: review.DiffAll, Files: []review.ChangedFile{{ID: "file", Path: "value", Fingerprint: "old"}}}
	snapshot.VersionID = VersionID(snapshot.ID, snapshot.Revision)
	if err := store.PinVersion(snapshot, map[string]review.FilePatch{"file": {Patch: "retained patch"}}); err != nil {
		t.Fatal(err)
	}
	// A schema-3 store has retained review versions, but no current-version pointers.
	if _, err := store.db.Exec("DROP TABLE current_versions"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	current, err := migrated.CurrentVersion(user.ID, snapshot.ID)
	if err != nil || current.VersionID != snapshot.VersionID || current.ID != snapshot.ID {
		t.Fatalf("migration lost current version: %#v, %v", current, err)
	}
	patch, err := migrated.Preview(user.ID, snapshot.ID, "file", "old")
	if err != nil || patch.Patch != "retained patch" {
		t.Fatalf("migration lost preview: %#v, %v", patch, err)
	}
	item, err := migrated.Context(user.ID, binding.ContextID, time.Now())
	if err != nil || item.ID != binding.ContextID {
		t.Fatalf("context identity: %#v, %v", item, err)
	}
}

func TestPublicationAndPruningRetainCurrentAndReviewedVersions(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "1001")
	binding, err := store.RegisterGitSubmission(user.ID, "register", "input", "/repo", "/repo/.git", "/repo/.git")
	if err != nil {
		t.Fatal(err)
	}
	var pinned, latest review.RepositoryDiff
	for index := 0; index < 8; index++ {
		revision := fmt.Sprint(index)
		snapshot := review.RepositoryDiff{ID: binding.DiffIDs[review.DiffAll], LocationID: binding.LocationID, Revision: revision, Mode: review.DiffAll, Files: []review.ChangedFile{{ID: "file", Path: "value", Fingerprint: revision}}}
		snapshot.VersionID = VersionID(snapshot.ID, revision)
		if err := store.Publish(snapshot, map[string]review.FilePatch{"file": {Patch: revision}}, int64(index)); err != nil {
			t.Fatal(err)
		}
		// Deterministic retention order, including publications within the same millisecond.
		if _, err := store.db.Exec("UPDATE diff_versions SET created_at=? WHERE id=?", index, snapshot.VersionID); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			pinned = snapshot
		}
		latest = snapshot
	}
	comment := review.ReviewComment{ID: "comment", DiffID: pinned.ID, VersionID: pinned.VersionID, Path: "value", Scope: review.DiffAll, Fingerprint: "0", Side: "additions", Start: 1, End: 1, Body: "Retain review", Status: "open", CreatedAt: 1}
	if err := store.PutComment(binding.ContextID, comment); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneLiveVersions(latest.ID); err != nil {
		t.Fatal(err)
	}
	current, err := store.CurrentVersion(user.ID, latest.ID)
	if err != nil || current.VersionID != latest.VersionID {
		t.Fatalf("current version: %#v, %v", current, err)
	}
	if _, err := store.StoredVersion(user.ID, pinned.ID, pinned.VersionID, time.Now()); err != nil {
		t.Fatalf("review version pruned: %v", err)
	}
	if _, err := store.StoredVersion(user.ID, latest.ID, VersionID(latest.ID, "1"), time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreferenced version retained: %v", err)
	}
	preview, err := store.Preview(user.ID, pinned.ID, "file", "0")
	if err != nil || preview.Patch != "0" {
		t.Fatalf("review preview: %#v, %v", preview, err)
	}
	// A failed publication must leave the previous manifest/previews visible together.
	invalid := latest
	invalid.ID, invalid.VersionID = "missing-diff", "invalid-version"
	if err := store.Publish(invalid, nil, 9); err == nil {
		t.Fatal("published a missing diff")
	}
	current, err = store.CurrentVersion(user.ID, latest.ID)
	if err != nil || current.VersionID != latest.VersionID {
		t.Fatal("failed publication replaced current version")
	}
}
