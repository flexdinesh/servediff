package reviewservice

import (
	"context"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/diffsource"
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
	store    CommentStore
}

func NewCatalog(provider ContextProvider, store CommentStore) *Catalog {
	return &Catalog{provider: provider, store: store}
}
func (catalog *Catalog) ListContexts(ctx context.Context, limit int, cursor string, filter ingestion.Filter) (contextservice.Page, error) {
	return catalog.provider.ListFiltered(ctx, limit, cursor, filter)
}
func (catalog *Catalog) Review(ctx context.Context, id string) (*Service, error) {
	if id == "" {
		return nil, diffsource.Error(400, "context_id required; use list_contexts to select a review")
	}
	active, err := catalog.provider.Resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	service := New(active, catalog.store)
	service.strictContext = true
	return service, nil
}
func (catalog *Catalog) GetDiff(ctx context.Context, id string, scope review.DiffMode) (review.RepositoryDiff, error) {
	service, err := catalog.Review(ctx, id)
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	return service.snapshot(ctx, scope, false)
}

func (catalog *Catalog) GetPatch(ctx context.Context, id string, scope review.DiffMode, fileID string) (review.FilePatch, error) {
	service, err := catalog.Review(ctx, id)
	if err != nil {
		return review.FilePatch{}, err
	}
	snapshot, err := service.snapshot(ctx, scope, false)
	if err != nil {
		return review.FilePatch{}, err
	}
	for _, file := range snapshot.Files {
		if file.ID == fileID {
			return service.session.Source.Patch(ctx, scope, file, snapshot.Head)
		}
	}
	return review.FilePatch{}, diffsource.Error(404, "File is not in the stored diff")
}
