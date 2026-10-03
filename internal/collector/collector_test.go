package collector

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

type blockingSource struct {
	started   chan struct{}
	release   chan struct{}
	snapshots atomic.Int32
}

func (source *blockingSource) Root() string { return "/repo" }
func (source *blockingSource) Kind() string { return "local" }
func (source *blockingSource) Support() diffsource.Support {
	return diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}, Refresh: true}
}
func (source *blockingSource) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if source.snapshots.Add(1) == 1 {
		close(source.started)
		select {
		case <-source.release:
		case <-ctx.Done():
			return review.RepositoryDiff{}, ctx.Err()
		}
	}
	return review.RepositoryDiff{Revision: "same", Mode: mode, Files: []review.ChangedFile{{ID: "file", Path: "value", Fingerprint: "same"}}}, nil
}
func (source *blockingSource) Patch(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FilePatch, error) {
	return review.FilePatch{Patch: "collected patch"}, nil
}
func (source *blockingSource) Contents(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FileContents, error) {
	return review.FileContents{}, errors.New("unsupported")
}

// Signals that the second caller has joined the shared flight before releasing it.
type observedContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (ctx *observedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.joined) })
	return ctx.Context.Done()
}

func TestCollectionSurvivesCallerCancellationAndNewerInvalidation(t *testing.T) {
	for _, newer := range []bool{false, true} {
		name := "same-generation"
		if newer {
			name = "newer-generation"
		}
		t.Run(name, func(t *testing.T) {
			store, err := reviewstore.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			user, err := store.User("1001", "test")
			if err != nil {
				t.Fatal(err)
			}
			binding, err := store.RegisterGitSubmission(user.ID, "register", "input", "/repo", "/repo/.git", "/repo/.git")
			if err != nil {
				t.Fatal(err)
			}
			service := New(t.Context(), store)
			defer service.Close()
			source := &blockingSource{started: make(chan struct{}), release: make(chan struct{})}
			var opens atomic.Int32
			open := func(context.Context) (diffsource.Source, error) { opens.Add(1); return source, nil }
			firstCtx, cancel := context.WithCancel(t.Context())
			first := make(chan error, 1)
			go func() { first <- service.Refresh(firstCtx, binding, 1, review.DiffAll, open) }()
			<-source.started
			cancel()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Fatalf("first caller: %v", err)
			}
			generation := int64(1)
			if newer {
				generation = 2
			}
			secondCtx := &observedContext{Context: t.Context(), joined: make(chan struct{})}
			second := make(chan error, 1)
			go func() { second <- service.Refresh(secondCtx, binding, generation, review.DiffAll, open) }()
			<-secondCtx.joined
			close(source.release)
			if err := <-second; err != nil {
				t.Fatal(err)
			}
			expected := int32(1)
			if newer {
				expected = 2
			}
			if opens.Load() != expected || source.snapshots.Load() != expected*2 {
				t.Fatalf("shared collection/newer generation: opens=%d snapshots=%d", opens.Load(), source.snapshots.Load())
			}
			current, err := store.CurrentVersion(user.ID, binding.DiffIDs[review.DiffAll])
			if err != nil {
				t.Fatal(err)
			}
			preview, err := store.Preview(user.ID, current.ID, "file", "same")
			if err != nil || preview.Patch != "collected patch" {
				t.Fatalf("published preview: %#v, %v", preview, err)
			}
		})
	}
}
