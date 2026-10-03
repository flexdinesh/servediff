package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
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
	stateID := "state:" + settings.State
	if settings.State == "memory" {
		stateID = fmt.Sprint(os.Getpid())
	}
	status := Status{State: "running", InstanceID: fmt.Sprint(os.Getpid()), StateID: stateID, PID: os.Getpid(), Version: "test", ProtocolVersion: controlapi.ProtocolVersion, Settings: settings, URL: "http://127.0.0.1:7981"}
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

func publishTestEndpoint(t *testing.T, client *Client, status Status, afterStatus func(), captures *atomic.Int32) {
	t.Helper()
	token := strings.Repeat("t", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/control/v1/status":
			if afterStatus != nil {
				afterStatus()
			}
			_ = json.NewEncoder(w).Encode(status)
		case "/control/v1/captures":
			if captures != nil {
				captures.Add(1)
			}
			_ = json.NewEncoder(w).Encode(contextservice.Submission{Context: contextservice.Context{ID: status.StateID}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	if err := PublishDescriptor(client.RuntimeDirectory, Descriptor{Endpoint: server.URL, Token: token, Status: status}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureConnectionKeepsAuthenticatedEndpointDuringDescriptorSwap(t *testing.T) {
	client := &Client{RuntimeDirectory: t.TempDir(), Executable: "must-not-run"}
	settings := Settings{Host: "127.0.0.1", Port: 7981, State: "A"}
	initial := Status{State: "running", InstanceID: "instance-A", StateID: "database-A", ProtocolVersion: controlapi.ProtocolVersion, Settings: settings, URL: "http://127.0.0.1:7981"}
	replacement := initial
	replacement.InstanceID, replacement.StateID, replacement.Settings.State = "instance-B", "database-B", "B"
	var initialCaptures, replacementCaptures atomic.Int32
	// Replace discovery while the original authenticated status is in flight.
	publishTestEndpoint(t, client, initial, func() {
		publishTestEndpoint(t, client, replacement, nil, &replacementCaptures)
	}, &initialCaptures)
	connection, err := client.EnsureConnection(t.Context(), settings, Explicit{State: true})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := connection.Capture(t.Context(), "submission", []byte("patch"), "")
	if err != nil {
		t.Fatal(err)
	}
	if connection.Status().InstanceID != initial.InstanceID || submission.Context.ID != initial.StateID || initialCaptures.Load() != 1 || replacementCaptures.Load() != 0 {
		t.Fatalf("submission redirected: status=%+v submission=%+v writes=%d/%d", connection.Status(), submission, initialCaptures.Load(), replacementCaptures.Load())
	}
}

func TestRecoveryRejectsForeignStateAndSettings(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Status)
		want   error
	}{
		{"settings", func(status *Status) { status.Settings.State = "other.db" }, ErrSettingsChanged},
		{"port", func(status *Status) { status.Settings.Port++ }, ErrSettingsChanged},
		{"replaced same path", func(status *Status) { status.StateID = "new-database" }, ErrStateChanged},
		{"memory restart", func(status *Status) { status.InstanceID = "new-instance" }, ErrMemoryRestarted},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{RuntimeDirectory: t.TempDir(), Executable: "must-not-run"}
			initial := Status{State: "running", InstanceID: "initial", StateID: "database", ProtocolVersion: controlapi.ProtocolVersion, Settings: Settings{Host: "127.0.0.1", Port: 7981, State: "state.db"}}
			if test.name == "memory restart" {
				initial.Settings.State = "memory"
			}
			publishTestEndpoint(t, client, initial, nil, nil)
			connection, err := client.EnsureConnection(t.Context(), initial.Settings, Explicit{})
			if err != nil {
				t.Fatal(err)
			}
			replacement := initial
			test.change(&replacement)
			var captures atomic.Int32
			publishTestEndpoint(t, client, replacement, nil, &captures)
			if _, err := client.RecoverConnection(t.Context(), connection); !errors.Is(err, test.want) {
				t.Fatalf("recovery = %v, want %v", err, test.want)
			}
			if captures.Load() != 0 {
				t.Fatal("rejected replacement received payload")
			}
		})
	}
}

func TestRecoveryRestoresAcceptedPersistentSettings(t *testing.T) {
	client := testClient(t)
	settings := DefaultSettings()
	settings.State = filepath.Join(t.TempDir(), "custom.db")
	connection, err := client.EnsureConnection(t.Context(), settings, Explicit{State: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := client.RecoverConnection(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status().Settings != connection.Status().Settings || recovered.Status().StateID != connection.Status().StateID || recovered.Status().InstanceID == connection.Status().InstanceID {
		t.Fatalf("recovery changed accepted state: %+v => %+v", connection.Status(), recovered.Status())
	}
}

func TestRecoveryNeverRecreatesStoppedMemoryStore(t *testing.T) {
	client := testClient(t)
	settings := DefaultSettings()
	settings.State = "memory"
	connection, err := client.EnsureConnection(t.Context(), settings, Explicit{State: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RecoverConnection(t.Context(), connection); !errors.Is(err, ErrMemoryRestarted) {
		t.Fatalf("memory recovery = %v", err)
	}
	status, err := client.Status(t.Context())
	if err != nil || status.State != "stopped" {
		t.Fatalf("memory restarted: %+v %v", status, err)
	}
}

func TestRecoveryWaitHonorsDeadlineAndCancellation(t *testing.T) {
	for _, path := range []func(string) string{OwnershipPath, LifecyclePath} {
		t.Run(filepath.Base(path("")), func(t *testing.T) {
			client := &Client{RuntimeDirectory: t.TempDir(), Executable: "must-not-run"}
			lock, err := processlock.Acquire(path(client.RuntimeDirectory))
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			previous := &Connection{status: Status{StateID: "database", Settings: Settings{State: "state.db"}}}
			if _, err := client.RecoverConnection(ctx, previous); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("waiting deadline = %v", err)
			}
			ctx, stop := context.WithCancel(t.Context())
			stop()
			if _, err := client.RecoverConnection(ctx, previous); !errors.Is(err, context.Canceled) {
				t.Fatalf("waiting cancellation = %v", err)
			}
		})
	}
}

func TestBoundConnectionRequiresDatabaseIdentity(t *testing.T) {
	client := &Client{RuntimeDirectory: t.TempDir(), Executable: "must-not-run"}
	status := Status{State: "running", InstanceID: "instance", ProtocolVersion: controlapi.ProtocolVersion, Settings: DefaultSettings()}
	publishTestEndpoint(t, client, status, nil, nil)
	if _, err := client.EnsureConnection(t.Context(), status.Settings, Explicit{}); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("missing state identity = %v", err)
	}
}

func TestCancelledStartupDoesNotLaunchDaemon(t *testing.T) {
	client := &Client{RuntimeDirectory: t.TempDir(), Executable: "must-not-run"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.spawnConnection(ctx, DefaultSettings()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup = %v", err)
	}
	if _, err := os.Stat(LogPath(client.RuntimeDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled startup created log: %v", err)
	}
}
