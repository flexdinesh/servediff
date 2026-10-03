package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

type catalogProvider interface {
	Repositories(context.Context) ([]reviewstore.Repository, error)
	Worktrees(context.Context, string) ([]contextservice.Context, error)
	Subscribe() (<-chan contextservice.ChangeEvent, func())
}

func (multi *Multi) collectionRoutes(response http.ResponseWriter, request *http.Request, base *Handler) bool {
	path := request.URL.Path
	if path != "/api/v2/repositories" && path != "/api/v2/events" && !strings.HasPrefix(path, "/api/v2/repositories/") {
		return false
	}
	provider, ok := multi.provider.(catalogProvider)
	if !ok {
		base.problem(response, diffsource.Error(404, "Unknown API route"))
		return true
	}
	if request.Method != http.MethodGet {
		base.problem(response, diffsource.Error(405, "Method not allowed"))
		return true
	}
	if path == "/api/v2/events" {
		multi.events(response, request, provider)
		return true
	}
	if path == "/api/v2/repositories" {
		items, err := provider.Repositories(request.Context())
		if err != nil {
			base.problem(response, err)
		} else {
			_ = writeJSON(response, 200, map[string]interface{}{"repositories": items})
		}
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v2/repositories/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "worktrees" {
		base.problem(response, diffsource.Error(404, "Unknown API route"))
		return true
	}
	items, err := provider.Worktrees(request.Context(), parts[0])
	if err != nil {
		base.problem(response, err)
	} else {
		_ = writeJSON(response, 200, map[string]interface{}{"worktrees": items})
	}
	return true
}

func (multi *Multi) events(response http.ResponseWriter, request *http.Request, provider catalogProvider) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("X-Accel-Buffering", "no")
	events, unsubscribe := provider.Subscribe()
	defer unsubscribe()
	if _, err := fmt.Fprint(response, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-multi.background.Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			raw, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(response, "data: %s\n\n", raw); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(response, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
