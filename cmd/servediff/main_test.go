package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

type testListener struct{ address net.Addr }

func (listener testListener) Accept() (net.Conn, error) { return nil, errors.New("closed") }
func (listener testListener) Close() error              { return nil }
func (listener testListener) Addr() net.Addr            { return listener.address }

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

func TestListenRangeAdvancesPastBusyPort(t *testing.T) {
	var output bytes.Buffer
	listener, err := listenRangeWith("127.0.0.1", 7981, 7982, &output, func(_ string, address string) (net.Listener, error) {
		if strings.HasSuffix(address, ":7981") {
			return nil, errors.New("busy")
		}
		return testListener{address: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7982}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if listener.Addr().(*net.TCPAddr).Port != 7982 {
		t.Fatalf("unexpected listener: %s", listener.Addr())
	}
	if output.String() != "  port 7981 is busy; trying 7982\n" {
		t.Fatalf("unexpected output: %q", output.String())
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

func TestProcessIdentityHeader(t *testing.T) {
	handler := processHandler{pid: 42, next: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/", nil))
	if response.Header().Get(processHeader) != "42" {
		t.Fatalf("unexpected process header: %q", response.Header().Get(processHeader))
	}
}

func TestConfirmationAnswers(t *testing.T) {
	if !isYes("YES\n") || isYes("no") || isYes("") {
		t.Fatal("unexpected confirmation parsing")
	}
}
