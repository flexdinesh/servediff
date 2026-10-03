package contextservice

import (
	"context"
	"errors"
	"strings"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

type ChangeInput struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Detail string `json:"detail"`
}
type ChangeEvent struct {
	Kind         string  `json:"kind"`
	ContextID    string  `json:"contextId"`
	RepositoryID *string `json:"repositoryId"`
	Branch       string  `json:"branch,omitempty"`
	Detail       string  `json:"detail,omitempty"`
	Generation   int64   `json:"generation"`
}

// Change is a targeted invalidation. It never reads diffs or registers a repository.
func (service *Service) Change(ctx context.Context, input ChangeInput) (ChangeEvent, error) {
	if strings.TrimSpace(input.Path) == "" || len(input.Branch) > 1024 || len(input.Detail) > 4096 {
		return ChangeEvent{}, diffsource.Error(400, "A path and bounded branch/detail are required")
	}
	source, err := diffsource.OpenRepository(ctx, input.Path)
	if err != nil {
		return ChangeEvent{}, diffsource.Error(400, "Could not identify worktree: %v", err)
	}
	common, key, err := diffsource.RepositoryIdentity(ctx, source.Root())
	if err != nil {
		return ChangeEvent{}, err
	}
	repo, err := service.store.RepositoryByCommonDir(service.user.ID, common)
	if errors.Is(err, reviewstore.ErrNotFound) {
		return ChangeEvent{}, diffsource.Error(404, "Repository is not registered; run servediff --path PATH first")
	}
	if err != nil {
		return ChangeEvent{}, err
	}
	id, err := service.store.DiscoverGit(service.user.ID, source.Root(), common, key)
	if err != nil {
		return ChangeEvent{}, err
	}
	generation, err := service.store.MarkChanged(id)
	if err != nil {
		return ChangeEvent{}, err
	}
	event := ChangeEvent{Kind: "change", ContextID: id, RepositoryID: &repo, Branch: input.Branch, Detail: input.Detail, Generation: generation}
	service.Notify(event)
	return event, nil
}

func (service *Service) Subscribe() (<-chan ChangeEvent, func()) {
	events := make(chan ChangeEvent, 32)
	service.mu.Lock()
	service.subscribers[events] = struct{}{}
	service.mu.Unlock()
	return events, func() {
		service.mu.Lock()
		defer service.mu.Unlock()
		if _, ok := service.subscribers[events]; ok {
			delete(service.subscribers, events)
			close(events)
		}
	}
}
func (service *Service) Notify(event ChangeEvent) {
	service.mu.Lock()
	defer service.mu.Unlock()
	for subscriber := range service.subscribers {
		select {
		case subscriber <- event:
		default:
			// A lagging client reconnects and refreshes its selected target.
			delete(service.subscribers, subscriber)
			close(subscriber)
		}
	}
}
