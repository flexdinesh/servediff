package main

import "github.com/mattn/go-isatty"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/flexdinesh/diffx/internal/collector"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/submission"
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
	values, err := parseOptionsMode(arguments, stderr, true)
	if errors.Is(err, errHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if values.hostSet || values.portSet || values.stateSet || values.webDirSet || values.retentionDaysSet || values.replace || values.fixture != "" {
		return errors.New("sync does not accept local server options")
	}
	values, err = resolvedCollectorSettings(values)
	if err != nil {
		return err
	}
	if values.server == "" || values.token == "" {
		return errors.New("sync requires server and bearer token; use diffx config set server URL and config set token TOKEN")
	}
	defer func() {
		if failure != nil {
			failure = fmt.Errorf("%s", strings.ReplaceAll(failure.Error(), values.token, "[redacted]"))
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
	var fresh ingestion.Request
	reusedSubmission := ""
	if !values.retry {
		progress.show("Collect · reading selected checkout")
		values.progress = func(stage, detail string) { progress.show(stage + " · " + detail) }
		var collectErr error
		fresh, collectErr = collectSubmission(ctx, values)
		if collectErr != nil {
			failures = append(failures, collectErr)
		} else {
			fingerprint := syncFingerprint(fresh)
			for _, item := range pending {
				if fingerprint != "" && syncFingerprint(item.Request) == fingerprint {
					reusedSubmission = item.Request.SubmissionID
					break
				}
			}
			if reusedSubmission == "" {
				item := submission.Pending{Request: fresh}
				if err := box.Save(item); err != nil {
					return err
				}
				pending = append(pending, item)
			}
		}
	}
	if len(pending) == 0 {
		if len(failures) > 0 {
			return errors.Join(failures...)
		}
		return errors.New("no pending submissions")
	}
	progress.show("Upload · connecting")
	target, err := submission.Resolve(ctx, values.server, values.token)
	if err != nil {
		return err
	}
	target.Progress = func(job ingestion.Job) {
		progress.show(fmt.Sprintf("Ingest · %s · attempt %d", job.State, job.Attempt))
	}
	uploaded, unchanged := 0, 0
	for index := 0; index < len(pending); index++ {
		item := pending[index]
		if item.StateID != "" && item.StateID != target.Target.Identity {
			failures = append(failures, fmt.Errorf("submission %s belongs to another server database; retained for recovery", item.Request.SubmissionID))
			if item.Request.SubmissionID == reusedSubmission {
				freshItem := submission.Pending{Request: fresh}
				if err := box.Save(freshItem); err != nil {
					return err
				}
				pending = append(pending, freshItem)
			}
			continue
		}
		fingerprint := syncFingerprint(item.Request)
		ack, err := box.Acknowledgement(item.Request)
		if err != nil {
			return err
		}
		if fingerprint != "" && ack.Fingerprint == fingerprint && ack.StateID == target.Target.Identity && ack.ContextID != "" {
			current, err := target.Current(ctx, ack.ContextID)
			if err != nil {
				return err
			}
			if current {
				if err := box.Remove(item.Request.SubmissionID); err != nil {
					return err
				}
				unchanged++
				continue
			}
		}
		item.StateID = target.Target.Identity
		if err := box.Save(item); err != nil {
			return err
		}
		progress.show(fmt.Sprintf("Upload · %d/%d", index+1, len(pending)))
		receipt, err := target.Deliver(ctx, item.Request)
		if err != nil {
			failures = append(failures, fmt.Errorf("submission %s retained; resume with sync --retry: %w", item.Request.SubmissionID, err))
			continue
		}
		uploaded++
		if err := box.Acknowledge(item.Request, fingerprint, target.Target.Identity, receipt.ContextID); err != nil {
			return err
		}
		if item.Request.SubmissionID == reusedSubmission {
			// A retry can commit older content without becoming the stream head.
			// Publish the fresh collection only when that saved payload is stale.
			current, err := target.Current(ctx, receipt.ContextID)
			if err != nil {
				return err
			}
			if !current {
				freshItem := submission.Pending{Request: fresh}
				if err := box.Save(freshItem); err != nil {
					return err
				}
				pending = append(pending, freshItem)
			}
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
	} else if unchanged > 0 {
		fmt.Fprintf(stdout, "Synced %d observations (%d unchanged)\n", uploaded, unchanged)
	} else {
		fmt.Fprintf(stdout, "Synced %d observations\n", uploaded)
	}
	return nil
}

// Session metadata must reach the server even when checkout contents match.
func syncFingerprint(request ingestion.Request) string {
	fingerprint := collector.Fingerprint(request)
	if fingerprint == "" {
		return ""
	}
	data, err := json.Marshal([]interface{}{fingerprint, request.Metadata.Agent, request.Metadata.RunID, request.Metadata.AgentSession})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
