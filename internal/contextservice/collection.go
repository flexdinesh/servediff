package contextservice

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

func (service *Service) Repositories(ctx context.Context) ([]reviewstore.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := service.store.Repositories(service.user.ID)
	for index := range items {
		if items[index].Name == "" {
			items[index].Name = filepath.Base(items[index].Root)
		}
	}
	return items, err
}

// Worktrees discovers only the selected repository; catalog reads never invoke this.
func (service *Service) Worktrees(ctx context.Context, repositoryID string) ([]Context, error) {
	service.catalogMu.Lock()
	defer service.catalogMu.Unlock()
	repository, err := service.store.Repository(service.user.ID, repositoryID)
	if err != nil {
		return nil, requestError(err)
	}
	name, worktrees, err := diffsource.RepositoryWorktrees(ctx, repository.Root)
	if err != nil {
		// A registered checkout may have moved; another retained checkout can provide discovery.
		items, listErr := service.store.RepositoryContexts(service.user.ID, repositoryID)
		if listErr != nil {
			return nil, listErr
		}
		for _, item := range items {
			if item.Root != nil {
				name, worktrees, err = diffsource.RepositoryWorktrees(ctx, *item.Root)
				if err == nil {
					break
				}
			}
		}
	}
	if err != nil {
		return nil, diffsource.Error(503, "source_unavailable: could not discover worktrees: %v", err)
	}
	for _, worktree := range worktrees {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		common, key, err := diffsource.RepositoryIdentity(ctx, worktree.Root)
		if err != nil {
			continue
		}
		owner, err := service.store.RepositoryByCommonDir(service.user.ID, common)
		if err != nil || owner != repositoryID {
			continue
		}
		root, err := filepath.EvalSymlinks(worktree.Root)
		if err != nil {
			continue
		}
		id, err := service.store.DiscoverGit(service.user.ID, root, common, key)
		if err != nil {
			return nil, err
		}
		var worktreeName *string
		if worktree.Linked {
			folder := filepath.Base(root)
			worktreeName = &folder
		}
		if err := service.store.WorktreeMetadata(id, name, worktree.Branch, worktreeName); err != nil {
			return nil, err
		}
	}
	if err := service.store.RepositoryName(repositoryID, name); err != nil {
		return nil, err
	}
	items, err := service.store.RepositoryContexts(service.user.ID, repositoryID)
	if err != nil {
		return nil, err
	}
	// Keep unavailable checkout records and reviews, but don't list removed checkouts.
	roots := make(map[string]bool)
	for _, worktree := range worktrees {
		root, err := filepath.EvalSymlinks(worktree.Root)
		if err == nil {
			roots[root] = true
		}
	}
	result := make([]Context, 0, len(items))
	for _, item := range items {
		if item.Root != nil && roots[*item.Root] {
			result = append(result, service.present(item))
		}
	}
	return result, nil
}

func (service *Service) Refresh(ctx context.Context, id string, mode review.DiffMode) error {
	if _, err := review.ParseDiffMode(string(mode)); err != nil {
		return diffsource.Error(400, "Invalid diff scope")
	}
	item, err := service.store.Context(service.user.ID, id, time.Now())
	if err != nil {
		return requestError(err)
	}
	if item.Kind != "worktree" {
		return nil
	}
	binding, err := service.store.ContextBinding(service.user.ID, id, time.Now())
	if err != nil {
		return requestError(err)
	}
	err = service.collector.Refresh(ctx, binding, item.Generation, mode, func(task context.Context) (diffsource.Source, error) { return service.sourceFor(task, item) })
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		service.setAvailability(id, "unavailable")
	} else if err == nil {
		service.setAvailability(id, "available")
	}
	return err
}
