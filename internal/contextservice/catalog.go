package contextservice

import (
	"context"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
)

type worktreeMetadata struct {
	name             string
	branch           string
	worktreeName     *string
	lastChangedAt    int64
	changedFileCount *int
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
	refreshed := make(map[string]bool)
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
			changes, changeErr := diffsource.WorktreeChanges(ctx, root, key)
			service.mu.Lock()
			metadata.lastChangedAt = service.metadata[id].lastChangedAt
			if changeErr == nil {
				metadata.lastChangedAt = changes.LastChangedAt
				metadata.changedFileCount = &changes.ChangedFileCount
			}
			service.metadata[id] = metadata
			service.mu.Unlock()
			refreshed[id] = true
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	for _, item := range items {
		if metadata, ok := service.metadata[item.ID]; ok && !refreshed[item.ID] {
			metadata.changedFileCount = nil
			service.metadata[item.ID] = metadata
		}
	}
	service.mu.Unlock()
	service.catalogUpdated = time.Now()
	return nil
}
