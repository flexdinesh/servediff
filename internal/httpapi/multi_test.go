package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
)

type testProvider struct {
	sessions []session.Session
}

func (provider *testProvider) UserID() string {
	if len(provider.sessions) == 0 {
		return ""
	}
	return provider.sessions[0].User.ID
}
func (provider *testProvider) Resolve(_ context.Context, id string) (session.Session, error) {
	for _, active := range provider.sessions {
		if active.ContextID == id {
			return active, nil
		}
	}
	return session.Session{}, diffsource.Error(404, "Context not found")
}
func (provider *testProvider) Get(ctx context.Context, id string) (contextservice.Context, error) {
	active, err := provider.Resolve(ctx, id)
	return contextservice.Context{ID: id, Kind: "capture", Name: "patch", Capabilities: active.Capabilities, Availability: "available"}, err
}
func (provider *testProvider) List(ctx context.Context, limit int, _ string) (contextservice.Page, error) {
	page := contextservice.Page{Contexts: make([]contextservice.Context, 0)}
	for _, active := range provider.sessions {
		entry, _ := provider.Get(ctx, active.ContextID)
		page.Contexts = append(page.Contexts, entry)
		if len(page.Contexts) == limit {
			break
		}
	}
	if len(provider.sessions) > limit {
		cursor := "more"
		page.NextCursor = &cursor
	}
	return page, nil
}

func TestMultiContextCaptureIsolation(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := &testProvider{}
	for range 2 {
		source, err := diffsource.OpenPatch(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		provider.sessions = append(provider.sessions, patchSession(t, store, source, string(raw), session.Policies{}))
	}
	server := httptest.NewServer(NewMulti(provider, store, fstest.MapFS{"index.html": {Data: []byte("web")}}, ""))
	defer server.Close()
	first := provider.sessions[0]
	second := provider.sessions[1]
	firstBase := server.URL + "/api/v2/contexts/" + first.ContextID
	secondBase := server.URL + "/api/v2/contexts/" + second.ContextID
	snapshot := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=all", nil))
	other := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, secondBase+"/diffs/current?scope=all", nil))
	if snapshot.ID == other.ID {
		t.Fatal("independent captures share identity")
	}
	for _, suffix := range []string{
		"/diffs/" + snapshot.ID + "/versions/" + snapshot.VersionID,
		"/diffs/" + snapshot.ID + "/files/" + snapshot.Files[0].ID + "/patch?scope=all&versionId=" + snapshot.VersionID + "&fileVersion=" + snapshot.Files[0].Fingerprint,
	} {
		response := request(t, server.Client(), http.MethodGet, secondBase+suffix, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("foreign resource %s status %d", suffix, response.StatusCode)
		}
		response.Body.Close()
	}
	comment := review.ReviewComment{ID: "foreign", DiffID: snapshot.ID, VersionID: snapshot.VersionID, Path: snapshot.Files[0].Path, Scope: review.DiffAll, Fingerprint: snapshot.Files[0].Fingerprint, Side: "additions", Start: 1, End: 1, Code: "line", Body: "body", Status: "open", CreatedAt: 1}
	response := request(t, server.Client(), http.MethodPost, secondBase+"/comments/import", map[string]any{"comments": []review.ReviewComment{comment}})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign import status %d", response.StatusCode)
	}
	response.Body.Close()
	response = request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/session", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("legacy ambiguity status %d", response.StatusCode)
	}
	problem := decode[map[string]any](t, response)
	if problem["code"] != "context_required" {
		t.Fatalf("legacy problem %#v", problem)
	}
	response = request(t, server.Client(), http.MethodGet, firstBase+"/diffs/"+snapshot.ID+"/versions/"+snapshot.VersionID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("own version status %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestMultiGlobalRoutesWithoutContext(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewServer(NewMulti(&testProvider{}, store, fstest.MapFS{"index.html": {Data: []byte("web")}}, ""))
	defer server.Close()
	for _, suffix := range []string{"/api/v2/contexts", "/api/v2/metrics", "/api/v1/metrics", "/api/v1/diffs/captures", "/openapi.yaml", "/contexts/example"} {
		response := request(t, server.Client(), http.MethodGet, server.URL+suffix, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("global route %s status %d", suffix, response.StatusCode)
		}
		response.Body.Close()
	}
	response := request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts?limit=501", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit status %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestMultiWorktreesAndRetainedFiles(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.User("uid", "user")
	if err != nil {
		t.Fatal(err)
	}
	provider := contextservice.New(store, user)
	roots := []string{t.TempDir(), t.TempDir()}
	targets := make([]contextservice.Submission, 0, 2)
	for index, root := range roots {
		gitCommand(t, root, "init")
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitCommand(t, root, "add", ".")
		gitCommand(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		target, err := provider.Register(t.Context(), fmt.Sprintf("worktree-%d", index), root)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	server := httptest.NewServer(NewMulti(provider, store, fstest.MapFS{"index.html": {Data: []byte("web")}}, ""))
	defer server.Close()
	first := targets[0]
	firstBase := server.URL + "/api/v2/contexts/" + first.Context.ID
	secondBase := server.URL + "/api/v2/contexts/" + targets[1].Context.ID
	snapshot := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=all", nil))
	active, err := provider.Resolve(t.Context(), first.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	previews := make(map[string]review.FilePatch)
	for _, file := range snapshot.Files {
		preview, err := active.Source.Patch(t.Context(), snapshot.Mode, file, snapshot.Head)
		if err != nil {
			t.Fatal(err)
		}
		previews[file.ID] = preview
	}
	if err := store.PinVersion(snapshot, previews); err != nil {
		t.Fatal(err)
	}
	response := request(t, server.Client(), http.MethodGet, secondBase+"/diffs/"+snapshot.ID+"/versions/"+snapshot.VersionID, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign worktree version: %d", response.StatusCode)
	}
	response.Body.Close()
	comment := review.ReviewComment{ID: "owned-comment", DiffID: snapshot.ID, VersionID: snapshot.VersionID, Path: snapshot.Files[0].Path, Scope: review.DiffAll, Fingerprint: snapshot.Files[0].Fingerprint, Side: "additions", Start: 1, End: 1, Code: "after", Body: "body", Status: "open", CreatedAt: 1}
	if err := store.PutComment(first.Context.ID, comment); err != nil {
		t.Fatal(err)
	}
	response = request(t, server.Client(), http.MethodDelete, secondBase+"/comments/owned-comment", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign worktree comment: %d", response.StatusCode)
	}
	response.Body.Close()
	response = request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=all", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("prime live snapshot: %d", response.StatusCode)
	}
	response.Body.Close()
	if err := os.RemoveAll(roots[0]); err != nil {
		t.Fatal(err)
	}
	file := snapshot.Files[0]
	response = request(t, server.Client(), http.MethodGet, firstBase+"/diffs/"+snapshot.ID+"/files/"+file.ID+"/patch?scope=all&versionId="+snapshot.VersionID+"&fileVersion="+file.Fingerprint, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("retained file of missing worktree: %d", response.StatusCode)
	}
	response.Body.Close()
	response = request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=all", nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing worktree: %d", response.StatusCode)
	}
	problem := decode[map[string]any](t, response)
	if problem["code"] != "source_unavailable" {
		t.Fatalf("missing source problem: %#v", problem)
	}
	response = request(t, server.Client(), http.MethodGet, secondBase+"/diffs/current?scope=all", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("healthy worktree: %d", response.StatusCode)
	}
	response.Body.Close()
}

func gitCommand(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func TestMultiLegacyForegroundIsPinned(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := &testProvider{}
	for range 2 {
		source, err := diffsource.OpenPatch(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		provider.sessions = append(provider.sessions, patchSession(t, store, source, string(raw), session.Policies{}))
	}
	active := provider.sessions[0]
	server := httptest.NewServer(NewMulti(provider, store, fstest.MapFS{}, active.ContextID))
	defer server.Close()
	response := request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/diffs/current?scope=all", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("foreground legacy status %d", response.StatusCode)
	}
	snapshot := decode[review.RepositoryDiff](t, response)
	if snapshot.ID != active.DiffIDs[review.DiffAll] {
		t.Fatalf("foreground legacy diff %s", snapshot.ID)
	}
}

// blockedSource exposes the computation lifetime without relying on Git timing.
type blockedSource struct {
	diffsource.Source
	started  chan context.Context
	finished chan struct{}
}

func (source *blockedSource) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	source.started <- ctx
	<-ctx.Done()
	close(source.finished)
	return review.RepositoryDiff{}, ctx.Err()
}

func TestMultiSharedSnapshotLifetime(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	patch, err := diffsource.OpenPatch(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	active := patchSession(t, store, patch, string(raw), session.Policies{})
	source := &blockedSource{Source: patch, started: make(chan context.Context, 1), finished: make(chan struct{})}
	active.Source = source
	lifetime, stop := context.WithCancel(t.Context())
	defer stop()
	handler := NewMultiWithContext(lifetime, &testProvider{sessions: []session.Session{active}}, store, fstest.MapFS{}, "")
	caller, cancelCaller := context.WithCancel(t.Context())
	defer cancelCaller()
	request := httptest.NewRequest(http.MethodGet, "/api/v2/contexts/"+active.ContextID+"/diffs/current?scope=all", nil).WithContext(caller)
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	var computation context.Context
	select {
	case computation = <-source.started:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot did not start")
	}
	cancelCaller()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled caller did not return")
	}
	if err := computation.Err(); err != nil {
		t.Fatalf("caller cancelled shared computation: %v", err)
	}
	secondDone := make(chan struct{})
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/api/v2/contexts/"+active.ContextID+"/diffs/current?scope=all", nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(secondDone)
	}()
	stop()
	select {
	case <-source.finished:
	case <-time.After(2 * time.Second):
		t.Fatal("service shutdown did not cancel snapshot")
	}
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shared waiter did not return after shutdown")
	}
}
