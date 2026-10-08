package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
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
		return errors.New("local mode watches a checkout; use sync for remote collection or serve for fixtures")
	}
	settings, _, err := resolvedServerSettings(values)
	if err != nil {
		return err
	}
	options, err := collectionOptions(values)
	if err != nil {
		return err
	}
	request, err := collector.Collect(ctx, values.directory, options)
	if err != nil {
		return err
	}
	input := daemon.InitialInput{Kind: "worktree", Path: request.Metadata.Root, Ingestion: &request}
	input.Watch = func(ctx context.Context, service *contextservice.Service) error {
		return collector.Watch(ctx, request.Metadata.Root, options, collector.Fingerprint(request), time.Second, func(ctx context.Context, next ingestion.Request) error {
			_, err := service.Ingest(ctx, next)
			return err
		}, func(err error) { fmt.Fprintf(stderr, "Watch: %v\n", err) })
	}
	return daemon.RunForeground(ctx, settings, input, func(status daemon.Status) (bool, error) {
		if values.replace {
			return true, nil
		}
		if stdin == nil || !isatty.IsTerminal(stdin.Fd()) {
			return false, fmt.Errorf("servediff already running for %s (PID %d); use --replace to restart here", status.WatchPath, status.PID)
		}
		fmt.Fprintf(stderr, "servediff is running for %s (PID %d).\nStop it and start watching %s? [y/N] ", status.WatchPath, status.PID, input.Path)
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
		address := status.BrowserURL + "/?" + url.Values{"watch": {request.Metadata.CheckoutKey}, "source": {request.Metadata.SourceID}}.Encode()
		fmt.Fprintf(stdout, "Watching %s\n%s\nMCP: %s/mcp\n", input.Path, address, status.BrowserURL)
		openBrowser(values, address, stderr)
	})
}
