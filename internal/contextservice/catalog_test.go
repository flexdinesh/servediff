package contextservice

import (
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
	if len(service.sources) != 0 {
		t.Fatal("catalog loaded filesystem sources")
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
	if err != nil || old.Branch == nil || *old.Branch != input.Metadata.Branch || old.ChangedFileCount == nil || *old.ChangedFileCount != 1 || old.LastChangedAt != input.Metadata.CollectedAt {
		t.Fatalf("old observation mutated: %#v, %v", old, err)
	}
	latest, err := restarted.Get(t.Context(), after.Context.ID)
	if err != nil || latest.Branch == nil || *latest.Branch != "next" || latest.ChangedFileCount == nil || *latest.ChangedFileCount != 0 {
		t.Fatalf("new observation: %#v, %v", latest, err)
	}
}
