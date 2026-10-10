// Package contextservice manages durable review targets independently of transports.
package contextservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewdata"
	"github.com/flexdinesh/diffx/internal/session"
)

type Context struct {
	Stale            bool                            `json:"stale"`
	Sessions         []reviewdata.SessionAssociation `json:"sessions,omitempty"`
	Observation      *ingestion.Metadata             `json:"observation,omitempty"`
	ID               string                          `json:"id"`
	Kind             string                          `json:"kind"`
	Source           string                          `json:"source"`
	Name             string                          `json:"name"`
	Root             *string                         `json:"root"`
	LocationID       *string                         `json:"locationId"`
	RepositoryID     *string                         `json:"repositoryId"`
	CreatedAt        int64                           `json:"createdAt"`
	LastSubmittedAt  int64                           `json:"lastSubmittedAt"`
	LastChangedAt    int64                           `json:"lastChangedAt"`
	ChangedFileCount *int                            `json:"changedFileCount"`
	ExpiresAt        *int64                          `json:"expiresAt"`
	SubmittedFrom    *string                         `json:"submittedFrom"`
	Capabilities     session.Capabilities            `json:"capabilities"`
	Availability     string                          `json:"availability"`
	Branch           *string                         `json:"branch"`
	WorktreeName     *string                         `json:"worktreeName"`
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
	events     events
	store      Store
	user       reviewdata.User
	background context.Context
	cancel     context.CancelFunc
}

func New(store Store, user reviewdata.User) *Service {
	return NewWithContext(context.Background(), store, user)
}
func NewWithContext(ctx context.Context, store Store, user reviewdata.User) *Service {
	background, cancel := context.WithCancel(ctx)
	return &Service{store: store, user: user, background: background, cancel: cancel}
}
func (service *Service) Close() error   { service.cancel(); return nil }
func (service *Service) UserID() string { return service.user.ID }

func (service *Service) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := service.store.DeleteContext(service.user.ID, id); err != nil {
		return requestError(err)
	}
	service.events.publish(id)
	return nil
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
	if item.Metadata == nil {
		return session.Session{}, review.Error(500, "Observation metadata missing")
	}
	snapshot, err := service.store.ObservationSnapshot(service.user.ID, id, review.DiffAll)
	if err != nil {
		return session.Session{}, requestError(err)
	}
	source := &storedSource{store: service.store, ownerID: service.user.ID, id: id, metadata: *item.Metadata, binding: binding, kind: snapshot.Source}
	resolved := session.Resolve(source, session.Policies{})
	resolved.User = session.User{ID: service.user.ID, Name: service.user.Name}
	resolved.ContextID = binding.ContextID
	resolved.LocationID = binding.LocationID
	resolved.RepositoryID = binding.RepositoryID
	resolved.DiffIDs = binding.DiffIDs
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
	return service.present(item)
}

type cursorValue struct {
	Time int64  `json:"t"`
	ID   string `json:"id"`
}

func (service *Service) List(ctx context.Context, limit int, cursor string) (Page, error) {
	return service.ListFiltered(ctx, limit, cursor, ingestion.Filter{})
}

func (service *Service) ListFiltered(ctx context.Context, limit int, cursor string, filter ingestion.Filter) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return Page{}, review.Error(400, "Limit must be between 1 and 500")
	}
	var before cursorValue
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &before) != nil || before.ID == "" || before.Time < 0 {
			return Page{}, review.Error(400, "Invalid context cursor")
		}
	}
	var items []reviewdata.ContextInfo
	var err error
	if filter != (ingestion.Filter{}) {
		items, err = service.store.ObservationContexts(service.user.ID, limit+1, before.Time, before.ID, filter)
	} else {
		items, err = service.store.Contexts(service.user.ID, limit+1, before.Time, before.ID, time.Now())
	}
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
		value, err := service.present(item)
		if err != nil {
			return Page{}, err
		}
		page.Contexts = append(page.Contexts, value)
	}
	return page, nil
}

func (service *Service) Count(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return service.store.ContextCount(service.user.ID, time.Now())
}

func (service *Service) present(item reviewdata.ContextInfo) (Context, error) {
	if item.Kind == "observation" && item.Metadata != nil {
		m := item.Metadata
		binding, err := service.store.ContextBinding(service.user.ID, item.ID, time.Now())
		scopes := []review.DiffMode{}
		if err != nil {
			return Context{}, requestError(err)
		}
		for _, mode := range []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged} {
			if _, ok := binding.DiffIDs[mode]; ok {
				scopes = append(scopes, mode)
			}
		}
		snapshot, err := service.store.ObservationSnapshot(service.user.ID, item.ID, review.DiffAll)
		if err != nil {
			return Context{}, requestError(err)
		}
		name := review.RemoteRepositoryName(m.RemoteURL, m.RepositoryName)
		if name == "" {
			name = "Piped"
		}
		value := Context{Source: snapshot.Source, Stale: item.Stale, ID: item.ID, Kind: "observation", Name: name, Root: &m.Root, RepositoryID: item.RepositoryID, CreatedAt: item.CreatedAt, LastSubmittedAt: item.LastSubmittedAt, LastChangedAt: m.CollectedAt, ExpiresAt: item.ExpiresAt, ChangedFileCount: item.ChangedFileCount, SubmittedFrom: item.SubmittedFrom, Observation: m, Sessions: item.Sessions, Branch: &m.Branch, WorktreeName: &m.WorktreeName, Availability: "available", Capabilities: storedCapabilities(scopes, snapshot.Source == "local")}
		if snapshot.Source == "stdin" {
			value.Name, value.Stale = "Piped", false
			value.Root, value.RepositoryID, value.Branch, value.WorktreeName = nil, nil, nil, nil
		}
		return value, nil
	}
	return Context{}, review.Error(500, "Observation metadata missing")
}

func requestError(err error) error {
	switch {
	case errors.Is(err, reviewdata.ErrNotFound):
		return review.Error(404, "Context not found")
	case errors.Is(err, reviewdata.ErrExpired):
		return review.Error(410, "Capture expired")
	case errors.Is(err, reviewdata.ErrSubmissionConflict):
		return review.Error(409, "Submission ID reused with different input")
	default:
		return err
	}
}

// Unavailable worktrees retain their review bindings and stored versions.
