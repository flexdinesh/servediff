package daemon

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/processlock"
)

// Run the same detached executable path as production, with a small test server.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__daemon" {
		if err := helperDaemon(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperDaemon(args []string) error {
	flags := flag.NewFlagSet("__daemon", flag.ContinueOnError)
	settings := DefaultSettings()
	var dir string
	flags.StringVar(&settings.Host, "host", settings.Host, "")
	flags.IntVar(&settings.Port, "port", settings.Port, "")
	flags.StringVar(&settings.State, "state", "", "")
	flags.StringVar(&settings.WebDir, "web-dir", "", "")
	flags.StringVar(&dir, "runtime-dir", "", "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if settings.Host == "fail-start" {
		return errors.New("requested test startup failure")
	}
	lock, err := processlock.TryAcquire(OwnershipPath(dir))
	if err != nil {
		return err
	}
	defer lock.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	if settings.Port <= 0 {
		settings.Port = listener.Addr().(*net.TCPAddr).Port
	}
	status := Status{State: "running", InstanceID: fmt.Sprint(os.Getpid()), PID: os.Getpid(), Version: "test", ProtocolVersion: controlapi.ProtocolVersion, Settings: settings, URL: "http://127.0.0.1:7981"}
	d := Descriptor{Endpoint: "http://" + listener.Addr().String(), Token: strings.Repeat("x", 64), Status: status}
	done := make(chan struct{})
	server := &http.Server{Handler: controlapi.New(d.Token, nil, func() (Status, error) { return status, nil }, func() { close(done) })}
	if err := PublishDescriptor(dir, d); err != nil {
		return err
	}
	defer RemoveDescriptor(dir, status.InstanceID)
	go server.Serve(listener)
	<-done
	return server.Close()
}

func testClient(t *testing.T) *Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{RuntimeDirectory: t.TempDir(), Executable: executable}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Stop(ctx); err != nil {
			t.Errorf("cleanup stop: %v", err)
		}
	})
	return client
}

func TestConcurrentStartupAndRestart(t *testing.T) {
	client := testClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var group sync.WaitGroup
	results := make(chan Status, 8)
	errorsFound := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			status, err := client.Ensure(ctx, DefaultSettings(), Explicit{})
			if err != nil {
				errorsFound <- err
			} else {
				results <- status
			}
		})
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	var initial Status
	for status := range results {
		if initial.PID == 0 {
			initial = status
		}
		if initial.PID != status.PID {
			t.Errorf("duplicate daemons: %d and %d", initial.PID, status.PID)
		}
	}
	if initial.PID == 0 {
		t.Fatal("no daemon started")
	}
	requested := DefaultSettings()
	requested.Port = 4100
	if _, err := client.Ensure(ctx, requested, Explicit{Port: true}); err == nil {
		t.Fatal("conflicting port accepted")
	}
	status, err := client.Status(ctx)
	if err != nil || status.PID != initial.PID {
		t.Fatalf("conflict changed daemon: %+v %v", status, err)
	}
	restarted, err := client.Restart(ctx, requested, Explicit{Port: true})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.PID == initial.PID || restarted.Settings.Port != 4100 {
		t.Fatalf("restart = %+v", restarted)
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = client.Status(ctx)
	if err != nil || status.State != "stopped" {
		t.Fatalf("after stop = %+v %v", status, err)
	}
}

func TestOwnershipWithoutDescriptorNeverStarts(t *testing.T) {
	client := &Client{RuntimeDirectory: t.TempDir(), Executable: "must-not-run"}
	lock, err := processlock.Acquire(OwnershipPath(client.RuntimeDirectory))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	status, err := client.Ensure(context.Background(), DefaultSettings(), Explicit{})
	if !errors.Is(err, ErrUnavailable) || status.State != "unavailable" {
		t.Fatalf("ensure = %+v %v", status, err)
	}
}

func TestStaleDescriptorReportsStopped(t *testing.T) {
	client := testClient(t)
	d := Descriptor{Endpoint: "http://127.0.0.1:1", Token: strings.Repeat("x", 64), Status: Status{InstanceID: "stale", PID: os.Getpid(), State: "running"}}
	if err := PublishDescriptor(client.RuntimeDirectory, d); err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil || status.State != "stopped" {
		t.Fatalf("status = %+v %v", status, err)
	}
	if _, err := os.Stat(DescriptorPath(client.RuntimeDirectory)); err != nil {
		t.Fatal("status mutated stale descriptor")
	}
}

func TestStartupFailureIncludesChildError(t *testing.T) {
	client := testClient(t)
	settings := DefaultSettings()
	settings.Host = "fail-start"
	_, err := client.Start(context.Background(), settings, Explicit{Host: true})
	if err == nil || !strings.Contains(err.Error(), "requested test startup failure") {
		t.Fatalf("startup error = %v", err)
	}
}

func TestDescriptorRejectsRemoteEndpoint(t *testing.T) {
	dir := t.TempDir()
	d := Descriptor{Endpoint: "http://192.0.2.1:4000", Token: strings.Repeat("x", 64), Status: Status{InstanceID: "x"}}
	if err := PublishDescriptor(dir, d); err == nil {
		t.Fatal("remote endpoint accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid descriptor written: %v", err)
	}
}

func TestCompatibilityAndExplicitPortZero(t *testing.T) {
	settings := Settings{Host: "127.0.0.1", Port: 7981, State: "state"}
	status := Status{State: "running", Settings: settings, ProtocolVersion: controlapi.ProtocolVersion}
	requested := settings
	requested.Port = 0
	if err := checkSettings(status, requested, Explicit{Port: true}); err != nil {
		t.Fatal(err)
	}
	status.ProtocolVersion++
	if err := checkSettings(status, requested, Explicit{}); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("protocol = %v", err)
	}
}

func TestCrashedDaemonCanBeReplaced(t *testing.T) {
	client := testClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	initial, err := client.Start(ctx, DefaultSettings(), Explicit{})
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(initial.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	for {
		status, err := client.Status(ctx)
		if err == nil && status.State == "stopped" {
			break
		}
		if err := pause(ctx); err != nil {
			t.Fatal(err)
		}
	}
	replacement, err := client.Ensure(ctx, DefaultSettings(), Explicit{})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.PID == initial.PID {
		t.Fatal("dead daemon reused")
	}
}

func TestLogsRemainBounded(t *testing.T) {
	dir := t.TempDir()
	writer, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	message := strings.Repeat("x", logLimit+100)
	if count, err := writer.Write([]byte(message)); err != nil || count != len(message) {
		t.Fatalf("write = %d %v", count, err)
	}
	if _, err := writer.Write([]byte("next")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{LogPath(dir), LogPath(dir) + ".1"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > logLimit {
			t.Fatalf("unbounded log: %d", info.Size())
		}
	}
}
