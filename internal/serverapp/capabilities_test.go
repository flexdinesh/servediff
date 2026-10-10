package serverapp_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/flexdinesh/diffx/internal/contextservice"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/serverapp"
	"github.com/flexdinesh/diffx/internal/session"
)

func TestAdmissionCapabilitiesMatchHealthAndRoutes(t *testing.T) {
	for _, test := range []struct {
		name  string
		queue bool
		want  serverapp.Capabilities
	}{
		{"foreground", false, serverapp.Capabilities{}},

		{"remote queued", true, serverapp.Capabilities{IngestionEnabled: true, QueuedIngestion: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := reviewstore.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			user, err := store.User("local", "Local")
			if err != nil {
				t.Fatal(err)
			}
			service := contextservice.NewWithContext(t.Context(), store, user)
			defer service.Close()
			options := serverapp.Options{}
			if test.queue {
				options.Queue = store.Queue()
			}
			server := httptest.NewServer(serverapp.Handler(t.Context(), service, store, fstest.MapFS{}, "", options))
			defer server.Close()
			d := deployment{server: server}
			health := decode[ingestion.Health](t, request(t, d, "GET", "/api/v2/health", nil))
			if health.IngestionEnabled != test.want.IngestionEnabled || health.QueuedIngestion != test.want.QueuedIngestion {
				t.Fatalf("health capabilities: %+v, want %+v", health, test.want)
			}
			for path, enabled := range map[string]bool{"/api/v2/ingestions": false, "/api/v2/ingestion-jobs": test.want.QueuedIngestion} {
				response := request(t, d, "POST", path, bytes.NewBufferString("{}"))
				response.Body.Close()
				want := http.StatusNotFound
				if enabled {
					want = http.StatusBadRequest
				}
				if response.StatusCode != want {
					t.Fatalf("%s: status %d, want %d", path, response.StatusCode, want)
				}
			}
		})
	}
}

func TestLocalAndQueuedRemoteShareSnapshotContract(t *testing.T) {
	input := collectGit(t)
	input.Metadata.Trigger, input.Metadata.RunID = "manual", ""
	store, err := reviewstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.User("local", "Local")
	if err != nil {
		t.Fatal(err)
	}
	service := contextservice.NewWithContext(t.Context(), store, user)
	defer service.Close()
	submitted, err := service.Ingest(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(serverapp.Handler(t.Context(), service, store, fstest.MapFS{}, submitted.Context.ID, serverapp.Options{}))
	defer local.Close()
	remote := start(t, ":memory:", true)
	plugin := input
	plugin.Metadata.Trigger, plugin.Metadata.Agent, plugin.Metadata.RunID = "agent-hook", "codex", "session-1"
	plugin.Metadata.AgentSession = &ingestion.AgentSession{Harness: "codex", ID: "session-1", Name: "Feature work"}
	receipt, err := ingestion.NewClient(remote.server.URL, remote.token).SyncTo(t.Context(), plugin, remote.owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		deployment deployment
		id         string
		input      ingestion.Request
	}{
		{deployment{server: local}, submitted.Context.ID, input},
		{remote, receipt.ContextID, plugin},
	} {
		context := decode[contextservice.Context](t, request(t, test.deployment, "GET", "/api/v2/contexts/"+test.id, nil))
		if context.Observation == nil || !reflect.DeepEqual(*context.Observation, test.input.Metadata) {
			t.Fatalf("checkout/session metadata diverged: %+v, want %+v", context.Observation, test.input.Metadata)
		}
		if !reflect.DeepEqual(context.Capabilities, submitted.Context.Capabilities) || context.Capabilities.Diff.Refresh.Enabled() {
			t.Fatalf("snapshot capabilities depend on mode: %+v", context.Capabilities)
		}
		active := decode[struct {
			Capabilities session.Capabilities `json:"capabilities"`
		}](t, request(t, test.deployment, "GET", "/api/v2/contexts/"+test.id+"/session", nil))
		if !reflect.DeepEqual(active.Capabilities, context.Capabilities) {
			t.Fatal("catalog/session capabilities diverged")
		}
		for _, scope := range input.Scopes {
			snapshot := decode[review.RepositoryDiff](t, request(t, test.deployment, "GET", "/api/v2/contexts/"+test.id+"/diffs/current?scope="+string(scope.Snapshot.Mode), nil))
			if snapshot.Revision != scope.Snapshot.Revision || snapshot.Branch != scope.Snapshot.Branch || !reflect.DeepEqual(snapshot.Files, scope.Snapshot.Files) {
				t.Fatalf("snapshot contract diverged: %+v", snapshot)
			}
		}
	}
}
