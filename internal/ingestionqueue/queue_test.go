package ingestionqueue

import (
	"context"
	"errors"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
	"testing"
	"time"
)

type recordingQueue struct {
	action, contextID string
	at                time.Time
	completeError     error
}

func (*recordingQueue) Accept(context.Context, string, ingestion.Request) (Job, error) {
	panic("unexpected admission")
}
func (*recordingQueue) Get(context.Context, string, string) (Job, error) { panic("unexpected status") }
func (*recordingQueue) Claim(context.Context, time.Duration) (Delivery, error) {
	return Delivery{}, ErrEmpty
}
func (*recordingQueue) Renew(context.Context, Delivery, time.Duration) error { return nil }
func (q *recordingQueue) Complete(_ context.Context, _ Delivery, id string) error {
	q.action = "complete"
	q.contextID = id
	return q.completeError
}
func (q *recordingQueue) Retry(_ context.Context, _ Delivery, at time.Time, _ string) error {
	q.action = "retry"
	q.at = at
	return nil
}
func (q *recordingQueue) Fail(context.Context, Delivery, string) error { q.action = "fail"; return nil }
func TestWorkerOutcomeContract(t *testing.T) {
	for _, c := range []struct {
		name    string
		attempt int
		failure error
		action  string
	}{
		{"committed", 1, nil, "complete"},
		{"transient", 1, errors.New("storage unavailable"), "retry"},
		{"permanent", 1, review.Error(400, "invalid observation"), "fail"},
		{"exhausted", 5, errors.New("storage unavailable"), "fail"},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := &recordingQueue{}
			before := time.Now()
			err := process(t.Context(), q, Delivery{OwnerID: "owner", Job: Job{ID: "job", Attempt: c.attempt}}, func(ctx context.Context, owner string, _ ingestion.Request) (string, error) {
				if owner != "owner" {
					t.Fatalf("wrong owner %s", owner)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("processing has no deadline")
				}
				return "context", c.failure
			})
			if err != nil || q.action != c.action {
				t.Fatalf("action=%s error=%v", q.action, err)
			}
			if c.action == "complete" && q.contextID != "context" {
				t.Fatal("lost committed context")
			}
			if c.action == "retry" && !q.at.After(before) {
				t.Fatal("retry not delayed")
			}
		})
	}
}
func TestWorkerCancellationAndLeaseFencing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	q := &recordingQueue{}
	err := process(ctx, q, Delivery{}, func(context.Context, string, ingestion.Request) (string, error) { cancel(); return "committed", nil })
	if err != nil || q.action != "" {
		t.Fatalf("shutdown changed job: %s %v", q.action, err)
	}
	q = &recordingQueue{completeError: ErrLeaseLost}
	err = process(t.Context(), q, Delivery{}, func(context.Context, string, ingestion.Request) (string, error) { return "committed", nil })
	if !errors.Is(err, ErrLeaseLost) || q.action != "complete" {
		t.Fatalf("lost lease retried: %s %v", q.action, err)
	}
}
