package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/mcpapi"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/serverapp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverPatch = "diff --git a/value.txt b/value.txt\n--- a/value.txt\n+++ b/value.txt\n@@ -1 +1 @@\n-old\n+new\n"

func TestRunForcesCloseWhenIdleConnectionExhaustsShutdownGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan Status, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Settings{Host: "127.0.0.1", Port: 0, State: "memory"}, "", nil,
			func(status Status, _ *contextservice.Submission) { ready <- status })
	}()
	var status Status
	select {
	case status = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timed out")
	}
	address, err := url.Parse(status.BrowserURL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", address.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// An incomplete request remains active beyond the shutdown grace period.
	if _, err := connection.Write([]byte("GET / HTTP/1.1\r\nHost: ")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("forced shutdown should succeed: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("shutdown exceeded grace and cleanup budget")
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, connection); err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("connection survived service shutdown")
		}
	}
}

func TestRunSeparatesControlAndPublicEndpoints(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	runtimeDir := t.TempDir()
	ready := make(chan Status, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Settings{Host: "127.0.0.1", Port: 0, State: "memory"}, runtimeDir, nil, func(status Status, _ *contextservice.Submission) { ready <- status })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server failed to stop")
		}
	})
	var status Status
	select {
	case status = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("startup timed out")
	}
	for _, path := range []string{"/control/v1/status", "/control/v1/shutdown"} {
		response, err := http.Get(status.BrowserURL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("public control %s = %d", path, response.StatusCode)
		}
	}
	response, err := http.Get(status.BrowserURL + "/api/v2/contexts")
	if err != nil {
		t.Fatal(err)
	}
	var catalog contextservice.Page
	err = json.NewDecoder(response.Body).Decode(&catalog)
	_ = response.Body.Close()
	if err != nil || len(catalog.Contexts) != 0 {
		t.Fatalf("empty catalog: %#v %v", catalog, err)
	}
	request, err := http.NewRequest(http.MethodGet, status.BrowserURL+"/api/v2/contexts", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example"
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("foreign host = %d", response.StatusCode)
	}
	descriptor, err := ReadDescriptor(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(descriptor.Endpoint + "/control/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("unauthenticated control = %d", response.StatusCode)
	}
	client := controlapi.NewClient(descriptor.Endpoint, descriptor.Token)
	submitted, err := client.Capture(t.Context(), "capture-one", []byte(serverPatch), "")
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(status.BrowserURL + "/api/v2/contexts/" + submitted.Context.ID + "/diffs/current?scope=all")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("captured diff = %d", response.StatusCode)
	}
	if err := client.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestScopedMCPDoesNotShareReviewContext(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.User("test", "test")
	if err != nil {
		t.Fatal(err)
	}
	service := contextservice.New(store, user)
	a, err := service.Capture(t.Context(), "a", serverPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Capture(t.Context(), "b", serverPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	comment := review.ReviewComment{ID: "a-comment", DiffID: a.Context.ID, VersionID: a.Snapshot.VersionID, Path: "value.txt", Scope: review.DiffAll, Fingerprint: a.Snapshot.Files[0].Fingerprint, Side: "additions", Start: 1, End: 1, Body: "Only A", Status: "open", CreatedAt: 1}
	if err := store.PutComment(a.Context.ID, comment); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(serverapp.Handler(t.Context(), service, store, nil, ""))
	defer server.Close()
	response, err := http.Get(server.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("global MCP standalone GET = %d", response.StatusCode)
	}
	connect := func(id string) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
		session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp/contexts/" + id, HTTPClient: server.Client(), DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: mcpapi.ProtocolVersion})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	sessionA, sessionB := connect(a.Context.ID), connect(b.Context.ID)
	result, err := sessionA.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_review_comments", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("A comments: %#v %v", result, err)
	}
	var text strings.Builder
	for _, block := range result.Content {
		if content, ok := block.(*mcp.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	if !strings.Contains(text.String(), "Only A") {
		t.Fatalf("A comment missing: %s", text.String())
	}
	result, err = sessionB.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_review_comments", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("B comments: %#v %v", result, err)
	}
	text.Reset()
	for _, block := range result.Content {
		if content, ok := block.(*mcp.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	if strings.Contains(text.String(), "Only A") {
		t.Fatal("A comment leaked into B")
	}
	result, err = sessionB.CallTool(t.Context(), &mcp.CallToolParams{Name: "resolve_review_comment", Arguments: map[string]any{"comment_id": "a-comment"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("foreign comment resolved")
	}
	comments, err := store.Comments(a.Context.ID)
	if err != nil || len(comments) != 1 || comments[0].Status != "open" {
		t.Fatalf("foreign mutation: %#v %v", comments, err)
	}
}

func TestDefaultPortExhaustionLeavesListenersRunning(t *testing.T) {
	var listeners []net.Listener
	for port := 7981; port <= 7990; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listeners = append(listeners, listener)
			t.Cleanup(func() { _ = listener.Close() })
		}
	}
	listener, err := listenWeb(Settings{Host: "127.0.0.1", Port: -1})
	if err == nil {
		_ = listener.Close()
		t.Fatal("all default ports should be occupied")
	}
	for _, listener := range listeners {
		connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatalf("listener affected: %v", err)
		}
		_ = connection.Close()
	}
}
