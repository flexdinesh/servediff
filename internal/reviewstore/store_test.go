package reviewstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/servediff/internal/review"
)

func TestStorePersistsNodeCompatibleData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	comment := review.ReviewComment{ID: "one", Path: "file.go", Scope: review.DiffAll, Fingerprint: "version", Side: "additions", Start: 1, End: 1, Code: "+ line", Body: "Review", Status: "open", CreatedAt: 1}
	if err := store.PutComment("session", comment); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMark("session", review.ReviewMark{FileID: "file", FileVersion: "version", Scope: review.DiffAll}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Comments("session")) != 1 || len(reopened.Marks("session", review.DiffAll)) != 1 {
		t.Fatal("persisted state was not restored")
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions: %o", stat.Mode().Perm())
	}
}

func TestEmptyCommentsAreNonNil(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if comments := store.Comments("session"); comments == nil || len(comments) != 0 {
		t.Fatalf("expected empty non-nil comments, got %#v", comments)
	}
	comments, err := store.ImportComments("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	if comments == nil || len(comments) != 0 {
		t.Fatalf("expected empty non-nil imported comments, got %#v", comments)
	}
}
