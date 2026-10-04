package contextservice

import (
	"context"
	"sync"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
)

func (service *Service) Ingest(ctx context.Context, input ingestion.Request) (Submission, error) {
	if err := ctx.Err(); err != nil {
		return Submission{}, err
	}
	if err := ingestion.Validate(input); err != nil {
		return Submission{}, diffsource.Error(400, "%v", err)
	}
	binding, err := service.store.Ingest(service.user.ID, input)
	if err != nil {
		return Submission{}, requestError(err)
	}
	service.events.publish(binding.ContextID)
	item, err := service.Get(ctx, binding.ContextID)
	if err != nil {
		return Submission{}, err
	}
	snapshot, err := service.store.ObservationSnapshot(service.user.ID, binding.ContextID, review.DiffAll)
	if err != nil {
		return Submission{}, requestError(err)
	}
	return Submission{Context: item, Snapshot: snapshot}, nil
}

type storedSource struct {
	store    *reviewstore.Store
	ownerID  string
	id       string
	metadata ingestion.Metadata
	kind     string
	binding  reviewstore.Binding
}

func (source *storedSource) Root() string { return source.metadata.Root }
func (source *storedSource) Kind() string { return source.kind }
func (source *storedSource) Support() diffsource.Support {
	scopes := []review.DiffMode{}
	for _, mode := range []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged} {
		if _, ok := source.binding.DiffIDs[mode]; ok {
			scopes = append(scopes, mode)
		}
	}
	return diffsource.Support{Scopes: scopes, StagingMetadata: len(scopes) > 1, FileContents: source.kind == "local"}
}
func (source *storedSource) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if err := ctx.Err(); err != nil {
		return review.RepositoryDiff{}, err
	}
	value, err := source.store.ObservationSnapshot(source.ownerID, source.id, mode)
	return value, requestError(err)
}
func (source *storedSource) Patch(ctx context.Context, mode review.DiffMode, file review.ChangedFile, expected *string) (review.FilePatch, error) {
	if err := ctx.Err(); err != nil {
		return review.FilePatch{}, err
	}
	snapshot, err := source.Snapshot(ctx, mode)
	if err != nil {
		return review.FilePatch{}, err
	}
	found := false
	for _, candidate := range snapshot.Files {
		if candidate.ID == file.ID && candidate.Fingerprint == file.Fingerprint {
			found = true
			break
		}
	}
	if !found {
		return review.FilePatch{}, diffsource.Error(404, "File not in observation")
	}
	value, err := source.store.ObservationPatch(source.ownerID, source.id, mode, file.ID)
	return value, requestError(err)
}
func (source *storedSource) Contents(ctx context.Context, mode review.DiffMode, file review.ChangedFile, expected *string) (review.FileContents, error) {
	preview, err := source.Patch(ctx, mode, file, expected)
	if err != nil {
		return review.FileContents{}, err
	}
	if preview.Contents == nil {
		return review.FileContents{}, diffsource.Error(404, "Full file contents were not collected")
	}
	return *preview.Contents, nil
}

func storedCapabilities(scopes []review.DiffMode, contents bool) session.Capabilities {
	return session.Capabilities{
		Diff:   session.DiffCapabilities{Scopes: session.ScopesCapability{State: session.Enabled, Values: scopes}, Refresh: session.Capability{State: session.Unavailable}, StagingMetadata: session.Capability{State: state(len(scopes) > 1)}},
		Files:  session.FileCapabilities{Contents: session.Capability{State: state(contents)}},
		Review: session.ReviewCapabilities{Comments: session.Capability{State: session.Enabled}},
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// Notifications are hints. Reconnecting clients always reload the durable catalog.
type events struct {
	mu        sync.Mutex
	listeners map[chan string]struct{}
}

func (broker *events) publish(id string) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for listener := range broker.listeners {
		select {
		case listener <- id:
		default:
		}
	}
}
func (service *Service) Subscribe() (<-chan string, func()) {
	channel := make(chan string, 1)
	service.events.mu.Lock()
	if service.events.listeners == nil {
		service.events.listeners = make(map[chan string]struct{})
	}
	service.events.listeners[channel] = struct{}{}
	service.events.mu.Unlock()
	return channel, func() {
		service.events.mu.Lock()
		delete(service.events.listeners, channel)
		service.events.mu.Unlock()
	}
}
