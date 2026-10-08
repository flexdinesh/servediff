package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/config"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/review"
)

func TestForegroundPipedPatchAndPersistentHistory(t *testing.T) {
	harness := newServiceHarness(t)
	patch, err := os.ReadFile("../fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	configuration, err := json.Marshal(config.Values{Host: "127.0.0.1", State: harness.state, Port: &port, RetentionDays: 7, Server: "http://127.0.0.1:1", Token: "remote-token"})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, configuration, 0600); err != nil {
		t.Fatal(err)
	}
	harness.environment = append(harness.environment, "SERVEDIFF_CONFIG_PATH="+configPath, "SERVEDIFF_SERVER_URL=http://127.0.0.1:1", "SSH_CONNECTION=test")
	client := &daemon.Client{RuntimeDirectory: harness.runtimeDir}
	var original review.RepositoryDiff
	for _, source := range []string{"pipe", "file"} {
		t.Run(source, func(t *testing.T) {
			var stdin io.Reader = bytes.NewReader(patch)
			var args []string
			if source == "file" {
				path := filepath.Join(t.TempDir(), "saved.patch")
				if err := os.WriteFile(path, patch, 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				stdin, args = file, []string{"--no-browser"}
			}
			logPath := filepath.Join(t.TempDir(), "output.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command := exec.Command(serviceBinary, args...)
			command.Env, command.Dir, command.Stdin = harness.environment, t.TempDir(), stdin
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			var status daemon.Status
			deadline := time.Now().Add(8 * time.Second)
			for {
				status, err = client.Status(t.Context())
				if err == nil && status.State == "running" {
					break
				}
				select {
				case err := <-done:
					output, _ := os.ReadFile(logPath)
					t.Fatalf("patch server exited: %v\n%s", err, output)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("patch server unavailable")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if status.PID != command.Process.Pid || status.WatchPath != "" {
				t.Fatalf("patch not served in invoking process: %+v", status)
			}
			var catalog contextservice.Page
			requestJSON(t, status.BrowserURL+"/api/v2/contexts", &catalog)
			if len(catalog.Contexts) != 1 {
				t.Fatalf("patch duplicated or lost: %+v", catalog)
			}
			entry := catalog.Contexts[0]
			if entry.Source != "stdin" || entry.RepositoryID != nil || entry.Branch != nil || !entry.Capabilities.Diff.Scopes.Allows(review.DiffAll) || entry.Capabilities.Diff.Scopes.Allows(review.DiffStaged) || entry.Capabilities.Files.Contents.Enabled() {
				t.Fatalf("incorrect patch identity/capabilities: %+v", entry)
			}
			var diff review.RepositoryDiff
			requestJSON(t, status.BrowserURL+"/api/v2/contexts/"+entry.ID+"/diffs/current?scope=all", &diff)
			if diff.Source != "stdin" || len(diff.Files) != 12 {
				t.Fatalf("patch contents: %+v", diff)
			}
			if source == "pipe" {
				original = diff
			} else if diff.ID != original.ID || diff.VersionID != original.VersionID {
				t.Fatalf("restart lost patch identity: before=%+v after=%+v", original, diff)
			}
			response, err := http.Post(status.BrowserURL+"/api/v2/ingestions", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("patch exposed HTTP ingestion: %d", response.StatusCode)
			}
			output, err := harness.run(patch, "--no-browser")
			if err == nil || !strings.Contains(string(output), "--replace") {
				t.Fatalf("patch replacement not guarded: %s %v", output, err)
			}
			if err := client.Stop(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("patch server did not exit gracefully")
			}
			output, err = os.ReadFile(logPath)
			if err != nil || !strings.Contains(string(output), status.BrowserURL+"/contexts/"+entry.ID) || !strings.Contains(string(output), "mode:               pipe") || !strings.Contains(string(output), "ctrl-c to stop") {
				t.Fatalf("incorrect startup output: %s %v", output, err)
			}
		})
	}
}

func TestForegroundRejectsPathAndPipedStdinBeforeStartup(t *testing.T) {
	harness := newServiceHarness(t)
	root := createWorktree(t, "conflicting-input")
	for _, args := range [][]string{{root}, {"--path", root}, {"pipe"}} {
		output, err := harness.run([]byte("not a patch"), args...)
		if err == nil || !strings.Contains(string(output), "cannot be combined") {
			t.Fatalf("accepted conflicting input %v: %s %v", args, output, err)
		}
	}
	if _, err := daemon.ReadDescriptor(harness.runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("invalid input started a service: %v", err)
	}
}

func TestForegroundWatchReplacementAndPersistentHistory(t *testing.T) {
	harness := newServiceHarness(t)
	first, second := createWorktree(t, "first-local"), createWorktree(t, "second-local")
	client := &daemon.Client{RuntimeDirectory: harness.runtimeDir}
	start := func(path string, extra ...string) (*exec.Cmd, chan error) {
		t.Helper()
		args := append([]string{path, "--state", harness.state, "--port", "0", "--no-browser"}, extra...)
		command := exec.Command(serviceBinary, args...)
		command.Env = append(harness.environment, "SERVEDIFF_SERVER_URL=http://127.0.0.1:1")
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		t.Cleanup(func() { _ = command.Process.Kill() })
		return command, done
	}
	await := func(previous string) daemon.Status {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			status, err := client.Status(t.Context())
			if err == nil && status.State == "running" && status.InstanceID != previous {
				return status
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("foreground instance unavailable")
		return daemon.Status{}
	}
	original, done := start(first)
	status := await("")
	if status.PID != original.Process.Pid || status.WatchPath != first {
		t.Fatalf("wrong foreground owner: %+v", status)
	}
	response, err := http.Post(status.BrowserURL+"/api/v2/ingestions", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("foreground exposed HTTP ingestion: %d", response.StatusCode)
	}
	if output, err := harness.run(nil, second, "--no-browser"); err == nil || !strings.Contains(string(output), "--replace") {
		t.Fatalf("noninteractive replacement not guarded: %s %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(first, "value.txt"), []byte("watch update\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		var catalog contextCatalog
		requestJSON(t, status.BrowserURL+"/api/v2/contexts", &catalog)
		if len(catalog.Contexts) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watch failed to ingest edit")
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, replacementDone := start(second, "--replace", "--host", "0.0.0.0")
	replacement := await(status.InstanceID)
	if replacement.Settings.Host != "0.0.0.0" {
		t.Fatal("host flag ignored")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("old foreground did not exit gracefully: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old process survived replacement")
	}
	var catalog contextCatalog
	requestJSON(t, replacement.BrowserURL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 3 {
		t.Fatalf("replacement lost history: %+v", catalog)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-replacementDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("replacement did not terminate")
	}
}
