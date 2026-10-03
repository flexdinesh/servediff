package diffsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type gitHelperReady struct {
	PID     int
	Address string
}

func TestMain(tests *testing.M) {
	if mode := os.Getenv("SERVEDIFF_GIT_TEST_HELPER"); mode != "" {
		os.Exit(runGitTestHelper(mode))
	}
	os.Exit(tests.Run())
}

func runGitTestHelper(mode string) int {
	if mode == "head-drain" {
		if !strings.Contains(strings.Join(os.Args[1:], " "), "rev-parse --verify HEAD") {
			fmt.Fprintln(os.Stdout, "empty-tree-fallback")
			return 0
		}
		mode = "exit"
	}
	switch mode {
	case "success":
		fmt.Fprintln(os.Stdout, "ok")
		return 0
	case "error":
		fmt.Fprintln(os.Stderr, "helper failure")
		return 7
	case "overflow":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 1024))
		return 0
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 65*1024))
		return 0
	case "continuous-output":
		chunk := []byte(strings.Repeat("x", 32*1024))
		for {
			_, _ = os.Stdout.Write(chunk)
		}
	}
	signal.Ignore(syscall.SIGTERM)
	root := os.Getenv("SERVEDIFF_GIT_TEST_ROOT")
	if mode != "child" {
		executable, err := os.Executable()
		if err != nil {
			return 20
		}
		child := exec.Command(executable)
		child.Env = append(os.Environ(), "SERVEDIFF_GIT_TEST_HELPER=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			return 21
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(root, "child-ready")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = child.Process.Kill()
				return 22
			}
			time.Sleep(time.Millisecond)
		}
		if err := os.WriteFile(filepath.Join(root, "leader-ready"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			_ = child.Process.Kill()
			return 23
		}
		if mode == "exit" {
			return 0
		}
		for {
			<-time.After(time.Hour)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 24
	}
	ready, err := json.Marshal(gitHelperReady{PID: os.Getpid(), Address: listener.Addr().String()})
	if err != nil {
		return 25
	}
	fmt.Fprintln(os.Stdout, "child stdout")
	fmt.Fprintln(os.Stderr, "child stderr")
	if err := os.WriteFile(filepath.Join(root, "child-ready"), ready, 0o600); err != nil {
		return 26
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			return 27
		}
		_ = connection.Close()
	}
}

func installGitTestHelper(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), contents, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	t.Setenv("SERVEDIFF_GIT_TEST_ROOT", root)
	return root
}

func waitGitHelperReady(t *testing.T, root string) gitHelperReady {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var ready gitHelperReady
		data, err := os.ReadFile(filepath.Join(root, "child-ready"))
		_, leaderErr := os.Stat(filepath.Join(root, "leader-ready"))
		if err == nil && leaderErr == nil && json.Unmarshal(data, &ready) == nil && ready.PID > 0 && ready.Address != "" {
			process, err := os.FindProcess(ready.PID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				// Failing assertions must never leave this test's child running.
				connection, err := net.DialTimeout("tcp", ready.Address, 100*time.Millisecond)
				if err == nil {
					_ = connection.Close()
					_ = process.Kill()
				}
				_ = process.Release()
			})
			return ready
		}
		select {
		case <-deadline.C:
			t.Fatal("Git helper did not reach filesystem handshake")
		case <-ticker.C:
		}
	}
}

func assertGitHelperStopped(t *testing.T, ready gitHelperReady) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", ready.Address, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = connection.Close()
		if time.Now().After(deadline) {
			t.Fatal("Git descendant still running after cleanup")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGitCancellationKillsOwnedDescendantsAndReleasesSlots(t *testing.T) {
	root := installGitTestHelper(t)
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "hang")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := runGit(ctx, root, 1024, "ignored")
		finished <- err
	}()
	ready := waitGitHelperReady(t, root)
	started := time.Now()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Git cancellation remained blocked on inherited output")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation cleanup too slow: %v", elapsed)
	}
	assertGitHelperStopped(t, ready)
	if len(gitProcesses.active) != 0 || len(gitProcesses.admitted) != 0 {
		t.Fatal("cancelled Git invocation retained admission")
	}
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "success")
	if output, err := runGit(t.Context(), root, 1024, "ignored"); err != nil || output != "ok\n" {
		t.Fatalf("subsequent Git command failed: %q %v", output, err)
	}
}

func TestGitLeaderExitBoundsInheritedOutputDrain(t *testing.T) {
	root := installGitTestHelper(t)
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "exit")
	finished := make(chan error, 1)
	go func() {
		_, err := runGit(t.Context(), root, 1024, "ignored")
		finished <- err
	}()
	ready := waitGitHelperReady(t, root)
	select {
	case err := <-finished:
		if !errors.Is(err, exec.ErrWaitDelay) {
			t.Fatalf("incomplete pipe drain treated as success: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Git leader exit remained blocked on descendant output")
	}
	assertGitHelperStopped(t, ready)
	if len(gitProcesses.active) != 0 || len(gitProcesses.admitted) != 0 {
		t.Fatal("Git leader exit retained admission")
	}
}

func TestGitDeadlineKillsOwnedDescendantsAndPreservesDeadlineError(t *testing.T) {
	root := installGitTestHelper(t)
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "hang")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := runGit(ctx, root, 1024, "ignored")
		finished <- err
	}()
	ready := waitGitHelperReady(t, root)
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Git deadline remained blocked on descendant output")
	}
	assertGitHelperStopped(t, ready)
	if len(gitProcesses.active) != 0 || len(gitProcesses.admitted) != 0 {
		t.Fatal("Git deadline retained admission")
	}
}

func TestGitHeadDrainFailureCannotBecomeUnbornHeadFallback(t *testing.T) {
	root := installGitTestHelper(t)
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "head-drain")
	finished := make(chan error, 1)
	go func() {
		source := gitSource{root: root}
		_, _, err := source.headAndBase(t.Context())
		finished <- err
	}()
	ready := waitGitHelperReady(t, root)
	select {
	case err := <-finished:
		if !errors.Is(err, exec.ErrWaitDelay) {
			t.Fatalf("pipe failure converted to unborn HEAD: %v", err)
		}
		var failure *gitFailure
		if errors.As(err, &failure) {
			t.Fatal("pipe failure classified as a semantic Git exit")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HEAD query remained blocked on descendant output")
	}
	assertGitHelperStopped(t, ready)
}

func TestGitProcessErrorsAndOutputLimitsRemainVisible(t *testing.T) {
	root := installGitTestHelper(t)
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "error")
	_, err := runGit(t.Context(), root, 1024, "ignored")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "helper failure") {
		t.Fatalf("process error changed: %v", err)
	}
	for _, mode := range []string{"overflow", "stderr-overflow"} {
		t.Setenv("SERVEDIFF_GIT_TEST_HELPER", mode)
		if _, err := runGit(t.Context(), root, 8, "ignored"); !errors.Is(err, errOutputLimit) {
			t.Fatalf("%s error hidden: %v", mode, err)
		}
	}
}

func TestGitOutputLimitTerminatesContinuousWriter(t *testing.T) {
	root := installGitTestHelper(t)
	t.Setenv("SERVEDIFF_GIT_TEST_HELPER", "continuous-output")
	started := time.Now()
	if _, err := runGit(t.Context(), root, 8, "ignored"); !errors.Is(err, errOutputLimit) {
		t.Fatalf("continuous output limit hidden: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("output limit retained worker until command timeout: %v", elapsed)
	}
	if len(gitProcesses.active) != 0 || len(gitProcesses.admitted) != 0 {
		t.Fatal("output limit retained admission")
	}
}
