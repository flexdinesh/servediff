package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
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
