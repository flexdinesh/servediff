package ingestion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientReceiptURLsAndBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v2/ingestions" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("ingestion request: %s %s %v", r.Method, r.URL, r.Header)
		}
		w.WriteHeader(201)
		_, _ = fmt.Fprint(w, `{"contextId":"observation","reviewUrl":"/contexts/observation","mcpUrl":"/mcp/contexts/observation"}`)
	}))
	defer server.Close()
	receipt, err := NewClient(server.URL+"/", "secret").Submit(t.Context(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ReviewURL != server.URL+"/contexts/observation" || receipt.MCPURL != server.URL+"/mcp/contexts/observation" {
		t.Fatalf("receipt: %#v", receipt)
	}
}

func TestClientRejectsUnsafeEndpointsAndReceipts(t *testing.T) {
	for _, endpoint := range []string{"file:///tmp/server", "http://user:password@example.com", "https://example.com/path", "https://example.com?token=secret", "https://example.com#fragment", "example.com"} {
		if _, err := NewClient(endpoint, "secret").Submit(t.Context(), validRequest()); err == nil {
			t.Errorf("endpoint accepted: %s", endpoint)
		}
	}
	for _, body := range []string{
		`{"contextId":"x","reviewUrl":"https://foreign.example/review","mcpUrl":"/mcp"}`,
		`{"contextId":"x","reviewUrl":"/review","mcpUrl":"//foreign.example/mcp"}`,
		`{"reviewUrl":"/review","mcpUrl":"/mcp"}`,
		`{"contextId":"x"}`,
		`{"contextId":`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "").Submit(t.Context(), validRequest()); err == nil {
				t.Fatal("unsafe/malformed receipt accepted")
			}
		})
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer server.Close()
	_, err := NewClient(server.URL, "secret").Submit(t.Context(), validRequest())
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 307 {
		t.Fatalf("redirect error: %v", err)
	}
	if called {
		t.Fatal("ingestion credentials/payload forwarded through redirect")
	}
}

func TestClientTruncatedResponseRemainsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = fmt.Fprint(w, `{"contextId":`)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "").Submit(t.Context(), validRequest())
	var transport *url.Error
	if !errors.As(err, &transport) {
		t.Fatalf("ambiguous transport failure must remain retryable: %T %v", err, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewClient(server.URL, "").Submit(ctx, validRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled submission: %v", err)
	}
}

func TestClientPreservesHTTPProblemStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = fmt.Fprint(w, `{"status":200,"detail":"Submission identity already used"}`)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "").Submit(t.Context(), validRequest())
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 409 || !strings.Contains(problem.Detail, "identity") {
		t.Fatalf("HTTP problem: %v", err)
	}
}
