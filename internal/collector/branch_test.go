package collector

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

func branchOptions() Options { return Options{SourceID: "machine", Hostname: "host", Base: "auto"} }

func branchRepository(t *testing.T) string {
	t.Helper()
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "switch", "-c", "feature")
	write(t, root, "tracked", "committed\n")
	git(t, root, "commit", "-am", "feature")
	return root
}

func TestBranchComparisonIncludesCommitsAndPreservesStagingScopes(t *testing.T) {
	root := branchRepository(t)
	write(t, root, "tracked", "staged\n")
	git(t, root, "add", "tracked")
	write(t, root, "tracked", "working\n")
	write(t, root, "untracked", "new\n")
	request, err := Collect(t.Context(), root, branchOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Scopes) != 3 || request.Metadata.Branch != "feature" {
		t.Fatalf("branch collection: %+v", request)
	}
	for _, expectation := range []struct {
		mode          review.DiffMode
		before, after string
	}{
		{review.DiffAll, "original\n", "working\n"},
		{review.DiffStaged, "committed\n", "staged\n"},
		{review.DiffUnstaged, "staged\n", "working\n"},
	} {
		patch := patchFor(t, request, expectation.mode, "tracked")
		if patch.Contents == nil || patch.Contents.Before != expectation.before || patch.Contents.After != expectation.after {
			t.Fatalf("%s contents: %+v", expectation.mode, patch)
		}
	}
	if patch := patchFor(t, request, review.DiffAll, "untracked"); patch.Contents == nil || patch.Contents.After != "new\n" {
		t.Fatalf("untracked: %+v", patch)
	}
	if err := ingestion.Validate(request); err != nil {
		t.Fatal(err)
	}
}

func TestRemovedWorktreeBranchRecoversOnlyCommittedObjects(t *testing.T) {
	root := branchRepository(t)
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "switch", "main")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", linked, "feature")
	git(t, root, "worktree", "remove", linked)
	write(t, root, "tracked", "unrelated staged main\n")
	git(t, root, "add", "tracked")
	write(t, root, "tracked", "unrelated dirty main\n")
	write(t, root, "untracked-main", "unrelated\n")
	options := branchOptions()
	options.Branch = "feature"
	request, fingerprint, err := CollectChanged(t.Context(), root, options, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Scopes) != 1 || len(request.Scopes[0].Snapshot.Files) != 1 || request.Metadata.Branch != "feature" || request.Metadata.Head == nil || *request.Metadata.Head != head || request.Metadata.LinkedWorktree != nil {
		t.Fatalf("recovery provenance: %+v", request)
	}
	patch := patchFor(t, request, review.DiffAll, "tracked")
	if patch.Contents == nil || patch.Contents.Before != "original\n" || patch.Contents.After != "committed\n" || strings.Contains(patch.Patch, "unrelated") {
		t.Fatalf("recovered contents: %+v", patch)
	}
	write(t, root, "tracked", "later unrelated main\n")
	_, same, err := CollectChanged(t.Context(), root, options, fingerprint)
	if !errors.Is(err, ErrUnchanged) || same != fingerprint {
		t.Fatalf("current checkout leaked into branch identity: %q, %v", same, err)
	}
	live := collect(t, root)
	if live.Metadata.CheckoutKey == request.Metadata.CheckoutKey || live.Metadata.BranchID == request.Metadata.BranchID {
		t.Fatal("branch recovery reused unrelated checkout identity")
	}
	if err := ingestion.Validate(request); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultCollectionUsesMergeBaseAndExplicitHeadKeepsWorkingChanges(t *testing.T) {
	root := branchRepository(t)
	mergeBase := git(t, root, "rev-parse", "main")
	git(t, root, "switch", "main")
	write(t, root, "main-only", "main change\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "diverged main")
	baseTip := git(t, root, "rev-parse", "main")
	git(t, root, "switch", "feature")
	request := collect(t, root)
	if comparison := request.Metadata.Comparison; comparison == nil || comparison.Kind != "branch" || comparison.BaseRef != "refs/heads/main" || comparison.BaseCommit != baseTip || comparison.MergeBase != mergeBase {
		t.Fatalf("comparison lost resolved baseline: %+v", comparison)
	}
	if len(request.Scopes[0].Snapshot.Files) != 1 {
		t.Fatalf("default branch changes leaked into feature: %+v", request.Scopes[0].Snapshot.Files)
	}
	patchFor(t, request, review.DiffAll, "tracked")
	options := branchOptions()
	options.Base = "HEAD"
	working, err := Collect(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if comparison := working.Metadata.Comparison; comparison == nil || comparison.Kind != "working-tree" || comparison.BaseRef != "HEAD" || comparison.BaseCommit != *working.Metadata.Head || comparison.MergeBase != *working.Metadata.Head || len(working.Scopes[0].Snapshot.Files) != 0 {
		t.Fatalf("explicit HEAD included branch changes: %+v", working)
	}
	if Fingerprint(working) == Fingerprint(request) {
		t.Fatal("different comparison policies shared identity")
	}
}

func TestComparisonRecreatedUntrackedFileUsesBranchBase(t *testing.T) {
	root := branchRepository(t)
	git(t, root, "rm", "tracked")
	write(t, root, "tracked", "recreated\n")
	request, err := Collect(t.Context(), root, branchOptions())
	if err != nil {
		t.Fatal(err)
	}
	patch := patchFor(t, request, review.DiffAll, "tracked")
	if patch.Contents == nil || patch.Contents.Before != "original\n" || patch.Contents.After != "recreated\n" {
		t.Fatalf("recreated branch before: %+v", patch)
	}
	patch = patchFor(t, request, review.DiffStaged, "tracked")
	if patch.Contents == nil || patch.Contents.Before != "committed\n" || patch.Contents.After != "" {
		t.Fatalf("staged deletion before: %+v", patch)
	}
}

func TestObjectComparisonHashesUnavailablePreviewsModesAndRenames(t *testing.T) {
	root := branchRepository(t)
	git(t, root, "mv", "tracked", "renamed")
	write(t, root, "renamed", "original\n")
	write(t, root, "binary", "binary\x00one")
	write(t, root, "large", strings.Repeat("a", diffsource.MaxFileBytes+1))
	if err := os.Chmod(filepath.Join(root, "renamed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target-one", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "nontext and metadata")
	options := branchOptions()
	options.Branch = "feature"
	first, fingerprint, err := CollectChanged(t.Context(), root, options, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"binary", "large"} {
		patch := patchFor(t, first, review.DiffAll, name)
		if patch.Contents != nil || patch.Message == nil {
			t.Fatalf("%s unavailable preview: %+v", name, patch)
		}
	}
	if patch := patchFor(t, first, review.DiffAll, "link"); patch.Contents == nil || patch.Contents.After != "target-one" {
		t.Fatalf("object symlink: %+v", patch)
	}
	renamed := false
	for _, file := range first.Scopes[0].Snapshot.Files {
		if file.Path == "renamed" && file.Status == "R" && file.OldPath != nil && *file.OldPath == "tracked" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("committed rename lost: %+v", first.Scopes[0].Snapshot.Files)
	}
	write(t, root, "binary", "binary\x00two")
	write(t, root, "large", strings.Repeat("b", diffsource.MaxFileBytes+1))
	git(t, root, "commit", "-am", "new hidden contents")
	second, next, err := CollectChanged(t.Context(), root, options, fingerprint)
	if err != nil || next == fingerprint || first.ContentHash == second.ContentHash || next == "" {
		t.Fatalf("object identity ignored unpreviewable changes: %s %s %v", fingerprint, next, err)
	}
}

func TestBranchCollectionRetriesWithFreshHeadAfterRefMoves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git wrapper requires a POSIX shell")
	}
	root := branchRepository(t)
	first := git(t, root, "rev-parse", "HEAD")
	git(t, root, "commit", "--allow-empty", "-m", "next head")
	next := git(t, root, "rev-parse", "HEAD")
	git(t, root, "reset", "--hard", first)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case " $* " in
  *" --numstat "*)
    if [ ! -f "$SERVEDIFF_TEST_MOVED" ]; then
      "$SERVEDIFF_TEST_REAL_GIT" update-ref refs/heads/feature "$SERVEDIFF_TEST_NEXT_HEAD" || exit 9
      printf 'moved\n' > "$SERVEDIFF_TEST_MOVED"
    fi
    ;;
esac
exec "$SERVEDIFF_TEST_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_TEST_REAL_GIT", realGit)
	t.Setenv("SERVEDIFF_TEST_MOVED", filepath.Join(bin, "moved"))
	t.Setenv("SERVEDIFF_TEST_NEXT_HEAD", next)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	request, err := Collect(t.Context(), root, branchOptions())
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.Head == nil || *request.Metadata.Head != next {
		t.Fatalf("retry retained stale target: %+v", request.Metadata)
	}
	patch := patchFor(t, request, review.DiffAll, "tracked")
	if patch.Contents == nil || patch.Contents.Before != "original\n" || patch.Contents.After != "committed\n" {
		t.Fatalf("ref retry lost branch contents: %+v", patch)
	}
}

func TestStableFingerprintSurvivesCheckoutMoveAndBranchRename(t *testing.T) {
	root := branchRepository(t)
	options := branchOptions()
	request, fingerprint, err := CollectChanged(t.Context(), root, options, "")
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "branch", "-m", "feature", "renamed-feature")
	moved := filepath.Join(t.TempDir(), "moved-checkout")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	_, same, err := CollectChanged(t.Context(), moved, options, fingerprint)
	if !errors.Is(err, ErrUnchanged) || same != fingerprint {
		t.Fatalf("labels changed stable fingerprint: %s %v", same, err)
	}
	updated, err := Collect(t.Context(), moved, options)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Metadata.Branch != "renamed-feature" || updated.Metadata.Root != moved || updated.Metadata.BranchID != request.Metadata.BranchID || updated.Metadata.CheckoutKey != request.Metadata.CheckoutKey || updated.ContentHash != request.ContentHash {
		t.Fatalf("moved provenance: before %+v after %+v", request.Metadata, updated.Metadata)
	}
}
