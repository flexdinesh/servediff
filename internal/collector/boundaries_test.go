package collector

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/diffx/internal/diffsource"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
)

func TestSessionProvenanceDoesNotChangeContentIdentity(t *testing.T) {
	root := branchRepository(t)
	options := Options{SourceID: "machine", Hostname: "host", Agent: "codex", RunID: "session-a", SessionName: "Feature work", TriggerRoot: root}
	first, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if session := first.Metadata.AgentSession; session == nil || session.Harness != "codex" || session.ID != "session-a" || session.Name != "Feature work" || first.Metadata.TriggerRoot != root || first.Metadata.Agent != "codex" || first.Metadata.RunID != "session-a" {
		t.Fatalf("session provenance lost: %+v", first.Metadata)
	}
	options.Agent, options.RunID, options.SessionName, options.TriggerRoot = "claude", "session-b", "Review", filepath.Join(root, "subdir")
	second, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != second.ContentHash || Fingerprint(first) != Fingerprint(second) {
		t.Fatal("session/trigger provenance changed snapshot identity")
	}
	options.RunID = ""
	third, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if third.Metadata.AgentSession != nil {
		t.Fatal("incomplete legacy session labels fabricated a session")
	}
}

func TestCollectorResolvesTriggerDirectory(t *testing.T) {
	root := branchRepository(t)
	subdir := filepath.Join(root, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)
	request, err := Collect(t.Context(), ".", Options{SourceID: "machine", Hostname: "host", TriggerRoot: "."})
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.TriggerRoot != subdir || request.Metadata.Root != root {
		t.Fatalf("trigger directory must remain distinct from checkout root: %+v", request.Metadata)
	}
}

func TestComparisonFallbackAndDetachedMetadata(t *testing.T) {
	root := repository(t)
	unborn := collect(t, root)
	if comparison := unborn.Metadata.Comparison; comparison == nil || comparison.Kind != "working-tree" || comparison.BaseRef != "HEAD" || comparison.BaseCommit != "" || comparison.MergeBase != "" {
		t.Fatalf("unborn fallback unspecified: %+v", comparison)
	}
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "branch", "-m", "custom")
	write(t, root, "tracked", "working\n")
	fallback := collect(t, root)
	if comparison := fallback.Metadata.Comparison; comparison == nil || comparison.Kind != "working-tree" || comparison.BaseCommit != *fallback.Metadata.Head || comparison.MergeBase != *fallback.Metadata.Head {
		t.Fatalf("missing default fallback unspecified: %+v", comparison)
	}
	if patch := patchFor(t, fallback, review.DiffAll, "tracked"); patch.Contents == nil || patch.Contents.Before != "original\n" || patch.Contents.After != "working\n" {
		t.Fatalf("fallback lost working changes: %+v", patch)
	}
	git(t, root, "branch", "main")
	git(t, root, "restore", "tracked")
	git(t, root, "commit", "--allow-empty", "-m", "detached target")
	git(t, root, "checkout", "--detach")
	detached := collect(t, root)
	if comparison := detached.Metadata.Comparison; comparison == nil || comparison.Kind != "branch" || comparison.BaseRef != "refs/heads/main" || comparison.BaseCommit == *detached.Metadata.Head {
		t.Fatalf("detached comparison lost branch baseline: %+v", comparison)
	}
	for _, request := range []ingestion.Request{unborn, fallback, detached} {
		if err := ingestion.Validate(request); err != nil {
			t.Fatal(err)
		}
	}
}

func TestComparisonCanonicalRefsAndBaselineChanges(t *testing.T) {
	root := branchRepository(t)
	options := Options{SourceID: "machine", Hostname: "host", Base: "main"}
	first, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	options.Base = "refs/heads/main"
	same, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(first) != Fingerprint(same) {
		t.Fatal("equivalent baseline names changed collection identity")
	}
	git(t, root, "switch", "main")
	git(t, root, "commit", "--allow-empty", "-m", "base advanced")
	git(t, root, "switch", "feature")
	next, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != next.ContentHash || first.Metadata.Comparison.MergeBase != next.Metadata.Comparison.MergeBase || first.Metadata.Comparison.BaseCommit == next.Metadata.Comparison.BaseCommit || Fingerprint(first) == Fingerprint(next) {
		t.Fatalf("resolved baseline update lost despite identical content: before %+v after %+v", first.Metadata.Comparison, next.Metadata.Comparison)
	}
	options.Base = next.Metadata.Comparison.MergeBase
	literal, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if comparison := literal.Metadata.Comparison; comparison.BaseRef != options.Base || comparison.BaseCommit != options.Base || comparison.MergeBase != options.Base {
		t.Fatalf("literal baseline unresolved: %+v", comparison)
	}
}

func TestCheckoutDiscoverySelectsOnlyInputWithoutWorkspaceOrBranchRecovery(t *testing.T) {
	root := branchRepository(t)
	git(t, root, "branch", "recoverable")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "other", linked)
	subdir := filepath.Join(linked, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := DiscoverCheckout(t.Context(), subdir, "machine", "")
	if err != nil || len(result.Sources) != 1 || len(result.Diagnostics) != 0 {
		t.Fatalf("selected checkout: %+v %v", result, err)
	}
	source := result.Sources[0]
	if source.Path != linked || source.InputPath != subdir || source.Base != "auto" || source.Branch != "" || source.Identity == "" {
		t.Fatalf("selected checkout lost identity or trigger: %+v", source)
	}
	if result, err := DiscoverCheckout(t.Context(), filepath.Dir(linked), "machine", "auto"); !errors.Is(err, diffsource.ErrNotRepository) || len(result.Sources) != 0 {
		t.Fatalf("non-Git workspace silently scanned: %+v %v", result, err)
	}
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	result, err = DiscoverCheckout(t.Context(), root, "machine", "HEAD")
	if err != nil || len(result.Sources) != 1 || len(result.Diagnostics) != 0 || result.Sources[0].Path != root || result.Sources[0].Base != "HEAD" {
		t.Fatalf("unrelated missing checkout affected selection: %+v %v", result, err)
	}
	if result, err := DiscoverCheckout(t.Context(), linked, "machine", "auto"); err == nil || len(result.Sources) != 0 {
		t.Fatalf("unavailable selected checkout accepted: %+v %v", result, err)
	}
}
