package contextservice

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
		if active.Capabilities.Diff.Refresh.Enabled() {
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
		if !reflect.DeepEqual(item.Capabilities, active.Capabilities) {
			t.Fatalf("catalog/session capabilities diverged: %+v, %+v", item.Capabilities, active.Capabilities)
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
