package reviewstore

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testUser(t *testing.T, store *Store, uid string) User {
	t.Helper()
	user, err := store.User(uid, uid)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestUserAndWorktreeIdentity(t *testing.T) {
	store := openTestStore(t)
	first := testUser(t, store, "1001")
	renamed, err := store.User("1001", "new-name")
	if err != nil || renamed.ID != first.ID || renamed.Name != "new-name" {
		t.Fatalf("renamed user = %#v, %v", renamed, err)
	}
	other := testUser(t, store, "1002")
	if other.ID == first.ID {
		t.Fatal("different OS users share an ID")
	}
	a, err := store.RegisterGit(first.ID, "/repo/one", "/repo/.git", "/repo/.git/worktrees/one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.RegisterGit(first.ID, "/repo/two", "/repo/.git", "/repo/.git/worktrees/two")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.RegisterGit(first.ID, "/repo/one", "/repo/.git", "/repo/.git/worktrees/one")
	if err != nil {
		t.Fatal(err)
	}
	if a.RepositoryID == nil || b.RepositoryID == nil || *a.RepositoryID != *b.RepositoryID || a.ContextID == b.ContextID || a.DiffIDs[review.DiffAll] == b.DiffIDs[review.DiffAll] || again.ContextID != a.ContextID {
		t.Fatalf("worktree grouping: %#v %#v %#v", a, b, again)
	}
	comment := review.ReviewComment{ID: "c1", DiffID: a.DiffIDs[review.DiffAll], Path: "file", Scope: review.DiffAll, Fingerprint: "f", Side: "additions", Start: 1, End: 1, Body: "review", Status: "open", CreatedAt: 1}
	if err := store.PutComment(a.ContextID, comment); err != nil {
		t.Fatal(err)
	}
	visible, err := store.Comments(a.ContextID)
	if err != nil || len(visible) != 1 {
		t.Fatalf("first worktree comments: %v, %v", visible, err)
	}
	isolated, err := store.Comments(b.ContextID)
	if err != nil || len(isolated) != 0 {
		t.Fatalf("second worktree comments: %v, %v", isolated, err)
	}
	if err := store.PutComment(b.ContextID, comment); err == nil {
		t.Fatal("cross-worktree comment accepted")
	}
}

func TestCaptureIdentityRetentionAndCascade(t *testing.T) {
	store := openTestStore(t)
	user := testUser(t, store, "1001")
	manifest := review.RepositoryDiff{Source: "stdin", Mode: review.DiffAll, Revision: "same-content", Files: []review.ChangedFile{}}
	a, err := store.Capture(user.ID, "patch", manifest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Capture(user.ID, "patch", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if a.ContextID == b.ContextID || a.VersionID == b.VersionID {
		t.Fatal("identical captures share identity")
	}
	listed, err := store.ListCaptures(user.ID, time.Now())
	if err != nil || len(listed) != 2 || listed[0].ExpiresAt-listed[0].CreatedAt != CaptureLifetime.Milliseconds() {
		t.Fatalf("capture retention: %#v, %v", listed, err)
	}
	raw, reopened, err := store.ReopenCapture(user.ID, a.ContextID, time.Now())
	if err != nil || raw != "patch" || reopened.VersionID != a.VersionID {
		t.Fatalf("reopen = %q, %#v, %v", raw, reopened, err)
	}
	other := testUser(t, store, "1002")
	if _, _, err := store.ReopenCapture(other.ID, a.ContextID, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user capture lookup: %v", err)
	}
	comment := review.ReviewComment{ID: "capture-comment", DiffID: a.ContextID, VersionID: a.VersionID, Path: "file", Scope: review.DiffAll, Fingerprint: "f", Side: "additions", Start: 1, End: 1, Body: "review", Status: "open", CreatedAt: 1}
	wrongVersion := comment
	wrongVersion.VersionID = b.VersionID
	if err := store.PutComment(a.ContextID, wrongVersion); err == nil {
		t.Fatal("cross-diff version accepted")
	}
	if err := store.PutComment(a.ContextID, comment); err != nil {
		t.Fatal(err)
	}
	cutoffTime := time.Now()
	cutoff := cutoffTime.UnixMilli()
	if _, err := store.db.Exec(`UPDATE diffs SET expires_at=? WHERE id=?`, cutoff, a.ContextID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReopenCapture(user.ID, a.ContextID, cutoffTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired capture still readable: %v", err)
	}
	if err := store.PruneExpired(cutoffTime); err != nil {
		t.Fatal(err)
	}
	comments, err := store.Comments(a.ContextID)
	if err != nil || len(comments) != 0 {
		t.Fatalf("expired review survived: %v, %v", comments, err)
	}
	if _, _, err := store.ReopenCapture(user.ID, b.ContextID, time.Now()); err != nil {
		t.Fatalf("unexpired capture removed: %v", err)
	}
}

func TestStateFileVersionAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() || (runtime.GOOS != "windows" && stat.Mode().Perm() != 0o600) {
		t.Fatalf("state permissions = %v, %v", stat, err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	user := testUser(t, store, "old")
	if _, err := store.db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("unsupported older schema was opened")
	}
	// Failure must release the ownership lock and preserve existing rows.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var preservedID string
	if err := db.QueryRow("SELECT id FROM users WHERE os_uid='old'").Scan(&preservedID); err != nil || preservedID != user.ID {
		t.Fatalf("existing user changed: %s, %v", preservedID, err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("newer schema was silently reset")
	}

}

func TestLegacyReviewFilesAreDiscarded(t *testing.T) {
	directory := t.TempDir()
	legacy := filepath.Join(directory, "reviews", "old.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"version":1,"sessions":{"old":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "reviews", "other.json"), []byte(`{"unrelated":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLegacyReviews(filepath.Join(directory, "state.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy review remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "reviews", "other.json")); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}
