package collector

import (
	"errors"
	"fmt"
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

func TestWorktreeDiscoveryOriginFirstWithoutWorkspaceOrBranchRecovery(t *testing.T) {
	root := branchRepository(t)
	git(t, root, "branch", "recoverable")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "other", linked)
	subdir := filepath.Join(linked, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := DiscoverWorktrees(t.Context(), subdir, "machine", "")
	if err != nil || len(result.Sources) != 2 || len(result.Diagnostics) != 0 {
		t.Fatalf("registered worktrees: %+v %v", result, err)
	}
	if result.Sources[0].Path != linked || result.Sources[1].Path != root || result.Sources[0].Identity == result.Sources[1].Identity {
		t.Fatalf("origin/worktree identities lost: %+v", result.Sources)
	}
	for _, source := range result.Sources {
		if source.InputPath != subdir || source.Base != "auto" || source.Branch != "" {
			t.Fatalf("implicit branch recovery or lost trigger directory: %+v", source)
		}
	}
	if result, err := DiscoverWorktrees(t.Context(), filepath.Dir(linked), "machine", "auto"); !errors.Is(err, diffsource.ErrNotRepository) || len(result.Sources) != 0 {
		t.Fatalf("non-Git workspace silently scanned: %+v %v", result, err)
	}
	git(t, root, "worktree", "remove", "--force", linked)
	result, err = DiscoverWorktrees(t.Context(), root, "machine", "HEAD")
	if err != nil || len(result.Sources) != 1 || result.Sources[0].Base != "HEAD" {
		t.Fatalf("removed worktree recovered by default: %+v %v", result, err)
	}
}

func TestWorktreeDiscoveryReportsUnavailableCheckout(t *testing.T) {
	root := branchRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "other", linked)
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	result, err := DiscoverWorktrees(t.Context(), root, "machine", "auto")
	if err != nil || len(result.Sources) != 1 || len(result.Diagnostics) != 1 {
		t.Fatalf("missing checkout needs diagnostic without false source: %+v %v", result, err)
	}
}

func TestWorktreeDiscoveryIncludesEveryRegisteredCheckout(t *testing.T) {
	root := branchRepository(t)
	worktrees := t.TempDir()
	// The registered Git list is finite and must not inherit workspace scanning's
	// unrelated safety bound. No checkout files are needed for discovery.
	for index := 0; index < discoveryLimit; index++ {
		linked := filepath.Join(worktrees, fmt.Sprintf("linked-%03d", index))
		git(t, root, "worktree", "add", "--detach", "--no-checkout", linked)
	}
	result, err := DiscoverWorktrees(t.Context(), root, "machine", "auto")
	if err != nil || len(result.Diagnostics) != 0 || len(result.Sources) != discoveryLimit+1 {
		t.Fatalf("registered checkout list truncated: %d sources, diagnostics %v, err %v", len(result.Sources), result.Diagnostics, err)
	}
	if result.Sources[0].Path != root {
		t.Fatalf("origin not first: %+v", result.Sources[0])
	}
	for _, source := range result.Sources {
		if source.InputPath != root || source.Branch != "" {
			t.Fatalf("registered checkout lost trigger directory: %+v", source)
		}
	}
}
