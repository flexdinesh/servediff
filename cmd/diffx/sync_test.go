package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/diffx/internal/hooks"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/remoteserver"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/submission"
	"github.com/flexdinesh/diffx/internal/webui"
)

func TestSyncPrintDebugCommitsSelectedCheckoutAndRecoversRemovedCheckout(t *testing.T) {
	root := cliRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	producerGit(t, root, "worktree", "add", "-b", "linked", linked)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("DIFFX_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("DIFFX_RUNTIME_DIR", t.TempDir())
	token := strings.Repeat("a", 40)
	t.Setenv("DIFFX_TOKEN", token)
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, closeServices, err := remoteserver.Handler(t.Context(), store, "admin", token, webui.Assets())
	if err != nil {
		t.Fatal(err)
	}
	defer closeServices()
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			http.Error(w, "offline", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("DIFFX_SERVER_URL", server.URL)
	var out, debug bytes.Buffer
	if err := run(t.Context(), []string{"sync", root, "--print", "--debug"}, nil, &out, &debug); err == nil {
		t.Fatal("offline sync succeeded")
	}
	if out.Len() != 0 {
		t.Fatalf("printed success before commit: %s", out.String())
	}
	// Saved captures must be sufficient even after the originating checkout disappears.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	available.Store(true)
	out.Reset()
	debug.Reset()
	if err := run(t.Context(), []string{"sync", "--retry", "--print", "--debug"}, nil, &out, &debug); err != nil {
		t.Fatal(err)
	}
	if out.String() != server.URL+"\n" || !strings.Contains(debug.String(), "Ingest · complete") {
		t.Fatalf("output %q; debug %q", out.String(), debug.String())
	}
	if strings.Contains(debug.String(), token) {
		t.Fatal("debug leaked credentials")
	}
	owner, err := store.AuthenticateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	contexts, err := store.ObservationContexts(owner.ID, 10, 0, "", ingestion.Filter{})
	if err != nil || len(contexts) != 1 {
		t.Fatalf("selected checkout publication: %d %v", len(contexts), err)
	}
	if err := run(t.Context(), []string{"sync", "--retry"}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "no pending") {
		t.Fatalf("acknowledged uploads remained pending: %v", err)
	}
}

func TestSyncRequiresRemoteAndConfigMasksToken(t *testing.T) {
	t.Setenv("DIFFX_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("DIFFX_SERVER_URL", "")
	t.Setenv("DIFFX_TOKEN", "")
	if err := run(t.Context(), []string{"sync"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("sync silently selected local mode")
	}
	var output bytes.Buffer
	if err := runConfigInput([]string{"set", "token", "-"}, strings.NewReader("private-token\n"), &output); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run(t.Context(), []string{"config", "get", "token"}, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private-token") || !strings.Contains(output.String(), "redacted") {
		t.Fatalf("token output: %s", output.String())
	}
}

// Exercise the real queue, worker and stored catalog while counting uploads.
type syncFixture struct {
	store   *reviewstore.Store
	ownerID string
	server  *httptest.Server
	uploads atomic.Int64
	online  atomic.Bool
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("DIFFX_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("DIFFX_RUNTIME_DIR", t.TempDir())
	t.Setenv("DIFFX_SOURCE_ID", "machine")
	token := strings.Repeat("b", 40)
	t.Setenv("DIFFX_TOKEN", token)
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, closeServices, err := remoteserver.Handler(t.Context(), store, "admin", token, webui.Assets())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeServices() })
	owner, err := store.AuthenticateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &syncFixture{store: store, ownerID: owner.ID}
	fixture.online.Store(true)
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fixture.online.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/ingestion-jobs" {
			fixture.uploads.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fixture.server.Close)
	t.Setenv("DIFFX_SERVER_URL", fixture.server.URL)
	return fixture
}

func TestSyncAndHooksCollectOneCheckoutAndDeduplicate(t *testing.T) {
	root := cliRepository(t)
	producerGit(t, root, "branch", "main")
	linked := filepath.Join(t.TempDir(), "linked")
	producerGit(t, root, "worktree", "add", "-b", "linked", linked)
	filename := filepath.Join(linked, "file.txt")
	if err := os.WriteFile(filename, []byte("committed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	producerGit(t, linked, "commit", "-am", "feature change")
	subdir := filepath.Join(linked, "subdir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)
	fixture := newSyncFixture(t)
	sync := func(wantUploads int64) string {
		t.Helper()
		var output bytes.Buffer
		if err := run(t.Context(), []string{"sync"}, nil, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got := fixture.uploads.Load(); got != wantUploads {
			t.Fatalf("uploads = %d, want %d", got, wantUploads)
		}
		return output.String()
	}
	sync(1)
	contexts, err := fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{})
	if err != nil || len(contexts) != 1 {
		t.Fatalf("selected checkout contexts: %#v %v", contexts, err)
	}
	originalID := contexts[0].ID
	metadata := contexts[0].Metadata
	if metadata == nil || metadata.Root != linked || metadata.Comparison == nil || metadata.Comparison.Kind != "branch" || metadata.Comparison.BaseRef != "refs/heads/main" {
		t.Fatalf("selected checkout lost merge-base comparison: %+v", metadata)
	}
	local, err := collectInitialInput(t.Context(), options{directory: subdir, sourceID: "machine"}, initialInput{Kind: "worktree", Path: subdir})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.ObservationSnapshot(fixture.ownerID, originalID, review.DiffAll)
	if err != nil || len(stored.Files) != len(local.Scopes[0].Snapshot.Files) || len(stored.Files) != 1 {
		t.Fatalf("local/remote selected checkout diff: %+v %v", stored, err)
	}
	if output := sync(1); output != "Synced 0 observations (1 unchanged)\n" {
		t.Fatalf("unchanged output: %q", output)
	}

	// Hook sessions must associate with the shared review, then skip repeats.
	hookState := t.TempDir()
	hook := func(session string, wantUploads int64) {
		t.Helper()
		engine := newHookEngine(hookState)
		events, err := engine.Expand(t.Context(), hooks.Event{Path: subdir, InputPath: subdir, Agent: "codex", RunID: session, Base: "auto"})
		if err != nil || len(events) != 1 || events[0].Path != linked {
			t.Fatalf("hook checkout selection: %+v %v", events, err)
		}
		job := ""
		engine.Launch = func(value string) error { job = value; return nil }
		if err := engine.Schedule(events[0]); err != nil {
			t.Fatal(err)
		}
		if err := engine.Run(t.Context(), job); err != nil {
			t.Fatal(err)
		}
		if got := fixture.uploads.Load(); got != wantUploads {
			t.Fatalf("hook uploads = %d, want %d", got, wantUploads)
		}
	}
	hook("A", 2)
	hook("A", 2)
	hook("B", 3)
	sync(3)
	for _, session := range []string{"A", "B"} {
		contexts, err := fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{Harness: "codex", SessionID: session})
		if err != nil || len(contexts) != 1 || contexts[0].ID != originalID {
			t.Fatalf("session %s duplicated or lost review: %+v %v", session, contexts, err)
		}
	}

	// Changed contents upload; returning to old contents restores its stream head.
	if err := os.WriteFile(filename, []byte("working\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(4)
	if err := os.WriteFile(filename, []byte("committed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(5)
	contexts, err = fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{})
	if err != nil || len(contexts) != 2 {
		t.Fatalf("changed content history: %+v %v", contexts, err)
	}
	for _, context := range contexts {
		if context.ID == originalID && context.Stale {
			t.Fatal("recollected content stayed stale")
		}
	}
	if err := fixture.store.DeleteContext(fixture.ownerID, originalID); err != nil {
		t.Fatal(err)
	}
	sync(6)
	contexts, err = fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{})
	if err != nil || len(contexts) != 2 {
		t.Fatalf("deleted context suppressed fresh collection: %+v %v", contexts, err)
	}
}

func TestSyncReusesPendingPayloadAndRefreshesStaleReplay(t *testing.T) {
	root := cliRepository(t)
	fixture := newSyncFixture(t)
	fixture.online.Store(false)
	sync := func() error {
		return run(t.Context(), []string{"sync", root}, nil, io.Discard, io.Discard)
	}
	if err := sync(); err == nil {
		t.Fatal("offline sync succeeded")
	}
	path, err := reviewstore.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(os.Getenv("DIFFX_TOKEN")))
	route := fixture.server.URL + ":" + hex.EncodeToString(digest[:])
	pending := func() []submission.Pending {
		t.Helper()
		box, err := submission.Open(t.Context(), filepath.Dir(path), route)
		if err != nil {
			t.Fatal(err)
		}
		defer box.Close()
		items, err := box.Pending()
		if err != nil || len(items) != 1 {
			t.Fatalf("pending duplicate: %+v %v", items, err)
		}
		return items
	}
	first := pending()[0]
	if err := sync(); err == nil {
		t.Fatal("offline repeat succeeded")
	}
	if second := pending()[0]; !reflect.DeepEqual(first, second) {
		t.Fatal("unchanged retry replaced immutable payload")
	}
	// A newer observation from another producer must not be superseded by an old retry.
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("other producer\n"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err := collectSubmission(t.Context(), options{directory: root, sourceID: "machine"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Ingest(fixture.ownerID, other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture.online.Store(true)
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	if fixture.uploads.Load() != 2 {
		t.Fatalf("stale retry needed fresh submission: %d uploads", fixture.uploads.Load())
	}
	contexts, err := fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{})
	if err != nil || len(contexts) != 2 {
		t.Fatalf("stale retry duplicated history: %+v %v", contexts, err)
	}
	for _, context := range contexts {
		if context.Metadata != nil && context.Metadata.CollectedAt == first.Request.Metadata.CollectedAt && context.Stale {
			t.Fatal("matching fresh collection did not advance old replay")
		}
	}
	if err := sync(); err != nil || fixture.uploads.Load() != 2 {
		t.Fatalf("acknowledged replay uploaded again: %v, %d uploads", err, fixture.uploads.Load())
	}
}

func TestSyncRecordsNewSessionMetadataWithoutDuplicatingReviews(t *testing.T) {
	root := cliRepository(t)
	fixture := newSyncFixture(t)
	for _, step := range []struct {
		session, name string
		uploads       int64
	}{
		{"A", "First", 1}, {"A", "First", 1},
		{"B", "Second", 2}, {"B", "Renamed", 3}, {"B", "Renamed", 3},
	} {
		if err := run(t.Context(), []string{"sync", root, "--harness", "codex", "--run-id", step.session, "--session-name", step.name}, nil, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got := fixture.uploads.Load(); got != step.uploads {
			t.Fatalf("session %s/%s: %d uploads, want %d", step.session, step.name, got, step.uploads)
		}
		contexts, err := fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{Harness: "codex", SessionID: step.session, SessionName: step.name})
		if err != nil || len(contexts) != 1 {
			t.Fatalf("session association: %+v %v", contexts, err)
		}
	}
	contexts, err := fixture.store.ObservationContexts(fixture.ownerID, 10, 0, "", ingestion.Filter{})
	if err != nil || len(contexts) != 1 {
		t.Fatalf("session uploads duplicated snapshot: %+v %v", contexts, err)
	}
}

func TestSyncKeepsForeignPendingPayloadAndPublishesFreshCollection(t *testing.T) {
	root := cliRepository(t)
	fixture := newSyncFixture(t)
	request, err := collectSubmission(t.Context(), options{directory: root, sourceID: "machine", trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := reviewstore.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(os.Getenv("DIFFX_TOKEN")))
	route := fixture.server.URL + ":" + hex.EncodeToString(digest[:])
	box, err := submission.Open(t.Context(), filepath.Dir(path), route)
	if err != nil {
		t.Fatal(err)
	}
	if err := box.Save(submission.Pending{Request: request, StateID: "old-database"}); err != nil {
		t.Fatal(err)
	}
	box.Close()
	err = run(t.Context(), []string{"sync", root}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "another server database") || fixture.uploads.Load() != 1 {
		t.Fatalf("fresh collection blocked or old payload rerouted: %v, %d uploads", err, fixture.uploads.Load())
	}
	box, err = submission.Open(t.Context(), filepath.Dir(path), route)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	pending, err := box.Pending()
	if err != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0].Request, request) || pending[0].StateID != "old-database" {
		t.Fatalf("foreign retry mutated or lost: %+v %v", pending, err)
	}
}
