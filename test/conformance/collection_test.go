package conformance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavedConfigTemporaryOverridesAndTargetedChange(t *testing.T) {
	harness := newServiceHarness(t)
	root := createWorktree(t, "hooked")
	if output, err := harness.run(nil, "change", "--path", root); err == nil || !strings.Contains(string(output), "stopped") {
		t.Fatalf("hook should require a running service: %v, %s", err, output)
	}
	harness.requireRun(t, nil, "service", "config", "set", "state", harness.state)
	harness.requireRun(t, nil, "service", "config", "set", "port", "0")
	configPath := filepath.Join(harness.runtimeDir, "config.json")
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	harness.requireRun(t, nil, "--path", root, "--no-browser")
	before := harness.status(t)
	var catalog contextCatalog
	requestJSON(t, before.URL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 1 {
		t.Fatalf("registered contexts: %#v", catalog)
	}
	id := catalog.Contexts[0].ID
	client := http.Client{Timeout: 5 * time.Second}
	stream, err := client.Get(before.URL + "/api/v2/events")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("event connection: %s", stream.Status)
	}
	reader := bufio.NewReader(stream.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "connected") {
		t.Fatalf("subscription: %s, %v", line, err)
	}
	harness.requireRun(t, nil, "change", "--path", root, "--branch", "feature", "--detail", "Agent finished")
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			break
		}
	}
	var event struct{ Kind, ContextID, Branch, Detail string }
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "change" || event.ContextID != id || event.Branch != "feature" || event.Detail != "Agent finished" {
		t.Fatalf("hook delivery: %#v", event)
	}
	stream.Body.Close()
	unknown := createWorktree(t, "unregistered")
	if _, err := harness.run(nil, "change", "--path", unknown); err == nil {
		t.Fatal("hook admitted an unregistered repository")
	}
	var original currentDiff
	requestJSON(t, before.URL+"/api/v2/contexts/"+id+"/diffs/current?scope=all", &original)
	if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte("changed again\n"), 0600); err != nil {
		t.Fatal(err)
	}
	harness.requireRun(t, nil, "change", "--path", root)
	var refreshed currentDiff
	requestJSON(t, before.URL+"/api/v2/contexts/"+id+"/diffs/current?scope=all", &refreshed)
	if refreshed.VersionID == original.VersionID || refreshed.ID != original.ID {
		t.Fatal("demand refresh lost identity or reused stale version")
	}
	harness.requireRun(t, nil, "service", "restart", "--config", fmt.Sprintf("{\"port\":%d}", before.Settings.Port))
	after := harness.status(t)
	if before.InstanceID == after.InstanceID || before.URL != after.URL || after.Worktrees != 1 {
		t.Fatalf("override restart: %#v", after)
	}
	unchanged, err := os.ReadFile(configPath)
	if err != nil || string(saved) != string(unchanged) {
		t.Fatal("temporary override persisted")
	}
	harness.requireRun(t, nil, "service", "config", "remove", "port")
	value := harness.requireRun(t, nil, "service", "config", "get", "port")
	if strings.TrimSpace(string(value)) != "null" {
		t.Fatalf("remove should restore default: %s", value)
	}
}
