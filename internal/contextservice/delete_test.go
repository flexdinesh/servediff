package contextservice

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteInvalidatesCaptureAndPublishesCatalogChange(t *testing.T) {
	service := testService(t)
	submission, err := service.Capture(t.Context(), "request", testPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	id := submission.Context.ID
	if _, ok := service.cached(id); !ok {
		t.Fatal("capture not cached")
	}
	events, unsubscribe := service.Subscribe()
	defer unsubscribe()
	if err := service.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.cached(id); ok {
		t.Fatal("deleted capture still cached")
	}
	select {
	case changed := <-events:
		if changed != id {
			t.Fatalf("catalog event %q, want %q", changed, id)
		}
	default:
		t.Fatal("deletion did not publish catalog change")
	}
	_, err = service.Resolve(t.Context(), id)
	assertStatus(t, err, 404)
	assertStatus(t, service.Delete(t.Context(), id), 404)
	select {
	case <-events:
		t.Fatal("failed deletion published catalog change")
	default:
	}
	resubmitted, err := service.Capture(t.Context(), "request", testPatch, "")
	if err != nil || resubmitted.Context.ID == id {
		t.Fatalf("resubmitted capture: %#v, %v", resubmitted, err)
	}
}

func TestDeleteCancelledRequestPreservesContext(t *testing.T) {
	service := testService(t)
	submission, err := service.Capture(t.Context(), "request", testPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.Delete(ctx, submission.Context.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled deletion: %v", err)
	}
	if _, err := service.Get(t.Context(), submission.Context.ID); err != nil {
		t.Fatalf("cancelled deletion removed context: %v", err)
	}
}
