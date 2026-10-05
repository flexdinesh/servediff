package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/ingestion"
)

func cliRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "feature"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBareInvocationShowsHelpWithoutConsumingStdin(t *testing.T) {
	stdin := testInputFile(t, []byte("not a diff"))
	var output bytes.Buffer
	if err := run(t.Context(), nil, stdin, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	position, err := stdin.Seek(0, io.SeekCurrent)
	if err != nil || position != 0 {
		t.Fatalf("stdin consumed: %d %v", position, err)
	}
	if !strings.Contains(output.String(), "servediff review") || !strings.Contains(output.String(), "servediff pipe") {
		t.Fatalf("help: %s", output.String())
	}
}

func TestReviewManualAndAgentHookShareGitCollection(t *testing.T) {
	root := cliRepository(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	t.Setenv("SERVEDIFF_RUNTIME_DIR", runtimeDir)
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(runtimeDir, "config.json"))
	t.Setenv("SERVEDIFF_TOKEN", "secret-token")
	var received []ingestion.Request
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/ingestions" || r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request ingestion.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		received = append(received, request)
		if len(request.Scopes) == 0 {
			t.Error("missing scopes")
			http.Error(w, "bad request", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(ingestion.Receipt{ContextID: "observation", ReviewURL: server.URL + "/contexts/observation", MCPURL: server.URL + "/mcp/contexts/observation", Snapshot: request.Scopes[0].Snapshot})
	}))
	defer server.Close()
	t.Setenv("SERVEDIFF_SERVER_URL", server.URL)
	for _, trigger := range []string{"manual", "agent-hook"} {
		stdin := testInputFile(t, []byte("review must ignore redirected stdin"))
		arguments := []string{"review", "--path", root, "--source-id", "test-source", "--no-browser", "--trigger", trigger}
		if trigger == "agent-hook" {
			arguments = append(arguments, "--harness", "test-agent", "--run-id", "container-1")
		}
		var output bytes.Buffer
		if err := run(t.Context(), arguments, stdin, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		position, err := stdin.Seek(0, io.SeekCurrent)
		if err != nil || position != 0 {
			t.Fatalf("review consumed stdin: %d %v", position, err)
		}
		if !strings.Contains(output.String(), "/contexts/observation") || strings.Contains(output.String(), "service stop") {
			t.Fatalf("remote output: %s", output.String())
		}
	}
	if len(received) != 2 {
		t.Fatalf("received %d submissions", len(received))
	}
	for index, request := range received {
		expectedTrigger := []string{"manual", "agent-hook"}[index]
		if request.Metadata.Root != root || request.Metadata.Branch != "feature" || request.Metadata.SourceID != "test-source" || request.Metadata.Trigger != expectedTrigger || request.Metadata.RepositoryKey == "" || request.Metadata.CheckoutKey == "" || request.Metadata.Hostname == "" {
			t.Fatalf("metadata: %#v", request.Metadata)
		}
	}
	if received[0].SubmissionID == received[1].SubmissionID {
		t.Fatal("separate observations reused submission identity")
	}
	if received[1].Metadata.Agent != "test-agent" || received[1].Metadata.RunID != "container-1" {
		t.Fatalf("hook metadata: %#v", received[1].Metadata)
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "hooks" {
		t.Fatalf("remote ingestion created local service state: %v %v", entries, err)
	}
	log, err := os.ReadFile(filepath.Join(runtimeDir, "hooks", "hooks.log"))
	if err != nil || !strings.Contains(string(log), `"status":"acknowledged"`) {
		t.Fatalf("remote ingestion activity missing: %s %v", log, err)
	}
}

func TestPipeCollectsGitMetadata(t *testing.T) {
	root := cliRepository(t)
	patch, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	request, err := collectSubmission(t.Context(), "pipe", options{directory: root, sourceID: "test-source", trigger: "manual"}, testInputFile(t, patch))
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.RepositoryKey == "" || request.Metadata.Branch != "feature" || request.Metadata.Root != root || len(request.Scopes) != 1 {
		t.Fatalf("pipe metadata: %#v", request)
	}
}

func TestCollectorOptionsRejectAmbiguousInputsBeforeStartup(t *testing.T) {
	cases := [][]string{
		{"."}, {"change"}, {"watch"},
		{"review", "--server", "http://example.test", "--state", "memory"},
		{"review", "--trigger", "unknown"},
		{"review", "--fixture", "input.diff"},
		{"pipe", "--capture", "saved"},
		{"review", "--config", "{}"},
		{"review", "--host", "0.0.0.0"},
		{"service", "start", "--server", "http://example.test"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := run(t.Context(), args, nil, io.Discard, io.Discard); err == nil {
				t.Fatalf("accepted %v", args)
			}
		})
	}
}

func TestHelpDoesNotExposeEnvironmentCredentials(t *testing.T) {
	t.Setenv("SERVEDIFF_TOKEN", "secret-token")
	t.Setenv("SERVEDIFF_SERVER_URL", "https://secret-user:secret-password@example.test")
	var output bytes.Buffer
	if err := run(t.Context(), []string{"--help"}, nil, io.Discard, &output); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-token", "secret-user", "secret-password"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("help exposed %s", secret)
		}
	}
}

func TestConfigCLISetGetRemove(t *testing.T) {
	t.Setenv("SERVEDIFF_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	var output bytes.Buffer
	for _, args := range [][]string{{"service", "config", "set", "host", "0.0.0.0"}, {"service", "config", "get", "host"}, {"service", "config", "remove", "host"}, {"service", "config", "get", "host"}} {
		if err := run(t.Context(), args, nil, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(output.String(), `"0.0.0.0"`) || !strings.Contains(output.String(), `"127.0.0.1"`) {
		t.Fatalf("config output: %s", output.String())
	}
}

func TestReviewDefaultsToCurrentCheckout(t *testing.T) {
	root := cliRepository(t)
	t.Chdir(root)
	request, err := collectSubmission(t.Context(), "review", options{directory: ".", sourceID: "test-source", trigger: "manual"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.Root != root || request.Metadata.Branch != "feature" {
		t.Fatalf("current checkout: %#v", request.Metadata)
	}
}

func TestRemoteRetryReplaysCollectedSnapshot(t *testing.T) {
	root := cliRepository(t)
	var payloads [][]byte
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		payloads = append(payloads, raw)
		if len(payloads) == 1 {
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("newer content\n"), 0600); err != nil {
				t.Error(err)
				return
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		var request ingestion.Request
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(ingestion.Receipt{ContextID: "observation", ReviewURL: server.URL + "/contexts/observation", MCPURL: server.URL + "/mcp/contexts/observation", Snapshot: request.Scopes[0].Snapshot})
	}))
	defer server.Close()
	if err := run(t.Context(), []string{"review", "--path", root, "--server", server.URL, "--source-id", "test-source", "--no-browser"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 || !bytes.Equal(payloads[0], payloads[1]) {
		t.Fatalf("retry recollected or changed payload; submissions=%d", len(payloads))
	}
}

func TestRemoteRejectionDoesNotRetry(t *testing.T) {
	root := cliRepository(t)
	attempts := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(ingestion.Problem{Status: http.StatusForbidden, Detail: "unauthorized source"})
	}))
	defer server.Close()
	err := run(t.Context(), []string{"review", "--path", root, "--server", server.URL, "--source-id", "test-source", "--no-browser"}, nil, io.Discard, io.Discard)
	if err == nil || attempts != 1 {
		t.Fatalf("rejected submission: attempts=%d error=%v", attempts, err)
	}
}
