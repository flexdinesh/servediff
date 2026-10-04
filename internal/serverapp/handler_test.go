package serverapp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/mcpapi"
	"github.com/flexdinesh/servediff/internal/remoteserver"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/serverapp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const token = "test-token-at-least-thirty-two-bytes-long"

type deployment struct {
	server *httptest.Server
	store  *reviewstore.Store
	owner  string
	token  string
	close  func()
}

func start(t *testing.T, state string, remote bool) deployment {
	t.Helper()
	store, err := reviewstore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": {Data: []byte("stored review UI")}}
	var handler http.Handler
	var closeService func() error
	identity, credential := "local-test", ""
	if remote {
		identity, credential = "account:team", token
	}
	user, err := store.User(identity, "team")
	if err != nil {
		t.Fatal(err)
	}
	if remote {
		handler, closeService, err = remoteserver.Handler(t.Context(), store, "team", token, assets)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		service := contextservice.NewWithContext(t.Context(), store, user)
		handler, closeService = serverapp.Handler(t.Context(), service, store, assets, ""), service.Close
	}
	server := httptest.NewServer(handler)
	var once sync.Once
	close := func() { once.Do(func() { server.Close(); _ = closeService(); _ = store.Close() }) }
	t.Cleanup(close)
	return deployment{server, store, user.ID, credential, close}
}

func request(t *testing.T, d deployment, method, path string, body io.Reader) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, d.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if d.token != "" {
		r.Header.Set("Authorization", "Bearer "+d.token)
	}
	response, err := d.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer response.Body.Close()
	var result T
	if response.StatusCode != 200 {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("query status %d: %s", response.StatusCode, raw)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func collectGit(t *testing.T) ingestion.Request {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, raw)
		}
	}
	git("init")
	git("symbolic-ref", "HEAD", "refs/heads/shared")
	git("remote", "add", "origin", "https://example.com/acme/project.git")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "container-a", Hostname: "sandbox", RunID: "job-a", Trigger: "agent-hook", SubmissionID: "turn-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	return input
}

// The public protocol, rather than a storage shortcut, carries producer data
// into both deployment compositions. Neither checkout nor Git exists at query time.
func TestPublicIngestionSurvivesProducerRemovalAndServerRestart(t *testing.T) {
	input := collectGit(t)
	t.Setenv("PATH", t.TempDir())
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%v", remote), func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "reviews.sqlite")
			d := start(t, state, remote)
			client := ingestion.NewClient(d.server.URL, d.token)
			first, err := client.Submit(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			retry, err := client.Submit(t.Context(), input)
			if err != nil || !reflect.DeepEqual(first, retry) {
				t.Fatalf("retry receipt changed: %v %#v %#v", err, first, retry)
			}
			changed := input
			changed.Metadata.Hostname = "different"
			_, err = client.Submit(t.Context(), changed)
			var problem *ingestion.Problem
			if !errors.As(err, &problem) || problem.Status != 409 {
				t.Fatalf("conflicting retry: %v", err)
			}
			other := input
			other.Metadata.SourceID, other.Metadata.RunID = "container-b", "job-b"
			other.Metadata.Trigger = "manual"
			second, err := client.Submit(t.Context(), other)
			if err != nil {
				t.Fatal(err)
			}
			if first.ContextID == second.ContextID {
				t.Fatal("independent same-branch sources collapsed")
			}
			if _, err := d.store.Observation(d.owner, first.ContextID); err != nil {
				t.Fatalf("snapshot not owned by server principal: %v", err)
			}
			foreign, err := d.store.User("account:other", "other")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.store.Observation(foreign.ID, first.ContextID); !errors.Is(err, reviewstore.ErrNotFound) {
				t.Fatalf("cross-account observation exposed: %v", err)
			}
			file := first.Snapshot.Files[0]
			comment := review.ReviewComment{ID: "retained-comment", DiffID: first.Snapshot.ID, VersionID: first.Snapshot.VersionID, Path: file.Path, Scope: review.DiffAll, Fingerprint: file.Fingerprint, Side: "additions", Start: 1, End: 1, Code: "after", Body: "Check this change", Status: "open", CreatedAt: 1}
			if err := d.store.PutComment(first.ContextID, comment); err != nil {
				t.Fatal(err)
			}
			d.close()
			d = start(t, state, remote)
			page := decode[contextservice.Page](t, request(t, d, "GET", "/api/v2/contexts?repository=project&branch=shared", nil))
			if len(page.Contexts) != 2 {
				t.Fatalf("stored source catalog: %#v", page)
			}
			filtered := decode[contextservice.Page](t, request(t, d, "GET", "/api/v2/contexts?sourceId=container-b&runId=job-b&hostname=sandbox", nil))
			if len(filtered.Contexts) != 1 || filtered.Contexts[0].ID != second.ContextID {
				t.Fatalf("source filter: %#v", filtered)
			}
			base := "/api/v2/contexts/" + first.ContextID
			for _, mode := range []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged} {
				snapshot := decode[review.RepositoryDiff](t, request(t, d, "GET", base+"/diffs/current?scope="+string(mode), nil))
				if snapshot.Mode != mode || snapshot.VersionID == "" {
					t.Fatalf("stored scope: %#v", snapshot)
				}
				version := decode[review.RepositoryDiff](t, request(t, d, "GET", base+"/diffs/"+snapshot.ID+"/versions/"+snapshot.VersionID, nil))
				if !reflect.DeepEqual(version, snapshot) {
					t.Fatalf("immutable version changed: %#v %#v", snapshot, version)
				}
				for _, file := range snapshot.Files {
					fileBase := base + "/diffs/" + snapshot.ID + "/files/" + file.ID
					query := "?scope=" + string(mode) + "&versionId=" + snapshot.VersionID + "&fileVersion=" + file.Fingerprint
					patch := decode[review.FilePatch](t, request(t, d, "GET", fileBase+"/patch"+query, nil))
					if !strings.Contains(patch.Patch, "+after") {
						t.Fatalf("stored patch: %#v", patch)
					}
					contents := decode[review.FileContents](t, request(t, d, "GET", fileBase+"/contents"+query, nil))
					if contents.Before != "before\n" || contents.After != "after\n" {
						t.Fatalf("stored contents: %#v", contents)
					}
				}
			}
			mcpClient := mcp.NewClient(&mcp.Implementation{Name: "stored-observation-test", Version: "test"}, nil)
			httpClient := &http.Client{Transport: authorizedTransport{base: d.server.Client().Transport, token: d.token}}
			mcpSession, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: d.server.URL + "/mcp/contexts/" + first.ContextID, HTTPClient: httpClient, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: mcpapi.ProtocolVersion})
			if err != nil {
				t.Fatal(err)
			}
			defer mcpSession.Close()
			result, err := mcpSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_review_comments", Arguments: map[string]bool{}})
			if err != nil || result.IsError {
				t.Fatalf("stored MCP comment query: %#v %v", result, err)
			}
			raw, err := json.Marshal(result.StructuredContent)
			if err != nil || !strings.Contains(string(raw), "retained-comment") || !strings.Contains(string(raw), "Check this change") {
				t.Fatalf("retained MCP comments: %s %v", raw, err)
			}
		})
	}
}

type authorizedTransport struct {
	base  http.RoundTripper
	token string
}

func (transport authorizedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	if transport.token != "" {
		request.Header.Set("Authorization", "Bearer "+transport.token)
	}
	base := transport.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(request)
}

func emptyRequest() ingestion.Request {
	return ingestion.Request{ProtocolVersion: ingestion.ProtocolVersion, SubmissionID: "one", Metadata: ingestion.Metadata{SourceID: "source", Trigger: "manual", CollectedAt: 1, RepositoryKey: "repo", CheckoutKey: "checkout"}, Scopes: []ingestion.Scope{{Snapshot: review.RepositoryDiff{Source: "local", Mode: review.DiffAll, Revision: "revision", Files: []review.ChangedFile{}}, Patches: map[string]review.FilePatch{}}}}
}

func TestIngestionHTTPRejectsMalformedAndAmbiguousInput(t *testing.T) {
	d := start(t, "", false)
	raw, err := json.Marshal(emptyRequest())
	if err != nil {
		t.Fatal(err)
	}
	unsupported := emptyRequest()
	unsupported.ProtocolVersion++
	unsupportedRaw, err := json.Marshal(unsupported)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, contentType, body string
		status                          int
	}{
		{"wrong method", "GET", "application/json", "", 405},
		{"wrong media", "POST", "text/plain", string(raw), 415},
		{"malformed", "POST", "application/json", `{"`, 400},
		{"unknown identity", "POST", "application/json", `{"userId":"other"}`, 400},
		{"unknown nested field", "POST", "application/json", strings.Replace(string(raw), `"sourceId":`, `"userId":"other","sourceId":`, 1), 400},
		{"trailing JSON", "POST", "application/json", string(raw) + ` {}`, 400},
		{"trailing garbage", "POST", "application/json", string(raw) + ` x`, 400},
		{"unsupported protocol", "POST", "application/json", string(unsupportedRaw), 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v2/ingestions", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			d.server.Config.Handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	// One giant field tests the body bound rather than merely a validation bound.
	oversize := io.MultiReader(strings.NewReader(`{"submissionId":"`), strings.NewReader(strings.Repeat("x", ingestion.MaxRequestBytes)), strings.NewReader(`"}`))
	r := httptest.NewRequest("POST", "/api/v2/ingestions", oversize)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.server.Config.Handler.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized body status %d", w.Code)
	}
	page := decode[contextservice.Page](t, request(t, d, "GET", "/api/v2/contexts", nil))
	if len(page.Contexts) != 0 {
		t.Fatal("rejected input created observations")
	}
}

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin, fetch string
		allow         bool
	}{
		{"", "", true}, {"http://reviews.example", "same-origin", true}, {"https://reviews.example", "same-origin", true},
		{"https://other.example", "", false}, {"https://reviews.example.evil", "", false}, {"", "cross-site", false}, {"https://reviews.example", "cross-site", false},
	} {
		r := httptest.NewRequest("POST", "http://reviews.example/api/v2/ingestions", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.fetch)
		if got := serverapp.SameOrigin(r); got != tc.allow {
			t.Errorf("origin=%q fetch=%q: %v", tc.origin, tc.fetch, got)
		}
	}
}

func TestIngestionNotificationReferencesCommittedObservation(t *testing.T) {
	d := start(t, "", false)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", d.server.URL+"/api/v2/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := d.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
		t.Fatalf("SSE connected: %q %v", line, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	receipt, err := ingestion.NewClient(d.server.URL, "").Submit(t.Context(), emptyRequest())
	if err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "event: ingestion\n" {
		t.Fatalf("SSE event: %q %v", line, err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, receipt.ContextID) {
		t.Fatalf("SSE context: %q %v", line, err)
	}
	if _, err := d.store.ObservationSnapshot(d.owner, receipt.ContextID, review.DiffAll); err != nil {
		t.Fatalf("notification before commit: %v", err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	// Notifications are advisory; reconnecting clients recover the catalog via GET.
	page := decode[contextservice.Page](t, request(t, d, "GET", "/api/v2/contexts", nil))
	if len(page.Contexts) != 1 || page.Contexts[0].ID != receipt.ContextID {
		t.Fatalf("catalog recovery: %#v", page)
	}
}

func TestCrossOriginSubmissionCannotCommit(t *testing.T) {
	d := start(t, "", false)
	raw, err := json.Marshal(emptyRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://reviews.example/api/v2/ingestions", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://foreign.example")
	w := httptest.NewRecorder()
	d.server.Config.Handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin submission: %d", w.Code)
	}
}
