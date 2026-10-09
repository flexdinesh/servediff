package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
)

const serverPatch = "diff --git a/value.txt b/value.txt\n--- a/value.txt\n+++ b/value.txt\n@@ -1 +1 @@\n-old\n+new\n"

func TestRunForcesCloseWhenIdleConnectionExhaustsShutdownGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan Status, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Settings{Host: "127.0.0.1", Port: 0, State: "memory"}, "", nil,
			func(status Status, _ *contextservice.Submission) { ready <- status })
	}()
	var status Status
	select {
	case status = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timed out")
	}
	address, err := url.Parse(status.BrowserURL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", address.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// An incomplete request remains active beyond the shutdown grace period.
	if _, err := connection.Write([]byte("GET / HTTP/1.1\r\nHost: ")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("forced shutdown should succeed: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("shutdown exceeded grace and cleanup budget")
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, connection); err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("connection survived service shutdown")
		}
	}
}

func TestDefaultPortExhaustionLeavesListenersRunning(t *testing.T) {
	var listeners []net.Listener
	for port := 7981; port <= 7990; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listeners = append(listeners, listener)
			t.Cleanup(func() { _ = listener.Close() })
		}
	}
	listener, err := listenWeb(Settings{Host: "127.0.0.1", Port: -1})
	if err == nil {
		_ = listener.Close()
		t.Fatal("all default ports should be occupied")
	}
	for _, listener := range listeners {
		connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatalf("listener affected: %v", err)
		}
		_ = connection.Close()
	}
}
