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
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/diffx/internal/contextservice"
	"github.com/flexdinesh/diffx/internal/hooks"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/testsupport"
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
	endpoint := ""
	for _, value := range harness.environment {
		if strings.HasPrefix(value, "DIFFX_SERVER_URL=") {
			endpoint = strings.TrimPrefix(value, "DIFFX_SERVER_URL=")
		}
	}
	client := http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint + "/api/v2/contexts")
		if err == nil {
			var page contextservice.Page
			decodeErr := json.NewDecoder(response.Body).Decode(&page)
			response.Body.Close()
			if decodeErr == nil && len(page.Contexts) == count {
				return serviceStatus{BrowserURL: endpoint}, page.Contexts
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(filepath.Join(harness.runtimeDir, "hooks-v4", "hooks.log"))
	t.Fatalf("hook did not publish %d contexts: %s", count, log)
	return serviceStatus{}, nil
}

func TestAgentHooksPublishLatestCheckoutAndCompleteFileContents(t *testing.T) {
	harness := newServiceHarness(t)
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.User("remote-hook-test", "remote")
	if err != nil {
		t.Fatal(err)
	}
	service := contextservice.NewWithContext(t.Context(), store, owner)
	defer service.Close()
	server := testsupport.Server(t, service, store)
	defer server.Close()
	harness.environment = append(harness.environment, "DIFFX_SERVER_URL="+server.URL, "DIFFX_TOKEN=test-token")

	harness.environment = append(harness.environment, "XDG_CONFIG_HOME="+filepath.Join(t.TempDir(), "config"))
	configFile := filepath.Join(t.TempDir(), "machine.json")
	config, err := json.Marshal(map[string]interface{}{"state": harness.state, "port": 0, "host": "0.0.0.0"})
	if err != nil || os.WriteFile(configFile, config, 0600) != nil {
		t.Fatal("write isolated config")
	}
	first, second := createWorktree(t, "hook-first"), createWorktree(t, "hook-second")
	for _, path := range []string{first, second} {
		promptHook(t, harness, hookInput(t, path), "--harness", "codex", "--config-file", configFile)
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
	harness.requireRun(t, nil, "sync", "--path", first, "--config-file", configFile, "--no-browser")
	if err := os.WriteFile(filepath.Join(first, "value.txt"), []byte(filepath.Base(first)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	promptHook(t, harness, nil, "--harness", "pi", "--path", first, "--config-file", configFile)
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
	promptHook(t, harness, nil, "--harness", "pi", "--path", first, "--config-file", configFile)
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
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "test-database", ProtocolVersion: ingestion.ProtocolVersion, QueuedIngestion: true, IngestionEnabled: true})
			return
		}
		if r.URL.Path == "/api/v2/contexts/context" {
			confirmations <- struct{}{}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "context", "kind": "observation", "availability": "available", "stale": false, "expiresAt": time.Now().Add(time.Hour).UnixMilli()})
			return
		}
		if r.URL.Path != "/api/v2/ingestion-jobs" || r.Header.Get("X-Diffx-State") != "test-database" {
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
		_ = json.NewEncoder(w).Encode(ingestion.Job{ID: input.SubmissionID, SubmissionID: input.SubmissionID, State: "succeeded", ContextID: "context"})
	}))
	defer func() {
		once.Do(func() { close(release) })
		server.Close()
	}()
	harness.environment = append(harness.environment, "DIFFX_SERVER_URL="+server.URL, "DIFFX_TOKEN=test-token", "XDG_CONFIG_HOME="+t.TempDir())
	promptHook(t, harness, hookInput(t, path), "--harness", "claude")
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
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "other-database", ProtocolVersion: ingestion.ProtocolVersion, QueuedIngestion: true, IngestionEnabled: true})
			return
		}
		if r.URL.Path != "/api/v2/ingestion-jobs" || r.Header.Get("X-Diffx-State") != "other-database" {
			http.NotFound(w, r)
			return
		}
		var input ingestion.Request
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		otherRequests <- input
		_ = json.NewEncoder(w).Encode(ingestion.Job{ID: input.SubmissionID, SubmissionID: input.SubmissionID, State: "succeeded", ContextID: "other"})
	}))
	defer other.Close()
	otherHarness := harness
	otherHarness.environment = append(append([]string(nil), harness.environment...), "DIFFX_SERVER_URL="+other.URL)
	promptHook(t, otherHarness, nil, "--harness", "pi", "--path", path)
	select {
	case <-otherRequests:
	case <-time.After(10 * time.Second):
		t.Fatal("second destination was coalesced into first worker")
	}
	// Return a durable receipt before testing the acknowledged-state skip.
	once.Do(func() { close(release) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		acks, _ := filepath.Glob(filepath.Join(harness.runtimeDir, "hooks-v4", "jobs", "*", "*.ack.json"))
		if len(acks) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not record durable acknowledgement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for range 3 {
		promptHook(t, harness, hookInput(t, path), "--harness", "claude")
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
	// A new session must associate with the review even when content is unchanged.
	promptHook(t, harness, nil, "--harness", "opencode", "--run-id", "second-session", "--path", path)
	select {
	case input := <-requests:
		if input.Metadata.AgentSession == nil || input.Metadata.AgentSession.Harness != "opencode" || input.Metadata.AgentSession.ID != "second-session" {
			t.Fatalf("missing new session association: %#v", input.Metadata)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("new session did not submit unchanged content")
	}

}

func isolatedCollectorHarness(t *testing.T) serviceHarness {
	t.Helper()
	harness := newServiceHarness(t)
	harness.environment = append(harness.environment,
		"DIFFX_SERVER_URL=", "DIFFX_TOKEN=", "DIFFX_SOURCE_ID=conformance-source",
		"XDG_CONFIG_HOME="+t.TempDir(),
	)
	return harness
}

func collectorGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func waitCollectorStatus(t *testing.T, harness serviceHarness, matches func(hooks.Activity) bool) hooks.Activity {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var output []byte
	for time.Now().Before(deadline) {
		var err error
		output, err = harness.run(nil, "collector", "status")
		var statuses []hooks.Activity
		if err == nil && json.Unmarshal(output, &statuses) == nil {
			for _, status := range statuses {
				if matches(status) {
					return status
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(filepath.Join(harness.runtimeDir, "hooks-v4", "hooks.log"))
	t.Fatalf("collector status never matched: %s\n%s", output, log)
	return hooks.Activity{}
}

func collectorActivities(t *testing.T, harness serviceHarness) []hooks.Activity {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(harness.runtimeDir, "hooks-v4", "hooks.log"))
	if err != nil {
		t.Fatal(err)
	}
	var activities []hooks.Activity
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var activity hooks.Activity
		if err := json.Unmarshal(line, &activity); err != nil || activity.Time.IsZero() {
			t.Fatalf("invalid structured collector activity: %s: %v", line, err)
		}
		activities = append(activities, activity)
	}
	return activities
}

func TestHookIgnoresParentWorkspaceAndReviewRecoversExplicitBranch(t *testing.T) {
	harness := isolatedCollectorHarness(t)
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
	server := testsupport.Server(t, service, store)
	defer server.Close()
	harness.environment = append(harness.environment, "DIFFX_SERVER_URL="+server.URL, "DIFFX_TOKEN=test-token")
	root := createWorktree(t, "renamed-repository")
	collectorGit(t, root, "checkout", "--", "value.txt")
	collectorGit(t, root, "branch", "-M", "main")
	branch := "codex/recovered-feature"
	collectorGit(t, root, "checkout", "-b", branch)
	after := "committed feature\ncomplete captured contents\n"
	if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	collectorGit(t, root, "add", "value.txt")
	collectorGit(t, root, "commit", "--quiet", "-m", "feature")
	collectorGit(t, root, "checkout", "main")
	if dirty := collectorGit(t, root, "status", "--porcelain"); dirty != "" {
		t.Fatalf("fixture checkout should be clean: %s", dirty)
	}
	configFile := filepath.Join(t.TempDir(), "machine.json")
	config, err := json.Marshal(map[string]interface{}{"state": harness.state, "port": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, config, 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Dir(root)
	promptHook(t, harness, hookInput(t, workspace), "--harness", "codex", "--config-file", configFile)
	status := waitCollectorStatus(t, harness, func(activity hooks.Activity) bool {
		return activity.InputPath == workspace && activity.Stage == "discovery" && activity.Status == "complete"
	})
	if status.FileCount != 0 {
		t.Fatalf("non-Git hook discovered child repositories: %+v", status)
	}

	captures, err := filepath.Glob(filepath.Join(harness.runtimeDir, "hooks-v4", "jobs", "*", "capture.pending.json"))
	if err != nil || len(captures) != 0 {
		t.Fatalf("non-Git hook captured child repositories: %v %v", captures, err)
	}
	output, err := harness.run(nil, "sync", "--path", root, "--branch", branch, "--base", "main", "--config-file", configFile, "--no-browser")
	if err != nil {
		t.Fatalf("explicit branch recovery: %v %s", err, output)
	}
	serverStatus, contexts := waitHookCatalog(t, harness, 1)
	var recovered *contextservice.Context
	for index := range contexts {
		if contexts[index].Branch != nil && *contexts[index].Branch == branch {
			recovered = &contexts[index]
		}
	}
	if recovered == nil || recovered.ChangedFileCount == nil || *recovered.ChangedFileCount != 1 {
		t.Fatalf("missing committed feature branch: %+v", contexts)
	}
	base := serverStatus.BrowserURL + "/api/v2/contexts/" + recovered.ID
	var diff review.RepositoryDiff
	requestJSON(t, base+"/diffs/current?scope=all", &diff)
	if diff.Branch != branch || len(diff.Files) != 1 || diff.Files[0].Path != "value.txt" {
		t.Fatalf("wrong branch diff: %+v", diff)
	}
	var contents review.FileContents
	requestJSON(t, base+"/diffs/"+diff.ID+"/files/"+diff.Files[0].ID+"/contents?scope=all&versionId="+diff.VersionID+"&fileVersion="+diff.Files[0].Fingerprint, &contents)
	if contents.Before != "before\n" || contents.After != after {
		t.Fatalf("branch object contents incomplete: %+v", contents)
	}
	if recovered.Observation == nil || recovered.Observation.RepositoryKey == "" || recovered.Observation.CheckoutKey == "" || recovered.Observation.BranchID == "" {
		t.Fatalf("explicit branch recovery lacks provenance: %+v", recovered)
	}
}

func TestCollectorRetryDeliversCaptureAfterHealthOutageAndCheckoutRemoval(t *testing.T) {
	harness := isolatedCollectorHarness(t)
	root := createWorktree(t, "ephemeral-checkout")
	collectorGit(t, root, "branch", "-M", "main")
	requests := make(chan ingestion.Request, 4)
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			if !available.Load() {
				paths, err := filepath.Glob(filepath.Join(harness.runtimeDir, "hooks-v4", "jobs", "*", "capture.pending.json"))
				if err != nil || len(paths) != 1 {
					t.Errorf("health contacted before capture persisted: %v %v", paths, err)
				} else {
					data, readErr := os.ReadFile(paths[0])
					var saved struct {
						Request ingestion.Request `json:"request"`
					}
					if readErr != nil || json.Unmarshal(data, &saved) != nil || saved.Request.SubmissionID == "" {
						t.Errorf("health contacted before complete immutable capture: %v %s", readErr, data)
					}
				}
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "recovered-database", ProtocolVersion: ingestion.ProtocolVersion, QueuedIngestion: true, IngestionEnabled: true})
			return
		}
		if r.URL.Path != "/api/v2/ingestion-jobs" || r.Header.Get("X-Diffx-State") != "recovered-database" {
			http.NotFound(w, r)
			return
		}
		var input ingestion.Request
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		_ = json.NewEncoder(w).Encode(ingestion.Job{ID: input.SubmissionID, SubmissionID: input.SubmissionID, State: "succeeded", ContextID: "recovered-context"})
	}))
	defer server.Close()
	harness.environment = append(harness.environment, "DIFFX_SERVER_URL="+server.URL, "DIFFX_TOKEN=isolated-token")
	promptHook(t, harness, hookInput(t, root), "--harness", "codex")
	waitCollectorStatus(t, harness, func(activity hooks.Activity) bool { return activity.Status == "waiting" })
	paths, err := filepath.Glob(filepath.Join(harness.runtimeDir, "hooks-v4", "jobs", "*", "*.pending.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("capture not persisted before health: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Request ingestion.Request `json:"request"`
	}
	if err := json.Unmarshal(data, &captured); err != nil || captured.Request.SubmissionID == "" || len(captured.Request.Scopes) != 3 {
		t.Fatalf("invalid durable capture: %v %s", err, data)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	otherRequests := make(chan struct{}, 1)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/health" {
			_ = json.NewEncoder(w).Encode(ingestion.Health{StateID: "wrong-database", ProtocolVersion: ingestion.ProtocolVersion, QueuedIngestion: true, IngestionEnabled: true})
			return
		}
		select {
		case otherRequests <- struct{}{}:
		default:
		}
		http.Error(w, "wrong destination", http.StatusBadRequest)
	}))
	defer other.Close()
	wrong := harness
	wrong.environment = append(append([]string(nil), harness.environment...), "DIFFX_SERVER_URL="+other.URL)
	wrong.requireRun(t, nil, "collector", "retry")
	select {
	case <-otherRequests:
		t.Fatal("retry sent capture to wrong destination")
	case <-time.After(300 * time.Millisecond):
	}
	available.Store(true)
	harness.requireRun(t, nil, "collector", "retry")
	var delivered ingestion.Request
	select {
	case delivered = <-requests:
	case <-time.After(10 * time.Second):
		log, _ := os.ReadFile(filepath.Join(harness.runtimeDir, "hooks-v4", "hooks.log"))
		t.Fatalf("retry lost removed checkout's capture: %s", log)
	}
	before, _ := json.Marshal(captured.Request)
	after, _ := json.Marshal(delivered)
	if !bytes.Equal(before, after) {
		t.Fatalf("retry mutated immutable capture: before=%s after=%s", before, after)
	}
	status := waitCollectorStatus(t, harness, func(activity hooks.Activity) bool {
		return activity.Status == "complete" && activity.ContextID == "recovered-context"
	})
	if status.SubmissionID != captured.Request.SubmissionID || status.Path != root {
		t.Fatalf("retry acknowledgement lost source metadata: %+v", status)
	}
	for _, activity := range collectorActivities(t, harness) {
		if strings.Contains(activity.Error, "isolated-token") || strings.Contains(activity.Destination, "isolated-token") {
			t.Fatalf("collector activity leaked token: %+v", activity)
		}
	}
}
