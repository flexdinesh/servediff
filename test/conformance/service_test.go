package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/processlock"
)

var serviceBinary string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "servediff-process-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "servediff"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	serviceBinary = filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", serviceBinary, "../../cmd/servediff")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(directory)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}

type serviceStatus struct {
	State      string `json:"state"`
	InstanceID string `json:"instanceId"`
	PID        int    `json:"pid"`
	URL        string `json:"url"`
	BrowserURL string `json:"browserUrl"`
	Settings   struct {
		Host  string `json:"host"`
		Port  int    `json:"port"`
		State string `json:"state"`
	} `json:"settings"`
	Worktrees int `json:"worktrees"`
	Captures  int `json:"captures"`
}

type contextEntry struct {
	ID   string  `json:"id"`
	Kind string  `json:"kind"`
	Root *string `json:"root"`
}

type contextCatalog struct {
	Contexts []contextEntry `json:"contexts"`
}

type currentDiff struct {
	ID        string `json:"id"`
	VersionID string `json:"versionId"`
	Files     []struct {
		Path string `json:"path"`
	} `json:"files"`
}

type serviceHarness struct {
	environment  []string
	state        string
	runtimeDir   string
	defaultState string
}

func newServiceHarness(t *testing.T) serviceHarness {
	t.Helper()
	directory := t.TempDir()
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "SERVEDIFF_RUNTIME_DIR") && !strings.EqualFold(key, "SERVEDIFF_EXIT_ON_STDIN_CLOSE") && !strings.EqualFold(key, "XDG_STATE_HOME") {
			environment = append(environment, entry)
		}
	}
	runtimeDir := filepath.Join(directory, "runtime")
	defaultState := filepath.Join(directory, "default-state")
	environment = append(environment, "SERVEDIFF_RUNTIME_DIR="+runtimeDir, "XDG_STATE_HOME="+defaultState)
	harness := serviceHarness{environment: environment, state: filepath.Join(directory, "state.db"), runtimeDir: runtimeDir, defaultState: defaultState}
	t.Cleanup(func() {
		output, err := harness.run(nil, "service", "stop")
		if err != nil {
			t.Errorf("stop isolated daemon: %v\n%s", err, output)
		}
	})
	return harness
}

func (harness serviceHarness) run(input []byte, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, serviceBinary, arguments...)
	command.Env = harness.environment
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	return command.CombinedOutput()
}

func (harness serviceHarness) requireRun(t *testing.T, input []byte, arguments ...string) []byte {
	t.Helper()
	output, err := harness.run(input, arguments...)
	if err != nil {
		t.Fatalf("servediff %v: %v\n%s", arguments, err, output)
	}
	return output
}

func (harness serviceHarness) status(t *testing.T) serviceStatus {
	t.Helper()
	output := harness.requireRun(t, nil, "service", "status", "--json")
	var status serviceStatus
	if err := json.Unmarshal(output, &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, output)
	}
	if status.State != "running" || status.PID <= 0 || status.InstanceID == "" {
		t.Fatalf("expected authenticated running status, got %#v", status)
	}
	return status
}

func (harness serviceHarness) start(t *testing.T) serviceStatus {
	t.Helper()
	harness.requireRun(t, nil, "service", "start", "--state", harness.state, "--port", "0")
	return harness.status(t)
}

func requestJSON(t *testing.T, endpoint string, result interface{}) {
	t.Helper()
	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("GET %s: %s: %s", endpoint, response.Status, body)
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		t.Fatal(err)
	}
}

func createWorktree(t *testing.T, name string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "servediff-test@example.invalid"},
		{"config", "user.name", "servediff test"},
		{"config", "commit.gpgsign", "false"},
	}
	for _, arguments := range commands {
		command := exec.Command("git", arguments...)
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}
	path := filepath.Join(directory, "value.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"add", "value.txt"}, {"commit", "--quiet", "-m", "initial"}} {
		command := exec.Command("git", arguments...)
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}
	if err := os.WriteFile(path, []byte(name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestDaemonConcurrentIngestion(t *testing.T) {
	harness := newServiceHarness(t)
	first, second := createWorktree(t, "first"), createWorktree(t, "second")
	type result struct {
		output []byte
		err    error
	}
	results := make(chan result, 2)
	for _, directory := range []string{first, second} {
		go func() {
			output, err := harness.run(nil, "review", directory, "--state", harness.state, "--port", "0", "--no-browser")
			results <- result{output: output, err: err}
		}()
	}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Errorf("concurrent submission: %v\n%s", result.err, result.output)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	status := harness.status(t)
	if status.Worktrees != 0 || status.Captures != 2 {
		t.Fatalf("both short-lived collectors must publish to one daemon: %#v", status)
	}
	var catalog contextCatalog
	requestJSON(t, status.URL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 2 || catalog.Contexts[0].ID == catalog.Contexts[1].ID {
		t.Fatalf("distinct worktrees lost: %#v", catalog)
	}
	for _, entry := range catalog.Contexts {
		if entry.Kind != "observation" {
			t.Fatalf("unexpected context kind: %#v", entry)
		}
		var diff currentDiff
		requestJSON(t, status.URL+"/api/v2/contexts/"+entry.ID+"/diffs/current?scope=all", &diff)
		if len(diff.Files) != 1 || diff.Files[0].Path != "value.txt" {
			t.Fatalf("worktree data unavailable after client exited: %#v", diff)
		}
	}
	harness.requireRun(t, nil, "review", first, "--no-browser")
	if repeated := harness.status(t); repeated.InstanceID != status.InstanceID || repeated.Captures != 2 {
		t.Fatalf("repeat collection must reuse daemon and unchanged observation: %#v", repeated)
	}
	var repeatedCatalog contextCatalog
	requestJSON(t, status.URL+"/api/v2/contexts", &repeatedCatalog)
	for _, entry := range repeatedCatalog.Contexts {
		found := false
		for _, original := range catalog.Contexts {
			if original.ID == entry.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("repeat collection replaced context: %#v", repeatedCatalog)
		}
	}
	if err := os.WriteFile(filepath.Join(first, "value.txt"), []byte("changed again\n"), 0600); err != nil {
		t.Fatal(err)
	}
	harness.requireRun(t, nil, "review", first, "--no-browser")
	if changed := harness.status(t); changed.Captures != 3 {
		t.Fatalf("changed content failed to create observation: %#v", changed)
	}
}

func TestReviewPrintsURLForSubmittedObservation(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "0.0.0.0"} {
		t.Run(host, func(t *testing.T) {
			harness := newServiceHarness(t)
			harness.requireRun(t, nil, "service", "config", "set", "host", host)
			output := harness.requireRun(t, nil, "review", createWorktree(t, "review-url"), "--state", harness.state, "--port", "0", "--no-browser")
			status := harness.status(t)
			var catalog contextCatalog
			requestJSON(t, status.BrowserURL+"/api/v2/contexts", &catalog)
			if len(catalog.Contexts) != 1 || status.Settings.Host != host {
				t.Fatalf("submission/listener: %#v %#v", catalog, status)
			}
			id := catalog.Contexts[0].ID
			want := "http://localhost:" + strconv.Itoa(status.Settings.Port) + "/contexts/" + id
			if !strings.Contains(string(output), "  url:                "+want+"\n") {
				t.Fatalf("missing submitted observation URL %s: %s", want, output)
			}
			var diff currentDiff
			requestJSON(t, "http://localhost:"+strconv.Itoa(status.Settings.Port)+"/api/v2/contexts/"+id+"/diffs/current?scope=all", &diff)
			if len(diff.Files) != 1 || diff.Files[0].Path != "value.txt" {
				t.Fatalf("URL selected wrong diff: %#v", diff)
			}
		})
	}
}

func TestDaemonCaptureSurvivesRestart(t *testing.T) {
	harness := newServiceHarness(t)
	before := harness.start(t)
	patch, err := os.ReadFile("../fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		harness.requireRun(t, patch, "pipe", "--no-browser")
	}
	var catalog contextCatalog
	requestJSON(t, before.URL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 1 {
		t.Fatalf("identical pipes must reuse one capture: %#v", catalog)
	}
	id := catalog.Contexts[0].ID
	for _, entry := range catalog.Contexts {
		if entry.Kind != "observation" {
			t.Fatalf("pipe must be first-class capture: %#v", entry)
		}
	}
	var original currentDiff
	requestJSON(t, before.URL+"/api/v2/contexts/"+id+"/diffs/current?scope=all", &original)
	if len(original.Files) != 12 || original.VersionID == "" {
		t.Fatalf("unexpected captured fixture: %#v", original)
	}
	harness.requireRun(t, nil, "service", "restart", "--state", harness.state, "--port", strconv.Itoa(before.Settings.Port))
	after := harness.status(t)
	if after.InstanceID == before.InstanceID || after.Captures != 1 || after.Settings != before.Settings || after.URL != before.URL {
		t.Fatalf("restart must replace process and retain data/settings: before=%#v after=%#v", before, after)
	}
	var restored currentDiff
	requestJSON(t, after.URL+"/api/v2/contexts/"+id+"/diffs/current?scope=all", &restored)
	if restored.ID != original.ID || restored.VersionID != original.VersionID || len(restored.Files) != len(original.Files) {
		t.Fatalf("captured diff changed across restart: original=%#v restored=%#v", original, restored)
	}
	if queried := harness.status(t); queried.Captures != 1 {
		t.Fatalf("querying an observation must not duplicate it: %#v", queried)
	}
	harness.requireRun(t, patch, "pipe", "--no-browser")
	var repeated contextCatalog
	requestJSON(t, after.URL+"/api/v2/contexts", &repeated)
	if len(repeated.Contexts) != 1 || repeated.Contexts[0].ID != id {
		t.Fatalf("restart lost dedupe identity: %#v", repeated)
	}
}

func TestDaemonIgnoresInheritedGitRouting(t *testing.T) {
	harness := newServiceHarness(t)
	first, second := createWorktree(t, "environment-origin"), createWorktree(t, "requested")
	harness.environment = append(harness.environment, "GIT_DIR="+filepath.Join(first, ".git"), "GIT_WORK_TREE="+first)
	harness.requireRun(t, nil, "review", second, "--state", harness.state, "--port", "0", "--no-browser")
	status := harness.status(t)
	var catalog contextCatalog
	requestJSON(t, status.URL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 1 || catalog.Contexts[0].Root == nil {
		t.Fatalf("requested worktree not registered: %#v", catalog)
	}
	want, err := filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(*catalog.Contexts[0].Root) != filepath.Clean(want) {
		t.Fatalf("inherited Git routing selected wrong worktree: got %q want %q", *catalog.Contexts[0].Root, want)
	}
}

func TestDaemonExplicitConflictDoesNotRestart(t *testing.T) {
	harness := newServiceHarness(t)
	before := harness.start(t)
	patch, err := os.ReadFile("../fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	output, err := harness.run(patch, "pipe", "--port", "4000", "--no-browser")
	if err == nil || !strings.Contains(string(output), "restart") {
		t.Fatalf("conflict should suggest explicit restart: err=%v output=%s", err, output)
	}
	output, err = harness.run(patch, "pipe", "--state", filepath.Join(t.TempDir(), "other.db"), "--no-browser")
	if err == nil {
		t.Fatalf("different state path must fail before capture: %s", output)
	}
	after := harness.status(t)
	if after.InstanceID != before.InstanceID || after.Captures != 0 || after.Settings.Host != before.Settings.Host {
		t.Fatalf("conflicts mutated running daemon: before=%#v after=%#v", before, after)
	}
}

func TestDaemonStopIsIdempotent(t *testing.T) {
	harness := newServiceHarness(t)
	harness.requireRun(t, nil, "service", "stop")
	harness.start(t)
	harness.requireRun(t, nil, "service", "stop")
	harness.requireRun(t, nil, "service", "stop")
	output, err := harness.run(nil, "service", "status", "--json")
	if err == nil {
		t.Fatalf("stopped status must return failure: %s", output)
	}
	var status serviceStatus
	if err := json.Unmarshal(output, &status); err != nil || status.State != "stopped" {
		t.Fatalf("expected stopped JSON status, err=%v output=%s", err, output)
	}
}

func TestDaemonRetriesCommittedCaptureUsingRunningState(t *testing.T) {
	harness := newServiceHarness(t)
	before := harness.start(t)
	descriptor, err := daemon.ReadDescriptor(harness.runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := url.Parse(descriptor.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	type committedCapture struct {
		id  string
		err error
	}
	committed := make(chan committedCapture, 1)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	forwarder := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forwarded := request.Clone(request.Context())
		forwarded.URL.Scheme, forwarded.URL.Host = upstream.Scheme, upstream.Host
		forwarded.Host = upstream.Host
		forwarded.RequestURI = ""
		result, err := forwarder.Do(forwarded)
		if err != nil {
			// Discovery probes after the deliberate crash must see the old
			// daemon as unreachable so the CLI can start its replacement.
			http.Error(response, "test daemon unavailable", http.StatusBadGateway)
			return
		}
		defer result.Body.Close()
		body, err := io.ReadAll(result.Body)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadGateway)
			return
		}
		if request.URL.Path != "/control/v1/ingestions" || result.StatusCode != http.StatusOK {
			for name, values := range result.Header {
				response.Header()[name] = values
			}
			response.WriteHeader(result.StatusCode)
			_, _ = response.Write(body)
			return
		}
		var accepted struct {
			Context contextEntry `json:"context"`
		}
		if err := json.Unmarshal(body, &accepted); err != nil {
			committed <- committedCapture{err: err}
			http.Error(response, err.Error(), http.StatusBadGateway)
			return
		}
		if err := crashVerifiedDaemon(request.Context(), harness.runtimeDir, descriptor); err != nil {
			committed <- committedCapture{err: err}
			http.Error(response, err.Error(), http.StatusBadGateway)
			return
		}
		committed <- committedCapture{id: accepted.Context.ID}
		// No acknowledgement reaches the CLI, although the first daemon
		// committed the capture and its durable deduplication record.
		hijacker, ok := response.(http.Hijacker)
		if !ok {
			panic(http.ErrAbortHandler)
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		_ = connection.Close()
	}))
	t.Cleanup(proxy.Close)
	proxied := descriptor
	proxied.Endpoint = proxy.URL
	if err := daemon.PublishDescriptor(harness.runtimeDir, proxied); err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile("../fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	// Omitted --state must reuse the running custom database, including
	// recovery after the accepted response is lost.
	harness.requireRun(t, patch, "pipe", "--no-browser")
	var accepted committedCapture
	select {
	case accepted = <-committed:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not intercept a committed capture")
	}
	if accepted.err != nil || accepted.id == "" {
		t.Fatalf("inject lost acknowledgement: %v", accepted.err)
	}
	after := harness.status(t)
	if after.InstanceID == before.InstanceID || after.Settings != before.Settings || after.Captures != 1 {
		t.Fatalf("retry must restore running settings and deduplicate capture: before=%#v after=%#v", before, after)
	}
	var catalog contextCatalog
	requestJSON(t, after.URL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 1 || catalog.Contexts[0].ID != accepted.id {
		t.Fatalf("retry lost or duplicated acknowledged database result: accepted=%q catalog=%#v", accepted.id, catalog)
	}
	if _, err := os.Stat(filepath.Join(harness.defaultState, "servediff", "state.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry unexpectedly opened default database: %v", err)
	}
}

func crashVerifiedDaemon(ctx context.Context, runtimeDir string, descriptor daemon.Descriptor) error {
	status, err := controlapi.NewClient(descriptor.Endpoint, descriptor.Token).Status(ctx)
	if err != nil {
		return err
	}
	if status.State != "running" || status.PID <= 0 || status.InstanceID != descriptor.Status.InstanceID || status.PID != descriptor.Status.PID || status.Captures != 1 {
		return errors.New("refusing to crash process without authenticated daemon identity and committed capture")
	}
	process, err := os.FindProcess(status.PID)
	if err != nil {
		return err
	}
	defer process.Release()
	if err := process.Kill(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lock, err := processlock.TryAcquire(daemon.OwnershipPath(runtimeDir))
		if err == nil {
			return lock.Close()
		}
		if !errors.Is(err, processlock.ErrLocked) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("crashed test daemon did not release ownership")
}
