package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLegacyShimsOnlyRemovesNodeEntrypoints(t *testing.T) {
	directory := t.TempDir()
	legacy := filepath.Join(directory, "servediff")
	current := filepath.Join(directory, "servediff.cmd")
	if err := os.WriteFile(legacy, []byte(`node C:\global\node_modules\servediff-workspace\apps\server\src\cli.ts`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("current Go command"), 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := removeLegacyShims(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != legacy {
		t.Fatalf("unexpected removals: %v", removed)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy shim remains: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current command removed: %v", err)
	}
}
