package contextservice

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/ingestion"
)

func catalogGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s, %v", args, output, err)
	}
}

func TestCatalogContainsOnlySubmittedWorktreeObservations(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	catalogGit(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	catalogGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	catalogGit(t, root, "remote", "add", "origin", "https://github.com/owner/servediff.git")
	linked := filepath.Join(t.TempDir(), "elsewhere")
	catalogGit(t, root, "worktree", "add", "-qb", "feature/picker", linked)
	input, err := collector.Collect(t.Context(), linked, collector.Options{SourceID: "source", SubmissionID: "linked"})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.Ingest(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	catalogGit(t, root, "worktree", "add", "-qb", "feature/new", filepath.Join(t.TempDir(), "never-submitted"))
	t.Setenv("PATH", t.TempDir())
	page, err := service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 1 || page.Contexts[0].ID != submitted.Context.ID {
		t.Fatalf("server discovered unsubmitted worktrees: %#v, %v", page, err)
	}
	item := page.Contexts[0]
	if item.Name != "servediff" || item.Root == nil || *item.Root != linked || item.Branch == nil || *item.Branch != "feature/picker" || item.WorktreeName == nil || *item.WorktreeName != "elsewhere" {
		t.Fatalf("captured Git metadata: %#v", item)
	}
}

func TestOldObservationUsesRemoteNameWithoutReadingGit(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	catalogGit(t, root, "remote", "add", "origin", "git@github.com:owner/servediff.git")
	input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", SubmissionID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	input.Metadata.RepositoryName = "main"
	input.Metadata.LinkedWorktree = nil
	submitted, err := service.Ingest(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	opened, err := service.Get(t.Context(), submitted.Context.ID)
	if err != nil || opened.Name != "servediff" || opened.Observation == nil || opened.Observation.LinkedWorktree != nil {
		t.Fatalf("old snapshot presentation: %#v, %v", opened, err)
	}
	page, err := service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 1 || page.Contexts[0].Name != "servediff" {
		t.Fatalf("old snapshot catalog: %#v, %v", page, err)
	}
}

func TestPipedCatalogSeparatesNewAndLegacyImportsFromRepositories(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	local, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", SubmissionID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	for index, patch := range []string{testPatch, testPatch + "\n"} {
		input, err := collector.CollectPatch(t.Context(), patch, root, collector.Options{SourceID: "source", SubmissionID: fmt.Sprintf("piped-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			// Older producers attached the invoking repository's metadata.
			input.Metadata = local.Metadata
			input.Metadata.Trigger = "manual"
		}
		submitted, err := service.Ingest(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		item, err := service.Get(t.Context(), submitted.Context.ID)
		if err != nil || item.Source != "stdin" || item.Name != "Piped" || item.RepositoryID != nil || item.Root != nil || item.Branch != nil || item.WorktreeName != nil || item.Stale {
			t.Fatalf("piped catalog identity: %#v, %v", item, err)
		}
	}
	page, err := service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 3 {
		t.Fatalf("retained imports: %#v, %v", page, err)
	}
	for _, item := range page.Contexts {
		if item.Source == "stdin" && (item.RepositoryID != nil || item.Stale) {
			t.Fatalf("piped import grouped or superseded: %#v", item)
		}
	}
}

func TestCatalogSearchKeepsSameBranchSourcesSeparate(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	catalogGit(t, root, "symbolic-ref", "HEAD", "refs/heads/shared")
	catalogGit(t, root, "remote", "add", "origin", "https://example.com/acme/servediff.git")
	for _, source := range []string{"container-a", "container-b"} {
		input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: source, Hostname: "agent-host", RunID: "run-" + source, SubmissionID: "same-submission"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Ingest(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	both, err := service.ListFiltered(t.Context(), 100, "", ingestion.Filter{Repository: "servediff", Branch: "shared"})
	if err != nil || len(both.Contexts) != 2 || both.Contexts[0].ID == both.Contexts[1].ID || both.Contexts[0].RepositoryID == nil || both.Contexts[1].RepositoryID == nil || *both.Contexts[0].RepositoryID != *both.Contexts[1].RepositoryID {
		t.Fatalf("source grouping: %#v, %v", both, err)
	}
	for _, filter := range []ingestion.Filter{{Query: "CONTAINER-A"}, {SourceID: "container-a", Hostname: "agent-host", RunID: "run-container-a"}, {Worktree: filepath.Base(root), SourceID: "container-a"}} {
		page, err := service.ListFiltered(t.Context(), 100, "", filter)
		if err != nil || len(page.Contexts) != 1 || page.Contexts[0].Observation == nil || page.Contexts[0].Observation.SourceID != "container-a" {
			t.Fatalf("filter %#v: %#v, %v", filter, page, err)
		}
	}
}

func TestCatalogCountsAndBranchRemainCapturedUntilNewIngestion(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", SubmissionID: "before"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.Ingest(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	catalogGit(t, root, "add", ".")
	catalogGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	catalogGit(t, root, "checkout", "-qb", "next")
	afterInput, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", SubmissionID: "after"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := service.Ingest(t.Context(), afterInput)
	if err != nil {
		t.Fatal(err)
	}
	if before.Context.ChangedFileCount == nil || *before.Context.ChangedFileCount != 1 || after.Context.ChangedFileCount == nil || *after.Context.ChangedFileCount != 0 {
		t.Fatalf("counts: before %#v, after %#v", before.Context, after.Context)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	restarted := New(service.store, service.user)
	t.Cleanup(func() { _ = restarted.Close() })
	old, err := restarted.Get(t.Context(), before.Context.ID)
	if err != nil || old.Stale || old.Branch == nil || *old.Branch != input.Metadata.Branch || old.ChangedFileCount == nil || *old.ChangedFileCount != 1 || old.LastChangedAt != input.Metadata.CollectedAt {
		t.Fatalf("old observation mutated: %#v, %v", old, err)
	}
	latest, err := restarted.Get(t.Context(), after.Context.ID)
	if err != nil || latest.Stale || latest.Branch == nil || *latest.Branch != "next" || latest.ChangedFileCount == nil || *latest.ChangedFileCount != 0 {
		t.Fatalf("new observation: %#v, %v", latest, err)
	}
}

func TestCatalogStaleStatusSurvivesRestartAndFilteredPages(t *testing.T) {
	service := testService(t)
	root := testRepo(t)
	input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", RunID: "before-run", SubmissionID: "before"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.Ingest(t.Context(), input)
	if err != nil || before.Context.Stale {
		t.Fatalf("initial observation: %#v, %v", before, err)
	}
	if err := os.Remove(filepath.Join(root, "file")); err != nil {
		t.Fatal(err)
	}
	afterInput, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "source", RunID: "after-run", SubmissionID: "after"})
	if err != nil {
		t.Fatal(err)
	}
	afterInput.Metadata.CollectedAt = input.Metadata.CollectedAt + 1
	after, err := service.Ingest(t.Context(), afterInput)
	if err != nil || after.Context.Stale || after.Context.ChangedFileCount == nil || *after.Context.ChangedFileCount != 0 {
		t.Fatalf("empty latest observation: %#v, %v", after, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	restarted := New(service.store, service.user)
	t.Cleanup(func() { _ = restarted.Close() })
	for _, identity := range []struct {
		id    string
		stale bool
	}{{before.Context.ID, true}, {after.Context.ID, false}} {
		item, err := restarted.Get(t.Context(), identity.id)
		if err != nil || item.Stale != identity.stale {
			t.Fatalf("freshness after restart: %#v, %v", item, err)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Stale *bool `json:"stale"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil || wire.Stale == nil || *wire.Stale != identity.stale {
			t.Fatalf("wire freshness: %s, %v", encoded, err)
		}
	}
	filtered, err := restarted.ListFiltered(t.Context(), 1, "", ingestion.Filter{Query: "before-run"})
	if err != nil || len(filtered.Contexts) != 1 || filtered.Contexts[0].ID != before.Context.ID || !filtered.Contexts[0].Stale {
		t.Fatalf("search omitted latest: %#v, %v", filtered, err)
	}
	seen := 0
	cursor := ""
	for {
		page, err := restarted.List(t.Context(), 1, cursor)
		if err != nil || len(page.Contexts) != 1 {
			t.Fatalf("catalog page: %#v, %v", page, err)
		}
		item := page.Contexts[0]
		if item.Stale != (item.ID == before.Context.ID) {
			t.Fatalf("page freshness: %#v", item)
		}
		seen++
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if seen != 2 {
		t.Fatalf("catalog returned %d observations, want 2", seen)
	}
	fresh := input
	fresh.SubmissionID = "before-fresh"
	fresh.Metadata.CollectedAt = afterInput.Metadata.CollectedAt + 1
	reused, err := restarted.Ingest(t.Context(), fresh)
	if err != nil || reused.Context.ID != before.Context.ID || reused.Context.Stale || reused.Context.LastChangedAt != input.Metadata.CollectedAt {
		t.Fatalf("deduplicated latest observation: %#v, %v", reused, err)
	}
	late := afterInput
	late.SubmissionID = "after-late"
	delayed, err := restarted.Ingest(t.Context(), late)
	if err != nil || delayed.Context.ID != after.Context.ID || !delayed.Context.Stale {
		t.Fatalf("delayed deduplicated empty observation: %#v, %v", delayed, err)
	}
	filtered, err = restarted.ListFiltered(t.Context(), 1, "", ingestion.Filter{Query: "before-run"})
	if err != nil || len(filtered.Contexts) != 1 || filtered.Contexts[0].ID != before.Context.ID || filtered.Contexts[0].Stale {
		t.Fatalf("deduplicated freshness outside search: %#v, %v", filtered, err)
	}
}
