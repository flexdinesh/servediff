package contextservice

import (
	"context"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
)

type worktreeMetadata struct {
	name         string
	branch       string
	worktreeName *string
}

// Refresh lightweight Git metadata at most once per interval, never diff snapshots.
func (service *Service) refreshCatalog(ctx context.Context, force bool) error {
	service.catalogMu.Lock()
	defer service.catalogMu.Unlock()
	if !force && time.Since(service.catalogUpdated) < 10*time.Second {
		return nil
	}
	items, err := service.store.WorktreeContexts(service.user.ID)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.Root == nil || item.CommonDir == nil || seen[*item.CommonDir] {
			continue
		}
		name, worktrees, err := diffsource.RepositoryWorktrees(ctx, *item.Root)
		if err != nil {
			continue // Another checkout may still be available for this repository.
		}
		for _, worktree := range worktrees {
			commonDir, key, err := diffsource.RepositoryIdentity(ctx, worktree.Root)
			if err != nil || commonDir != *item.CommonDir {
				continue // Missing/prunable checkouts retain their existing reviews.
			}
			seen[*item.CommonDir] = true
			root, err := filepath.EvalSymlinks(worktree.Root)
			if err != nil {
				continue
			}
			id, err := service.store.DiscoverGit(service.user.ID, root, commonDir, key)
			if err != nil {
				return err
			}
			metadata := worktreeMetadata{name: name, branch: worktree.Branch}
			if worktree.Linked {
				folder := filepath.Base(root)
				metadata.worktreeName = &folder
			}
			service.mu.Lock()
			service.metadata[id] = metadata
			service.mu.Unlock()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.catalogUpdated = time.Now()
	return nil
}
