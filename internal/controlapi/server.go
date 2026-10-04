package controlapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/ingestion"
)

type contextService interface {
	Register(context.Context, string, string) (contextservice.Submission, error)
	Capture(context.Context, string, string, string) (contextservice.Submission, error)
	OpenCapture(context.Context, string) (contextservice.Submission, error)
}

type handler struct {
	token    string
	service  contextService
	status   func() (Status, error)
	shutdown func()
	draining atomic.Bool
	parses   chan struct{}
}

func New(token string, service *contextservice.Service, status func() (Status, error), shutdown func()) http.Handler {
	return newHandler(token, service, status, shutdown)
}

func newHandler(token string, service contextService, status func() (Status, error), shutdown func()) *handler {
	return &handler{token: token, service: service, status: status, shutdown: shutdown, parses: make(chan struct{}, 2)}
}

func (handler *handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Header.Get("Origin") != "" || request.Header.Get("Sec-Fetch-Site") != "" || request.Header.Get("Sec-Fetch-Mode") != "" {
		writeProblem(response, http.StatusForbidden, "browser_request", "Browser access to service control denied")
		return
	}
	if handler.token == "" || subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte("Bearer "+handler.token)) != 1 {
		writeProblem(response, http.StatusUnauthorized, "unauthorized", "Service control authentication failed")
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/control/v1/status" {
		value, err := handler.status()
		if err != nil {
			writeError(response, err)
			return
		}
		if handler.draining.Load() {
			value.State = "draining"
		}
		writeJSON(response, http.StatusOK, value)
		return
	}
	if request.Method != http.MethodPost {
		writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
		return
	}
	if request.URL.Path == "/control/v1/shutdown" {
		first := handler.draining.CompareAndSwap(false, true)
		writeJSON(response, http.StatusOK, struct{}{})
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		if first {
			go handler.shutdown()
		}
		return
	}
	if handler.draining.Load() {
		writeProblem(response, http.StatusServiceUnavailable, "service_draining", "Service is stopping")
		return
	}
	var result contextservice.Submission
	var err error
	switch {
	case request.URL.Path == "/control/v1/ingestions":
		ingestService, ok := handler.service.(interface {
			Ingest(context.Context, ingestion.Request) (contextservice.Submission, error)
		})
		if !ok {
			err = diffsource.Error(503, "Ingestion unavailable")
			break
		}
		var input ingestion.Request
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, ingestion.MaxRequestBytes))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err == nil {
			var trailing interface{}
			if tail := decoder.Decode(&trailing); tail != io.EOF {
				err = diffsource.Error(400, "Trailing ingestion data")
			} else {
				result, err = ingestService.Ingest(request.Context(), input)
			}
		} else {
			var max *http.MaxBytesError
			if !errors.As(err, &max) {
				err = diffsource.Error(400, "Malformed ingestion request")
			}
		}
	case request.URL.Path == "/control/v1/worktrees":
		var input struct {
			SubmissionID string `json:"submissionId"`
			Path         string `json:"path"`
		}
		if err = decodeJSON(response, request, &input); err == nil {
			if input.SubmissionID == "" || input.Path == "" {
				err = diffsource.Error(http.StatusBadRequest, "submissionId and path required")
			} else {
				result, err = handler.service.Register(request.Context(), input.SubmissionID, input.Path)
			}
		}
	case request.URL.Path == "/control/v1/captures":
		select {
		case handler.parses <- struct{}{}:
			defer func() { <-handler.parses }()
		default:
			writeProblem(response, http.StatusServiceUnavailable, "service_overloaded", "Capture parser busy; retry submission")
			return
		}
		if request.Header.Get(submissionHeader) == "" {
			err = diffsource.Error(http.StatusBadRequest, "submission ID required")
			break
		}
		var submittedFrom []byte
		submittedFrom, err = base64.RawURLEncoding.DecodeString(request.Header.Get(submittedFromHeader))
		if err != nil || len(submittedFrom) > 32<<10 {
			err = diffsource.Error(http.StatusBadRequest, "Invalid submission directory")
			break
		}
		var raw []byte
		raw, err = io.ReadAll(http.MaxBytesReader(response, request.Body, MaxPatchBytes))
		if err == nil {
			result, err = handler.service.Capture(request.Context(), request.Header.Get(submissionHeader), string(raw), string(submittedFrom))
		}
	case strings.HasPrefix(request.URL.Path, "/control/v1/captures/") && strings.HasSuffix(request.URL.Path, "/open"):
		id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/control/v1/captures/"), "/open")
		if id == "" || strings.Contains(id, "/") {
			err = diffsource.Error(http.StatusNotFound, "Capture not found")
		} else {
			result, err = handler.service.OpenCapture(request.Context(), id)
		}
	default:
		err = diffsource.Error(http.StatusNotFound, "Control endpoint not found")
	}
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, value interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return err
		}
		return diffsource.Error(http.StatusBadRequest, "Malformed control request")
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return diffsource.Error(http.StatusBadRequest, "Trailing control request data")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value interface{}) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeProblem(response http.ResponseWriter, status int, code, detail string) {
	response.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail})
}

func writeError(response http.ResponseWriter, err error) {
	var limit *http.MaxBytesError
	var requestError *diffsource.RequestError
	switch {
	case errors.As(err, &limit):
		writeProblem(response, http.StatusRequestEntityTooLarge, "input_too_large", "Input exceeds control request limit")
	case errors.As(err, &requestError):
		writeProblem(response, requestError.Status, "request_failed", requestError.Detail)
	default:
		writeProblem(response, http.StatusInternalServerError, "request_failed", err.Error())
	}
}
