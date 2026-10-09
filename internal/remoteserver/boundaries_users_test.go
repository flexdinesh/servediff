package remoteserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/mcpapi"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/testsupport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunDefaultAdminPersistsAcrossRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	var token, originalStateID string
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithCancel(t.Context())
		ready, done := make(chan string, 1), make(chan error, 1)
		bootstrapPath := make(chan string, 1)
		go func() {
			done <- Run(ctx, Settings{State: state, Listen: "127.0.0.1:0", BootstrapReady: func(path string) { bootstrapPath <- path }}, func(address string) { ready <- address })
		}()
		stop := func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("server shutdown timed out")
			}
		}
		var address string
		select {
		case address = <-ready:
		case err := <-done:
			cancel()
			t.Fatalf("server startup failed: %v", err)
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("server startup timed out")
		}
		if attempt == 0 {
			select {
			case path := <-bootstrapPath:
				if path != state+".admin-token" {
					stop()
					t.Fatal("unexpected credential path")
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					stop()
					t.Fatal(err)
				}
				token = strings.TrimSpace(string(raw))
			default:
				stop()
				t.Fatal("bootstrap credential path was not reported")
			}
		}
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+address+"/api/v2/health", nil)
		if err != nil {
			stop()
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if err != nil {
			stop()
			t.Fatal(err)
		}
		var health ingestion.Health
		err = json.NewDecoder(response.Body).Decode(&health)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || health.StateID == "" {
			stop()
			t.Fatalf("persisted admin authentication failed: status %d error %v", response.StatusCode, err)
		}
		if attempt == 0 {
			originalStateID = health.StateID
		} else if originalStateID != health.StateID {
			stop()
			t.Fatal("restart changed authenticated user identity")
		}
		stop()
	}
}

type credentialTransport struct {
	base  http.RoundTripper
	token string
}

func (transport credentialTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(request)
}

func TestGlobalMCPOwnershipAcrossUsers(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tokens := []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	for index, name := range []string{"admin", "other"} {
		if _, err := store.ProvisionUser(name, tokens[index]); err != nil {
			t.Fatal(err)
		}
	}
	handler, closeServices, err := MultiHandler(t.Context(), store, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeServices()
	server := httptest.NewServer(handler)
	defer server.Close()
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-before\n+after\n"
	input, err := collector.CollectPatch(t.Context(), patch, t.TempDir(), collector.Options{SourceID: "shared", SubmissionID: "same"})
	if err != nil {
		t.Fatal(err)
	}
	contexts := make([]string, 2)
	for index, token := range tokens {
		receipt, err := testsupport.NewClient(server.URL, token).Submit(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		contexts[index] = receipt.ContextID
	}
	for index, token := range tokens {
		client := mcp.NewClient(&mcp.Implementation{Name: "ownership-test", Version: "1"}, nil)
		session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: credentialTransport{server.Client().Transport, token}}, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: mcpapi.ProtocolVersion})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_contexts", Arguments: map[string]string{}})
		if err != nil || result.IsError {
			t.Fatalf("list_contexts failed: %v", err)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), contexts[index]) || strings.Contains(string(raw), contexts[1-index]) {
			t.Fatal("global MCP catalog crossed user boundary")
		}
		for _, name := range []string{"get_diff", "get_review_comments", "resolve_review_comment", "get_file_patch"} {
			arguments := map[string]string{"context_id": contexts[1-index]}
			if name == "resolve_review_comment" {
				arguments["comment_id"] = "foreign-comment"
			}
			if name == "get_file_patch" {
				arguments["file_id"] = "foreign-file"
			}
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("foreign context accepted by %s", name)
			}
		}
		result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_diff", Arguments: map[string]string{"context_id": contexts[index]}})
		if err != nil || result.IsError {
			t.Fatalf("own global MCP diff failed: %v", err)
		}
	}
}
