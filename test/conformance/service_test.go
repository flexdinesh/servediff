package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var serviceBinary string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "diffx-process-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "diffx"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	serviceBinary = filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", serviceBinary, "../../cmd/diffx")
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
		if !strings.EqualFold(key, "DIFFX_RUNTIME_DIR") && !strings.EqualFold(key, "DIFFX_EXIT_ON_STDIN_CLOSE") && !strings.EqualFold(key, "XDG_STATE_HOME") {
			environment = append(environment, entry)
		}
	}
	runtimeDir := filepath.Join(directory, "runtime")
	defaultState := filepath.Join(directory, "default-state")
	environment = append(environment, "DIFFX_RUNTIME_DIR="+runtimeDir, "XDG_STATE_HOME="+defaultState)
	harness := serviceHarness{environment: environment, state: filepath.Join(directory, "state.db"), runtimeDir: runtimeDir, defaultState: defaultState}

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
		t.Fatalf("diffx %v: %v\n%s", arguments, err, output)
	}
	return output
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
		{"config", "user.email", "diffx-test@example.invalid"},
		{"config", "user.name", "diffx test"},
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
