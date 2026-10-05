package serverapp_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/servediff/internal/ingestion"
)

func TestHealthIdentitySurvivesRestartAndRequiresAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first := start(t, path, true)
	if _, err := ingestion.NewClient(first.server.URL, "wrong").Health(t.Context()); err == nil {
		t.Fatal("unauthenticated health accepted")
	}
	health, err := ingestion.NewClient(first.server.URL, first.token).Health(t.Context())
	if err != nil || health.StateID != first.owner {
		t.Fatalf("health %#v, %v", health, err)
	}
	first.close()
	second := start(t, path, true)
	restarted, err := ingestion.NewClient(second.server.URL, second.token).Health(t.Context())
	if err != nil || restarted != health {
		t.Fatalf("persistent identity changed on restart: %#v, %v", restarted, err)
	}
	replacement := start(t, ":memory:", true)
	replaced, err := ingestion.NewClient(replacement.server.URL, replacement.token).Health(t.Context())
	if err != nil || replaced.StateID == health.StateID {
		t.Fatalf("replacement retained old identity: %#v, %v", replaced, err)
	}
}

func TestIngestionRejectsChangedDestinationBeforeSaving(t *testing.T) {
	input := collectGit(t)
	destination := start(t, ":memory:", true)
	client := ingestion.NewClient(destination.server.URL, destination.token)
	_, err := client.SubmitTo(t.Context(), input, "old-database")
	var problem *ingestion.Problem
	if !errors.As(err, &problem) || problem.Status != 409 {
		t.Fatalf("replacement destination accepted: %v", err)
	}
	if _, err := client.SubmitTo(t.Context(), input, destination.owner); err != nil {
		t.Fatalf("valid destination rejected: %v", err)
	}
}
