package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/processmetrics"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
	contract "github.com/flexdinesh/servediff/packages/api"
)

// Provider resolves persistent identities into request-local source bindings.
type Provider interface {
	Resolve(context.Context, string) (session.Session, error)
	List(context.Context, int, string) (contextservice.Page, error)
	Get(context.Context, string) (contextservice.Context, error)
	Delete(context.Context, string) error
	UserID() string
}

type contextCache struct {
	snapshots *snapshotCache
	used      time.Time
	submitted int64
}

type Multi struct {
	background       context.Context
	provider         Provider
	store            *reviewstore.Store
	assets           fs.FS
	defaultContextID string
	metrics          *processmetrics.Collector
	mu               sync.Mutex
	caches           map[string]contextCache
}

func NewMulti(provider Provider, store *reviewstore.Store, assets fs.FS, defaultContextID string) http.Handler {
	return NewMultiWithContext(context.Background(), provider, store, assets, defaultContextID)
}

// NewMultiWithContext ties shared snapshot work to the service lifetime.
func NewMultiWithContext(background context.Context, provider Provider, store *reviewstore.Store, assets fs.FS, defaultContextID string) http.Handler {
	return &Multi{background: background, provider: provider, store: store, assets: assets, defaultContextID: defaultContextID, metrics: processmetrics.New(), caches: make(map[string]contextCache)}
}

func (multi *Multi) cache(id string, submitted int64) *snapshotCache {
	multi.mu.Lock()
	defer multi.mu.Unlock()
	now := time.Now()
	for key, entry := range multi.caches {
		if now.Sub(entry.used) > 10*time.Minute {
			entry.snapshots.mu.Lock()
			active := len(entry.snapshots.flights) > 0
			entry.snapshots.mu.Unlock()
			if !active {
				delete(multi.caches, key)
			}
		}
	}
	entry, ok := multi.caches[id]
	if !ok || entry.submitted != submitted {
		entry = contextCache{snapshots: newSnapshotCache(multi.background), submitted: submitted}
		entry.snapshots.onUpdate = multi.trim
	}
	entry.used = now
	multi.caches[id] = entry

	multi.trimLocked()

	return entry.snapshots
}

func (multi *Multi) trim() {
	multi.mu.Lock()
	defer multi.mu.Unlock()
	multi.trimLocked()
}

func (multi *Multi) trimLocked() {
	// Account retained snapshot data, not total process memory or in-flight work.
	const maxBytes = 64 * 1024 * 1024
	for {
		var total int64
		oldestID := ""
		var oldest time.Time
		for key, entry := range multi.caches {
			entry.snapshots.mu.Lock()
			total += entry.snapshots.bytes
			active := len(entry.snapshots.flights) > 0
			entry.snapshots.mu.Unlock()
			if !active && (oldestID == "" || entry.used.Before(oldest)) {
				oldestID, oldest = key, entry.used
			}
		}
		if (len(multi.caches) <= 128 && total <= maxBytes) || oldestID == "" {
			return
		}
		delete(multi.caches, oldestID)
	}
}

func (multi *Multi) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	base := &Handler{store: multi.store, assets: multi.assets, metrics: multi.metrics}
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Cache-Control", "no-store")
	if err := base.checkRequest(request); err != nil {
		base.problem(response, err)
		return
	}
	pathname := request.URL.Path
	if pathname == "/openapi.yaml" {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			base.problem(response, diffsource.Error(405, "Method not allowed"))
			return
		}
		response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		if request.Method == http.MethodGet {
			_, _ = response.Write(contract.OpenAPI)
		}
		return
	}
	if pathname == "/api/v1/metrics" || pathname == "/api/v2/metrics" {
		if request.Method != http.MethodGet {
			base.problem(response, diffsource.Error(405, "Method not allowed"))
			return
		}
		_ = writeJSON(response, 200, multi.metrics.Collect())
		return
	}
	if pathname == "/api/v1/diffs/captures" {
		if request.Method != http.MethodGet {
			base.problem(response, diffsource.Error(405, "Method not allowed"))
			return
		}
		captures, err := multi.store.ListCaptures(multi.provider.UserID(), time.Now())
		if err != nil {
			base.problem(response, err)
			return
		}
		_ = writeJSON(response, 200, map[string]any{"captures": captures})
		return
	}
	if pathname == "/api/v2/contexts" {
		if request.Method != http.MethodGet {
			base.problem(response, diffsource.Error(405, "Method not allowed"))
			return
		}
		limit := 100
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 500 {
				base.problem(response, diffsource.Error(400, "Limit must be between 1 and 500"))
				return
			}
			limit = parsed
		}
		var page contextservice.Page
		var err error
		query := request.URL.Query()
		filter := ingestion.Filter{Query: query.Get("q"), Repository: query.Get("repository"), Branch: query.Get("branch"), Worktree: query.Get("worktree"), Hostname: query.Get("hostname"), SourceID: query.Get("sourceId"), RunID: query.Get("runId"), Harness: query.Get("harness"), SessionID: query.Get("sessionId"), SessionName: query.Get("sessionName")}
		if provider, ok := multi.provider.(interface {
			ListFiltered(context.Context, int, string, ingestion.Filter) (contextservice.Page, error)
		}); ok {
			page, err = provider.ListFiltered(request.Context(), limit, query.Get("cursor"), filter)
		} else {
			page, err = multi.provider.List(request.Context(), limit, query.Get("cursor"))
		}
		if err != nil {
			base.problem(response, err)
			return
		}
		_ = writeJSON(response, 200, page)
		return
	}
	contextID := ""
	scopedPath := ""
	if strings.HasPrefix(pathname, "/api/v2/contexts/") {
		parts := strings.SplitN(strings.TrimPrefix(pathname, "/api/v2/contexts/"), "/", 2)
		contextID = parts[0]
		if contextID == "" {
			base.problem(response, diffsource.Error(404, "Context not found"))
			return
		}
		if len(parts) == 1 {
			if request.Method == http.MethodDelete {
				if err := multi.provider.Delete(request.Context(), contextID); err != nil {
					base.problem(response, err)
					return
				}
				multi.mu.Lock()
				delete(multi.caches, contextID)
				multi.mu.Unlock()
				response.WriteHeader(http.StatusNoContent)
				return
			}
			if request.Method != http.MethodGet {
				base.problem(response, diffsource.Error(405, "Method not allowed"))
				return
			}
			metadata, err := multi.provider.Get(request.Context(), contextID)
			if err != nil {
				base.problem(response, err)
				return
			}
			_ = writeJSON(response, 200, metadata)
			return
		}
		scopedPath = "/api/v1/" + parts[1]
	} else if strings.HasPrefix(pathname, "/api/v1/") {
		contextID = multi.defaultContextID
		if contextID == "" {
			page, err := multi.provider.List(request.Context(), 2, "")
			if err != nil {
				base.problem(response, err)
				return
			}
			if len(page.Contexts) != 1 || page.NextCursor != nil {
				_ = writeJSONType(response, 409, "application/problem+json; charset=utf-8", map[string]any{"type": "about:blank", "title": "Context required", "status": 409, "code": "context_required", "detail": "Select an explicit context using /api/v2/contexts/{contextId}."})
				return
			}
			contextID = page.Contexts[0].ID
		}
		scopedPath = pathname
	} else if strings.HasPrefix(pathname, "/api/") {
		base.problem(response, diffsource.Error(404, "Unknown API route"))
		return
	} else {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			base.problem(response, diffsource.Error(405, "Method not allowed"))
			return
		}
		assetRequest := request
		if _, ok := routeValue(pathname, "/contexts/"); ok {
			assetRequest = request.Clone(request.Context())
			assetRequest.URL.Path = "/"
		}
		base.serveAsset(response, assetRequest)
		return
	}
	active, err := multi.provider.Resolve(request.Context(), contextID)
	if err != nil {
		if errors.Is(err, reviewstore.ErrNotFound) {
			err = diffsource.Error(404, "Context not found")
		}
		base.problem(response, err)
		return
	}
	metadata, err := multi.provider.Get(request.Context(), contextID)
	if err != nil {
		base.problem(response, err)
		return
	}
	cache := multi.cache(contextID, metadata.LastSubmittedAt)
	if metadata.Availability == "unavailable" {
		cache = newSnapshotCache(multi.background)
	}
	handler := &Handler{session: active, store: multi.store, review: reviewservice.New(active, multi.store), assets: multi.assets, metrics: multi.metrics, cache: cache, strictContext: true}
	scoped := request.Clone(request.Context())
	scoped.URL.Path = scopedPath
	handler.ServeHTTP(response, scoped)
}
