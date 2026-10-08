package conformance

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/daemon"
)

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
