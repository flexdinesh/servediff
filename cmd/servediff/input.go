package main

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/hooks"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
	buildversion "github.com/flexdinesh/servediff/internal/version"
)

type loadedInput struct {
	directory string
	mode      string
	snapshot  review.RepositoryDiff
	processed time.Duration
	captureID string
	contextID string
	mcpURL    string
	submitted bool
	remote    bool
}

func acquireInput(values options, stdin *os.File) (daemon.InitialInput, error) {
	if values.capture != "" {
		return daemon.InitialInput{Kind: "reopen", CaptureID: values.capture}, nil
	}
	cwd, err := filepath.Abs(".")
	if err != nil {
		return daemon.InitialInput{}, err
	}
	var input io.Reader
	if values.fixture != "" {
		file, err := os.Open(values.fixture)
		if err != nil {
			return daemon.InitialInput{}, err
		}
		defer file.Close()
		input = file
	} else {
		if stdin == nil {
			return daemon.InitialInput{}, errors.New("stdin unavailable")
		}
		piped, err := redirected(stdin)
		if err != nil {
			return daemon.InitialInput{}, err
		}
		if values.directory == "-" || piped {
			input = stdin
		}
	}
	if input != nil {
		raw, err := io.ReadAll(io.LimitReader(input, diffsource.MaxInputBytes+1))
		if err != nil {
			return daemon.InitialInput{}, err
		}
		if len(raw) > diffsource.MaxInputBytes {
			return daemon.InitialInput{}, errors.New("piped diff exceeds the 16 MiB input limit")
		}
		return daemon.InitialInput{Kind: "capture", Raw: raw, SubmittedFrom: cwd}, nil
	}
	if !values.repositorySet {
		return daemon.InitialInput{}, errors.New("provide a repository path, a fixture, or pipe a Git diff")
	}
	path, err := filepath.Abs(values.directory)
	return daemon.InitialInput{Kind: "worktree", Path: path}, err
}

func newSubmissionID() string { return rand.Text() }

func submitInput(ctx context.Context, client *daemon.Connection, id string, input daemon.InitialInput) (contextservice.Submission, error) {
	if input.Ingestion != nil {
		return client.Ingest(ctx, *input.Ingestion)
	}
	switch input.Kind {
	case "worktree":
		return client.Register(ctx, id, input.Path)
	case "capture":
		return client.Capture(ctx, id, input.Raw, input.SubmittedFrom)
	case "reopen":
		return client.OpenCapture(ctx, input.CaptureID)
	default:
		return contextservice.Submission{}, errors.New("unknown diff input")
	}
}

func submissionInput(value contextservice.Submission, elapsed time.Duration) loadedInput {
	input := loadedInput{snapshot: value.Snapshot, processed: elapsed, mode: "git"}
	if value.Context.Root != nil {
		input.directory = *value.Context.Root
	}
	if value.Context.Kind == "capture" {
		input.mode = "pipe"
		input.captureID = value.Context.ID
	}
	return input
}

func redirected(stdin *os.File) (bool, error) {
	stat, err := stdin.Stat()
	if err != nil {
		return false, err
	}
	return stat.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0 || stat.Mode().IsRegular(), nil
}

func collectionOptions(values options) (collector.Options, error) {
	sourceID := values.sourceID
	if sourceID == "" {
		var err error
		sourceID, err = collector.SourceID()
		if err != nil {
			return collector.Options{}, err
		}
	}
	hostname, err := os.Hostname()
	if err != nil {
		return collector.Options{}, err
	}
	return collector.Options{SourceID: sourceID, Hostname: hostname, RunID: values.runID, Agent: values.agent, Trigger: values.trigger, CollectorVersion: buildversion.String(), SubmissionID: newSubmissionID(), Base: values.base, Branch: values.branch,
		OnSource: func(metadata ingestion.Metadata, comparison diffsource.Comparison) {
			recordCollectorActivity(sourceActivity(metadata, comparison))
		}}, nil
}

func sourceActivity(metadata ingestion.Metadata, comparison diffsource.Comparison) hooks.Activity {
	return hooks.Activity{Stage: "source", Status: "observed", Path: metadata.Root,
		Branch: metadata.Branch, BranchID: metadata.BranchID, RepositoryKey: metadata.RepositoryKey, CheckoutKey: metadata.CheckoutKey,
		Base: comparison.BaseRef + "@" + comparison.BaseOID, Head: comparison.HeadOID, Agent: metadata.Agent, RunID: metadata.RunID}
}

func collectSubmission(ctx context.Context, command string, values options, stdin *os.File) (request ingestion.Request, failure error) {
	recordCollectorActivity(hooks.Activity{Stage: "collection", Status: "started", InputPath: values.directory, Agent: values.agent, RunID: values.runID, Branch: values.branch, Base: values.base})
	defer func() {
		activity := hooks.Activity{Stage: "collection", Status: "collected", InputPath: values.directory}
		if failure != nil {
			activity.Status, activity.Error = "failed", failure.Error()
		} else {
			activity.Path, activity.Branch, activity.Base, activity.SubmissionID = request.Metadata.Root, request.Metadata.Branch, values.base, request.SubmissionID
			activity.RepositoryKey, activity.CheckoutKey = request.Metadata.RepositoryKey, request.Metadata.CheckoutKey
			if request.Metadata.Head != nil {
				activity.Head = *request.Metadata.Head
			}
			if len(request.Scopes) > 0 {
				activity.FileCount = len(request.Scopes[0].Snapshot.Files)
			}
		}
		recordCollectorActivity(activity)
	}()
	settings, err := collectionOptions(values)
	if err != nil {
		return ingestion.Request{}, err
	}
	if command == "review" {
		return collector.Collect(ctx, values.directory, settings)
	}
	if command != "pipe" {
		return ingestion.Request{}, errors.New("expected review or pipe command")
	}
	if stdin == nil {
		return ingestion.Request{}, errors.New("stdin unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, diffsource.MaxInputBytes+1))
	if err != nil {
		return ingestion.Request{}, err
	}
	if len(raw) > diffsource.MaxInputBytes {
		return ingestion.Request{}, errors.New("piped diff exceeds the 16 MiB input limit")
	}
	return collector.CollectPatch(ctx, string(raw), values.directory, settings)
}

func recordCollectorActivity(activity hooks.Activity, secrets ...string) {
	for _, secret := range secrets {
		if secret != "" {
			activity.Error = strings.ReplaceAll(activity.Error, secret, "[redacted]")
		}
	}
	directory, err := hooks.StateDirectory()
	if err == nil {
		hooks.Engine{Directory: directory}.Record(activity)
	}
}

func collectInitialInput(ctx context.Context, values options, input daemon.InitialInput) (ingestion.Request, error) {
	settings, err := collectionOptions(values)
	if err != nil {
		return ingestion.Request{}, err
	}
	if input.Kind == "worktree" {
		return collector.Collect(ctx, input.Path, settings)
	}
	return collector.CollectPatch(ctx, string(input.Raw), input.SubmittedFrom, settings)
}
