package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestEscapedDirectoryPreservesFlagDelimiter(t *testing.T) {
	values, err := parseOptions([]string{"--no-browser", "--", "-repository"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if values.directory != "-repository" || !values.noBrowser {
		t.Fatalf("options = %#v", values)
	}
}

func TestHelpDocumentsServiceWithoutInternalFlags(t *testing.T) {
	var output bytes.Buffer
	if err := run(t.Context(), []string{"--help"}, nil, io.Discard, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "servediff service") || !strings.Contains(output.String(), "servediff serve") || strings.Contains(output.String(), "runtime-dir") {
		t.Fatalf("help = %s", output.String())
	}
}

func TestInternalDefaultPortIsNotAPublicPort(t *testing.T) {
	if _, err := parseOptions([]string{"--port", "-1"}, io.Discard); err == nil {
		t.Fatal("negative port accepted")
	}
	values, err := parseOptionsMode([]string{"--port", "-1", "--runtime-dir", "runtime"}, io.Discard, true)
	if err != nil || values.port != -1 || values.runtimeDir != "runtime" {
		t.Fatalf("internal options = %#v, %v", values, err)
	}
}
