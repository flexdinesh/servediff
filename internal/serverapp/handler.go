// Package serverapp composes stored-data application services and transports.
// Local lifecycle and remote authentication live outside this package.
package serverapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/httpapi"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/mcpapi"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	buildversion "github.com/flexdinesh/servediff/internal/version"
)

func Handler(ctx context.Context, service *contextservice.Service, store *reviewstore.Store, assets fs.FS, defaultID string) http.Handler {
	mux := http.NewServeMux()
	mcp := contextMCP(service, store, defaultID)
	mux.Handle("/mcp", mcp)
	mux.Handle("/mcp/", mcp)
	mux.Handle("/control/", http.NotFoundHandler())
	mux.HandleFunc("/api/v2/ingestions", func(w http.ResponseWriter, r *http.Request) { ingest(service, w, r) })
	mux.HandleFunc("/api/v2/events", func(w http.ResponseWriter, r *http.Request) { stream(ctx, service, w, r) })
	mux.Handle("/", httpapi.NewMultiWithContext(ctx, service, store, assets, defaultID))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !SameOrigin(r) {
			problem(w, 403, "Cross-origin access denied")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// SameOrigin accepts HTTP and HTTPS deployments, including TLS termination.
func SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return r.Header.Get("Sec-Fetch-Site") != "cross-site" && (origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host)
}

func ingest(service *contextservice.Service, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		problem(w, 405, "Method not allowed")
		return
	}
	if media := strings.Split(r.Header.Get("Content-Type"), ";")[0]; media != "application/json" {
		problem(w, 415, "Expected application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, ingestion.MaxRequestBytes))
	decoder.DisallowUnknownFields()
	var input ingestion.Request
	if err := decoder.Decode(&input); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			problem(w, 413, "Snapshot exceeds 64 MiB limit")
		} else {
			problem(w, 400, "Malformed ingestion request")
		}
		return
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		problem(w, 400, "Trailing ingestion data")
		return
	}
	result, err := service.Ingest(r.Context(), input)
	if err != nil {
		applicationError(w, err)
		return
	}
	id := result.Context.ID
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ingestion.Receipt{ContextID: id, ReviewURL: "/contexts/" + id, MCPURL: "/mcp/contexts/" + id, Snapshot: result.Snapshot})
}

func stream(ctx context.Context, service *contextservice.Service, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		problem(w, 405, "Method not allowed")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, "Streaming unavailable")
		return
	}
	events, unsubscribe := service.Subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(value string) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(w, value); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !write(": connected\n\n") {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		case id := <-events:
			raw, _ := json.Marshal(struct {
				ContextID string `json:"contextId"`
			}{id})
			if !write("event: ingestion\ndata: " + string(raw) + "\n\n") {
				return
			}
		case <-ticker.C:
			if !write(": heartbeat\n\n") {
				return
			}
		}
	}
}

func contextMCP(service *contextservice.Service, store *reviewstore.Store, defaultID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := defaultID
		if strings.HasPrefix(r.URL.Path, "/mcp/contexts/") {
			id = strings.TrimPrefix(r.URL.Path, "/mcp/contexts/")
			if id == "" || strings.Contains(id, "/") {
				http.NotFound(w, r)
				return
			}
		} else if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if id == "" {
			page, err := service.List(r.Context(), 2, "")
			if err != nil {
				applicationError(w, err)
				return
			}
			if len(page.Contexts) != 1 || page.NextCursor != nil {
				problem(w, 409, "Use /mcp/contexts/{contextId}")
				return
			}
			id = page.Contexts[0].ID
		}
		active, err := service.Resolve(r.Context(), id)
		if err != nil {
			applicationError(w, err)
			return
		}
		mcpapi.New(reviewservice.New(active, store), active.Capabilities.Review.Comments.Enabled(), buildversion.String()).ServeHTTP(w, r)
	})
}

func applicationError(w http.ResponseWriter, err error) {
	var request *diffsource.RequestError
	if errors.As(err, &request) {
		problem(w, request.Status, request.Detail)
		return
	}
	problem(w, 500, "Server could not process request")
}
func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}{"about:blank", http.StatusText(status), status, detail})
}
