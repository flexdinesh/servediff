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
)

func request(t *testing.T, client *http.Client, method, url, token string, body any) *http.Response {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
	handler := New(source, store, "test-token", fstest.MapFS{"index.html": {Data: []byte("web")}})
	server := httptest.NewServer(handler)
	defer server.Close()
	unauthorized := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/session", "", nil)
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status: %d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()
	for _, endpoint := range []string{
		"/api/v1/comments/export?scope=",
		"/api/v1/comments/export?includeResolved=",
	} {
		response := request(t, server.Client(), http.MethodGet, server.URL+endpoint, "test-token", nil)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status: %d", endpoint, response.StatusCode)
		}
		response.Body.Close()
	}
	diffResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/diffs/current?scope=all", "test-token", nil)
	if diffResponse.StatusCode != http.StatusOK {
		t.Fatalf("diff status: %d", diffResponse.StatusCode)
	}
	snapshot := decode[review.RepositoryDiff](t, diffResponse)
	file := snapshot.Files[0]
	emptyMark := request(t, server.Client(), http.MethodPut, server.URL+"/api/v1/review-marks/"+file.ID+"?scope=all", "test-token", map[string]string{"fileVersion": ""})
	if emptyMark.StatusCode != http.StatusConflict {
		t.Fatalf("empty mark status: %d", emptyMark.StatusCode)
	}
	emptyMark.Body.Close()
	createdResponse := request(t, server.Client(), http.MethodPost, server.URL+"/api/v1/comments", "test-token", map[string]any{
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
	markResponse := request(t, server.Client(), http.MethodPut, server.URL+"/api/v1/review-marks/"+file.ID+"?scope=all", "test-token", map[string]string{"fileVersion": file.Fingerprint})
	if markResponse.StatusCode != http.StatusOK {
		t.Fatalf("mark status: %d", markResponse.StatusCode)
	}
	markResponse.Body.Close()
	exportResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/comments/export", "test-token", nil)
	exported, _ := io.ReadAll(exportResponse.Body)
	exportResponse.Body.Close()
	if !strings.Contains(string(exported), "Keep the new value.") || !strings.Contains(string(exported), `applicability="anchored"`) {
		t.Fatalf("unexpected export: %s", exported)
	}
	asset := request(t, server.Client(), http.MethodGet, server.URL+"/", "", nil)
	content, _ := io.ReadAll(asset.Body)
	asset.Body.Close()
	if string(content) != "web" {
		t.Fatalf("unexpected asset: %q", content)
	}
}
