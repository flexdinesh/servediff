package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	client := &Client{RuntimeDirectory: t.TempDir()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Stop(ctx); err != nil {
			t.Errorf("cleanup stop: %v", err)
		}
	})
	return client
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
