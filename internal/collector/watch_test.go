package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

func TestWatchRetriesSameCaptureAndPublishesCleanAndBranchTransitions(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "before\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	initial, fingerprint := changed(t, root, "")
	write(t, root, "tracked", "after\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	received := make(chan ingestion.Request, 10)
	done := make(chan error, 1)
	attempts := 0
	go func() {
		done <- Watch(ctx, root, Options{SourceID: "machine", Hostname: "host", Base: "HEAD"}, fingerprint, 20*time.Millisecond, func(ctx context.Context, request ingestion.Request) error {
			received <- request
			attempts++
			if attempts == 1 {
				return errors.New("ambiguous commit")
			}
			return nil
		}, nil)
	}()
	next := func() ingestion.Request {
		t.Helper()
		select {
		case value := <-received:
			return value
		case <-time.After(5 * time.Second):
			t.Fatal("watch did not publish")
			return ingestion.Request{}
		}
	}
	first, retry := next(), next()
	if first.SubmissionID != retry.SubmissionID || first.ContentHash != retry.ContentHash {
		t.Fatal("retry recollected an immutable submission")
	}
	write(t, root, "tracked", "before\n")
	clean := next()
	if len(clean.Scopes[0].Snapshot.Files) != 0 || clean.SubmissionID == first.SubmissionID {
		t.Fatalf("clean transition lost: %+v", clean)
	}
	git(t, root, "switch", "-c", "new-branch")
	branch := next()
	if branch.Metadata.Branch != "new-branch" || branch.Metadata.CheckoutKey != initial.Metadata.CheckoutKey {
		t.Fatalf("branch switch: %+v", branch.Metadata)
	}
	if patchFor(t, first, review.DiffAll, "tracked").Contents.After != "after\n" {
		t.Fatal("captured contents changed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not stop")
	}
}
