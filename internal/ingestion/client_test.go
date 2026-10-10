package ingestion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientRejectsPreviousProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Health{StateID: "database", ProtocolVersion: 3, QueuedIngestion: true, IngestionEnabled: true})
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "secret").Health(t.Context()); err == nil {
		t.Fatal("accepted server with incompatible retry protection")
	}
}

func TestClientRejectsRedirects(t *testing.T) {
	var received atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Store(true) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := NewClient(redirect.URL, "secret")
	if _, err := client.Health(t.Context()); err == nil {
		t.Fatal("health followed redirect")
	}
	if _, err := client.SyncTo(t.Context(), Request{SubmissionID: "id"}, "state", nil); err == nil {
		t.Fatal("upload followed redirect")
	}
	if received.Load() {
		t.Fatal("credential sent to redirected destination")
	}
}
func TestClientRequiresCompletedMatchingJob(t *testing.T) {
	for _, job := range []Job{
		{ID: "job", SubmissionID: "other", State: "succeeded", ContextID: "context"},
		{ID: "job", SubmissionID: "id", State: "succeeded"},
		{ID: "job", SubmissionID: "id", State: "invalid"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(job) }))
		_, err := NewClient(server.URL, "secret").SyncTo(context.Background(), Request{SubmissionID: "id"}, "state", nil)
		server.Close()
		if err == nil {
			t.Fatalf("accepted invalid completion: %+v", job)
		}
	}
}
