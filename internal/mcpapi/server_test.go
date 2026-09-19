package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeReviewService struct {
	list    func(context.Context, bool) ([]reviewservice.Comment, error)
	resolve func(string) (reviewservice.Resolution, error)
}

func (service fakeReviewService) ListComments(ctx context.Context, includeResolved bool) ([]reviewservice.Comment, error) {
	return service.list(ctx, includeResolved)
}

func (service fakeReviewService) ResolveComment(commentID string) (reviewservice.Resolution, error) {
	return service.resolve(commentID)
}

func connect(t *testing.T, server *httptest.Server, protocolVersion string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "servediff-test", Version: "test"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{},
	})
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           server.Client(),
		DisableStandaloneSSE: true,
	}, &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolsAndCalls(t *testing.T) {
	includeResolved := false
	resolvedID := ""
	service := fakeReviewService{
		list: func(_ context.Context, include bool) ([]reviewservice.Comment, error) {
			includeResolved = include
			return []reviewservice.Comment{{
				ReviewComment: review.ReviewComment{
					ID: "comment-1", Path: "main.go", Scope: review.DiffAll,
					Fingerprint: "fingerprint", Side: "additions", Start: 3, End: 3,
					Code: "+ value", Body: "Check this.", Status: "open", CreatedAt: 1,
				},
				Applicability: "anchored",
				Actionable:    true,
			}}, nil
		},
		resolve: func(commentID string) (reviewservice.Resolution, error) {
			resolvedID = commentID
			return reviewservice.Resolution{CommentID: commentID, Status: "resolved"}, nil
		},
	}
	server := httptest.NewServer(New(service, true, "test"))
	defer server.Close()
	session := connect(t, server, ProtocolVersion)

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 2 {
		t.Fatalf("tools: %#v", listed.Tools)
	}
	tools := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	getter := tools["get_review_comments"]
	resolver := tools["resolve_review_comment"]
	if getter == nil || resolver == nil {
		t.Fatalf("tool names: %#v", tools)
	}
	if getter.InputSchema == nil || getter.OutputSchema == nil || getter.Annotations == nil || !getter.Annotations.ReadOnlyHint || !getter.Annotations.IdempotentHint {
		t.Fatalf("getter: %#v", getter)
	}
	if resolver.InputSchema == nil || resolver.OutputSchema == nil || resolver.Annotations == nil || resolver.Annotations.ReadOnlyHint || !resolver.Annotations.IdempotentHint {
		t.Fatalf("resolver: %#v", resolver)
	}

	getResult, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_review_comments", Arguments: map[string]any{"include_resolved": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if getResult.IsError || len(getResult.Content) != 1 {
		t.Fatalf("get result: %#v", getResult)
	}
	var got getReviewCommentsOutput
	raw, err := json.Marshal(getResult.StructuredContent)
	if err == nil {
		err = json.Unmarshal(raw, &got)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !includeResolved || len(got.Comments) != 1 || got.Comments[0].ID != "comment-1" || !got.Comments[0].Actionable {
		t.Fatalf("get output: %#v", got)
	}

	resolveResult, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "resolve_review_comment", Arguments: map[string]any{"comment_id": "comment-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolveResult.IsError || resolvedID != "comment-1" {
		t.Fatalf("resolve result: %#v, id %q", resolveResult, resolvedID)
	}
}

func TestToolErrorIsVisibleToModel(t *testing.T) {
	service := fakeReviewService{
		list: func(context.Context, bool) ([]reviewservice.Comment, error) { return nil, nil },
		resolve: func(commentID string) (reviewservice.Resolution, error) {
			return reviewservice.Resolution{}, &reviewservice.CommentNotFoundError{CommentID: commentID}
		},
	}
	server := httptest.NewServer(New(service, true, "test"))
	defer server.Close()
	session := connect(t, server, ProtocolVersion)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "resolve_review_comment", Arguments: map[string]any{"comment_id": "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result: %#v", result)
	}
	if text := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "missing") {
		t.Fatalf("error text: %q", text)
	}
}

func TestRequestCancellationReachesService(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	service := fakeReviewService{
		list: func(ctx context.Context, _ bool) ([]reviewservice.Comment, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		},
		resolve: func(string) (reviewservice.Resolution, error) {
			return reviewservice.Resolution{}, errors.New("unexpected resolve")
		},
	}
	server := httptest.NewServer(New(service, true, "test"))
	defer server.Close()
	session := connect(t, server, ProtocolVersion)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_review_comments", Arguments: map[string]any{}})
	}()
	<-started
	cancel()
	<-done
	<-canceled
}

func TestCommentsDisabledAdvertisesNoTools(t *testing.T) {
	service := fakeReviewService{
		list:    func(context.Context, bool) ([]reviewservice.Comment, error) { return nil, nil },
		resolve: func(string) (reviewservice.Resolution, error) { return reviewservice.Resolution{}, nil },
	}
	server := httptest.NewServer(New(service, false, "test"))
	defer server.Close()
	session := connect(t, server, ProtocolVersion)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 0 {
		t.Fatalf("tools: %#v", listed.Tools)
	}
}

func TestLegacyProtocolCannotUseTools(t *testing.T) {
	service := fakeReviewService{
		list:    func(context.Context, bool) ([]reviewservice.Comment, error) { return nil, nil },
		resolve: func(string) (reviewservice.Resolution, error) { return reviewservice.Resolution{}, nil },
	}
	server := httptest.NewServer(New(service, true, "test"))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "legacy-test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           server.Client(),
		DisableStandaloneSSE: true,
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		return
	}
	defer session.Close()
	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("legacy client unexpectedly used tools")
	}
}
