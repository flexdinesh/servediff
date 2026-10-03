package contextservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

const testPatch = "diff --git a/file b/file\n--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new\n"

func testService(t *testing.T) *Service {
	t.Helper()
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.User("test-user", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, user)
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func assertStatus(t *testing.T, err error, status int) {
	t.Helper()
	var problem *diffsource.RequestError
	if !errors.As(err, &problem) || problem.Status != status {
		t.Fatalf("error = %v, want status %d", err, status)
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	command := exec.Command("git", "init", "--quiet", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s, %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCaptureCatalogAndImmutableIdentity(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	first, err := service.Capture(ctx, "one", testPatch, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.Capture(ctx, "one", testPatch, "/repo")
	if err != nil || retry.Context.ID != first.Context.ID || retry.Snapshot.VersionID != first.Snapshot.VersionID {
		t.Fatalf("retry: %#v, %v", retry, err)
	}
	second, err := service.Capture(ctx, "two", testPatch, "/repo")
	if err != nil || second.Context.ID == first.Context.ID {
		t.Fatalf("independent capture: %#v, %v", second, err)
	}
	item := first.Context
	if item.Kind != "capture" || item.Root != nil || item.LocationID != nil || item.ExpiresAt == nil || item.SubmittedFrom == nil ||
		item.Capabilities.Diff.Refresh.Enabled() || item.Capabilities.Diff.Scopes.Allows(review.DiffStaged) {
		t.Fatalf("capture metadata: %#v", item)
	}
	opened, err := service.OpenCapture(ctx, item.ID)
	if err != nil || opened.Snapshot.ID != first.Snapshot.ID || opened.Snapshot.VersionID != first.Snapshot.VersionID {
		t.Fatalf("open capture: %#v, %v", opened, err)
	}
	_, err = service.Capture(ctx, "one", testPatch+"\n", "/repo")
	assertStatus(t, err, 409)
	page, err := service.List(ctx, 1, "")
	if err != nil || len(page.Contexts) != 1 || page.NextCursor == nil {
		t.Fatalf("first page: %#v, %v", page, err)
	}
	next, err := service.List(ctx, 1, *page.NextCursor)
	if err != nil || len(next.Contexts) != 1 || next.NextCursor != nil || next.Contexts[0].ID == page.Contexts[0].ID {
		t.Fatalf("second page: %#v, %v", next, err)
	}
	worktrees, captures, err := service.Count(ctx)
	if err != nil || worktrees != 0 || captures != 2 {
		t.Fatalf("counts %d %d, %v", worktrees, captures, err)
	}
}

func TestInvalidCaptureDoesNotRegister(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	_, err := service.Capture(ctx, "invalid", "not a Git patch", "")
	assertStatus(t, err, 400)
	page, err := service.List(ctx, 0, "")
	if err != nil || len(page.Contexts) != 0 {
		t.Fatalf("invalid capture persisted: %#v, %v", page, err)
	}
	_, err = service.Capture(ctx, "", "", "")
	assertStatus(t, err, 400)
	_, err = service.List(ctx, 501, "")
	assertStatus(t, err, 400)
	_, err = service.List(ctx, 100, "invalid")
	assertStatus(t, err, 400)
}

func TestWorktreeRegistrationAndUnavailableIsolation(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	root := testRepo(t)
	first, err := service.Register(ctx, "one", root)
	if err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(root, "sub")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := service.Register(ctx, "two", subdir)
	if err != nil || first.Context.ID != second.Context.ID || first.Snapshot.ID != second.Snapshot.ID {
		t.Fatalf("same worktree identity: %#v, %v", second, err)
	}
	item := first.Context
	if item.Kind != "worktree" || item.Root == nil || item.LocationID == nil || item.RepositoryID == nil ||
		!item.Capabilities.Diff.Refresh.Enabled() || !item.Capabilities.Diff.Scopes.Allows(review.DiffStaged) {
		t.Fatalf("worktree metadata: %#v", item)
	}
	otherRoot := testRepo(t)
	other, err := service.Register(ctx, "three", otherRoot)
	if err != nil || other.Context.ID == item.ID || other.Snapshot.ID == first.Snapshot.ID {
		t.Fatalf("different worktree identity: %#v, %v", other, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(ctx, item.ID)
	if err != nil || resolved.ContextID != item.ID {
		t.Fatalf("missing worktree lost binding: %#v, %v", resolved, err)
	}
	_, err = resolved.Source.Snapshot(ctx, review.DiffAll)
	assertStatus(t, err, 503)
	unavailable, err := service.Get(ctx, item.ID)
	if err != nil || unavailable.Availability != "unavailable" {
		t.Fatalf("unavailable metadata: %#v, %v", unavailable, err)
	}
	healthy, err := service.Resolve(ctx, other.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := healthy.Source.Snapshot(ctx, review.DiffAll); err != nil {
		t.Fatalf("one unavailable source broke another: %v", err)
	}
	restarted := New(service.store, service.user)
	unchecked, err := restarted.Get(ctx, item.ID)
	if err != nil || unchecked.Availability != "unchecked" {
		t.Fatalf("reconstructed catalog: %#v, %v", unchecked, err)
	}
	_, err = service.OpenCapture(ctx, item.ID)
	assertStatus(t, err, 404)
}

func TestCatalogOwnershipAndCancelledSubmission(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	created, err := service.Capture(ctx, "request", testPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := service.store.User("other-user", "other-user")
	if err != nil {
		t.Fatal(err)
	}
	other := New(service.store, otherUser)
	_, err = other.Get(ctx, created.Context.ID)
	assertStatus(t, err, 404)
	_, err = other.Resolve(ctx, created.Context.ID)
	assertStatus(t, err, 404)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = service.Capture(cancelled, "cancelled", testPatch, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request accepted: %v", err)
	}
	_, captures, err := service.Count(ctx)
	if err != nil || captures != 1 {
		t.Fatalf("cancelled capture persisted: %d, %v", captures, err)
	}
}

func TestResolveReusesSourcesWhileLiveDiffsStillChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git invocation counter uses a Unix executable shim")
	}
	service := testService(t)
	ctx := context.Background()
	root := testRepo(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	countPath := filepath.Join(shimDir, "calls")
	shim := []byte("#!/bin/sh\nprintf . >> \"$SERVEDIFF_TEST_GIT_COUNT\"\nexec \"$SERVEDIFF_TEST_REAL_GIT\" \"$@\"\n")
	if err := os.WriteFile(filepath.Join(shimDir, "git"), shim, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_TEST_REAL_GIT", realGit)
	t.Setenv("SERVEDIFF_TEST_GIT_COUNT", countPath)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	submitted, err := service.Register(ctx, "request", root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if _, err := service.Resolve(ctx, submitted.Context.ID); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(countPath)
	if err != nil || len(after) != len(before) {
		t.Fatalf("cached Resolve reran Git: %d -> %d, %v", len(before), len(after), err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(ctx, submitted.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := resolved.Source.Snapshot(ctx, review.DiffAll)
	if err != nil || current.Revision == submitted.Snapshot.Revision {
		t.Fatalf("source cache froze live diff: %#v, %v", current, err)
	}
	// A different Git directory at the same path must require registration.
	if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, ".old-git")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(realGit, "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("replace Git directory: %s, %v", output, err)
	}
	unavailable, err := service.Resolve(ctx, submitted.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = unavailable.Source.Snapshot(ctx, review.DiffAll)
	assertStatus(t, err, 503)
	if _, err := service.Register(ctx, "register-replacement", root); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Resolve(ctx, submitted.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Source.Snapshot(ctx, review.DiffAll); err != nil {
		t.Fatalf("explicit registration did not restore source: %v", err)
	}
}

func TestCaptureCacheReusesParseAndEvictionPreservesVersion(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	submitted, err := service.Capture(ctx, "first", testPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Resolve(ctx, submitted.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		resolved, err := service.Resolve(ctx, submitted.Context.ID)
		if err != nil || resolved.Source != first.Source {
			t.Fatalf("immutable capture reparsed: %v", err)
		}
	}
	// Exhaust the source-count budget, then reconstruct the evicted capture.
	for index := range maxCachedSources {
		if _, err := service.Capture(ctx, fmt.Sprintf("other-%d", index), testPatch, ""); err != nil {
			t.Fatal(err)
		}
	}
	service.mu.Lock()
	retainedCount := len(service.sources)
	retainedBytes := service.sourceBytes
	service.mu.Unlock()
	if retainedCount > maxCachedSources || retainedBytes > maxSourceBytes {
		t.Fatalf("retained budget exceeded: %d entries, %d bytes", retainedCount, retainedBytes)
	}
	reopened, err := service.OpenCapture(ctx, submitted.Context.ID)
	if err != nil || reopened.Snapshot.ID != submitted.Snapshot.ID || reopened.Snapshot.VersionID != submitted.Snapshot.VersionID {
		t.Fatalf("eviction changed immutable version: %#v, %v", reopened, err)
	}
}

// The shim blocks Git before it has spawned children, so cancellation must
// terminate the shared subprocess rather than waiting for an unrelated child.
func TestBlockingGitShimHelper(t *testing.T) {
	if os.Getenv("SERVEDIFF_TEST_BLOCKING_GIT") != "1" {
		return
	}
	started := os.Getenv("SERVEDIFF_TEST_GIT_STARTED")
	released := os.Getenv("SERVEDIFF_TEST_GIT_RELEASED")
	if err := os.WriteFile(started, []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(released); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(3)
		}
		time.Sleep(10 * time.Millisecond)
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		os.Exit(4)
	}
	command := exec.Command(os.Getenv("SERVEDIFF_TEST_REAL_GIT"), os.Args[separator+1:]...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		os.Exit(5)
	}
	os.Exit(0)
}

func installBlockingGit(t *testing.T) (started, released string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Git shim uses Unix executable dispatch")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	started, released = filepath.Join(directory, "started"), filepath.Join(directory, "released")
	shim := []byte("#!/bin/sh\nexec \"$SERVEDIFF_TEST_EXECUTABLE\" -test.run=TestBlockingGitShimHelper -- \"$@\"\n")
	if err := os.WriteFile(filepath.Join(directory, "git"), shim, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_TEST_BLOCKING_GIT", "1")
	t.Setenv("SERVEDIFF_TEST_REAL_GIT", realGit)
	t.Setenv("SERVEDIFF_TEST_EXECUTABLE", executable)
	t.Setenv("SERVEDIFF_TEST_GIT_STARTED", started)
	t.Setenv("SERVEDIFF_TEST_GIT_RELEASED", released)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return started, released
}

func awaitGitStart(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("shared Git load did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseCancelsAndDrainsSourceLoading(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	submitted, err := service.Register(ctx, "register", testRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	service.invalidateSource(submitted.Context.ID)
	started, _ := installBlockingGit(t)
	result := make(chan error, 1)
	go func() { _, err := service.Resolve(ctx, submitted.Context.ID); result <- err }()
	awaitGitStart(t, started)
	began := time.Now()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("Close waited for blocked Git instead of cancelling it")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pending load result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left a caller waiting for source loading")
	}
	if _, err := service.Resolve(ctx, submitted.Context.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed service accepted new load: %v", err)
	}
}

func TestCallerCancellationDoesNotCancelSharedSourceLoading(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	submitted, err := service.Register(ctx, "register", testRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	service.invalidateSource(submitted.Context.ID)
	started, released := installBlockingGit(t)
	caller, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { _, err := service.Resolve(caller, submitted.Context.ID); first <- err }()
	awaitGitStart(t, started)
	second := make(chan error, 1)
	go func() { _, err := service.Resolve(ctx, submitted.Context.ID); second <- err }()
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled caller: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled caller still waiting")
	}
	if err := os.WriteFile(released, []byte("continue"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("one cancellation affected another caller: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shared load did not complete")
	}
	available, err := service.Get(ctx, submitted.Context.ID)
	if err != nil || available.Availability != "available" {
		t.Fatalf("cancelled load poisoned availability: %#v, %v", available, err)
	}
}
