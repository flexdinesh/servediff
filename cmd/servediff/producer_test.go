package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/hooks"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/serverapp"
	"github.com/flexdinesh/servediff/internal/webui"
)

func producerGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, output, err)
	}
}

func TestCollectorDestinationConfigAndExplicitLocalOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"server":"https://file.example.com","token":"file-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_SERVER_URL", "https://env.example.com")
	t.Setenv("SERVEDIFF_TOKEN", "env-token")
	for _, scenario := range []struct {
		args          []string
		server, token string
	}{
		{[]string{"--config-file", path}, "https://env.example.com", "env-token"},
		{[]string{"--config-file", path, "--server", "https://flag.example.com", "--token", "flag-token"}, "https://flag.example.com", "flag-token"},
		{[]string{"--config-file", path, "--server=", "--token="}, "", ""},
	} {
		parsed, err := parseOptions(scenario.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		values, err := resolvedCollectorSettings(parsed)
		if err != nil || values.server != scenario.server || values.token != scenario.token {
			t.Fatalf("resolved: %#v %v", values, err)
		}
	}
}

func TestReviewCollectsAllWorktreesAndPrintsOrigin(t *testing.T) {
	root := cliRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	producerGit(t, root, "worktree", "add", "-b", "other", linked)
	if err := os.WriteFile(filepath.Join(linked, "file.txt"), []byte("other edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	var received []ingestion.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "database", ProtocolVersion: ingestion.ProtocolVersion})
			return
		}
		if r.Header.Get("X-Servediff-State") != "database" {
			t.Error("submission not pinned")
		}
		var request ingestion.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		received = append(received, request)
		id := "linked"
		if request.Metadata.Root == root {
			id = "origin"
		}
		_ = json.NewEncoder(w).Encode(ingestion.Receipt{ContextID: id, ReviewURL: "/contexts/" + id, MCPURL: "/mcp/contexts/" + id, Snapshot: request.Scopes[0].Snapshot})
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := run(t.Context(), []string{"review", "--path", root, "--server", server.URL, "--source-id", "machine", "--no-browser"}, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(received) != 2 || received[0].Metadata.Root != root || received[1].Metadata.Root != linked {
		t.Fatalf("sources: %#v", received)
	}
	for _, request := range received {
		if request.Metadata.TriggerRoot != root {
			t.Fatalf("origin provenance: %#v", request.Metadata)
		}
	}
	if !strings.Contains(output.String(), "/contexts/origin") || strings.Contains(output.String(), "/contexts/linked") {
		t.Fatalf("output: %s", output.String())
	}
}

func TestHookDiscoveryIgnoresNonGitAndObjectOnlyBranches(t *testing.T) {
	workspace := t.TempDir()
	root := cliRepository(t)
	producerGit(t, root, "branch", "unopened")
	t.Setenv("SERVEDIFF_SOURCE_ID", "machine")
	engine := newHookEngine(t.TempDir())
	events, err := engine.Expand(t.Context(), hooks.Event{Path: workspace, InputPath: workspace, Agent: "codex", Base: "auto"})
	if err != nil || len(events) != 0 {
		t.Fatalf("non-Git: %#v %v", events, err)
	}
	events, err = engine.Expand(t.Context(), hooks.Event{Path: root, InputPath: root, Agent: "codex", Base: "auto"})
	if err != nil || len(events) != 1 || events[0].Branch != "" {
		t.Fatalf("registered checkouts: %#v %v", events, err)
	}
}

func TestHookInitialCleanSessionsAndMissingAcknowledgement(t *testing.T) {
	root := cliRepository(t)
	filename := filepath.Join(root, "file.txt")
	if err := os.WriteFile(filename, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_SOURCE_ID", "machine")
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SERVEDIFF_CONFIG_PATH", configPath)
	t.Setenv("SERVEDIFF_TOKEN", "")
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.User("test", "test")
	if err != nil {
		t.Fatal(err)
	}
	service := contextservice.NewWithContext(t.Context(), store, user)
	defer service.Close()
	server := httptest.NewServer(serverapp.Handler(t.Context(), service, store, webui.Assets(), ""))
	defer server.Close()
	t.Setenv("SERVEDIFF_SERVER_URL", server.URL)
	directory := t.TempDir()
	sync := func(session string) {
		t.Helper()
		engine := newHookEngine(directory)
		events, err := engine.Expand(t.Context(), hooks.Event{Path: root, InputPath: root, Agent: "codex", RunID: session, SessionName: "Session " + session, Base: "auto", ConfigFile: configPath})
		if err != nil || len(events) != 1 {
			t.Fatalf("expand: %v %#v", err, events)
		}
		job := ""
		engine.Launch = func(value string) error { job = value; return nil }
		if err := engine.Schedule(events[0]); err != nil {
			t.Fatal(err)
		}
		if err := engine.Run(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	sync("A")
	page, err := service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 0 {
		t.Fatalf("initial clean stored: %#v %v", page, err)
	}
	if err := os.WriteFile(filename, []byte("dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sync("A")
	sync("B")
	page, err = service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 1 {
		t.Fatalf("session dedupe: %#v %v", page, err)
	}
	dirtyID := page.Contexts[0].ID
	for _, session := range []string{"A", "B"} {
		filtered, err := service.ListFiltered(t.Context(), 100, "", ingestion.Filter{Harness: "codex", SessionID: session})
		if err != nil || len(filtered.Contexts) != 1 || filtered.Contexts[0].ID != dirtyID {
			t.Fatalf("session %s: %#v %v", session, filtered, err)
		}
	}
	// Lose producer state while dirty server state remains. Clean collection must
	// reconcile durable history and advance the server head.
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sync("B")
	page, err = service.List(t.Context(), 100, "")
	if err != nil || len(page.Contexts) != 2 {
		t.Fatalf("clean transition: %#v %v", page, err)
	}
	found := false
	for _, item := range page.Contexts {
		if item.ID == dirtyID {
			found = true
			if !item.Stale {
				t.Fatal("old dirty head remains current")
			}
		}
	}
	if !found {
		t.Fatal("dirty history lost")
	}
}

func TestRemoteFailureNeverStartsLocalService(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("SERVEDIFF_RUNTIME_DIR", runtime)
	target, err := resolveDestination(context.Background(), options{server: "http://127.0.0.1:1"}, daemon.Settings{}, daemon.Explicit{}, nil)
	if err == nil || target != nil {
		t.Fatalf("remote accepted: %#v %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(runtime, "daemon.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local fallback: %v", err)
	}
}

func TestUnavailableWorktreeNeverProducesFalseEmptyCapture(t *testing.T) {
	root := cliRepository(t)
	linked := filepath.Join(t.TempDir(), "removed")
	producerGit(t, root, "worktree", "add", "-b", "removed", linked)
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	requests, err := collectSubmissions(t.Context(), "review", options{directory: root, sourceID: "machine", trigger: "manual"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing worktree not reported: %v", err)
	}
	if len(requests) != 1 || requests[0].Metadata.Root != root || len(requests[0].Scopes[0].Snapshot.Files) == 0 {
		t.Fatalf("false empty capture: %#v", requests)
	}
}

func TestConfiguredTokenRedactedInRejectedDelivery(t *testing.T) {
	token := "persisted-private-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(ingestion.Problem{Status: http.StatusForbidden, Detail: "Rejected " + token})
	}))
	defer server.Close()
	target := destination{endpoint: server.URL, token: token, remote: ingestion.NewClient(server.URL, token)}
	_, err := target.deliver(t.Context(), ingestion.Request{})
	var problem *ingestion.Problem
	if err == nil || strings.Contains(err.Error(), token) || !errors.As(err, &problem) {
		t.Fatalf("credential rejection: %v", err)
	}
}
