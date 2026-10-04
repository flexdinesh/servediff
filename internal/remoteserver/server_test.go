package remoteserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flexdinesh/servediff/internal/reviewstore"
)

const testToken = "remote-test-credential-at-least-thirty-two-bytes"

func TestRemoteAuthenticationCoversUIAPIAndMCP(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, closeService, err := Handler(t.Context(), store, "account", testToken, fstest.MapFS{"index.html": {Data: []byte("review dashboard")}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeService()
	for _, path := range []string{"/", "/api/v2/contexts", "/mcp", "/api/v2/events", "/api/v2/ingestions"} {
		for _, auth := range []string{"", "Bearer wrong", "bearer " + testToken} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("unauthenticated %s: %d", path, w.Code)
			}
		}
	}
	for _, basic := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/v2/contexts", nil)
		if basic {
			r.SetBasicAuth("account", testToken)
		} else {
			r.Header.Set("Authorization", "Bearer "+testToken)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"contexts":[]`) {
			t.Fatalf("authorized query basic=%v: %d %s", basic, w.Code, w.Body.String())
		}
		if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("credentialed cache/referrer headers: %v", w.Header())
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("account", testToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "review dashboard") {
		t.Fatalf("browser basic auth: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("other", testToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("foreign account authentication: %d", w.Code)
	}
}

func TestRemoteRejectsMissingIdentityOrWeakCredentials(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct{ account, token string }{{"", testToken}, {" ", testToken}, {"account", "short"}, {"account\n", testToken}, {"account", testToken + "\r"}} {
		if _, _, err := Handler(t.Context(), store, tc.account, tc.token, fstest.MapFS{}); err == nil {
			t.Fatalf("invalid remote identity accepted: account=%q", tc.account)
		}
	}
}

func TestRemoteRunShutdownClosesActiveEventStream(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Settings{Listen: "127.0.0.1:0", State: filepath.Join(t.TempDir(), "reviews.sqlite"), Account: "account", Token: testToken}, func(address string) { ready <- address })
	}()
	var address string
	select {
	case address = <-ready:
	case err := <-done:
		t.Fatalf("server did not start: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("server startup timed out")
	}
	r, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+address+"/api/v2/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("event stream status %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote shutdown failed to cancel active stream")
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("shutdown stream: %v", err)
	}
}
