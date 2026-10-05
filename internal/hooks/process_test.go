package hooks

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(tests *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__hook-worker" {
		marker := os.Getenv("SERVEDIFF_TEST_HOOK_MARKER")
		if marker == "" {
			os.Exit(2)
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil || len(data) != 0 {
			os.Exit(3)
		}
		// The hook launcher must return while the worker is still running.
		time.Sleep(200 * time.Millisecond)
		_, _ = os.Stdout.WriteString("worker stdout must not reach hook host\n")
		_, _ = os.Stderr.WriteString("worker stderr must not reach hook host\n")
		if err := os.WriteFile(marker, []byte("finished"), 0o600); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(tests.Run())
}

func TestDetachedWorkerReturnsBeforeFinishing(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "done")
	t.Setenv("SERVEDIFF_TEST_HOOK_MARKER", marker)
	if err := LaunchDetached(executable, directory, key("checkout")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hook blocked until worker finished")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil {
			if string(data) != "finished" {
				t.Fatalf("worker result = %q", data)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached worker did not finish")
}
