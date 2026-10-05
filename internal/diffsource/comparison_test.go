package diffsource

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/review"
)

func comparisonRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "init", "--initial-branch=main")
	testGit(t, root, "config", "user.name", "Test")
	testGit(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "initial")
	return root
}

func TestDefaultBranchUsesLocalRemoteHeadCounterpartThenConventions(t *testing.T) {
	root := comparisonRepository(t)
	if name, err := DefaultBranch(t.Context(), root); err != nil || name != "main" {
		t.Fatalf("main fallback: %q %v", name, err)
	}
	testGit(t, root, "branch", "trunk")
	testGit(t, root, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	testGit(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if name, err := DefaultBranch(t.Context(), root); err != nil || name != "trunk" {
		t.Fatalf("remote HEAD local counterpart: %q %v", name, err)
	}
	testGit(t, root, "branch", "-D", "trunk")
	if name, err := DefaultBranch(t.Context(), root); err != nil || name != "main" {
		t.Fatalf("missing local counterpart: %q %v", name, err)
	}
	testGit(t, root, "branch", "-m", "main", "master")
	if name, err := DefaultBranch(t.Context(), root); err != nil || name != "master" {
		t.Fatalf("master fallback: %q %v", name, err)
	}
	testGit(t, root, "branch", "-m", "master", "custom")
	if _, err := DefaultBranch(t.Context(), root); !errors.Is(err, ErrNoDefaultBranch) {
		t.Fatalf("missing default: %v", err)
	}
}

func TestComparisonFallbackDoesNotHideExplicitFailures(t *testing.T) {
	root := comparisonRepository(t)
	testGit(t, root, "branch", "-m", "main", "custom")
	source, err := OpenComparison(t.Context(), root, "auto", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, comparison := ComparisonInfo(source); comparison {
		t.Fatal("missing automatic baseline did not fall back to checkout")
	}
	if _, err := OpenComparison(t.Context(), root, "missing", ""); err == nil {
		t.Fatal("explicit absent base silently fell back")
	}
	if _, err := OpenComparison(t.Context(), root, "auto", "custom"); !errors.Is(err, ErrNoDefaultBranch) {
		t.Fatalf("object recovery fell back to unrelated checkout: %v", err)
	}
	if _, err := OpenComparison(t.Context(), root, "custom", "missing"); err == nil {
		t.Fatal("missing recovery branch accepted")
	}
	unborn := t.TempDir()
	testGit(t, unborn, "init", "--initial-branch=main")
	source, err = OpenComparison(t.Context(), unborn, "auto", "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err := source.Snapshot(t.Context(), review.DiffAll); err != nil || snapshot.Head != nil || len(snapshot.Files) != 0 {
		t.Fatalf("unborn automatic fallback: %+v %v", snapshot, err)
	}
}

func TestComparisonDetectsMovedRefsAndSameHeadBranchSwitch(t *testing.T) {
	for _, mutate := range []string{"target", "base", "branch"} {
		t.Run(mutate, func(t *testing.T) {
			root := comparisonRepository(t)
			main := strings.TrimSpace(testGit(t, root, "rev-parse", "HEAD"))
			testGit(t, root, "switch", "-c", "feature")
			testGit(t, root, "commit", "--allow-empty", "-m", "new head")
			source, err := OpenComparison(t.Context(), root, "main", "")
			if err != nil {
				t.Fatal(err)
			}
			switch mutate {
			case "target":
				testGit(t, root, "update-ref", "refs/heads/feature", main)
			case "base":
				testGit(t, root, "update-ref", "refs/heads/main", "HEAD")
			case "branch":
				testGit(t, root, "switch", "-c", "other")
			}
			_, err = source.Snapshot(t.Context(), review.DiffAll)
			var problem *RequestError
			if !errors.As(err, &problem) || problem.Status != 409 {
				t.Fatalf("moved comparison accepted: %v", err)
			}
		})
	}
}

func TestLocalBranchesEnumeratesUnmergedRefsAndRejectsOverflow(t *testing.T) {
	root := comparisonRepository(t)
	testGit(t, root, "branch", "merged")
	testGit(t, root, "switch", "-c", "feature")
	testGit(t, root, "commit", "--allow-empty", "-m", "unmerged")
	branches, err := LocalBranches(t.Context(), root, "auto")
	if err != nil || len(branches) != 1 || branches[0] != "feature" {
		t.Fatalf("unmerged local branches: %v %v", branches, err)
	}
	head := strings.TrimSpace(testGit(t, root, "rev-parse", "HEAD"))
	var updates strings.Builder
	for index := 0; index < 128; index++ {
		updates.WriteString("create refs/heads/overflow-" + strconv.Itoa(index) + " " + head + "\n")
	}
	command := exec.Command("git", "update-ref", "--stdin")
	command.Dir = root
	command.Stdin = strings.NewReader(updates.String())
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create overflow branches: %s %v", raw, err)
	}
	if _, err := LocalBranches(t.Context(), root, "main"); err == nil || !strings.Contains(err.Error(), "128") {
		t.Fatalf("branch discovery silently truncated: %v", err)
	}
}

func TestComparisonUnavailableAfterCapturedRefsDisappear(t *testing.T) {
	for _, removed := range []string{"base", "target", "default"} {
		t.Run(removed, func(t *testing.T) {
			root := comparisonRepository(t)
			testGit(t, root, "branch", "feature")
			base := "main"
			if removed == "default" {
				base = "auto"
			}
			source, err := OpenComparison(t.Context(), root, base, "feature")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Snapshot(t.Context(), review.DiffAll); err != nil {
				t.Fatal(err)
			}
			if removed == "target" {
				testGit(t, root, "branch", "-D", "feature")
			} else {
				testGit(t, root, "switch", "feature")
				testGit(t, root, "branch", "-D", "main")
			}
			_, err = OpenComparison(t.Context(), root, base, "feature")
			if !errors.Is(err, ErrComparisonUnavailable) {
				t.Fatalf("missing %s ref cannot replay saved payload: %v", removed, err)
			}
		})
	}
}

func TestComparisonProcessFailuresAreNotUnavailable(t *testing.T) {
	root := comparisonRepository(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenComparison(ctx, root, "main", "main"); !errors.Is(err, context.Canceled) || errors.Is(err, ErrComparisonUnavailable) {
		t.Fatalf("cancellation became unavailable ref: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Git wrapper requires a POSIX shell")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case " $* " in
  *" rev-parse --verify "*) exit 7 ;;
esac
exec "$SERVEDIFF_TEST_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, base := range []string{"main", "auto"} {
		if _, err := OpenComparison(t.Context(), root, base, "main"); err == nil || errors.Is(err, ErrComparisonUnavailable) {
			t.Fatalf("unexpected Git exit became unavailable ref for %s: %v", base, err)
		}
	}
}
