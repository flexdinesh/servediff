package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/diffx/internal/diffsource"
)

func TestLocalInputDefaultsToCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	input, err := acquireLocalInput(options{directory: root, repositorySet: true}, nil)
	if err != nil || input.Kind != "worktree" || input.Path != root {
		t.Fatalf("path input: %+v %v", input, err)
	}
	input, err = acquireLocalInput(options{directory: "."}, nil)
	if err != nil || input.Kind != "worktree" || input.Path != root {
		t.Fatalf("default input: %+v %v", input, err)
	}
}

func TestLocalInputReadsPipeAndRedirectedFile(t *testing.T) {
	raw := []byte("a patch\n")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if _, err := writer.Write(raw); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()
	for _, stdin := range []*os.File{reader, testInputFile(t, raw)} {
		input, err := acquireLocalInput(options{directory: "."}, stdin)
		if err != nil || input.Kind != "capture" || !bytes.Equal(input.Raw, raw) || !filepath.IsAbs(input.SubmittedFrom) {
			t.Fatalf("patch input: %+v %v", input, err)
		}
	}
}

func TestLocalInputRejectsPathWithRedirectedStdin(t *testing.T) {
	for _, arguments := range [][]string{{"."}, {"--path", "."}} {
		values, err := parseOptions(arguments, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		stdin := testInputFile(t, []byte("patch"))
		if _, err := acquireLocalInput(values, stdin); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("accepted path with stdin: %v", err)
		}
		position, err := stdin.Seek(0, io.SeekCurrent)
		if err != nil || position != 0 {
			t.Fatalf("conflicting input consumed: %d %v", position, err)
		}
	}
}

func TestLocalPatchValidationBeforeStartup(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		raw  []byte
		want string
	}{
		{name: "invalid", raw: []byte("not a patch"), want: "No Git patch"},
		{name: "oversized", raw: bytes.Repeat([]byte("x"), diffsource.MaxInputBytes+1), want: "16 MiB"},
		{name: "base", args: []string{"--base", "HEAD"}, want: "base is only supported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DIFFX_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
			err := run(t.Context(), test.args, testInputFile(t, test.raw), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("patch validation: %v", err)
			}
		})
	}
}
