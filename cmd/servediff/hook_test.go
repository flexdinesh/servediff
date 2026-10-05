package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/hooks"
)

func TestHookNormalizesNativeInputAndExplicitArguments(t *testing.T) {
	root := t.TempDir()
	path, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	event, err := parseHookEvent([]string{"--agent", "codex"}, strings.NewReader(`{"cwd":`+string(path)+`,"session_id":"session","hook_event_name":"Stop","stop_hook_active":true}`), io.Discard)
	if err != nil || event.Path != root || event.RunID != "session" || event.Agent != "codex" {
		t.Fatalf("native input: %#v, %v", event, err)
	}
	event, err = parseHookEvent([]string{"--agent", "pi", "--path", root, "--run-id", "explicit", "--config-file", filepath.Join(root, "machine.json")}, nil, io.Discard)
	if err != nil || event.Path != root || event.RunID != "explicit" || !filepath.IsAbs(event.ConfigFile) {
		t.Fatalf("explicit args: %#v, %v", event, err)
	}
}

func TestHookInvalidEventsFailOpenWithoutConversationOutput(t *testing.T) {
	t.Setenv("SERVEDIFF_RUNTIME_DIR", t.TempDir())
	for _, input := range []string{`{`, `{}`, `{"cwd":"/tmp"} {}`, `null`} {
		var output strings.Builder
		if err := runHook([]string{"--agent", "claude"}, strings.NewReader(input), &output); err != nil || output.Len() != 0 {
			t.Fatalf("invalid event leaked into conversation: %v %q", err, output.String())
		}
	}
}

func TestHookSeparatesEffectiveRoutesWithoutPersistingCredentials(t *testing.T) {
	args := []string{"--agent", "pi", "--path", t.TempDir()}
	t.Setenv("SERVEDIFF_SERVER_URL", "https://first.example.com")
	t.Setenv("SERVEDIFF_TOKEN", "private-first-token")
	first, err := parseHookEvent(args, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVEDIFF_SERVER_URL", "https://second.example.com")
	second, err := parseHookEvent(args, nil, io.Discard)
	if err != nil || first.Routing == second.Routing {
		t.Fatal("different destinations share worker routing")
	}
	t.Setenv("SERVEDIFF_TOKEN", "private-second-token")
	third, err := parseHookEvent(args, nil, io.Discard)
	if err != nil || second.Routing == third.Routing {
		t.Fatal("different credentials share worker routing")
	}
	encoded, err := json.Marshal(third)
	if err != nil || strings.Contains(string(encoded), "private-second-token") {
		t.Fatal("hook event persisted credentials")
	}
}

func TestHookRetryNeedsNoCheckoutOrNativeInput(t *testing.T) {
	event, err := parseHookEvent([]string{"--agent", "codex", "--retry"}, nil, io.Discard)
	if err != nil || !event.Retry || event.Path != "" || event.Base != "auto" {
		t.Fatalf("retry: %#v %v", event, err)
	}
}

func TestCollectorStatusEmptyAndRetryStayFinite(t *testing.T) {
	t.Setenv("SERVEDIFF_RUNTIME_DIR", t.TempDir())
	var output strings.Builder
	if err := runCollector([]string{"status"}, &output); err != nil || strings.TrimSpace(output.String()) != "[]" {
		t.Fatalf("status: %q %v", output.String(), err)
	}
	output.Reset()
	if err := runCollector([]string{"retry"}, &output); err != nil || output.Len() != 0 {
		t.Fatalf("retry: %q %v", output.String(), err)
	}
}

func TestHookValidatesMovedAndCopiedIdentity(t *testing.T) {
	t.Setenv("SERVEDIFF_SOURCE_ID", "hook-location-test")
	root := cliRepository(t)
	identity, err := collector.SourceIdentity(t.Context(), root, "hook-location-test", "", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	previous := hooks.Event{Path: root, Base: "HEAD", Identity: identity, Resolved: true}
	engine := newHookEngine(t.TempDir())
	copyRoot := filepath.Join(t.TempDir(), "copy")
	if err := os.CopyFS(copyRoot, os.DirFS(root)); err != nil {
		t.Fatal(err)
	}
	current := previous
	current.Path = copyRoot
	if err := engine.ValidateLocation(previous, current); err == nil || !strings.Contains(err.Error(), "identity copied") {
		t.Fatalf("copied source accepted: %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := engine.ValidateLocation(previous, current); err != nil {
		t.Fatalf("relocated source rejected: %v", err)
	}
	if _, err := engine.Normalize(t.Context(), previous); !errors.Is(err, hooks.ErrUnavailable) {
		t.Fatalf("removed source not recoverable: %v", err)
	}
	current.Identity = "different-source"
	if _, err := engine.Normalize(t.Context(), current); !errors.Is(err, hooks.ErrUnavailable) {
		t.Fatalf("replaced source not rejected: %v", err)
	}
}
