package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageRequiresMarkerAndReplacesAssets(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("web"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stage(source, destination); err == nil {
		t.Fatal("expected unmarked destination error")
	}
	if err := os.WriteFile(filepath.Join(destination, "placeholder.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "stale.js"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stage(source, destination); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "index.html"))
	if err != nil || string(contents) != "web" {
		t.Fatalf("unexpected staged index: %q, %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "stale.js")); !os.IsNotExist(err) {
		t.Fatalf("stale asset remains: %v", err)
	}
}
