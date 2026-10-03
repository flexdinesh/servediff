package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
)

func TestSubmissionFailurePreservesUnknownCommitAndCancellation(t *testing.T) {
	transportError := errors.New("acknowledgement lost")
	cause := fmt.Errorf("%w; recovery failed: %w", transportError, context.Canceled)
	status := controlapi.Status{Settings: controlapi.Settings{State: "/review/custom.db"}}
	for _, kind := range []string{"worktree", "capture", "reopen"} {
		failure := submissionFailure(daemon.InitialInput{Kind: kind}, status, "retry-id", true, cause)
		if !errors.Is(failure, transportError) || !errors.Is(failure, context.Canceled) {
			t.Fatalf("%s lost failure causes: %v", kind, failure)
		}
		unknown := strings.Contains(failure.Error(), "may have been saved")
		if unknown != (kind != "reopen") {
			t.Fatalf("%s misreported commit outcome: %v", kind, failure)
		}
		if unknown && (!strings.Contains(failure.Error(), status.Settings.State) || !strings.Contains(failure.Error(), "retry-id")) {
			t.Fatalf("missing recovery provenance: %v", failure)
		}
	}
}

func TestNormalizeArgumentsAllowsFlagsAfterPath(t *testing.T) {
	actual := normalizeArguments([]string{".", "--port", "4000", "--no-browser"})
	expected := []string{"--port", "4000", "--no-browser", "."}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got %v, want %v", actual, expected)
	}
}

func TestOptionsDefaultToLocalhostAndAutomaticPort(t *testing.T) {
	values, err := parseOptions([]string{".", "--host", "192.0.2.1", "--port", "8123"}, io.Discard)
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
		"  serving directory:  /work/repository",
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
	explicit, err := parseOptions([]string{".", "--host=127.0.0.1", "-p", "0", "--state=", "--web-dir="}, io.Discard)
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
	input, err := acquireInput(options{capture: "saved-capture"}, stdin)
	if err != nil || input.Kind != "reopen" || input.CaptureID != "saved-capture" {
		t.Fatalf("capture reopening: %#v %v", input, err)
	}
	position, err := stdin.Seek(0, io.SeekCurrent)
	if err != nil || position != 0 {
		t.Fatalf("reopening consumed stdin: position=%d err=%v", position, err)
	}
	fixture := testInputFile(t, []byte("fixture patch"))
	input, err = acquireInput(options{fixture: fixture.Name(), directory: ".", repositorySet: true}, stdin)
	if err != nil || string(input.Raw) != "fixture patch" || input.Kind != "capture" {
		t.Fatalf("fixture priority: %#v %v", input, err)
	}
	input, err = acquireInput(options{directory: ".", repositorySet: true}, stdin)
	if err != nil || string(input.Raw) != "stdin patch" || input.Kind != "capture" {
		t.Fatalf("stdin priority over repository: %#v %v", input, err)
	}
}

func TestAcquireRepositoryInputRequiresExplicitPath(t *testing.T) {
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
	if _, err := acquireInput(options{directory: "."}, stdin); err == nil {
		t.Fatal("bare invocation accepted without repository or patch")
	}
	input, err := acquireInput(options{directory: "./service", repositorySet: true}, stdin)
	if err != nil || input.Kind != "worktree" || !filepath.IsAbs(input.Path) || filepath.Base(input.Path) != "service" {
		t.Fatalf("explicit directory input: %#v %v", input, err)
	}
}

func TestCaptureOptionsRejectConflictingInput(t *testing.T) {
	for _, arguments := range [][]string{
		{"--capture", "saved", "."},
		{"--capture", "saved", "--fixture", "patch.diff"},
	} {
		if _, err := parseOptions(arguments, io.Discard); err == nil {
			t.Fatalf("conflicting capture arguments accepted: %v", arguments)
		}
	}
}

func TestSubmissionOutputExplainsDaemonLifetime(t *testing.T) {
	var output bytes.Buffer
	writeSubmission(&output, contextservice.Submission{Context: contextservice.Context{ID: "capture-1", Kind: "capture"}}, time.Millisecond,
		"http://127.0.0.1:7981/contexts/capture-1", "http://127.0.0.1:7981/mcp/contexts/capture-1")
	value := output.String()
	for _, expected := range []string{"context ID:         capture-1", "capture ID:         capture-1", "/contexts/capture-1", "/mcp/contexts/capture-1", "servediff service stop"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("missing %q in %q", expected, value)
		}
	}
	if strings.Contains(value, "ctrl-c") {
		t.Fatal("submission output incorrectly suggests foreground server lifetime")
	}
}

func TestStoppedServiceStatusDoesNotStartService(t *testing.T) {
	t.Setenv("SERVEDIFF_RUNTIME_DIR", t.TempDir())
	var output bytes.Buffer
	if err := run(t.Context(), []string{"service", "status", "--json"}, nil, &output, io.Discard); !errors.Is(err, errServiceStopped) {
		t.Fatalf("stopped status: %v", err)
	}
	var status controlapi.Status
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "stopped" || status.PID != 0 {
		t.Fatalf("status started a service: %#v", status)
	}
}

func TestServiceOutputFormatsIPv6Listener(t *testing.T) {
	var output bytes.Buffer
	writeServiceStatus(&output, controlapi.Status{State: "running", Settings: controlapi.Settings{Host: "::", Port: 4000}}, false)
	if !strings.Contains(output.String(), "[::]:4000") {
		t.Fatalf("IPv6 listener lacks brackets: %q", output.String())
	}
}
