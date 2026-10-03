// Package contextservice manages durable review targets independently of transports.
package contextservice

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
)

type Context struct {
	ID              string               `json:"id"`
	Kind            string               `json:"kind"`
	Name            string               `json:"name"`
	Root            *string              `json:"root"`
	LocationID      *string              `json:"locationId"`
	RepositoryID    *string              `json:"repositoryId"`
	CreatedAt       int64                `json:"createdAt"`
	LastSubmittedAt int64                `json:"lastSubmittedAt"`
	LastChangedAt   int64                `json:"lastChangedAt"`
	ExpiresAt       *int64               `json:"expiresAt"`
	SubmittedFrom   *string              `json:"submittedFrom"`
	Capabilities    session.Capabilities `json:"capabilities"`
	Availability    string               `json:"availability"`
	Branch          *string              `json:"branch"`
	WorktreeName    *string              `json:"worktreeName"`
	Generation      int64                `json:"-"`
}

type Page struct {
	Contexts   []Context `json:"contexts"`
	NextCursor *string   `json:"nextCursor"`
}

type Submission struct {
	Context  Context               `json:"context"`
	Snapshot review.RepositoryDiff `json:"snapshot"`
}

type Service struct {
	store           *reviewstore.Store
	collector       *collector.Service
	subscribers     map[chan ChangeEvent]struct{}
	user            reviewstore.User
	mu              sync.Mutex
	availability    map[string]string
	catalogMu       sync.Mutex
	sources         map[string]*cachedSource
	loading         map[string]*sourceLoad
	generation      map[string]uint64
	identityChanged map[string]bool
	sourceBytes     int
	parseSlots      chan struct{}
	background      context.Context
	cancel          context.CancelFunc
	loaders         sync.WaitGroup
	closing         bool
}

func New(store *reviewstore.Store, user reviewstore.User) *Service {
	return NewWithContext(context.Background(), store, user)
}

func NewWithContext(ctx context.Context, store *reviewstore.Store, user reviewstore.User) *Service {
	background, cancel := context.WithCancel(ctx)
	return &Service{store: store, user: user, collector: collector.New(background, store), subscribers: make(map[chan ChangeEvent]struct{}), availability: make(map[string]string),
		sources: make(map[string]*cachedSource), loading: make(map[string]*sourceLoad),
		generation: make(map[string]uint64), identityChanged: make(map[string]bool), parseSlots: make(chan struct{}, 2),
		background: background, cancel: cancel}
}

// Close stops shared source work before the database owner is released.
func (service *Service) Close() error {
	service.mu.Lock()
	service.closing = true
	service.cancel()
	service.mu.Unlock()
	service.collector.Close()
	service.loaders.Wait()
	return nil
}

func (service *Service) UserID() string { return service.user.ID }

func (service *Service) Register(ctx context.Context, requestID, path string) (Submission, error) {
	if err := validateRequest(ctx, requestID); err != nil {
		return Submission{}, err
	}
	source, err := diffsource.OpenRepository(ctx, path)
	if err != nil {
		return Submission{}, diffsource.Error(400, "Could not register worktree: %v", err)
	}
	commonDir, worktreeKey, err := diffsource.RepositoryIdentity(ctx, source.Root())
	if err != nil {
		return Submission{}, diffsource.Error(400, "Could not identify worktree: %v", err)
	}
	snapshot := review.RepositoryDiff{Root: source.Root(), Name: filepath.Base(source.Root()), Source: "local", Mode: review.DiffAll, Files: []review.ChangedFile{}}
	binding, err := service.store.RegisterGitSubmission(service.user.ID, requestID, payloadHash("worktree", source.Root(), commonDir, worktreeKey), source.Root(), commonDir, worktreeKey)
	if err != nil {
		return Submission{}, requestError(err)
	}
	service.invalidateSource(binding.ContextID)
	service.cacheSource(binding.ContextID, newWorktreeCache(source, commonDir, worktreeKey), 0)
	service.setAvailability(binding.ContextID, "available")

	item, err := service.Get(ctx, binding.ContextID)
	if err != nil {
		return Submission{}, err
	}
	service.Notify(ChangeEvent{Kind: "catalog", ContextID: item.ID, RepositoryID: item.RepositoryID})
	return Submission{Context: item, Snapshot: bindSnapshot(snapshot, binding)}, nil
}

func (service *Service) Capture(ctx context.Context, requestID, raw, submittedFrom string) (Submission, error) {
	if err := validateRequest(ctx, requestID); err != nil {
		return Submission{}, err
	}
	if len(raw) > diffsource.MaxInputBytes {
		return Submission{}, diffsource.Error(413, "Piped diff exceeds the 16 MiB input limit")
	}
	if submittedFrom != "" {
		absolute, err := filepath.Abs(submittedFrom)
		if err != nil {
			return Submission{}, diffsource.Error(400, "Invalid submission directory")
		}
		submittedFrom = absolute
	}
	select {
	case service.parseSlots <- struct{}{}:
	default:
		return Submission{}, diffsource.Error(503, "Capture parsing is busy; retry shortly")
	}
	source, err := diffsource.OpenPatch(raw)
	<-service.parseSlots
	if err != nil {
		return Submission{}, err
	}
	snapshot, err := source.Snapshot(ctx, review.DiffAll)
	if err != nil {
		return Submission{}, err
	}
	if err := ctx.Err(); err != nil {
		return Submission{}, err
	}
	binding, err := service.store.CaptureSubmission(service.user.ID, requestID, payloadHash("capture", raw, submittedFrom), raw, submittedFrom, snapshot)
	if err != nil {
		return Submission{}, requestError(err)
	}
	service.invalidateSource(binding.ContextID)
	service.cacheSource(binding.ContextID, newCaptureCache(source, raw, snapshot), 0)
	item, err := service.Get(ctx, binding.ContextID)
	if err != nil {
		return Submission{}, err
	}
	service.Notify(ChangeEvent{Kind: "catalog", ContextID: item.ID})
	return Submission{Context: item, Snapshot: bindSnapshot(snapshot, binding)}, nil
}

func (service *Service) OpenCapture(ctx context.Context, id string) (Submission, error) {
	item, err := service.Get(ctx, id)
	if err != nil {
		return Submission{}, err
	}
	if item.Kind != "capture" {
		return Submission{}, diffsource.Error(404, "Capture not found")
	}
	resolved, err := service.Resolve(ctx, id)
	if err != nil {
		return Submission{}, err
	}
	snapshot, err := resolved.Source.Snapshot(ctx, review.DiffAll)
	if err != nil {
		return Submission{}, err
	}
	binding := reviewstore.Binding{ContextID: resolved.ContextID, LocationID: resolved.LocationID, RepositoryID: resolved.RepositoryID, DiffIDs: resolved.DiffIDs, VersionID: resolved.VersionID}
	return Submission{Context: item, Snapshot: bindSnapshot(snapshot, binding)}, nil
}

func (service *Service) Resolve(ctx context.Context, id string) (session.Session, error) {
	if err := service.background.Err(); err != nil {
		return session.Session{}, err
	}
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	now := time.Now()
	item, err := service.store.Context(service.user.ID, id, now)
	if err != nil {
		return session.Session{}, requestError(err)
	}
	binding, err := service.store.ContextBinding(service.user.ID, id, now)
	if err != nil {
		return session.Session{}, requestError(err)
	}
	var source diffsource.Source
	var collect func(context.Context, review.DiffMode) error
	if item.Kind == "worktree" && item.Root != nil {
		source = &collector.Source{Store: service.store, UserID: service.user.ID, Binding: binding, Path: *item.Root}
		collect = func(task context.Context, mode review.DiffMode) error { return service.Refresh(task, id, mode) }
	} else {
		source, err = service.sourceFor(ctx, item)
		if err != nil {
			return session.Session{}, err
		}
	}
	resolved := session.Resolve(source, session.Policies{})
	resolved.Collect = collect
	resolved.ReviewChanged = func() { service.Notify(ChangeEvent{Kind: "review", ContextID: id}) }
	resolved.User = session.User{ID: service.user.ID, Name: service.user.Name}
	resolved.ContextID = binding.ContextID
	resolved.LocationID = binding.LocationID
	resolved.RepositoryID = binding.RepositoryID
	resolved.DiffIDs = binding.DiffIDs
	resolved.VersionID = binding.VersionID
	return resolved, nil
}

func (service *Service) Get(ctx context.Context, id string) (Context, error) {
	if err := ctx.Err(); err != nil {
		return Context{}, err
	}
	item, err := service.store.Context(service.user.ID, id, time.Now())
	if err != nil {
		return Context{}, requestError(err)
	}

	return service.present(item), nil
}

type cursorValue struct {
	Time int64  `json:"t"`
	ID   string `json:"id"`
}

func (service *Service) List(ctx context.Context, limit int, cursor string) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return Page{}, diffsource.Error(400, "Limit must be between 1 and 500")
	}
	var before cursorValue
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &before) != nil || before.ID == "" || before.Time < 0 {
			return Page{}, diffsource.Error(400, "Invalid context cursor")
		}
	}

	items, err := service.store.Contexts(service.user.ID, limit+1, before.Time, before.ID, time.Now())
	if err != nil {
		return Page{}, err
	}
	page := Page{Contexts: make([]Context, 0, limit)}
	if len(items) > limit {
		last := items[limit-1]
		raw, err := json.Marshal(cursorValue{Time: last.LastSubmittedAt, ID: last.ID})
		if err != nil {
			return Page{}, err
		}
		next := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &next
		items = items[:limit]
	}
	for _, item := range items {
		page.Contexts = append(page.Contexts, service.present(item))
	}
	return page, nil
}

func (service *Service) Count(ctx context.Context) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return service.store.ContextCounts(service.user.ID, time.Now())
}

func (service *Service) present(item reviewstore.ContextInfo) Context {
	var branch, worktreeName *string
	lastChangedAt := item.LastSubmittedAt
	availability := "available"
	name := "Piped diff · " + time.UnixMilli(item.CreatedAt).Format("2006-01-02 15:04") + " · " + item.ID[:min(8, len(item.ID))]
	if item.Kind == "worktree" {
		lastChangedAt = item.LastChangedAt
		if item.Name != "" {
			name = item.Name
		}
		if item.Branch != "" {
			branch = &item.Branch
		}
		worktreeName = item.WorktreeName
		availability = "unchecked"
		if item.Root != nil && item.Name == "" {
			name = filepath.Base(*item.Root)
		}
		service.mu.Lock()
		if known, ok := service.availability[item.ID]; ok {
			availability = known
		}
		service.mu.Unlock()
	}
	return Context{ID: item.ID, Kind: item.Kind, Name: name, Branch: branch, WorktreeName: worktreeName, Generation: item.Generation, Root: item.Root, LocationID: item.LocationID,
		RepositoryID: item.RepositoryID, CreatedAt: item.CreatedAt, LastSubmittedAt: item.LastSubmittedAt, LastChangedAt: lastChangedAt,
		ExpiresAt: item.ExpiresAt, SubmittedFrom: item.SubmittedFrom, Capabilities: capabilities(item.Kind), Availability: availability}
}

func (service *Service) setAvailability(id, state string) {
	service.mu.Lock()
	service.availability[id] = state
	service.mu.Unlock()
}

func capabilities(kind string) session.Capabilities {
	support := diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}}
	if kind == "worktree" {
		support = worktreeSupport()
	}
	return session.Capabilities{
		Diff: session.DiffCapabilities{
			Scopes:          session.ScopesCapability{State: session.Enabled, Values: support.Scopes},
			Refresh:         session.Capability{State: state(support.Refresh)},
			StagingMetadata: session.Capability{State: state(support.StagingMetadata)},
		},
		Files:  session.FileCapabilities{Contents: session.Capability{State: state(support.FileContents)}},
		Review: session.ReviewCapabilities{Comments: session.Capability{State: session.Enabled}},
	}
}

func state(enabled bool) session.State {
	if enabled {
		return session.Enabled
	}
	return session.Unavailable
}

func bindSnapshot(snapshot review.RepositoryDiff, binding reviewstore.Binding) review.RepositoryDiff {
	snapshot.ID = binding.DiffIDs[snapshot.Mode]
	snapshot.LocationID = binding.LocationID
	snapshot.RepositoryID = binding.RepositoryID
	snapshot.VersionID = binding.VersionID
	if snapshot.VersionID == "" {
		snapshot.VersionID = reviewstore.VersionID(snapshot.ID, snapshot.Revision)
	}
	return snapshot
}

func validateRequest(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return diffsource.Error(400, "Submission ID required; maximum 128 bytes")
	}
	return nil
}

func payloadHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func requestError(err error) error {
	switch {
	case errors.Is(err, reviewstore.ErrNotFound):
		return diffsource.Error(404, "Context not found")
	case errors.Is(err, reviewstore.ErrExpired):
		return diffsource.Error(410, "Capture expired")
	case errors.Is(err, reviewstore.ErrSubmissionConflict):
		return diffsource.Error(409, "Submission ID reused with different input")
	default:
		return err
	}
}

// Unavailable worktrees retain their review bindings and stored versions.
type unavailableSource struct{ root string }

func (source *unavailableSource) Root() string { return source.root }
func (source *unavailableSource) Kind() string { return "local" }
func worktreeSupport() diffsource.Support {
	return diffsource.Support{Scopes: []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged}, Refresh: true, StagingMetadata: true, FileContents: true}
}
func (source *unavailableSource) Support() diffsource.Support { return worktreeSupport() }
func (source *unavailableSource) Snapshot(context.Context, review.DiffMode) (review.RepositoryDiff, error) {
	return review.RepositoryDiff{}, diffsource.Error(503, "source_unavailable: registered worktree cannot be opened")
}
func (source *unavailableSource) Patch(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FilePatch, error) {
	return review.FilePatch{}, diffsource.Error(503, "source_unavailable: registered worktree cannot be opened")
}
func (source *unavailableSource) Contents(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FileContents, error) {
	return review.FileContents{}, diffsource.Error(503, "source_unavailable: registered worktree cannot be opened")
}
