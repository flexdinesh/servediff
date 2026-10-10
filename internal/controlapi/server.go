package controlapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/flexdinesh/diffx/internal/review"
)

type handler struct {
	token    string
	status   func() (Status, error)
	shutdown func()
	draining atomic.Bool
}

func New(token string, status func() (Status, error), shutdown func()) http.Handler {
	return &handler{token: token, status: status, shutdown: shutdown}
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
	writeProblem(response, 404, "not_found", "Control endpoint not found")
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
	var requestError *review.RequestError
	switch {
	case errors.As(err, &limit):
		writeProblem(response, http.StatusRequestEntityTooLarge, "input_too_large", "Input exceeds control request limit")
	case errors.As(err, &requestError):
		writeProblem(response, requestError.Status, "request_failed", requestError.Detail)
	default:
		writeProblem(response, http.StatusInternalServerError, "request_failed", err.Error())
	}
}
