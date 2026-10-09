package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
)

func TestNormalizeArgumentsAllowsFlagsAfterPath(t *testing.T) {
	actual := normalizeArguments([]string{".", "--port", "4000", "--no-browser"})
	expected := []string{"--port", "4000", "--no-browser", "."}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got %v, want %v", actual, expected)
	}
}

func TestOptionsDefaultToLocalhostAndAutomaticPort(t *testing.T) {
	values, err := parseOptionsMode([]string{".", "--host", "192.0.2.1", "--port", "8123"}, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	if values.host != "192.0.2.1" || values.port != 8123 || !values.portSet || !values.repositorySet {
		t.Fatalf("unexpected explicit options: %#v", values)
	}
	defaults, err := parseOptions([]string{"."}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.host != "127.0.0.1" || defaults.portSet {
		t.Fatalf("unexpected defaults: %#v", defaults)
	}
}

func TestVersionExitsWithoutLoadingInput(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(t.Context(), []string{"--version"}, nil, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "servediff dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestStartupOutputSummarizesDiff(t *testing.T) {
	var output bytes.Buffer
	writeStartup(&output, loadedInput{
		directory: "/work/repository",
		mode:      "git",
		processed: 37 * time.Millisecond,
		snapshot: review.RepositoryDiff{Files: []review.ChangedFile{
			{Additions: 3, Deletions: 1},
			{Additions: 2, Binary: true},
		}},
	}, "http://127.0.0.1:7981")
	value := output.String()
	for _, expected := range []string{
		"  servediff ",
		"  collected from:    /work/repository",
		"  mode:               git",
		"  url:                http://127.0.0.1:7981",
		"  diff statistics 37ms",
		"    2 files changed",
		"    5 additions",
		"    1 deletion",
		"    1 binary file",
		"  ctrl-c to stop.",
	} {
		if !strings.Contains(value, expected) {
			t.Fatalf("missing %q in %q", expected, value)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(value, "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("line lacks indentation: %q", line)
		}
	}
}

func TestOptionsTrackExplicitDefaults(t *testing.T) {
	omitted, err := parseOptions([]string{"."}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if omitted.hostSet || omitted.portSet || omitted.stateSet || omitted.webDirSet {
		t.Fatalf("omitted settings marked explicit: %#v", omitted)
	}
	explicit, err := parseOptionsMode([]string{".", "--host=127.0.0.1", "-p", "0", "--state=", "--web-dir="}, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	if !explicit.hostSet || !explicit.portSet || !explicit.stateSet || !explicit.webDirSet || explicit.port != 0 {
		t.Fatalf("explicit defaults lost: %#v", explicit)
	}
}

func testInputFile(t *testing.T, raw []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.diff")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestAcquireInputBoundsFixtureAndStdin(t *testing.T) {
	for _, source := range []string{"stdin", "fixture"} {
		for _, size := range []int{diffsource.MaxInputBytes, diffsource.MaxInputBytes + 1} {
			name := source + "/within-limit"
			if size > diffsource.MaxInputBytes {
				name = source + "/over-limit"
			}
			t.Run(name, func(t *testing.T) {
				file := testInputFile(t, bytes.Repeat([]byte("x"), size))
				values := options{directory: ".", repositorySet: true}
				if source == "fixture" {
					values.fixture = file.Name()
				}
				input, err := acquireInput(values, file)
				if size > diffsource.MaxInputBytes {
					if err == nil || !strings.Contains(err.Error(), "16 MiB") {
						t.Fatalf("oversized input accepted: %v", err)
					}
					return
				}
				if err != nil || input.Kind != "capture" || len(input.Raw) != size {
					t.Fatalf("bounded input: kind=%q bytes=%d err=%v", input.Kind, len(input.Raw), err)
				}
				if !filepath.IsAbs(input.SubmittedFrom) {
					t.Fatalf("capture provenance not absolute: %q", input.SubmittedFrom)
				}
			})
		}
	}
}

func TestAcquireInputPriority(t *testing.T) {
	stdin := testInputFile(t, []byte("stdin patch"))
	fixture := testInputFile(t, []byte("fixture patch"))
	input, err := acquireInput(options{fixture: fixture.Name(), directory: ".", repositorySet: true}, stdin)
	if err != nil || string(input.Raw) != "fixture patch" || input.Kind != "capture" {
		t.Fatalf("fixture priority: %#v %v", input, err)
	}
	input, err = acquireInput(options{directory: ".", repositorySet: true}, stdin)
	if err != nil || string(input.Raw) != "stdin patch" || input.Kind != "capture" {
		t.Fatalf("stdin priority over repository: %#v %v", input, err)
	}
}

func TestAcquireRepositoryInputDefaultsToCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	piped, err := redirected(stdin)
	if err != nil {
		t.Fatal(err)
	}
	if piped {
		t.Skip("null device is not a terminal-like input on this platform")
	}
	input, err := acquireInput(options{directory: "."}, stdin)
	if err != nil || input.Kind != "worktree" || input.Path != root {
		t.Fatalf("default directory input: %#v %v", input, err)
	}
	input, err = acquireInput(options{directory: "./service", repositorySet: true}, stdin)
	if err != nil || input.Kind != "worktree" || !filepath.IsAbs(input.Path) || filepath.Base(input.Path) != "service" {
		t.Fatalf("explicit directory input: %#v %v", input, err)
	}
}

func TestStartupPrintsEveryReviewURL(t *testing.T) {
	var output bytes.Buffer
	urls := []string{
		"http://localhost:7981/contexts/observation",
		"http://192.168.1.20:7981/contexts/observation",
		"http://10.0.0.5:7981/contexts/observation",
	}
	writeStartup(&output, loadedInput{mode: "git"}, urls...)
	for _, url := range urls {
		if !strings.Contains(output.String(), "  url:                "+url+"\n") {
			t.Fatalf("missing review URL %q: %s", url, output.String())
		}
	}
}
