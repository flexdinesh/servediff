package contextservice

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

func TestRegistrationCatalogAndHooksNeverCollectDiffs(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	submission, err := service.Register(t.Context(), "register", root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.store.ContextBinding(service.user.ID, submission.Context.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range binding.DiffIDs {
		if _, err := service.store.CurrentVersion(service.user.ID, id); !errors.Is(err, reviewstore.ErrNotFound) {
			t.Fatalf("registration collected data: %v", err)
		}
	}
	events, unsubscribe := service.Subscribe()
	defer unsubscribe()
	event, err := service.Change(t.Context(), ChangeInput{Path: root, Branch: "feature", Detail: "agent hook"})
	if err != nil || event.ContextID != submission.Context.ID || event.Generation != 1 {
		t.Fatalf("change: %#v, %v", event, err)
	}
	select {
	case received := <-events:
		if received.Kind != "change" || received.Branch != "feature" {
			t.Fatalf("notification: %#v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("missing notification")
	}
	if _, err := service.store.CurrentVersion(service.user.ID, binding.DiffIDs[review.DiffAll]); !errors.Is(err, reviewstore.ErrNotFound) {
		t.Fatal("hook collected a dormant worktree")
	}
	if err := service.Refresh(t.Context(), submission.Context.ID, review.DiffStaged); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.CurrentVersion(service.user.ID, binding.DiffIDs[review.DiffStaged]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.CurrentVersion(service.user.ID, binding.DiffIDs[review.DiffAll]); !errors.Is(err, reviewstore.ErrNotFound) {
		t.Fatal("scope refresh collected another scope")
	}
	other := testRepo(t)
	if _, err := service.Change(t.Context(), ChangeInput{Path: other}); err == nil {
		t.Fatal("hook registered unknown repository")
	}
}

func TestStoredReadsNeedNoGitOrCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable shim")
	}
	service := testService(t)
	root := testRepo(t)
	submission, err := service.Register(t.Context(), "register", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Refresh(t.Context(), submission.Context.ID, review.DiffAll); err != nil {
		t.Fatal(err)
	}
	source, err := service.Resolve(t.Context(), submission.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Source.Snapshot(t.Context(), review.DiffAll)
	if err != nil || len(snapshot.Files) == 0 {
		t.Fatalf("snapshot: %#v, %v", snapshot, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Repositories(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(t.Context(), 100, ""); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(t.Context(), submission.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := resolved.Source.Snapshot(t.Context(), review.DiffAll)
	if err != nil || current.VersionID != snapshot.VersionID {
		t.Fatalf("stored snapshot: %#v, %v", current, err)
	}
	if _, err := resolved.Source.Patch(t.Context(), review.DiffAll, current.Files[0], current.Head); err != nil {
		t.Fatal(err)
	}
	if err := service.Refresh(t.Context(), submission.Context.ID, review.DiffAll); err == nil {
		t.Fatal("refresh accepted missing checkout")
	}
}
