package contextservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/servediff/internal/collector"
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

func TestCatalogOwnershipAndCancelledSubmission(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	created, err := service.Capture(ctx, "request", testPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := service.store.(*reviewstore.Store).User("other-user", "other-user")
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

func TestPathRegistrationRemoved(t *testing.T) {
	service := testService(t)
	_, err := service.Register(t.Context(), "request", "/does-not-exist")
	assertStatus(t, err, 410)
	page, err := service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 0 {
		t.Fatalf("removed registration persisted: %#v, %v", page, err)
	}
}

func TestObservationStoredOnlyAfterCheckoutChangesAndRemoval(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	collected, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", SubmissionID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.Ingest(t.Context(), collected)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Context.Kind != "observation" || submitted.Context.Observation == nil || submitted.Context.Capabilities.Diff.Refresh.Enabled() || !submitted.Context.Capabilities.Diff.Scopes.Allows(review.DiffStaged) {
		t.Fatalf("observation contract: %#v", submitted.Context)
	}
	retry, err := service.Ingest(t.Context(), collected)
	if err != nil || retry.Context.ID != submitted.Context.ID {
		t.Fatalf("retry: %#v, %v", retry, err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("changed after collection\n"), 0600); err != nil {
		t.Fatal(err)
	}
	catalogGit(t, root, "symbolic-ref", "HEAD", "refs/heads/other-branch")
	t.Setenv("PATH", t.TempDir()) // Queries must work without a Git executable.
	assertStored := func(service *Service) {
		t.Helper()
		active, err := service.Resolve(t.Context(), submitted.Context.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !active.Stored || active.Capabilities.Diff.Refresh.Enabled() {
			t.Fatalf("stored session: %#v", active)
		}
		snapshot, err := active.Source.Snapshot(t.Context(), review.DiffAll)
		if err != nil || snapshot.Revision != submitted.Snapshot.Revision || snapshot.Branch != submitted.Snapshot.Branch {
			t.Fatalf("snapshot changed without ingestion: %#v, %v", snapshot, err)
		}
		preview, err := active.Source.Patch(t.Context(), review.DiffAll, snapshot.Files[0], nil)
		if err != nil || preview.Contents == nil || preview.Contents.After != "new\n" {
			t.Fatalf("stored preview: %#v, %v", preview, err)
		}
		item, err := service.Get(t.Context(), submitted.Context.ID)
		if err != nil || item.Availability != "available" || item.LastChangedAt != collected.Metadata.CollectedAt {
			t.Fatalf("stored metadata: %#v, %v", item, err)
		}
	}
	assertStored(service)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	assertStored(service)
	restarted := New(service.store, service.user)
	t.Cleanup(func() { _ = restarted.Close() })
	assertStored(restarted)
	collected.Metadata.Agent = "conflict"
	_, err = service.Ingest(t.Context(), collected)
	assertStatus(t, err, 409)
}

func TestLegacyWorktreeDoesNotReadFilesystem(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	binding, err := service.store.(*reviewstore.Store).RegisterGit(service.user.ID, root, filepath.Join(root, ".git"), filepath.Join(root, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	item, err := service.Get(t.Context(), binding.ContextID)
	if err != nil || item.Availability != "unavailable" || item.Capabilities.Diff.Refresh.Enabled() {
		t.Fatalf("legacy catalog: %#v, %v", item, err)
	}
	active, err := service.Resolve(t.Context(), binding.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = active.Source.Snapshot(t.Context(), review.DiffAll)
	assertStatus(t, err, 503)
}

func TestClosedServiceRejectsStoredSourceResolution(t *testing.T) {
	service := testService(t)
	input, err := collector.CollectPatch(t.Context(), testPatch, "", collector.Options{SourceID: "source", SubmissionID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.Ingest(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = service.Resolve(t.Context(), submitted.Context.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("closed service resolved source: %v", err)
	}
}

func TestObservationOwnershipAndCancelledIngestion(t *testing.T) {
	service := testService(t)
	input, err := collector.CollectPatch(t.Context(), testPatch, "", collector.Options{SourceID: "source", SubmissionID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.Ingest(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := service.store.(*reviewstore.Store).User("other-observation-user", "other")
	if err != nil {
		t.Fatal(err)
	}
	other := New(service.store, otherUser)
	t.Cleanup(func() { _ = other.Close() })
	_, err = other.Get(t.Context(), submitted.Context.ID)
	assertStatus(t, err, 404)
	_, err = other.Resolve(t.Context(), submitted.Context.ID)
	assertStatus(t, err, 404)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	input.SubmissionID = "cancelled"
	_, err = service.Ingest(cancelled, input)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ingestion accepted: %v", err)
	}
	page, err := service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 1 {
		t.Fatalf("cancelled ingestion persisted: %#v, %v", page, err)
	}
}
