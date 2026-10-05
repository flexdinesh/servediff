package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/flexdinesh/servediff/internal/diffsource"
)

const discoveryLimit = 128

type DiscoverySource struct {
	InputPath string
	Path      string
	Branch    string
	Base      string
	Identity  string
}

type Discovery struct {
	Sources     []DiscoverySource
	Diagnostics []string
}

// SourceIdentity uses persisted Git identities. Paths and branch labels are
// location hints, not the source's identity. Branch identifies object-only
// recovery; an empty branch identifies the live checkout.
func SourceIdentity(ctx context.Context, root, sourceID, branch, base string) (string, error) {
	label := branch
	if label == "" {
		var err error
		label, err = diffsource.CurrentBranch(ctx, root)
		if err != nil {
			return "", err
		}
	}
	identity, err := diffsource.StableIdentity(ctx, root, sourceID, label)
	if err != nil {
		return "", err
	}
	checkout := identity.CheckoutKey
	if branch != "" {
		checkout = diffsource.BranchCheckoutKey(identity.RepositoryKey, identity.BranchID)
	}
	return hash("collector-source-v1", sourceID, identity.RepositoryKey, checkout, identity.BranchID, base), nil
}

// Discover examines only the supplied workspace (two levels, 128 directories),
// then Git's registered worktrees and unmerged local refs. It never fetches,
// switches branches, creates worktrees, or searches unrelated directories.
func Discover(ctx context.Context, input, sourceID, base string) (Discovery, error) {
	var result Discovery
	input, err := filepath.Abs(input)
	if err != nil {
		return result, err
	}
	if base == "" {
		base = "auto"
	}
	roots := make([]string, 0)
	visited := 0
	var scan func(string, int) error
	scan = func(path string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		source, err := diffsource.OpenRepository(ctx, path)
		if err == nil {
			roots = append(roots, source.Root())
			return nil
		}
		if !errors.Is(err, diffsource.ErrNotRepository) {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("inspect %s: %v", path, err))
			return nil
		}
		if depth == 2 {
			return nil
		}
		directory, err := os.Open(path)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("read workspace %s: %v", path, err))
			return nil
		}
		entries, readErr := directory.ReadDir(discoveryLimit + 1)
		_ = directory.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("read workspace %s: %v", path, readErr))
			return nil
		}
		if len(entries) > discoveryLimit {
			entries = entries[:discoveryLimit]
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("workspace %s reached 128-entry limit", path))
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" {
				continue
			}
			if visited == discoveryLimit {
				result.Diagnostics = append(result.Diagnostics, "workspace discovery reached 128-directory limit")
				return nil
			}
			if err := scan(filepath.Join(path, entry.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := scan(input, 0); err != nil {
		return result, err
	}
	seenRepositories, seenSources := make(map[string]bool), make(map[string]string)
	sourceLimitReported := false
	limitReached := func() bool {
		if len(result.Sources) < discoveryLimit {
			return false
		}
		if !sourceLimitReported {
			result.Diagnostics = append(result.Diagnostics, "source discovery reached global 128-source limit; remaining sources require a narrower workspace")
			sourceLimitReported = true
		}
		return true
	}
	add := func(root, branch, comparison string) {
		if limitReached() {
			return
		}
		identity, err := SourceIdentity(ctx, root, sourceID, branch, comparison)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("identify %s branch %q: %v", root, branch, err))
			return
		}
		if previous, exists := seenSources[identity]; exists {
			if previous != root {
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("collector identity copied across %s and %s; duplicate source skipped, use separate source IDs", previous, root))
			}
		} else {
			seenSources[identity] = root
			result.Sources = append(result.Sources, DiscoverySource{InputPath: input, Path: root, Branch: branch, Base: comparison, Identity: identity})
		}
	}
	for _, root := range roots {
		if limitReached() {
			break
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		facts, err := diffsource.Metadata(ctx, root)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("identify repository %s: %v", root, err))
			continue
		}
		// Enumeration deduplicates physical Git stores only. Independent clones
		// can share a repository key while retaining distinct checkout identities.
		if seenRepositories[facts.CommonDir] {
			continue
		}
		seenRepositories[facts.CommonDir] = true
		comparison := base
		if base == "auto" {
			comparison, err = diffsource.DefaultBranch(ctx, root)
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("%s: %v; live checkout uses HEAD, branch recovery skipped", root, err))
				comparison = "HEAD"
			}
		}
		_, worktrees, err := diffsource.RepositoryWorktrees(ctx, root)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("list worktrees %s: %v", root, err))
			continue
		}
		liveBranches := make(map[string]bool)
		for index, worktree := range worktrees {
			if limitReached() {
				break
			}
			if index >= discoveryLimit {
				result.Diagnostics = append(result.Diagnostics, "worktree discovery reached 128-worktree limit")
				break
			}
			if _, err := os.Stat(worktree.Root); err != nil {
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("worktree unavailable %s: %v", worktree.Root, err))
				continue
			}
			liveBranches[worktree.Branch] = true
			add(worktree.Root, "", base)
		}
		if limitReached() {
			break
		}
		if comparison == "HEAD" && base == "auto" {
			continue
		}
		branches, err := diffsource.LocalBranches(ctx, root, comparison)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("recover branches %s: %v", root, err))
			continue
		}
		for _, branch := range branches {
			if limitReached() {
				break
			}
			if !liveBranches[branch] {
				add(root, branch, base)
			}
		}
	}
	if len(result.Sources) == 0 {
		result.Diagnostics = append(result.Diagnostics, "no collectable Git sources found in supplied workspace")
	}
	return result, ctx.Err()
}
