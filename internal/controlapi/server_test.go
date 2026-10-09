package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifecycleAuthenticationAndIdempotentShutdown(t *testing.T) {
	var stops atomic.Int32
	server := httptest.NewServer(New("secret", func() (Status, error) {
		return Status{State: "running", ProtocolVersion: ProtocolVersion, InstanceID: "instance"}, nil
	}, func() { stops.Add(1) }))
	defer server.Close()
	for _, scenario := range []struct {
		token, origin, path string
		status              int
	}{
		{"", "", "/control/v1/status", 401},
		{"wrong", "", "/control/v1/status", 401},
		{"secret", "http://browser", "/control/v1/status", 403},
		{"secret", "", "/control/v1/status", 200},
	} {
		req, _ := http.NewRequest("GET", server.URL+scenario.path, nil)
		req.Header.Set("Authorization", "Bearer "+scenario.token)
		req.Header.Set("Origin", scenario.origin)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != scenario.status {
			t.Fatalf("status=%d want %d", response.StatusCode, scenario.status)
		}
	}
	client := NewClient(server.URL, "secret")
	for range 2 {
		if err := client.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for stops.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stops.Load() != 1 {
		t.Fatalf("shutdown called %d times", stops.Load())
	}
	status, err := client.Status(context.Background())
	if err != nil || status.State != "draining" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	req, _ := http.NewRequest("POST", server.URL+"/control/v1/ingest", nil)
	req.Header.Set("Authorization", "Bearer secret")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("retired ingestion status=%d", response.StatusCode)
	}
}
