package collector

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, raw)
	}
	return strings.TrimSpace(string(raw))
}

func repository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "--initial-branch=main")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	return root
}

func write(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, root string) ingestion.Request {
	t.Helper()
	request, err := Collect(context.Background(), root, Options{SourceID: "machine", Hostname: "host", Trigger: "agent-hook", Agent: "agent", RunID: "run"})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func patchFor(t *testing.T, request ingestion.Request, mode review.DiffMode, name string) review.FilePatch {
	t.Helper()
	for _, scope := range request.Scopes {
		if scope.Snapshot.Mode != mode {
			continue
		}
		for _, file := range scope.Snapshot.Files {
			if file.Path == name {
				patch, ok := scope.Patches[file.ID]
				if !ok {
					t.Fatalf("missing preview for %s", file.ID)
				}
				return patch
			}
		}
	}
	t.Fatalf("missing %s file %s", mode, name)
	return review.FilePatch{}
}

func TestCollectGitScopesAndFullContents(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "remote", "add", "origin", "https://username:secret@example.com/org/project.git?token=secret")
	write(t, root, "tracked", "staged\n")
	git(t, root, "add", "tracked")
	write(t, root, "tracked", "working\n")
	write(t, root, "untracked", "new\n")
	request := collect(t, root)
	if len(request.Scopes) != 3 {
		t.Fatalf("scopes: %d", len(request.Scopes))
	}
	if request.Metadata.Branch != "main" || request.Metadata.RepositoryName != "project" || request.Metadata.RemoteURL != "https://example.com/org/project.git" || request.Metadata.Head == nil || request.Metadata.CollectedAt <= 0 {
		t.Fatalf("metadata: %+v", request.Metadata)
	}
	if request.Metadata.Trigger != "agent-hook" || request.Metadata.Agent != "agent" || request.Metadata.RunID != "run" {
		t.Fatalf("producer metadata: %+v", request.Metadata)
	}
	for _, expectation := range []struct {
		mode          review.DiffMode
		before, after string
	}{
		{review.DiffAll, "original\n", "working\n"},
		{review.DiffStaged, "original\n", "staged\n"},
		{review.DiffUnstaged, "staged\n", "working\n"},
	} {
		patch := patchFor(t, request, expectation.mode, "tracked")
		if patch.Contents == nil || patch.Contents.Before != expectation.before || patch.Contents.After != expectation.after {
			t.Fatalf("%s contents: %+v", expectation.mode, patch.Contents)
		}
	}
	patch := patchFor(t, request, review.DiffAll, "untracked")
	if patch.Contents == nil || patch.Contents.Before != "" || patch.Contents.After != "new\n" {
		t.Fatalf("untracked contents: %+v", patch.Contents)
	}
}

func TestLinkedWorktreeAndSourceIdentity(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "feature", linked)
	main := collect(t, root)
	feature := collect(t, linked)
	if main.Metadata.RepositoryKey != feature.Metadata.RepositoryKey || main.Metadata.CheckoutKey == feature.Metadata.CheckoutKey || feature.Metadata.Branch != "feature" || feature.Metadata.WorktreeName != "linked" {
		t.Fatalf("main %+v linked %+v", main.Metadata, feature.Metadata)
	}
	otherSource, err := Collect(context.Background(), root, Options{SourceID: "container-2", Hostname: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if otherSource.Metadata.CheckoutKey == main.Metadata.CheckoutKey {
		t.Fatal("distinct source reused checkout identity")
	}
}

func TestDetachedUnbornAndEmptySnapshots(t *testing.T) {
	root := repository(t)
	unborn := collect(t, root)
	if unborn.Metadata.Head != nil || unborn.Metadata.Branch != "main" {
		t.Fatalf("unborn metadata: %+v", unborn.Metadata)
	}
	for _, scope := range unborn.Scopes {
		if scope.Snapshot.Files == nil || len(scope.Snapshot.Files) != 0 || len(scope.Patches) != 0 {
			t.Fatalf("empty scope: %+v", scope)
		}
	}
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "checkout", "--detach")
	detached := collect(t, root)
	if detached.Metadata.Head == nil || !strings.HasPrefix(detached.Metadata.Branch, "detached at ") {
		t.Fatalf("detached metadata: %+v", detached.Metadata)
	}
}

func TestPipeAttachesGitContextWithoutCollectingCurrentContents(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	write(t, root, "tracked", "captured\n")
	raw := git(t, root, "diff") + "\n"
	write(t, root, "tracked", "later\n")
	request, err := CollectPatch(context.Background(), raw, root, Options{SourceID: "source", Hostname: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.Branch != "main" || request.Metadata.RepositoryKey == "" || request.Metadata.CheckoutKey == "" || request.Metadata.Trigger != "pipe" {
		t.Fatalf("pipe metadata: %+v", request.Metadata)
	}
	patch := patchFor(t, request, review.DiffAll, "tracked")
	if !strings.Contains(patch.Patch, "+captured") || strings.Contains(patch.Patch, "+later") || patch.Contents != nil || patch.Message == nil {
		t.Fatalf("pipe preview: %+v", patch)
	}
	standalone, err := CollectPatch(context.Background(), raw, t.TempDir(), Options{SourceID: "source", Hostname: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if standalone.Metadata.RepositoryKey != "" || standalone.Metadata.CheckoutKey != "" || standalone.Scopes[0].Snapshot.Source != "stdin" {
		t.Fatalf("standalone: %+v", standalone.Metadata)
	}
}

func TestBinaryAndOversizeContentsRemainExplicit(t *testing.T) {
	root := repository(t)
	write(t, root, "binary", "binary\x00contents")
	write(t, root, "large", strings.Repeat("a", diffsource.MaxFileBytes+1))
	request := collect(t, root)
	for _, name := range []string{"binary", "large"} {
		patch := patchFor(t, request, review.DiffAll, name)
		if patch.Contents != nil || patch.Message == nil {
			t.Fatalf("%s missing explicit unavailable contents: %+v", name, patch)
		}
	}
}

func TestPreviewBudgetPreservesPatchWhenContentsDoNotFit(t *testing.T) {
	patch := review.FilePatch{Patch: "patch", Contents: &review.FileContents{Before: "old contents", After: "new contents"}}
	remaining := 8
	applyBudget(&patch, &remaining)
	if patch.Patch != "patch" || patch.Contents != nil || patch.Message == nil || remaining != 3 {
		t.Fatalf("budget fallback: %+v remaining %d", patch, remaining)
	}
	applyBudget(&patch, &remaining)
	if patch.Patch != "" || patch.Message == nil || remaining != 3 {
		t.Fatalf("budget unavailable: %+v remaining %d", patch, remaining)
	}
}

type changingSource struct {
	diffsource.Source
	calls int
}

func (source *changingSource) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	snapshot, err := source.Source.Snapshot(ctx, mode)
	source.calls++
	if source.calls > 3 {
		snapshot.Revision = "changed"
	}
	return snapshot, err
}

func TestCollectionRejectsInconsistentObservation(t *testing.T) {
	root := repository(t)
	source, err := diffsource.OpenRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = collectRepository(context.Background(), &changingSource{Source: source}, Options{SourceID: "source", Hostname: "host"})
	if !errors.Is(err, errChanged) {
		t.Fatalf("inconsistent snapshot accepted: %v", err)
	}
}

func TestCancellationStopsBothCollectionEntrypoints(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Collect(ctx, repository(t), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Git cancellation: %v", err)
	}
	if _, err := CollectPatch(ctx, "", "", Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pipe cancellation: %v", err)
	}
}

func TestSourceIdentityPersistsAcrossConcurrentInvocations(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SERVEDIFF_SOURCE_ID", "")
	var group sync.WaitGroup
	ids := make(chan string, 8)
	errors := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			id, err := SourceID()
			ids <- id
			errors <- err
		})
	}
	group.Wait()
	close(ids)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected string
	for id := range ids {
		if expected == "" {
			expected = id
		}
		if id == "" || id != expected {
			t.Fatalf("inconsistent IDs: %q and %q", id, expected)
		}
	}
	t.Setenv("SERVEDIFF_SOURCE_ID", "explicit-container")
	id, err := SourceID()
	if err != nil || id != "explicit-container" {
		t.Fatalf("explicit ID: %q %v", id, err)
	}
}

func TestContentIdentityIgnoresRewritesButTracksStaging(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	write(t, root, "tracked", "working\n")
	first := collect(t, root)
	write(t, root, "tracked", "working\n")
	if err := os.Chtimes(filepath.Join(root, "tracked"), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	rewritten := collect(t, root)
	if first.ContentHash == "" || rewritten.ContentHash != first.ContentHash {
		t.Fatalf("identical rewrite changed content identity: %s -> %s", first.ContentHash, rewritten.ContentHash)
	}
	git(t, root, "add", "tracked")
	staged := collect(t, root)
	if staged.ContentHash == rewritten.ContentHash {
		t.Fatal("staging did not change content identity")
	}
	// The combined diff is unchanged, but staged contents also matter.
	write(t, root, "tracked", "original\n")
	stagedWithCleanCombinedDiff := collect(t, root)
	git(t, root, "reset", "--", "tracked")
	clean := collect(t, root)
	if stagedWithCleanCombinedDiff.ContentHash == clean.ContentHash {
		t.Fatal("nonempty staged/unstaged scopes collided with clean checkout")
	}
}

func TestContentIdentityIncludesUnpreviewableBytes(t *testing.T) {
	for _, test := range []struct {
		name   string
		before string
		after  string
	}{
		{"binary", "before\x00binary", "after!\x00binary"},
		{"file limit", strings.Repeat("a", diffsource.MaxFileBytes+1), strings.Repeat("a", diffsource.MaxFileBytes) + "b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := repository(t)
			write(t, root, "untracked", test.before)
			first := collect(t, root)
			write(t, root, "untracked", test.after)
			second := collect(t, root)
			if first.ContentHash == "" || second.ContentHash == "" || first.ContentHash == second.ContentHash {
				t.Fatal("different full contents shared content identity")
			}
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "initial")
			write(t, root, "untracked", test.before)
			third := collect(t, root)
			write(t, root, "untracked", test.after)
			fourth := collect(t, root)
			if third.ContentHash == "" || fourth.ContentHash == "" || third.ContentHash == fourth.ContentHash {
				t.Fatal("tracked bytes shared content identity")
			}
		})
	}
}

func TestContentIdentityIncludesFilesAfterAggregatePreviewBudget(t *testing.T) {
	root := repository(t)
	value := strings.Repeat("a", 3<<19)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "z-last"} {
		write(t, root, name, value)
	}
	first := collect(t, root)
	preview := patchFor(t, first, review.DiffAll, "z-last")
	if preview.Patch != "" || preview.Contents != nil || preview.Message == nil {
		t.Fatalf("test must exhaust aggregate budget before last file: %+v", preview)
	}
	write(t, root, "z-last", value[:len(value)-1]+"b")
	second := collect(t, root)
	if first.ContentHash == "" || second.ContentHash == first.ContentHash {
		t.Fatal("file omitted from previews also omitted from content identity")
	}
}

func TestContentIdentityIncludesConflictStages(t *testing.T) {
	root := repository(t)
	var commits []string
	for _, value := range []string{"base\n", "ours-one\n", "ours-two\n", "theirs\n"} {
		write(t, root, "tracked", value)
		git(t, root, "add", ".")
		git(t, root, "commit", "-m", "change")
		commits = append(commits, git(t, root, "rev-parse", "HEAD"))
	}
	stages := func(ours string) {
		t.Helper()
		command := exec.Command("git", "update-index", "--index-info")
		command.Dir = root
		command.Stdin = strings.NewReader("0 " + strings.Repeat("0", 40) + "\ttracked\n" +
			"100644 " + git(t, root, "rev-parse", commits[0]+":tracked") + " 1\ttracked\n" +
			"100644 " + git(t, root, "rev-parse", ours+":tracked") + " 2\ttracked\n" +
			"100644 " + git(t, root, "rev-parse", commits[3]+":tracked") + " 3\ttracked\n")
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("set conflict stages: %v: %s", err, raw)
		}
	}
	stages(commits[1])
	first := collect(t, root)
	stages(commits[2])
	second := collect(t, root)
	if first.ContentHash == "" || second.ContentHash == "" || first.ContentHash == second.ContentHash {
		t.Fatal("different conflict index stages shared content identity")
	}
}

func TestContentIdentityForEmptyUnbornAndCleanCheckouts(t *testing.T) {
	root := repository(t)
	unborn := collect(t, root)
	if unborn.ContentHash == "" || collect(t, root).ContentHash != unborn.ContentHash {
		t.Fatal("unborn checkout lacks stable content identity")
	}
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	clean := collect(t, root)
	if clean.ContentHash == "" || collect(t, root).ContentHash != clean.ContentHash {
		t.Fatal("clean checkout lacks stable content identity")
	}
}

func TestPipeContentIdentityUsesInput(t *testing.T) {
	raw := "diff --git a/file b/file\n--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new\n"
	options := Options{SourceID: "source", Hostname: "host"}
	first, err := CollectPatch(t.Context(), raw, "", options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CollectPatch(t.Context(), raw, "", options)
	if err != nil {
		t.Fatal(err)
	}
	third, err := CollectPatch(t.Context(), strings.ReplaceAll(raw, "+new", "+different"), "", options)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash == "" || first.ContentHash != second.ContentHash || first.ContentHash == third.ContentHash {
		t.Fatal("piped identity does not reflect exact immutable input")
	}
}

func TestContentIdentityIncludesModesRenamesAndSymlinks(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	write(t, root, "tracked", "working\n")
	first := collect(t, root)
	if err := os.Chmod(filepath.Join(root, "tracked"), 0o700); err != nil {
		t.Fatal(err)
	}
	mode := collect(t, root)
	if mode.ContentHash == "" || mode.ContentHash == first.ContentHash {
		t.Fatal("file mode missing from content identity")
	}
	git(t, root, "add", ".")
	staged := collect(t, root)
	git(t, root, "mv", "tracked", "renamed")
	renamed := collect(t, root)
	if renamed.ContentHash == "" || renamed.ContentHash == staged.ContentHash {
		t.Fatal("rename missing from content identity")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("target-one", link); err != nil {
		t.Fatal(err)
	}
	linked := collect(t, root)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target-two", link); err != nil {
		t.Fatal(err)
	}
	if other := collect(t, root); linked.ContentHash == "" || other.ContentHash == linked.ContentHash {
		t.Fatal("symlink target missing from content identity")
	}
}

func TestChangedSubmoduleRemainsReviewableWithoutContentIdentity(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",module")
	if err := os.Mkdir(filepath.Join(root, "module"), 0o700); err != nil {
		t.Fatal(err)
	}
	request := collect(t, root)
	if request.ContentHash != "" {
		t.Fatal("submodule contents incorrectly treated as complete")
	}
	if err := ingestion.Validate(request); err != nil {
		t.Fatalf("unsupported content identity prevented valid review: %v", err)
	}
}
