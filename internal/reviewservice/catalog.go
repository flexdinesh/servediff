package reviewservice

import (
	"context"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/session"
)

// ContextProvider must resolve IDs within the authenticated user's catalog.
// Resolve validates ownership and expiry before any review storage is accessed.
type ContextProvider interface {
	ListFiltered(context.Context, int, string, ingestion.Filter) (contextservice.Page, error)
	Resolve(context.Context, string) (session.Session, error)
}

type Catalog struct {
	provider ContextProvider
	store    MutationStore
}

func NewCatalog(provider ContextProvider, store MutationStore) *Catalog {
	return &Catalog{provider: provider, store: store}
}
func (catalog *Catalog) ListContexts(ctx context.Context, limit int, cursor string, filter ingestion.Filter) (contextservice.Page, error) {
	return catalog.provider.ListFiltered(ctx, limit, cursor, filter)
}
func (catalog *Catalog) Review(ctx context.Context, id string) (*Service, error) {
	if id == "" {
		return nil, review.Error(400, "context_id required; use list_contexts to select a review")
	}
	active, err := catalog.provider.Resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	service := New(active, catalog.store)
	return service, nil
}
func (catalog *Catalog) GetDiff(ctx context.Context, id string, scope review.DiffMode) (review.RepositoryDiff, error) {
	service, err := catalog.Review(ctx, id)
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	return service.Snapshot(ctx, scope)
}

func (catalog *Catalog) GetPatch(ctx context.Context, id string, scope review.DiffMode, fileID string) (review.FilePatch, error) {
	service, err := catalog.Review(ctx, id)
	if err != nil {
		return review.FilePatch{}, err
	}
	snapshot, err := service.Snapshot(ctx, scope)
	if err != nil {
		return review.FilePatch{}, err
	}
	for _, file := range snapshot.Files {
		if file.ID == fileID {
			return service.session.Source.Patch(ctx, scope, file, snapshot.Head)
		}
	}
	return review.FilePatch{}, review.Error(404, "File is not in the stored diff")
}
