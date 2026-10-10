package submission

import (
	"testing"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
)

func TestAcknowledgementExpiryAndStreamIsolation(t *testing.T) {
	root := t.TempDir()
	box, err := Open(t.Context(), root, "route")
	if err != nil {
		t.Fatal(err)
	}
	request := ingestion.Request{Metadata: ingestion.Metadata{SourceID: "source", RepositoryKey: "repo", CheckoutKey: "checkout", BranchID: "branch", Agent: "codex", RunID: "A"}}
	if err := box.Acknowledge(request, "fingerprint", "database", "context"); err != nil {
		t.Fatal(err)
	}
	box.Close()
	box, err = Open(t.Context(), root, "route")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	ack, err := box.Acknowledgement(request)
	if err != nil || ack.Fingerprint != "fingerprint" || ack.StateID != "database" || ack.ContextID != "context" {
		t.Fatalf("durable acknowledgement: %+v %v", ack, err)
	}
	for _, mutate := range []func(*ingestion.Metadata){
		func(m *ingestion.Metadata) { m.SourceID = "other" },
		func(m *ingestion.Metadata) { m.CheckoutKey = "other" },
		func(m *ingestion.Metadata) { m.BranchID = "other" },
		func(m *ingestion.Metadata) { m.RunID = "B" },
		func(m *ingestion.Metadata) { m.Agent = "claude" },
		func(m *ingestion.Metadata) { m.Comparison = &ingestion.Comparison{Kind: "branch", BaseRef: "main"} },
	} {
		other := request
		mutate(&other.Metadata)
		if ack, err := box.Acknowledgement(other); err != nil || ack != (Acknowledgement{}) {
			t.Fatalf("acknowledgement leaked across stream/session: %+v %v", ack, err)
		}
	}
	ack.At = time.Now().Add(-24*time.Hour - time.Minute)
	if err := box.write(box.acknowledgementPath(request), ack); err != nil {
		t.Fatal(err)
	}
	if ack, err := box.Acknowledgement(request); err != nil || ack != (Acknowledgement{}) {
		t.Fatalf("expired acknowledgement suppressed retention refresh: %+v %v", ack, err)
	}
	if items, err := box.Pending(); err != nil || len(items) != 0 {
		t.Fatalf("acknowledgements parsed as pending uploads: %+v %v", items, err)
	}
}
