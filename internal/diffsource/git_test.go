package diffsource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

func testGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func TestRepositoryFingerprintDetectsSameSizeEditWithRestoredMtime(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	testGit(t, root, "config", "user.email", "servediff@example.com")
	testGit(t, root, "config", "user.name", "servediff")
	path := filepath.Join(root, "value.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", "value.txt")
	testGit(t, root, "commit", "-qm", "initial")
	if err := os.WriteFile(path, []byte("one!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := OpenRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(path, []byte("two!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 || len(second.Files) != 1 || first.Files[0].Fingerprint == second.Files[0].Fingerprint {
		t.Fatalf("fingerprint did not change: %#v %#v", first.Files, second.Files)
	}
}

func TestRepositorySeparatesDiffScopes(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	testGit(t, root, "config", "user.email", "servediff@example.com")
	testGit(t, root, "config", "user.name", "servediff")
	path := filepath.Join(root, "value.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", "value.txt")
	testGit(t, root, "commit", "-qm", "initial")
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", "value.txt")
	if err := os.WriteFile(path, []byte("three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := source.Snapshot(context.Background(), review.DiffStaged)
	if err != nil {
		t.Fatal(err)
	}
	unstaged, err := source.Snapshot(context.Background(), review.DiffUnstaged)
	if err != nil {
		t.Fatal(err)
	}
	all, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	for name, snapshot := range map[string]review.RepositoryDiff{"staged": staged, "unstaged": unstaged, "all": all} {
		if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "value.txt" {
			t.Fatalf("%s scope: %#v", name, snapshot.Files)
		}
	}
	if staged.Files[0].IndexStatus != "M" || staged.Files[0].WorktreeStatus != "M" {
		t.Fatalf("unexpected status metadata: %#v", staged.Files[0])
	}
	contents, err := source.Contents(context.Background(), review.DiffAll, all.Files[0], all.Head)
	if err != nil {
		t.Fatal(err)
	}
	if contents.Before != "one\n" || contents.After != "three\n" {
		t.Fatalf("unexpected contents: %#v", contents)
	}
}
