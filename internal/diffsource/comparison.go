package diffsource

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/flexdinesh/servediff/internal/review"
)

// ErrNoDefaultBranch means no existing local default branch can be resolved.
var ErrNoDefaultBranch = errors.New("no local default branch available for comparison")

// ErrComparisonUnavailable permits replaying an already captured comparison
// after its local refs disappear. It never masks process or cancellation errors.
var ErrComparisonUnavailable = errors.New("comparison source unavailable")

type Comparison struct {
	BaseRef     string
	BaseCommit  string
	BaseOID     string
	HeadOID     string
	ObjectsOnly bool
}

type gitComparison struct {
	Comparison
	branch string
	target string
}

// ComparisonInfo reports the captured comparison, including its merge base.
// A false result denotes ordinary checkout collection against HEAD.
func ComparisonInfo(source Source) (Comparison, bool) {
	git, ok := source.(*gitSource)
	if !ok || git.comparison == nil {
		return Comparison{}, false
	}
	return git.comparison.Comparison, true
}

// DefaultBranch resolves existing local refs only. It never fetches or consults
// a feature branch's upstream, which may simply track that same feature branch.
func DefaultBranch(ctx context.Context, root string) (string, error) {
	remote, err := runGit(ctx, root, 16<<10, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil && !comparisonGitExit(err, 1) {
		return "", err
	}
	candidates := []string{"main", "master"}
	if branch, ok := strings.CutPrefix(strings.TrimSpace(remote), "refs/remotes/origin/"); ok {
		candidates = append([]string{branch}, candidates...)
	}
	for _, branch := range candidates {
		if _, err := resolveCommit(ctx, root, "refs/heads/"+branch); err == nil {
			return branch, nil
		} else if !comparisonGitExit(err, 128) {
			return "", err
		}
	}
	return "", ErrNoDefaultBranch
}

// LocalBranches enumerates unmerged local branches with a finite result bound.
func LocalBranches(ctx context.Context, root, base string) ([]string, error) {
	if base == "" || base == "auto" {
		var err error
		base, err = DefaultBranch(ctx, root)
		if err != nil {
			return nil, err
		}
	}
	baseOID, err := resolveCommit(ctx, root, base)
	if err != nil {
		return nil, fmt.Errorf("resolve branch baseline %q: %w", base, err)
	}
	raw, err := runGit(ctx, root, 64<<10, "for-each-ref", "--count=129", "--format=%(refname:strip=2)", "--no-merged="+baseOID, "refs/heads/")
	if err != nil {
		return nil, err
	}
	branches := make([]string, 0)
	for _, branch := range strings.Split(strings.TrimSpace(raw), "\n") {
		if branch != "" {
			branches = append(branches, branch)
		}
	}
	if len(branches) > 128 {
		return nil, errors.New("branch discovery exceeds 128 unmerged local branches; specify a branch")
	}
	return branches, nil
}

func resolveCommit(ctx context.Context, root, ref string) (string, error) {
	raw, err := runGit(ctx, root, 1024, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	return strings.TrimSpace(raw), err
}

// OpenComparison captures a branch comparison without switching or creating a
// checkout. An empty branch reads the current checkout; a named branch reads
// immutable objects only, ignoring the current checkout's index and files.
func OpenComparison(ctx context.Context, directory, base, branch string) (Source, error) {
	opened, err := OpenRepository(ctx, directory)
	if err != nil {
		return nil, err
	}
	source, ok := opened.(*gitSource)
	if !ok {
		return nil, errors.New("Git comparison source unavailable")
	}
	auto := base == "auto" || base == "" && branch != ""
	if (base == "" || base == "HEAD") && branch == "" {
		return source, nil
	}
	if auto {
		base, err = DefaultBranch(ctx, source.root)
		if errors.Is(err, ErrNoDefaultBranch) && branch == "" {
			return source, nil
		}
		if err != nil {
			if errors.Is(err, ErrNoDefaultBranch) {
				return nil, fmt.Errorf("%w: %w", ErrComparisonUnavailable, err)
			}
			return nil, err
		}
		base = "refs/heads/" + base
	}
	target := "HEAD"
	if branch != "" {
		target = "refs/heads/" + branch
	}
	head, err := resolveCommit(ctx, source.root, target)
	if err != nil {
		if auto && branch == "" && comparisonGitExit(err, 128) {
			return source, nil // Unborn checkout: retain normal empty-tree collection.
		}
		return nil, comparisonFailure(fmt.Sprintf("resolve comparison target %q", target), err, 128)
	}
	baseTip, err := resolveCommit(ctx, source.root, base)
	if err != nil {
		return nil, comparisonFailure(fmt.Sprintf("resolve comparison baseline %q", base), err, 128)
	}
	// Store symbolic refs in a single form (main and refs/heads/main agree).
	// Literal commit baselines retain their resolved object ID.
	baseRef, err := runGit(ctx, source.root, 16<<10, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", base)
	if err != nil {
		return nil, comparisonFailure(fmt.Sprintf("resolve comparison reference %q", base), err, 128)
	}
	base = strings.TrimSpace(baseRef)
	if base == "" {
		base = baseTip
	}
	mergeBase, err := runGit(ctx, source.root, 1024, "merge-base", baseTip, head)
	if err != nil {
		if auto && branch == "" && comparisonGitExit(err, 1) {
			return source, nil // Unrelated histories cannot supply a branch baseline.
		}
		return nil, comparisonFailure("find comparison merge base", err, 1)
	}
	if branch == "" {
		label, err := source.branch(ctx, &head)
		if err != nil {
			return nil, err
		}
		branch = label
	}
	source.comparison = &gitComparison{
		Comparison: Comparison{BaseRef: base, BaseCommit: baseTip, BaseOID: strings.TrimSpace(mergeBase), HeadOID: head, ObjectsOnly: target != "HEAD"},
		branch:     branch, target: target,
	}
	return source, nil
}

func comparisonFailure(operation string, err error, unavailableCode int) error {
	if comparisonGitExit(err, unavailableCode) {
		return fmt.Errorf("%s: %w: %w", operation, ErrComparisonUnavailable, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func comparisonGitExit(err error, code int) bool {
	var failure *gitFailure
	var exit *exec.ExitError
	return errors.As(err, &failure) && errors.As(failure.err, &exit) && exit.ExitCode() == code
}

func (source *gitSource) verifyComparison(ctx context.Context) error {
	if source.comparison == nil {
		return nil
	}
	comparison := source.comparison
	for _, reference := range []struct{ ref, oid string }{{comparison.target, comparison.HeadOID}, {comparison.BaseRef, comparison.BaseCommit}} {
		current, err := resolveCommit(ctx, source.root, reference.ref)
		if err != nil {
			var failure *gitFailure
			if !errors.As(err, &failure) {
				return err
			}
		}
		if current != reference.oid {
			return Error(409, "Comparison ref changed during collection: %s", reference.ref)
		}
	}
	if !comparison.ObjectsOnly {
		branch, err := source.branch(ctx, &comparison.HeadOID)
		if err != nil {
			return err
		}
		if branch != comparison.branch {
			return Error(409, "Checkout branch changed during collection")
		}
	}
	return nil
}

func (source *gitSource) comparisonBase(mode review.DiffMode, headBase string) string {
	if mode == review.DiffAll && source.comparison != nil {
		return source.comparison.BaseOID
	}
	return headBase
}

func (source *gitSource) diffArgs(mode review.DiffMode, headBase string) []string {
	args := gitDiffArgs(mode, source.comparisonBase(mode, headBase))
	if source.objectsOnly() {
		args = append(args, source.comparison.HeadOID)
	}
	return args
}

func (source *gitSource) objectsOnly() bool {
	return source.comparison != nil && source.comparison.ObjectsOnly
}

func (source *gitSource) objectSubmodule(ctx context.Context, file review.ChangedFile) (bool, error) {
	previous := file.Path
	if file.OldPath != nil {
		previous = *file.OldPath
	}
	for _, object := range []struct{ revision, path string }{{source.comparison.BaseOID, previous}, {source.comparison.HeadOID, file.Path}} {
		mode, err := runGit(ctx, source.root, 1024, "ls-tree", "--format=%(objectmode)", object.revision, "--", object.path)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(mode) == "160000" {
			return true, nil
		}
	}
	return false, nil
}
