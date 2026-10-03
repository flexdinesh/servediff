// Package collector is the only live-diff reader. API sources read its committed SQLite versions.
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

type flight struct {
	done       chan struct{}
	err        error
	generation int64
}
type Service struct {
	store      *reviewstore.Store
	background context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	flights    map[string]*flight
	closed     bool
	workers    sync.WaitGroup
}

func New(ctx context.Context, store *reviewstore.Store) *Service {
	ctx, cancel := context.WithCancel(ctx)
	return &Service{store: store, background: ctx, cancel: cancel, flights: make(map[string]*flight)}
}
func (service *Service) Close() {
	service.mu.Lock()
	service.closed = true
	service.cancel()
	service.mu.Unlock()
	service.workers.Wait()
}

func (service *Service) Refresh(ctx context.Context, binding reviewstore.Binding, generation int64, mode review.DiffMode, open func(context.Context) (diffsource.Source, error)) error {
	key := binding.ContextID + ":" + string(mode)
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return context.Canceled
	}
	active := service.flights[key]
	if active == nil {
		active = &flight{done: make(chan struct{}), generation: generation}
		service.flights[key] = active
		service.workers.Add(1)
		go func() {
			defer service.workers.Done()
			task, cancel := context.WithTimeout(service.background, 30*time.Second)
			defer cancel()
			source, err := open(task)
			if err == nil {
				err = service.collect(task, source, binding, generation, mode)
			}
			service.mu.Lock()
			active.err = err
			delete(service.flights, key)
			close(active.done)
			service.mu.Unlock()
		}()
	}
	service.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-active.done:
		if active.err == nil && generation > active.generation {
			return service.Refresh(ctx, binding, generation, mode, open)
		}
		return active.err
	}
}

func (service *Service) collect(ctx context.Context, source diffsource.Source, binding reviewstore.Binding, generation int64, mode review.DiffMode) error {
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, err := source.Snapshot(ctx, mode)
		if err != nil {
			return err
		}
		previews := make(map[string]review.FilePatch, len(snapshot.Files))
		bytes := 0
		for _, file := range snapshot.Files {
			preview, err := source.Patch(ctx, mode, file, snapshot.Head)
			if err != nil {
				return err
			}
			if source.Support().FileContents && preview.Contents == nil && !file.Binary && file.Status != "U" {
				contents, err := source.Contents(ctx, mode, file, snapshot.Head)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err == nil {
					preview.Contents = &contents
				}
			}
			encoded, _ := json.Marshal(preview)
			bytes += len(encoded)
			if bytes > 16*1024*1024 {
				preview = review.FilePatch{Message: diffsource.Message("Preview budget exceeded (16 MiB). Narrow the diff scope.")}
			}
			previews[file.ID] = preview
		}
		current, err := source.Snapshot(ctx, mode)
		if err != nil {
			return err
		}
		if current.Revision != snapshot.Revision {
			continue
		}
		snapshot.ID = binding.DiffIDs[mode]
		snapshot.LocationID = binding.LocationID
		snapshot.RepositoryID = binding.RepositoryID
		snapshot.VersionID = reviewstore.VersionID(snapshot.ID, snapshot.Revision)
		if err := service.store.Publish(snapshot, previews, generation); err != nil {
			return err
		}
		return service.store.PruneLiveVersions(snapshot.ID)
	}
	return diffsource.Error(409, "Diff changed while collecting. Refresh to try again.")
}

type Source struct {
	Store   *reviewstore.Store
	UserID  string
	Binding reviewstore.Binding
	Path    string
}

func (source *Source) Root() string { return source.Path }
func (source *Source) Kind() string { return "local" }
func (source *Source) Support() diffsource.Support {
	return diffsource.Support{Scopes: []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged}, Refresh: true, StagingMetadata: true, FileContents: true}
}
func (source *Source) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if err := ctx.Err(); err != nil {
		return review.RepositoryDiff{}, err
	}
	snapshot, err := source.Store.CurrentVersion(source.UserID, source.Binding.DiffIDs[mode])
	if errors.Is(err, reviewstore.ErrNotFound) {
		return snapshot, diffsource.Error(503, "source_unavailable: no collected version; refresh changes")
	}
	return snapshot, err
}
func (source *Source) Patch(ctx context.Context, mode review.DiffMode, file review.ChangedFile, _ *string) (review.FilePatch, error) {
	if err := ctx.Err(); err != nil {
		return review.FilePatch{}, err
	}
	preview, err := source.Store.Preview(source.UserID, source.Binding.DiffIDs[mode], file.ID, file.Fingerprint)
	if errors.Is(err, reviewstore.ErrNotFound) {
		return preview, diffsource.Error(404, "File preview not retained")
	}
	return preview, err
}
func (source *Source) Contents(ctx context.Context, mode review.DiffMode, file review.ChangedFile, head *string) (review.FileContents, error) {
	preview, err := source.Patch(ctx, mode, file, head)
	if err != nil {
		return review.FileContents{}, err
	}
	if preview.Contents == nil {
		return review.FileContents{}, diffsource.Error(404, "Full context unavailable for this file")
	}
	return *preview.Contents, nil
}
