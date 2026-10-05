package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

func hookInput(t *testing.T, path string) []byte {
	t.Helper()
	input, err := json.Marshal(map[string]string{"cwd": path, "session_id": "test-session", "hook_event_name": "Stop"})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func promptHook(t *testing.T, harness serviceHarness, input []byte, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, serviceBinary, append([]string{"hook"}, args...)...)
	command.Env = harness.environment
	command.Stdin = bytes.NewReader(input)
	if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("completion hook must return promptly and silently: %v, %s", err, output)
	}
}

func waitHookCatalog(t *testing.T, harness serviceHarness, count int) (serviceStatus, []contextservice.Context) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	client := http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		output, err := harness.run(nil, "service", "status", "--json")
		var status serviceStatus
		if err == nil && json.Unmarshal(output, &status) == nil && status.State == "running" {
			response, err := client.Get(status.BrowserURL + "/api/v2/contexts")
			if err == nil {
				var page struct {
					Contexts []contextservice.Context `json:"contexts"`
				}
				decodeErr := json.NewDecoder(response.Body).Decode(&page)
				_ = response.Body.Close()
				if decodeErr == nil && len(page.Contexts) == count {
					return status, page.Contexts
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(filepath.Join(harness.runtimeDir, "hooks", "hooks.log"))
	t.Fatalf("hook did not publish %d contexts; %s", count, log)
	return serviceStatus{}, nil
}

func TestAgentHooksPublishLatestCheckoutAndCompleteFileContents(t *testing.T) {
	harness := newServiceHarness(t)
	harness.environment = append(harness.environment, "XDG_CONFIG_HOME="+filepath.Join(t.TempDir(), "config"))
	configFile := filepath.Join(t.TempDir(), "machine.json")
	config, err := json.Marshal(map[string]interface{}{"state": harness.state, "port": 0, "host": "0.0.0.0"})
	if err != nil || os.WriteFile(configFile, config, 0600) != nil {
		t.Fatal("write isolated config")
	}
	first, second := createWorktree(t, "hook-first"), createWorktree(t, "hook-second")
	for _, path := range []string{first, second} {
		promptHook(t, harness, hookInput(t, path), "--agent", "codex", "--config-file", configFile)
	}
	status, entries := waitHookCatalog(t, harness, 2)
	var firstID string
	for _, entry := range entries {
		if entry.Observation == nil || entry.Observation.Trigger != "agent-hook" || entry.Observation.Agent != "codex" || entry.Observation.RunID != "test-session" || entry.Root == nil {
			t.Fatalf("missing hook provenance: %#v", entry)
		}
		var diff review.RepositoryDiff
		base := status.BrowserURL + "/api/v2/contexts/" + entry.ID
		requestJSON(t, base+"/diffs/current?scope=all", &diff)
		if len(diff.Files) != 1 {
			t.Fatalf("missing checkout diff: %#v", diff)
		}
		var contents review.FileContents
		requestJSON(t, base+"/diffs/"+diff.ID+"/files/"+diff.Files[0].ID+"/contents?scope=all&versionId="+diff.VersionID+"&fileVersion="+diff.Files[0].Fingerprint, &contents)
		if contents.Before != "before\n" || contents.After != filepath.Base(*entry.Root)+"\n" {
			t.Fatalf("incomplete captured file: %#v", contents)
		}
		if *entry.Root == first {
			firstID = entry.ID
		}
	}
	// A manual review must invalidate the plugin's acknowledged-state skip.
	if err := os.WriteFile(filepath.Join(first, "value.txt"), []byte("manual change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	harness.requireRun(t, nil, "review", "--path", first, "--config-file", configFile, "--no-browser")
	if err := os.WriteFile(filepath.Join(first, "value.txt"), []byte(filepath.Base(first)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	promptHook(t, harness, nil, "--agent", "pi", "--path", first, "--config-file", configFile)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, entries = waitHookCatalog(t, harness, 3)
		current := false
		for _, entry := range entries {
			current = current || (entry.ID == firstID && !entry.Stale)
		}
		if current {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cached fingerprint left manual snapshot latest: %#v", entries)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A clean observation supersedes the previous changed state for its checkout.
	if err := os.WriteFile(filepath.Join(first, "value.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	promptHook(t, harness, nil, "--agent", "pi", "--path", first, "--config-file", configFile)
	_, entries = waitHookCatalog(t, harness, 4)
	clean, stale := false, false
	for _, entry := range entries {
		if entry.Root != nil && *entry.Root == first {
			clean = clean || (!entry.Stale && entry.ChangedFileCount != nil && *entry.ChangedFileCount == 0)
			stale = stale || entry.Stale
		}
	}
	if !clean || !stale {
		t.Fatalf("clean checkout did not supersede old changed observation: %#v", entries)
	}
}

func TestHookReturnsBeforeRemoteIngestionAndSkipsUnchangedUploads(t *testing.T) {
	harness := newServiceHarness(t)
	path := createWorktree(t, "async-remote")
	requests := make(chan ingestion.Request, 4)
	confirmations := make(chan struct{}, 4)
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "test-database", ProtocolVersion: ingestion.ProtocolVersion})
			return
		}
		if r.URL.Path == "/api/v2/contexts/context" {
			confirmations <- struct{}{}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "context", "kind": "observation", "availability": "available", "stale": false, "expiresAt": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		if r.URL.Path != "/api/v2/ingestions" || r.Header.Get("X-Servediff-State") != "test-database" {
			http.NotFound(w, r)
			return
		}
		var input ingestion.Request
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(ingestion.Receipt{ContextID: "context", ReviewURL: "/contexts/context", MCPURL: "/mcp/contexts/context", Snapshot: input.Scopes[0].Snapshot})
	}))
	defer func() {
		once.Do(func() { close(release) })
		server.Close()
	}()
	harness.environment = append(harness.environment, "SERVEDIFF_SERVER_URL="+server.URL, "SERVEDIFF_TOKEN=test-token", "XDG_CONFIG_HOME="+t.TempDir())
	promptHook(t, harness, hookInput(t, path), "--agent", "claude")
	select {
	case input := <-requests:
		if len(input.Scopes) != 3 || input.Metadata.Root != path {
			t.Fatalf("wrong checkout request: %#v", input)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker failed to survive parent hook exit")
	}
	// A different destination must run independently while the first upload is
	// blocked, even when checkout and machine-config paths are identical.
	otherRequests := make(chan ingestion.Request, 1)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "other-database", ProtocolVersion: ingestion.ProtocolVersion})
			return
		}
		if r.URL.Path != "/api/v2/ingestions" || r.Header.Get("X-Servediff-State") != "other-database" {
			http.NotFound(w, r)
			return
		}
		var input ingestion.Request
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		otherRequests <- input
		_ = json.NewEncoder(w).Encode(ingestion.Receipt{ContextID: "other", ReviewURL: "/contexts/other", MCPURL: "/mcp/contexts/other", Snapshot: input.Scopes[0].Snapshot})
	}))
	defer other.Close()
	otherHarness := harness
	otherHarness.environment = append(append([]string(nil), harness.environment...), "SERVEDIFF_SERVER_URL="+other.URL)
	promptHook(t, otherHarness, nil, "--agent", "pi", "--path", path)
	select {
	case <-otherRequests:
	case <-time.After(10 * time.Second):
		t.Fatal("second destination was coalesced into first worker")
	}
	// Return a durable receipt before testing the acknowledged-state skip.
	once.Do(func() { close(release) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		acks, _ := filepath.Glob(filepath.Join(harness.runtimeDir, "hooks", "jobs", "*", "*.ack.json"))
		if len(acks) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not record durable acknowledgement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for range 3 {
		promptHook(t, harness, nil, "--agent", "opencode", "--path", path)
	}
	select {
	case <-confirmations:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not validate acknowledged freshness")
	}
	select {
	case input := <-requests:
		t.Fatalf("unchanged completion uploaded again: %s", input.SubmissionID)
	case <-time.After(300 * time.Millisecond):
	}
	output, err := harness.run(nil, "service", "status", "--json")
	if err == nil || !strings.Contains(string(output), "stopped") {
		t.Fatalf("remote hook started a local service: %v %s", err, output)
	}
}
