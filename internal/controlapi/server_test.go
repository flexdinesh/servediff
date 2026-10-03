package controlapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

type testService struct {
	register func(context.Context, string, string) (contextservice.Submission, error)
	capture  func(context.Context, string, string, string) (contextservice.Submission, error)
}

func (service testService) Register(ctx context.Context, id, path string) (contextservice.Submission, error) {
	if service.register != nil {
		return service.register(ctx, id, path)
	}
	return contextservice.Submission{}, nil
}

func (service testService) Capture(ctx context.Context, id, raw, from string) (contextservice.Submission, error) {
	if service.capture != nil {
		return service.capture(ctx, id, raw, from)
	}
	return contextservice.Submission{}, nil
}

func (service testService) OpenCapture(context.Context, string) (contextservice.Submission, error) {
	return contextservice.Submission{}, nil
}

func statusValue() (Status, error) {
	return Status{State: "running", ProtocolVersion: ProtocolVersion}, nil
}

func TestControlAuthenticationAndBrowserDenial(t *testing.T) {
	handler := newHandler("secret", testService{}, statusValue, func() {})
	cases := []struct {
		name, authorization, header, value string
		want                               int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", authorization: "Bearer wrong", want: http.StatusUnauthorized},
		{name: "authenticated", authorization: "Bearer secret", want: http.StatusOK},
		{name: "origin", authorization: "Bearer secret", header: "Origin", value: "http://127.0.0.1", want: http.StatusForbidden},
		{name: "null origin", authorization: "Bearer secret", header: "Origin", value: "null", want: http.StatusForbidden},
		{name: "same site", authorization: "Bearer secret", header: "Sec-Fetch-Site", value: "same-site", want: http.StatusForbidden},
		{name: "navigation", authorization: "Bearer secret", header: "Sec-Fetch-Mode", value: "navigate", want: http.StatusForbidden},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/control/v1/status", nil)
			request.Header.Set("Authorization", test.authorization)
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status %d; want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestWorktreeRejectsMalformedJSONBeforeRegistration(t *testing.T) {
	var called atomic.Bool
	service := testService{register: func(context.Context, string, string) (contextservice.Submission, error) {
		called.Store(true)
		return contextservice.Submission{}, nil
	}}
	handler := newHandler("secret", service, statusValue, func() {})
	for _, body := range []string{
		`{"submissionId":"test","path":"/repo","unknown":true}`,
		`{"submissionId":"test","path":"/repo"}{}`,
		`{"submissionId":"test"}`,
		`null`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/control/v1/worktrees", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || called.Load() {
			t.Fatalf("body %s: status %d, called %v", body, response.Code, called.Load())
		}
	}
}

func TestCaptureSizeLimitBeforeParsing(t *testing.T) {
	var called atomic.Bool
	handler := newHandler("secret", testService{capture: func(context.Context, string, string, string) (contextservice.Submission, error) {
		called.Store(true)
		return contextservice.Submission{}, nil
	}}, statusValue, func() {})
	request := httptest.NewRequest(http.MethodPost, "/control/v1/captures", strings.NewReader(strings.Repeat("x", MaxPatchBytes+1)))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set(submissionHeader, "submission")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || called.Load() {
		t.Fatalf("status %d, parser called %v", response.Code, called.Load())
	}
}

type countingReader struct{ calls int }

func (reader *countingReader) Read([]byte) (int, error) {
	reader.calls++
	return 0, io.EOF
}

func TestCaptureOverloadRejectsBeforeReadingBody(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	service := testService{capture: func(ctx context.Context, _, _, _ string) (contextservice.Submission, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return contextservice.Submission{}, nil
	}}
	handler := newHandler("secret", service, statusValue, func() {})
	for range 2 {
		go func() {
			request := httptest.NewRequest(http.MethodPost, "/control/v1/captures", strings.NewReader("patch"))
			request.Header.Set("Authorization", "Bearer secret")
			request.Header.Set(submissionHeader, "submission")
			handler.ServeHTTP(httptest.NewRecorder(), request)
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("capture not started")
		}
	}
	body := &countingReader{}
	request := httptest.NewRequest(http.MethodPost, "/control/v1/captures", body)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set(submissionHeader, "third")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || body.calls != 0 {
		t.Fatalf("status %d, reads %d", response.Code, body.calls)
	}
}

func TestShutdownDrainsAndRunsOnce(t *testing.T) {
	var calls atomic.Int32
	stopped := make(chan struct{}, 2)
	handler := newHandler("secret", testService{}, statusValue, func() { calls.Add(1); stopped <- struct{}{} })
	server := httptest.NewServer(handler)
	defer server.Close()
	client := NewClient(server.URL, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(ctx)
	if err != nil || status.State != "draining" {
		t.Fatalf("status %+v: %v", status, err)
	}
	_, err = client.Register(ctx, "submission", "/repo")
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("register %v, shutdown calls %d", err, calls.Load())
	}
}

func TestClientCapturePreservesRawPatchAndDirectory(t *testing.T) {
	raw := "diff --git a/file b/file\n+line\r\n"
	from := "/repo/new\nline/日本語"
	service := testService{capture: func(_ context.Context, id, patch, directory string) (contextservice.Submission, error) {
		if id != "submission" || patch != raw || directory != from {
			t.Errorf("submission %q, patch %q, from %q", id, patch, directory)
		}
		return contextservice.Submission{}, nil
	}}
	server := httptest.NewServer(newHandler("secret", service, statusValue, func() {}))
	defer server.Close()
	if _, err := NewClient(server.URL, "secret").Capture(context.Background(), "submission", []byte(raw), from); err != nil {
		t.Fatal(err)
	}
}

func TestClientNeverRedirectsCredentials(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		leaked.Store(request.Header.Get("Authorization") != "")
		response.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	_, err := NewClient(redirector.URL, "secret").Status(context.Background())
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusTemporaryRedirect || leaked.Load() {
		t.Fatalf("error %v, leaked %v", err, leaked.Load())
	}
}

func TestClientRejectsNonLocalEndpoints(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:1", "http://example.com:1", "http://127.0.0.1", "http://127.0.0.1:1/path", "http://user@127.0.0.1:1"} {
		if _, err := NewClient(endpoint, "secret").Status(context.Background()); err == nil {
			t.Errorf("accepted endpoint %q", endpoint)
		}
	}
}

func TestCaptureSubmissionRetryAndReopen(t *testing.T) {
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.User("control-test", "Control Test")
	if err != nil {
		t.Fatal(err)
	}
	service := contextservice.New(store, user)
	server := httptest.NewServer(New("secret", service, statusValue, func() {}))
	defer server.Close()
	client := NewClient(server.URL, "secret")
	ctx := context.Background()
	raw := []byte("diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-before\n+after\n")
	first, err := client.Capture(ctx, "submission", raw, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := client.Capture(ctx, "submission", raw, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	independent, err := client.Capture(ctx, "another-submission", raw, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := client.OpenCapture(ctx, first.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Context.ID == "" || retry.Context.ID != first.Context.ID || reopened.Context.ID != first.Context.ID || independent.Context.ID == first.Context.ID {
		t.Fatalf("IDs: first=%q retry=%q reopened=%q independent=%q", first.Context.ID, retry.Context.ID, reopened.Context.ID, independent.Context.ID)
	}
	if first.Context.Kind != "capture" || first.Snapshot.VersionID != reopened.Snapshot.VersionID {
		t.Fatalf("capture changed on reopening: %+v -> %+v", first, reopened)
	}
	_, err = client.Capture(ctx, "submission", raw, "/different-repo")
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusConflict {
		t.Fatalf("changed request did not conflict: %v", err)
	}
}
