package main

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
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
		if len(raw) > 0 || values.directory == "-" || values.fixture != "" {
			return daemon.InitialInput{Kind: "capture", Raw: raw, SubmittedFrom: cwd}, nil
		}
	}

	path, err := filepath.Abs(values.directory)
	return daemon.InitialInput{Kind: "worktree", Path: path}, err
}

func newSubmissionID() string { return rand.Text() }

func submitInput(ctx context.Context, client *daemon.Connection, id string, input daemon.InitialInput) (contextservice.Submission, error) {
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
