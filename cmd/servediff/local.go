package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/mattn/go-isatty"
)

func runLocal(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	values, err := parseOptionsMode(arguments, stderr, false, true)
	if errors.Is(err, errHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if values.serverSet || values.tokenSet || values.branch != "" || values.fixture != "" || values.capture != "" || values.print || values.retry {
		return errors.New("local mode accepts a checkout path or piped diff; use sync for remote collection or serve for fixtures")
	}
	input, err := acquireLocalInput(values, stdin)
	if err != nil {
		return err
	}
	if input.Kind == "capture" && values.base != "" {
		return errors.New("base is only supported for a checkout path")
	}
	settings, _, err := resolvedServerSettings(values)
	if err != nil {
		return err
	}
	started := time.Now()
	options, err := collectionOptions(values)
	if err != nil {
		return err
	}
	var request ingestion.Request
	if input.Kind == "worktree" {
		request, err = collector.Collect(ctx, input.Path, options)
	} else {
		request, err = collector.CollectPatch(ctx, string(input.Raw), input.SubmittedFrom, options)
	}
	if err != nil {
		return err
	}
	input.Ingestion = &request
	label := "Piped diff"
	if input.Kind == "worktree" {
		input.Path, label = request.Metadata.Root, request.Metadata.Root
		input.Watch = func(ctx context.Context, service *contextservice.Service) error {
			return collector.Watch(ctx, request.Metadata.Root, options, collector.Fingerprint(request), time.Second, func(ctx context.Context, next ingestion.Request) error {
				_, err := service.Ingest(ctx, next)
				return err
			}, func(err error) { fmt.Fprintf(stderr, "Watch: %v\n", err) })
		}
	}
	return daemon.RunForeground(ctx, settings, input, func(status daemon.Status) (bool, error) {
		if values.replace {
			return true, nil
		}
		if stdin == nil || !isatty.IsTerminal(stdin.Fd()) {
			return false, fmt.Errorf("servediff already running (PID %d); use --replace to restart here", status.PID)
		}
		fmt.Fprintf(stderr, "servediff is running (PID %d).\nStop it and open %s? [y/N] ", status.PID, label)
		type response struct {
			answer string
			err    error
		}
		answers := make(chan response, 1)
		go func() {
			answer, err := bufio.NewReader(stdin).ReadString('\n')
			answers <- response{answer, err}
		}()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case reply := <-answers:
			if reply.err != nil && !errors.Is(reply.err, io.EOF) {
				return false, reply.err
			}
			return strings.EqualFold(strings.TrimSpace(reply.answer), "y") || strings.EqualFold(strings.TrimSpace(reply.answer), "yes"), nil
		}
	}, func(status daemon.Status, submitted *contextservice.Submission) {
		if input.Kind == "capture" && submitted != nil {
			address := status.BrowserURL + "/contexts/" + url.PathEscape(submitted.Context.ID)
			loaded := submissionInput(*submitted, time.Since(started))
			loaded.mode, loaded.directory, loaded.contextID, loaded.mcpURL = "pipe", "", submitted.Context.ID, status.BrowserURL+"/mcp"
			writeStartup(stdout, loaded, address)
			openBrowser(values, address, stderr)
			return
		}
		address := status.BrowserURL + "/?" + url.Values{"watch": {request.Metadata.CheckoutKey}, "source": {request.Metadata.SourceID}}.Encode()
		fmt.Fprintf(stdout, "Watching %s\n%s\nMCP: %s/mcp\n", input.Path, address, status.BrowserURL)
		openBrowser(values, address, stderr)
	})
}

func acquireLocalInput(values options, stdin *os.File) (daemon.InitialInput, error) {
	piped := false
	if stdin != nil {
		var err error
		piped, err = redirected(stdin)
		if err != nil {
			return daemon.InitialInput{}, err
		}
	}
	if piped && values.repositorySet && values.directory != "-" {
		return daemon.InitialInput{}, errors.New("checkout path cannot be combined with piped stdin")
	}
	if piped || values.directory == "-" {
		return acquireInput(values, stdin)
	}
	if !values.repositorySet {
		return daemon.InitialInput{}, errors.New("provide a repository path or pipe a Git diff")
	}
	path, err := filepath.Abs(values.directory)
	return daemon.InitialInput{Kind: "worktree", Path: path}, err
}
