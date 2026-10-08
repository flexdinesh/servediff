package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/submission"
	"github.com/mattn/go-isatty"
)

type syncProgress struct {
	writer            io.Writer
	enabled, terminal bool
	previous          string
}

func (progress *syncProgress) show(message string) {
	if !progress.enabled || message == progress.previous {
		return
	}
	progress.previous = message
	if progress.terminal {
		fmt.Fprintf(progress.writer, "\r\x1b[2K%s", message)
	} else {
		fmt.Fprintln(progress.writer, message)
	}
}
func (progress *syncProgress) close() {
	if progress.enabled && progress.terminal {
		fmt.Fprintln(progress.writer)
	}
}

func runSync(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) (failure error) {
	values, err := parseOptionsMode(arguments, stderr, false, true)
	if errors.Is(err, errHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if values.hostSet || values.portSet || values.stateSet || values.webDirSet || values.retentionDaysSet || values.replace || values.fixture != "" || values.capture != "" {
		return errors.New("sync does not accept local server options")
	}
	values, err = resolvedCollectorSettings(values)
	if err != nil {
		return err
	}
	if values.server == "" || values.token == "" {
		return errors.New("sync requires server and bearer token; use servediff config set server URL and config set token TOKEN")
	}
	defer func() {
		if failure != nil {
			failure = redactedError{cause: failure, secret: values.token}
		}
	}()
	progress := &syncProgress{writer: stderr, enabled: values.debug}
	if file, ok := stderr.(*os.File); ok {
		progress.terminal = isatty.IsTerminal(file.Fd())
	}
	defer progress.close()
	state, err := reviewstore.DefaultPath()
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(values.token))
	route := strings.TrimRight(values.server, "/") + ":" + hex.EncodeToString(digest[:])
	box, err := submission.Open(ctx, filepath.Dir(state), route)
	if err != nil {
		return err
	}
	defer box.Close()
	pending, err := box.Pending()
	if err != nil {
		return err
	}
	var failures []error
	if !values.retry {
		progress.show("Collect · discovering worktrees and reading changes")
		values.progress = func(stage, detail string) { progress.show(stage + " · " + detail) }
		requests, collectErr := collectSubmissions(ctx, "review", values, stdin)
		if collectErr != nil {
			failures = append(failures, collectErr)
		}
		for _, request := range requests {
			item := submission.Pending{Request: request}
			if err := box.Save(item); err != nil {
				return err
			}
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		if len(failures) > 0 {
			return errors.Join(failures...)
		}
		return errors.New("no pending submissions")
	}
	progress.show("Upload · connecting")
	target, err := resolveDestination(ctx, values, daemon.Settings{}, daemon.Explicit{}, nil)
	if err != nil {
		return err
	}
	if !target.queued {
		return errors.New("server does not support queued ingestion; upgrade it and resume with sync --retry")
	}
	target.progress = func(job ingestion.Job) {
		progress.show(fmt.Sprintf("Ingest · %s · attempt %d", job.State, job.Attempt))
	}
	for index, item := range pending {
		if item.StateID != "" && item.StateID != target.target.Identity {
			failures = append(failures, fmt.Errorf("submission %s belongs to another server database; retained for recovery", item.Request.SubmissionID))
			continue
		}
		item.StateID = target.target.Identity
		if err := box.Save(item); err != nil {
			return err
		}
		progress.show(fmt.Sprintf("Upload · %d/%d", index+1, len(pending)))
		if _, err := target.deliver(ctx, item.Request); err != nil {
			failures = append(failures, fmt.Errorf("submission %s retained; resume with sync --retry: %w", item.Request.SubmissionID, err))
			continue
		}
		if err := box.Remove(item.Request.SubmissionID); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	progress.show("Ingest · complete")
	if values.print {
		fmt.Fprintln(stdout, strings.TrimRight(values.server, "/"))
	} else {
		fmt.Fprintf(stdout, "Synced %d observations\n", len(pending))
	}
	return nil
}
