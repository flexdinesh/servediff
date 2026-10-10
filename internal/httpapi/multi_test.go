package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/flexdinesh/diffx/internal/collector"
	"github.com/flexdinesh/diffx/internal/contextservice"
	"github.com/flexdinesh/diffx/internal/diffsource"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/session"
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
	return contextservice.Context{ID: id, Kind: "observation", Name: "patch", Capabilities: active.Capabilities, Availability: "available"}, err
}
func (provider *testProvider) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for index, active := range provider.sessions {
		if active.ContextID == id {
			provider.sessions = append(provider.sessions[:index], provider.sessions[index+1:]...)
			return nil
		}
	}
	return diffsource.Error(404, "Context not found")
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

func TestMultiContextObservationIsolation(t *testing.T) {
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
	server := httptest.NewServer(NewMulti(provider, store, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()
	first := provider.sessions[0]
	second := provider.sessions[1]
	firstBase := server.URL + "/api/v2/contexts/" + first.ContextID
	secondBase := server.URL + "/api/v2/contexts/" + second.ContextID
	snapshot := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=all", nil))
	other := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, secondBase+"/diffs/current?scope=all", nil))
	if snapshot.ID == other.ID {
		t.Fatal("independent observations share identity")
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
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy ambiguity status %d", response.StatusCode)
	}
	problem := decode[map[string]any](t, response)
	if problem["status"] != float64(404) {
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
	server := httptest.NewServer(NewMulti(&testProvider{}, store, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()
	for _, suffix := range []string{"/api/v2/contexts", "/api/v2/metrics", "/openapi.yaml", "/contexts/example"} {
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

func TestMultiObservationsAndRetainedFilesWithoutCheckout(t *testing.T) {
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
		gitCommand(t, root, "symbolic-ref", "HEAD", "refs/heads/shared")
		gitCommand(t, root, "remote", "add", "origin", "https://example.com/acme/shared.git")
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitCommand(t, root, "add", ".")
		gitCommand(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: fmt.Sprintf("container-%d", index), SubmissionID: "submission"})
		if err != nil {
			t.Fatal(err)
		}
		target, err := provider.Ingest(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	server := httptest.NewServer(NewMulti(provider, store, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()
	page := decode[contextservice.Page](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts?repository=shared&branch=shared", nil))
	if len(page.Contexts) != 2 || page.Contexts[0].Observation == nil || page.Contexts[1].Observation == nil || page.Contexts[0].Observation.SourceID == page.Contexts[1].Observation.SourceID {
		t.Fatalf("same branch source catalog: %#v", page)
	}
	filtered := decode[contextservice.Page](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts?sourceId=container-0", nil))
	if len(filtered.Contexts) != 1 || filtered.Contexts[0].ID != targets[0].Context.ID {
		t.Fatalf("source filter: %#v", filtered)
	}
	absent := decode[contextservice.Page](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts?q=absent-source", nil))
	if len(absent.Contexts) != 0 {
		t.Fatalf("search: %#v", absent)
	}
	first := targets[0]
	firstBase := server.URL + "/api/v2/contexts/" + first.Context.ID
	secondBase := server.URL + "/api/v2/contexts/" + targets[1].Context.ID
	snapshot := first.Snapshot
	staged := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=staged", nil))
	if staged.Mode != review.DiffStaged || staged.ID == snapshot.ID || staged.VersionID == snapshot.VersionID {
		t.Fatalf("scope identities: %#v", staged)
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
	t.Setenv("PATH", t.TempDir())
	file := snapshot.Files[0]
	response = request(t, server.Client(), http.MethodGet, firstBase+"/diffs/"+snapshot.ID+"/files/"+file.ID+"/patch?scope=all&versionId="+snapshot.VersionID+"&fileVersion="+file.Fingerprint, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("retained file of missing worktree: %d", response.StatusCode)
	}
	response.Body.Close()
	contents := decode[review.FileContents](t, request(t, server.Client(), http.MethodGet, firstBase+"/diffs/"+snapshot.ID+"/files/"+file.ID+"/contents?scope=all&versionId="+snapshot.VersionID+"&fileVersion="+file.Fingerprint, nil))
	if contents.Before != "before\n" || contents.After != "after\n" {
		t.Fatalf("stored contents: %#v", contents)
	}
	response = request(t, server.Client(), http.MethodGet, firstBase+"/diffs/current?scope=all", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stored observation after checkout deletion: %d", response.StatusCode)
	}
	retained := decode[review.RepositoryDiff](t, response)
	if retained.VersionID != snapshot.VersionID || retained.Revision != snapshot.Revision {
		t.Fatalf("stored observation changed: %#v", retained)
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

func TestComparisonMetadataSurvivesRestartWithoutGit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviews.db")
	store, err := reviewstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	user, err := store.User("uid", "user")
	if err != nil {
		t.Fatal(err)
	}
	provider := contextservice.New(store, user)
	root := t.TempDir()
	gitCommand(t, root, "init", "--initial-branch=main")
	commit := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitCommand(t, root, "add", ".")
		gitCommand(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", name)
	}
	commit("initial")
	gitCommand(t, root, "checkout", "-b", "feature")
	commit("feature")
	gitCommand(t, root, "checkout", "main")
	commit("main-advanced")
	gitCommand(t, root, "checkout", "feature")
	expected := make(map[string]ingestion.Metadata)
	for _, base := range []string{"auto", "HEAD"} {
		input, err := collector.Collect(t.Context(), root, collector.Options{SourceID: "machine", SubmissionID: base, Base: base})
		if err != nil {
			t.Fatal(err)
		}
		if base == "auto" && (input.Metadata.Comparison == nil || input.Metadata.Comparison.MergeBase == input.Metadata.Comparison.BaseCommit) {
			t.Fatal("fixture must distinguish merge base from base ref tip")
		}
		target, err := provider.Ingest(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		expected[target.Context.ID] = input.Metadata
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := reviewstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store = reopened
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	server := httptest.NewServer(NewMulti(contextservice.New(store, user), store, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()
	page := decode[contextservice.Page](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts", nil))
	if len(page.Contexts) != len(expected) {
		t.Fatalf("catalog count: %d", len(page.Contexts))
	}
	for _, listed := range page.Contexts {
		want := expected[listed.ID]
		retained := decode[contextservice.Context](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts/"+listed.ID, nil))
		for _, got := range []*ingestion.Metadata{listed.Observation, retained.Observation} {
			if got == nil || !reflect.DeepEqual(got.Comparison, want.Comparison) || !reflect.DeepEqual(got.Head, want.Head) {
				t.Fatalf("stored comparison changed: want %+v, got %+v", want, got)
			}
		}
	}
}

func (p *testProvider) ListFiltered(ctx context.Context, limit int, cursor string, filter ingestion.Filter) (contextservice.Page, error) {
	return p.List(ctx, limit, cursor)
}
