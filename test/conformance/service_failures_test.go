package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/processlock"
)

// A transition happens after the upstream authenticated response was received,
// but before the CLI can act on it. No scheduling delay determines the race.
func installControlTransition(t *testing.T, harness serviceHarness, path string, transition func(daemon.Descriptor) error) <-chan error {
	t.Helper()
	descriptor, err := daemon.ReadDescriptor(harness.runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := url.Parse(descriptor.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	forwarder := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	completed := make(chan error, 1)
	var once sync.Once
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forwarded := request.Clone(request.Context())
		forwarded.URL.Scheme, forwarded.URL.Host = upstream.Scheme, upstream.Host
		forwarded.Host, forwarded.RequestURI = upstream.Host, ""
		result, err := forwarder.Do(forwarded)
		if err != nil {
			// Preserve an interrupted transport, rather than manufacturing an
			// application rejection that must not be retried.
			dropControlResponse(response)
			return
		}
		defer result.Body.Close()
		body, err := io.ReadAll(result.Body)
		if err != nil {
			dropControlResponse(response)
			return
		}
		if request.URL.Path == path && result.StatusCode == http.StatusOK {
			once.Do(func() { completed <- transition(descriptor) })
			if request.Method == http.MethodPost {
				dropControlResponse(response)
				return
			}
		}
		for name, values := range result.Header {
			response.Header()[name] = values
		}
		response.WriteHeader(result.StatusCode)
		_, _ = response.Write(body)
	}))
	t.Cleanup(func() {
		current, err := daemon.ReadDescriptor(harness.runtimeDir)
		if err == nil && current.Endpoint == proxy.URL {
			if err := daemon.PublishDescriptor(harness.runtimeDir, descriptor); err != nil {
				t.Errorf("restore untransitioned test descriptor: %v", err)
			}
		}
		proxy.Close()
	})
	proxied := descriptor
	proxied.Endpoint = proxy.URL
	if err := daemon.PublishDescriptor(harness.runtimeDir, proxied); err != nil {
		t.Fatal(err)
	}
	return completed
}

func dropControlResponse(response http.ResponseWriter) {
	hijacker, ok := response.(http.Hijacker)
	if !ok {
		panic(http.ErrAbortHandler)
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	_ = connection.Close()
}

func awaitControlTransition(t *testing.T, completed <-chan error) {
	t.Helper()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("inject service transition: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not reach the injected transition")
	}
}

func replaceTestDaemon(harness serviceHarness, descriptor daemon.Descriptor, state string, port int, mutate func() error) error {
	// Lifecycle commands must reach the original authenticated endpoint, rather
	// than recursively entering the proxy that is holding the CLI response.
	if err := daemon.PublishDescriptor(harness.runtimeDir, descriptor); err != nil {
		return err
	}
	if output, err := harness.run(nil, "service", "stop"); err != nil {
		return fmt.Errorf("stop original: %w: %s", err, output)
	}
	if mutate != nil {
		if err := mutate(); err != nil {
			return err
		}
	}
	if output, err := harness.run(nil, "service", "start", "--state", state, "--port", strconv.Itoa(port)); err != nil {
		return fmt.Errorf("start replacement: %w: %s", err, output)
	}
	return nil
}

func TestDaemonSubmissionCannotFollowDiscoveryToDifferentDatabase(t *testing.T) {
	harness := newServiceHarness(t)
	before := harness.start(t)
	otherState := filepath.Join(t.TempDir(), "other.db")
	completed := installControlTransition(t, harness, "/control/v1/status", func(descriptor daemon.Descriptor) error {
		return replaceTestDaemon(harness, descriptor, otherState, before.Settings.Port, nil)
	})
	patch, err := os.ReadFile("../fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	output, err := harness.run(patch, "--state", harness.state, "--no-browser")
	awaitControlTransition(t, completed)
	if err == nil {
		t.Fatalf("replacement database must reject submission: %s", output)
	}
	after := harness.status(t)
	if after.InstanceID == before.InstanceID || after.Settings.State != otherState || after.Captures != 0 {
		t.Fatalf("CLI restarted or wrote to replacement: before=%#v after=%#v output=%s", before, after, output)
	}
	harness.requireRun(t, nil, "service", "restart", "--state", harness.state)
	if original := harness.status(t); original.Captures != 0 {
		t.Fatalf("failed discovery transition unexpectedly committed original capture: %#v", original)
	}
}

func TestDaemonSubmissionRejectsListenerChangeAfterDiscovery(t *testing.T) {
	harness := newServiceHarness(t)
	before := harness.start(t)
	var reservation net.Listener
	defer func() {
		if reservation != nil {
			_ = reservation.Close()
		}
	}()
	completed := installControlTransition(t, harness, "/control/v1/status", func(descriptor daemon.Descriptor) error {
		return replaceTestDaemon(harness, descriptor, harness.state, 0, func() error {
			var err error
			reservation, err = net.Listen("tcp", net.JoinHostPort(before.Settings.Host, strconv.Itoa(before.Settings.Port)))
			return err
		})
	})
	patch, err := os.ReadFile("../fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	output, err := harness.run(patch, "--no-browser")
	awaitControlTransition(t, completed)
	if err == nil {
		t.Fatalf("changed listener must not produce stale success URL: %s", output)
	}
	after := harness.status(t)
	if after.InstanceID == before.InstanceID || after.Settings.Port == before.Settings.Port || after.Captures != 0 {
		t.Fatalf("CLI followed or restarted changed listener: before=%#v after=%#v output=%s", before, after, output)
	}
}

func TestDaemonLostAcknowledgementRejectsChangedState(t *testing.T) {
	for _, kind := range []string{"different database", "replaced database", "memory"} {
		t.Run(kind, func(t *testing.T) {
			harness := newServiceHarness(t)
			state := harness.state
			if kind == "memory" {
				state = "memory"
			}
			harness.requireRun(t, nil, "service", "start", "--state", state, "--port", "0")
			before := harness.status(t)
			replacementState := state
			if kind == "different database" {
				replacementState = filepath.Join(t.TempDir(), "replacement.db")
			}
			completed := installControlTransition(t, harness, "/control/v1/captures", func(descriptor daemon.Descriptor) error {
				var mutate func() error
				if kind == "replaced database" {
					mutate = func() error {
						for _, suffix := range []string{"", "-wal", "-shm"} {
							if err := os.Remove(state + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
								return err
							}
						}
						return nil
					}
				}
				return replaceTestDaemon(harness, descriptor, replacementState, before.Settings.Port, mutate)
			})
			patch, err := os.ReadFile("../fixtures/sample.diff")
			if err != nil {
				t.Fatal(err)
			}
			output, err := harness.run(patch, "--no-browser")
			awaitControlTransition(t, completed)
			if err == nil || !strings.Contains(string(output), "may have been") {
				t.Fatalf("lost acknowledgement needs explicit uncertain outcome, without replay: err=%v output=%s", err, output)
			}
			after := harness.status(t)
			if after.InstanceID == before.InstanceID || after.Settings.State != replacementState || after.Captures != 0 {
				t.Fatalf("uncertain submission mutated replacement: before=%#v after=%#v output=%s", before, after, output)
			}
			if kind == "different database" {
				harness.requireRun(t, nil, "service", "restart", "--state", state)
				if original := harness.status(t); original.Captures != 1 {
					t.Fatalf("original committed capture was lost or duplicated: %#v", original)
				}
			}
		})
	}
}

func TestDaemonCaptureValidationDoesNotRestart(t *testing.T) {
	harness := newServiceHarness(t)
	before := harness.start(t)
	output, err := harness.run([]byte("not a Git patch\n"), "--no-browser")
	if err == nil {
		t.Fatalf("invalid patch must fail: %s", output)
	}
	after := harness.status(t)
	if after.InstanceID != before.InstanceID || after.Captures != 0 {
		t.Fatalf("validation failure restarted or mutated service: before=%#v after=%#v", before, after)
	}
}

const blockingGitSource = `package main
import (
 "encoding/json"
 "net"
 "os"
 "os/exec"
)
func main() {
 if len(os.Args) > 1 && os.Args[1] == "__blocked_child" {
  listener, err := net.Listen("tcp", "127.0.0.1:0")
  if err != nil { panic(err) }
  data, err := json.Marshal(struct {
   Address string
   ParentAddress string
   PID int
   ParentPID int
  }{listener.Addr().String(), os.Getenv("SERVEDIFF_TEST_GIT_PARENT_ADDRESS"), os.Getpid(), os.Getppid()})
  if err != nil { panic(err) }
  if err := os.WriteFile(os.Getenv("SERVEDIFF_TEST_GIT_READY"), data, 0600); err != nil { panic(err) }
  for { connection, err := listener.Accept(); if err != nil { return }; connection.Close() }
 }
 blocked := false
 if _, err := os.Stat(os.Getenv("SERVEDIFF_TEST_GIT_GATE")); err == nil {
  for _, argument := range os.Args[1:] { if argument == "diff" { blocked = true } }
 }
 if blocked {
  listener, err := net.Listen("tcp", "127.0.0.1:0")
  if err != nil { panic(err) }
  child := exec.Command(os.Args[0], "__blocked_child")
  child.Env = append(os.Environ(), "SERVEDIFF_TEST_GIT_PARENT_ADDRESS="+listener.Addr().String())
  child.Stdout, child.Stderr = os.Stdout, os.Stderr
  if err := child.Start(); err != nil { panic(err) }
  for { connection, err := listener.Accept(); if err != nil { return }; connection.Close() }
 }
 command := exec.Command(os.Getenv("SERVEDIFF_TEST_REAL_GIT"), os.Args[1:]...)
 command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
 if err := command.Run(); err != nil {
  if exit, ok := err.(*exec.ExitError); ok { os.Exit(exit.ExitCode()) }
  panic(err)
 }
}
`

func TestDaemonShutdownCancelsGitAndItsDescendants(t *testing.T) {
	harness := newServiceHarness(t)
	worktree := createWorktree(t, "blocked-git")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "helper.go")
	if err := os.WriteFile(source, []byte(blockingGitSource), 0600); err != nil {
		t.Fatal(err)
	}
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	build := exec.Command("go", "build", "-o", filepath.Join(directory, name), source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build blocking Git helper: %v\n%s", err, output)
	}
	gate, ready := filepath.Join(directory, "block"), filepath.Join(directory, "ready.json")
	for index, entry := range harness.environment {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			harness.environment[index] = key + "=" + directory + string(os.PathListSeparator) + value
		}
	}
	harness.environment = append(harness.environment,
		"SERVEDIFF_TEST_REAL_GIT="+realGit,
		"SERVEDIFF_TEST_GIT_GATE="+gate,
		"SERVEDIFF_TEST_GIT_READY="+ready)
	harness.requireRun(t, nil, worktree, "--state", harness.state, "--port", "0", "--no-browser")
	status := harness.status(t)
	var catalog contextCatalog
	requestJSON(t, status.URL+"/api/v2/contexts", &catalog)
	if len(catalog.Contexts) != 1 {
		t.Fatalf("missing registered worktree: %#v", catalog)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := http.Client{Timeout: 15 * time.Second}
		response, err := client.Get(status.URL + "/api/v2/contexts/" + catalog.Contexts[0].ID + "/diffs/current?scope=all")
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	var helper struct {
		Address       string
		ParentAddress string
		PID           int
		ParentPID     int
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		data, err := os.ReadFile(ready)
		if err == nil && json.Unmarshal(data, &helper) == nil && helper.Address != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("live diff did not enter blocked Git helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// The subprocess belongs to this isolated fixture. Clean up explicitly if
	// a regression leaves it alive, so a failed test does not leak processes.
	t.Cleanup(func() {
		for _, address := range []string{helper.Address, helper.ParentAddress} {
			if connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
				_ = connection.Close()
				for _, pid := range []int{helper.PID, helper.ParentPID} {
					if process, err := os.FindProcess(pid); err == nil {
						_ = process.Kill()
						_ = process.Release()
					}
				}
			}
		}
	})
	started := time.Now()
	stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	stop := exec.CommandContext(stopContext, serviceBinary, "service", "stop")
	stop.Env = harness.environment
	if output, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("stop daemon with blocked Git: %v\n%s", err, output)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("service stop exceeded cleanup bound: %s", elapsed)
	}
	for _, address := range []string{helper.Address, helper.ParentAddress} {
		if connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
			_ = connection.Close()
			t.Fatalf("Git process still running after service stop: %s", address)
		}
	}
	lock, err := processlock.TryAcquire(harness.state + ".lock")
	if err != nil {
		t.Fatalf("shutdown retained database ownership: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("blocked snapshot request outlived completed shutdown")
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	harness.requireRun(t, nil, "service", "start", "--state", harness.state, "--port", "0")
	requestJSON(t, harness.status(t).URL+"/api/v2/contexts/"+catalog.Contexts[0].ID+"/diffs/current?scope=all", &currentDiff{})
}
