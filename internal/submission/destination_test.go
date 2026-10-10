package submission

import (
	"bytes"
	"encoding/json"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryRetriesExactPayloadAtPinnedDestination(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing credential")
		}
		if r.URL.Path == "/api/v2/health" {
			json.NewEncoder(w).Encode(ingestion.Health{ProtocolVersion: ingestion.ProtocolVersion, StateID: "database", QueuedIngestion: true, IngestionEnabled: true})
			return
		}
		if r.URL.Path != "/api/v2/ingestion-jobs" || r.Header.Get("X-Diffx-State") != "database" {
			t.Error("delivery lost destination identity")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, raw)
		if len(bodies) == 1 {
			http.Error(w, "unavailable", 503)
			return
		}
		json.NewEncoder(w).Encode(ingestion.Job{ID: "job", SubmissionID: "submission", State: "succeeded", ContextID: "context"})
	}))
	defer server.Close()
	destination, err := Resolve(t.Context(), server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := destination.Deliver(t.Context(), ingestion.Request{ProtocolVersion: ingestion.ProtocolVersion, SubmissionID: "submission"})
	if err != nil || receipt.ContextID != "context" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("retry changed immutable payload")
	}
}
func TestDeliveryRejectsMissingQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ingestion.Health{ProtocolVersion: ingestion.ProtocolVersion, StateID: "local"})
	}))
	defer server.Close()
	if _, err := Resolve(t.Context(), server.URL, "token"); err == nil {
		t.Fatal("accepted non-queue server")
	}
}
