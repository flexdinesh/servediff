package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

func TestDeleteContextEvictsCacheAndPreservesOtherOwners(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.User("owner", "owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.User("other", "other")
	if err != nil {
		t.Fatal(err)
	}
	provider := contextservice.New(store, owner)
	defer provider.Close()
	foreignProvider := contextservice.New(store, other)
	defer foreignProvider.Close()
	const patch = "diff --git a/file b/file\n--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new\n"
	target, err := provider.Capture(t.Context(), "request", patch, "")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := foreignProvider.Capture(t.Context(), "request", patch, "")
	if err != nil {
		t.Fatal(err)
	}
	multi := &Multi{background: t.Context(), provider: provider, store: store, assets: fstest.MapFS{}, caches: make(map[string]contextCache)}
	server := httptest.NewServer(multi)
	defer server.Close()
	base := server.URL + "/api/v2/contexts/"
	for _, item := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, target.Context.ID + "/diffs/current?scope=all", http.StatusOK},
		{http.MethodDelete, foreign.Context.ID, http.StatusNotFound},
		{http.MethodDelete, "missing", http.StatusNotFound},
		{http.MethodDelete, target.Context.ID, http.StatusNoContent},
		{http.MethodDelete, target.Context.ID, http.StatusNotFound},
		{http.MethodGet, target.Context.ID, http.StatusNotFound},
		{http.MethodGet, target.Context.ID + "/diffs/current?scope=all", http.StatusNotFound},
	} {
		response := request(t, server.Client(), item.method, base+item.path, nil)
		if response.StatusCode != item.status {
			t.Fatalf("%s %s: %d, want %d", item.method, item.path, response.StatusCode, item.status)
		}
		response.Body.Close()
	}
	multi.mu.Lock()
	_, cached := multi.caches[target.Context.ID]
	multi.mu.Unlock()
	if cached {
		t.Fatal("deleted context remains in HTTP snapshot cache")
	}
	if _, err := foreignProvider.Get(t.Context(), foreign.Context.ID); err != nil {
		t.Fatalf("foreign context removed: %v", err)
	}
	page := decode[contextservice.Page](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/v2/contexts", nil))
	if len(page.Contexts) != 0 {
		t.Fatalf("deleted context still listed: %#v", page)
	}
}
