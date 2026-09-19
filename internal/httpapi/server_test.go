package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
)

func request(t *testing.T, client *http.Client, method, url string, body any) *http.Response {
	t.Helper()
	var content io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		content = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, content)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer response.Body.Close()
	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAPIReviewWorkflow(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	source, err := diffsource.OpenPatch(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	handler := New(session.Resolve(source, session.Policies{}), store, fstest.MapFS{"index.html": {Data: []byte("web")}})
	server := httptest.NewServer(handler)
	defer server.Close()
	emptyCommentsResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/comments", nil)
	if emptyCommentsResponse.StatusCode != http.StatusOK {
		t.Fatalf("comments status: %d", emptyCommentsResponse.StatusCode)
	}
	emptyComments := decode[struct {
		Comments []review.ReviewComment `json:"comments"`
	}](t, emptyCommentsResponse)
	if emptyComments.Comments == nil || len(emptyComments.Comments) != 0 {
		t.Fatalf("expected empty comment array, got %#v", emptyComments.Comments)
	}
	for _, endpoint := range []string{
		"/api/v1/comments/export?scope=",
		"/api/v1/comments/export?includeResolved=",
	} {
		response := request(t, server.Client(), http.MethodGet, server.URL+endpoint, nil)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status: %d", endpoint, response.StatusCode)
		}
		response.Body.Close()
	}
	diffResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/diffs/current?scope=all", nil)
	if diffResponse.StatusCode != http.StatusOK {
		t.Fatalf("diff status: %d", diffResponse.StatusCode)
	}
	snapshot := decode[review.RepositoryDiff](t, diffResponse)
	file := snapshot.Files[0]
	emptyMark := request(t, server.Client(), http.MethodPut, server.URL+"/api/v1/review-marks/"+file.ID+"?scope=all", map[string]string{"fileVersion": ""})
	if emptyMark.StatusCode != http.StatusConflict {
		t.Fatalf("empty mark status: %d", emptyMark.StatusCode)
	}
	emptyMark.Body.Close()
	createdResponse := request(t, server.Client(), http.MethodPost, server.URL+"/api/v1/comments", map[string]any{
		"diffId": snapshot.Revision, "fileId": file.ID, "scope": "all", "fileVersion": file.Fingerprint,
		"side": "additions", "start": 1, "end": 1, "body": "Keep the new value.",
	})
	if createdResponse.StatusCode != http.StatusCreated {
		detail, _ := io.ReadAll(createdResponse.Body)
		t.Fatalf("create status: %d: %s", createdResponse.StatusCode, detail)
	}
	created := decode[review.ReviewComment](t, createdResponse)
	if created.Code != "+ export const value = 2;" || created.Origin == nil {
		t.Fatalf("created comment: %#v", created)
	}
	markResponse := request(t, server.Client(), http.MethodPut, server.URL+"/api/v1/review-marks/"+file.ID+"?scope=all", map[string]string{"fileVersion": file.Fingerprint})
	if markResponse.StatusCode != http.StatusOK {
		t.Fatalf("mark status: %d", markResponse.StatusCode)
	}
	markResponse.Body.Close()
	exportResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/comments/export", nil)
	exported, _ := io.ReadAll(exportResponse.Body)
	exportResponse.Body.Close()
	if !strings.Contains(string(exported), "Keep the new value.") || !strings.Contains(string(exported), `applicability="anchored"`) {
		t.Fatalf("unexpected export: %s", exported)
	}
	asset := request(t, server.Client(), http.MethodGet, server.URL+"/", nil)
	content, _ := io.ReadAll(asset.Body)
	asset.Body.Close()
	if string(content) != "web" {
		t.Fatalf("unexpected asset: %q", content)
	}
	hostRecorder := httptest.NewRecorder()
	hostRequest := httptest.NewRequest(http.MethodHead, "/openapi.yaml", nil)
	hostRequest.Host = "controlbox.device.host:7981"
	handler.ServeHTTP(hostRecorder, hostRequest)
	if hostRecorder.Code != http.StatusOK {
		t.Fatalf("custom host status: %d", hostRecorder.Code)
	}
	crossOriginRecorder := httptest.NewRecorder()
	crossOriginRequest := httptest.NewRequest(http.MethodHead, "/openapi.yaml", nil)
	crossOriginRequest.Host = "controlbox.device.host:7981"
	crossOriginRequest.Header.Set("Origin", "http://example.invalid")
	handler.ServeHTTP(crossOriginRecorder, crossOriginRequest)
	if crossOriginRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status: %d", crossOriginRecorder.Code)
	}
}

func TestSessionCapabilitiesAndEnforcement(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	source, err := diffsource.OpenPatch(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	active := session.Resolve(source, session.Policies{})
	server := httptest.NewServer(New(active, store, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()

	sessionResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/session", nil)
	if sessionResponse.StatusCode != http.StatusOK {
		t.Fatalf("session status: %d", sessionResponse.StatusCode)
	}
	got := decode[struct {
		Capabilities session.Capabilities `json:"capabilities"`
	}](t, sessionResponse)
	if got.Capabilities.Diff.Scopes.State != session.Enabled || len(got.Capabilities.Diff.Scopes.Values) != 1 || got.Capabilities.Diff.Scopes.Values[0] != review.DiffAll {
		t.Fatalf("scopes: %#v", got.Capabilities.Diff.Scopes)
	}
	if got.Capabilities.Diff.Refresh.State != session.Unavailable || got.Capabilities.Diff.StagingMetadata.State != session.Unavailable || got.Capabilities.Files.Contents.State != session.Unavailable || got.Capabilities.Review.Comments.State != session.Enabled {
		t.Fatalf("capabilities: %#v", got.Capabilities)
	}

	for _, endpoint := range []string{
		"/api/v1/diffs/current?scope=staged",
		"/api/v1/diffs/revision/files/file/contents?scope=all&fileVersion=version",
	} {
		response := request(t, server.Client(), http.MethodGet, server.URL+endpoint, nil)
		assertCapabilityProblem(t, response, map[string]string{
			"/api/v1/diffs/current?scope=staged":                                       session.DiffScopes,
			"/api/v1/diffs/revision/files/file/contents?scope=all&fileVersion=version": session.FilesContents,
		}[endpoint])
	}
}

func TestDisabledCommentsRejectEveryRoute(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	source, err := diffsource.OpenPatch(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	active := session.Resolve(source, session.Policies{Comments: session.DisablePolicy})
	server := httptest.NewServer(New(active, store, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()

	for _, test := range []struct {
		method string
		path   string
		body   any
	}{
		{method: http.MethodGet, path: "/api/v1/comments"},
		{method: http.MethodPost, path: "/api/v1/comments", body: map[string]string{}},
		{method: http.MethodDelete, path: "/api/v1/comments"},
		{method: http.MethodPost, path: "/api/v1/comments/import", body: map[string]string{}},
		{method: http.MethodGet, path: "/api/v1/comments/export"},
		{method: http.MethodPatch, path: "/api/v1/comments/id", body: map[string]string{}},
		{method: http.MethodDelete, path: "/api/v1/comments/id"},
	} {
		response := request(t, server.Client(), test.method, server.URL+test.path, test.body)
		assertCapabilityProblem(t, response, session.ReviewComments)
	}
}

func assertCapabilityProblem(t *testing.T, response *http.Response, capability string) {
	t.Helper()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("capability status: %d", response.StatusCode)
	}
	problem := decode[struct {
		Code       string `json:"code"`
		Capability string `json:"capability"`
	}](t, response)
	if problem.Code != "capability_not_enabled" || problem.Capability != capability {
		t.Fatalf("problem = %#v", problem)
	}
}
