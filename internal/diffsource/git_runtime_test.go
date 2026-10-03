package diffsource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

func TestGitProcessLimitBoundsWorkAndCancelsQueue(t *testing.T) {
	limit := newGitProcessLimit(4, 16)
	for range 4 {
		release, err := limit.acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	queued, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan error, 16)
	for range 16 {
		go func() {
			release, err := limit.acquire(queued)
			if release != nil {
				release()
			}
			results <- err
		}()
	}
	deadline := time.After(5 * time.Second)
	for len(limit.admitted) != 20 {
		select {
		case <-deadline:
			t.Fatal("queued callers did not reach admission limit")
		case <-time.After(time.Millisecond):
		}
	}
	_, err := limit.acquire(t.Context())
	var overloaded *RequestError
	if !errors.As(err, &overloaded) || overloaded.Status != 503 {
		t.Fatalf("queue overflow: %v", err)
	}
	cancel()
	for range 16 {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("queued cancellation: %v", err)
			}
		case <-deadline:
			t.Fatal("queued callers did not exit after cancellation")
		}
	}
	if len(limit.admitted) != 4 {
		t.Fatal("cancelled callers retained admission")
	}
}

func newRuntimeTestRepository(t *testing.T, filename string) string {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	testGit(t, root, "config", "user.email", "servediff@example.com")
	testGit(t, root, "config", "user.name", "servediff")
	if err := os.WriteFile(filepath.Join(root, filename), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", filename)
	testGit(t, root, "commit", "-qm", "initial")
	return root
}

func TestRepositoryIgnoresInheritedRoutingEnvironment(t *testing.T) {
	first := newRuntimeTestRepository(t, "first.txt")
	second := newRuntimeTestRepository(t, "second.txt")
	if err := os.WriteFile(filepath.Join(second, "second.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GIT_DIR":                          filepath.Join(first, ".git"),
		"GIT_WORK_TREE":                    first,
		"GIT_COMMON_DIR":                   filepath.Join(first, ".git"),
		"GIT_INDEX_FILE":                   filepath.Join(first, ".git", "index"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(first, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(first, "nonexistent"),
		"GIT_CEILING_DIRECTORIES":          second,
		"GIT_CONFIG_COUNT":                 "1",
		"GIT_CONFIG_KEY_0":                 "core.worktree",
		"GIT_CONFIG_VALUE_0":               first,
	} {
		t.Setenv(key, value)
	}
	source, err := OpenRepository(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}
	if source.Root() != canonical {
		t.Fatalf("inherited environment changed root: %q", source.Root())
	}
	common, worktree, err := RepositoryIdentity(t.Context(), source.Root())
	if err != nil {
		t.Fatal(err)
	}
	if common != filepath.Join(canonical, ".git") || worktree != common {
		t.Fatalf("inherited environment changed identity: %q %q", common, worktree)
	}
	snapshot, err := source.Snapshot(t.Context(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "second.txt" {
		t.Fatalf("inherited environment changed snapshot: %#v", snapshot.Files)
	}
}

func TestRepositoryAliasesCanonicalizeAndClonesRemainSeparate(t *testing.T) {
	root := newRuntimeTestRepository(t, "value.txt")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := OpenRepository(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenRepository(t.Context(), filepath.Join(alias, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Root() != second.Root() {
		t.Fatalf("alias/subdirectory created different roots: %q %q", first.Root(), second.Root())
	}
	common, worktree, err := RepositoryIdentity(t.Context(), alias)
	if err != nil {
		t.Fatal(err)
	}
	expectedCommon, expectedWorktree, err := RepositoryIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if common != expectedCommon || worktree != expectedWorktree {
		t.Fatalf("alias created different identity: %q %q", common, worktree)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, root, "clone", "-q", root, clone)
	cloneCommon, cloneWorktree, err := RepositoryIdentity(t.Context(), clone)
	if err != nil {
		t.Fatal(err)
	}
	if cloneCommon == common || cloneWorktree == worktree {
		t.Fatal("separate clone shares repository/worktree identity")
	}
}

func TestGitEnvironmentPreservesUserConfiguration(t *testing.T) {
	config := filepath.Join(t.TempDir(), "global-config")
	if err := os.WriteFile(config, []byte("[servediff]\n\tuser-setting = kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	value, err := runGit(t.Context(), t.TempDir(), 1024, "config", "--get", "servediff.user-setting")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(value) != "kept" {
		t.Fatalf("user config lost: %q", value)
	}
}

func TestRepositoryOperationsPreserveCancellation(t *testing.T) {
	root := newRuntimeTestRepository(t, "tracked.txt")
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenRepository(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(t.Context(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 1 {
		t.Fatalf("expected untracked file: %#v", snapshot.Files)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, operation := range map[string]func() error{
		"open":     func() error { _, err := OpenRepository(ctx, root); return err },
		"snapshot": func() error { _, err := source.Snapshot(ctx, review.DiffAll); return err },
		"patch": func() error {
			_, err := source.Patch(ctx, review.DiffAll, snapshot.Files[0], snapshot.Head)
			return err
		},
		"contents": func() error {
			_, err := source.Contents(ctx, review.DiffAll, snapshot.Files[0], snapshot.Head)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation changed into another error: %v", err)
			}
		})
	}
}
