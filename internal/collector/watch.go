package collector

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
)

// Watch polls full content identity, including untracked files and Git state.
// It follows only this checkout. Failed collection/publication never advances
// the acknowledged fingerprint or substitutes an empty observation.
func Watch(ctx context.Context, directory string, options Options, fingerprint string, interval time.Duration, submit func(context.Context, ingestion.Request) error, report func(error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	options.Branch = ""
	options.SubmissionID = ""
	lastSubmit := time.Now()
	var pending *ingestion.Request
	var pendingFingerprint string
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if pending == nil {
			previous := fingerprint
			if time.Since(lastSubmit) >= 12*time.Hour {
				previous = ""
			}
			request, next, err := CollectChanged(ctx, directory, options, previous)
			if errors.Is(err, ErrUnchanged) {
				continue
			}
			if err != nil {
				if ctx.Err() == nil && report != nil {
					report(err)
				}
				continue
			}
			pending, pendingFingerprint = &request, next
		}
		if err := submit(ctx, *pending); err != nil {
			if ctx.Err() == nil && report != nil {
				report(err)
			}
			continue
		}
		fingerprint, pending = pendingFingerprint, nil
		lastSubmit = time.Now()
	}
}
