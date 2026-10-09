package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/processmetrics"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewdata"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/flexdinesh/servediff/internal/session"
	contract "github.com/flexdinesh/servediff/packages/api"
)

// Provider resolves persistent identities into request-local source bindings.
type Provider interface {
	Resolve(context.Context, string) (session.Session, error)
	ListFiltered(context.Context, int, string, ingestion.Filter) (contextservice.Page, error)
	Get(context.Context, string) (contextservice.Context, error)
	Delete(context.Context, string) error
	UserID() string
}

type Multi struct {
	provider Provider
	store    Store
	assets   fs.FS
	metrics  *processmetrics.Collector
}

// NewMultiWithContext ties shared snapshot work to the service lifetime.
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
			base.problem(response, review.Error(405, "Method not allowed"))
			return
		}
		response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		if request.Method == http.MethodGet {
			_, _ = response.Write(contract.OpenAPI)
		}
		return
	}
	if pathname == "/api/v2/metrics" {
		if request.Method != http.MethodGet {
			base.problem(response, review.Error(405, "Method not allowed"))
			return
		}
		_ = writeJSON(response, 200, multi.metrics.Collect())
		return
	}
	if pathname == "/api/v2/contexts" {
		if request.Method != http.MethodGet {
			base.problem(response, review.Error(405, "Method not allowed"))
			return
		}
		limit := 100
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 500 {
				base.problem(response, review.Error(400, "Limit must be between 1 and 500"))
				return
			}
			limit = parsed
		}
		var page contextservice.Page
		var err error
		query := request.URL.Query()
		filter := ingestion.Filter{Query: query.Get("q"), Repository: query.Get("repository"), Branch: query.Get("branch"), Worktree: query.Get("worktree"), Hostname: query.Get("hostname"), SourceID: query.Get("sourceId"), RunID: query.Get("runId"), Harness: query.Get("harness"), SessionID: query.Get("sessionId"), SessionName: query.Get("sessionName")}
		page, err = multi.provider.ListFiltered(request.Context(), limit, query.Get("cursor"), filter)
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
			base.problem(response, review.Error(404, "Context not found"))
			return
		}
		if len(parts) == 1 {
			if request.Method == http.MethodDelete {
				if err := multi.provider.Delete(request.Context(), contextID); err != nil {
					base.problem(response, err)
					return
				}
				response.WriteHeader(http.StatusNoContent)
				return
			}
			if request.Method != http.MethodGet {
				base.problem(response, review.Error(405, "Method not allowed"))
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
		scopedPath = "/api/review/" + parts[1]
	} else if strings.HasPrefix(pathname, "/api/") {
		base.problem(response, review.Error(404, "Unknown API route"))
		return
	} else {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			base.problem(response, review.Error(405, "Method not allowed"))
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
		if errors.Is(err, reviewdata.ErrNotFound) {
			err = review.Error(404, "Context not found")
		}
		base.problem(response, err)
		return
	}
	handler := &Handler{session: active, store: multi.store, review: reviewservice.New(active, multi.store), assets: multi.assets, metrics: multi.metrics}
	scoped := request.Clone(request.Context())
	scoped.URL.Path = scopedPath
	handler.ServeHTTP(response, scoped)
}

func NewMulti(provider Provider, store Store, assets fs.FS) http.Handler {
	return &Multi{provider: provider, store: store, assets: assets, metrics: processmetrics.New()}
}
