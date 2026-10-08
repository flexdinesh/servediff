package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/remoteserver"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/webui"
)

func TestSyncPrintDebugCommitsAllWorktreesAndRecoversRemovedCheckout(t *testing.T) {
	root := cliRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	producerGit(t, root, "worktree", "add", "-b", "linked", linked)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("SERVEDIFF_RUNTIME_DIR", t.TempDir())
	token := strings.Repeat("a", 40)
	t.Setenv("SERVEDIFF_TOKEN", token)
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
	t.Setenv("SERVEDIFF_SERVER_URL", server.URL)
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
	if err != nil || len(contexts) != 2 {
		t.Fatalf("worktree publication: %d %v", len(contexts), err)
	}
	if err := run(t.Context(), []string{"sync", "--retry"}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "no pending") {
		t.Fatalf("acknowledged uploads remained pending: %v", err)
	}
}

func TestSyncRequiresRemoteAndConfigMasksToken(t *testing.T) {
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("SERVEDIFF_SERVER_URL", "")
	t.Setenv("SERVEDIFF_TOKEN", "")
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
