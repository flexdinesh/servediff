package main

import "github.com/mattn/go-isatty"

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

	"github.com/flexdinesh/diffx/internal/contextservice"
	"github.com/flexdinesh/diffx/internal/daemon"
)

func runLocal(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	values, err := parseOptionsMode(arguments, stderr, true)
	if errors.Is(err, errHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if values.serverSet || values.tokenSet || values.branch != "" || values.fixture != "" || values.print || values.retry {
		return errors.New("local mode accepts a checkout path or piped diff; use sync for remote collection or dev --fixture for fixtures")
	}
	input, err := acquireLocalInput(values, stdin)
	if err != nil {
		return err
	}
	if input.Kind == "capture" && values.base != "" {
		return errors.New("base is only supported for a checkout path")
	}
	settings, err := resolvedServerSettings(values)
	if err != nil {
		return err
	}
	started := time.Now()
	request, err := collectInitialInput(ctx, values, input)
	if err != nil {
		return err
	}

	label := "Piped diff"
	if input.Kind == "worktree" {
		input.Path, label = request.Metadata.Root, request.Metadata.Root
	}
	return daemon.RunForeground(ctx, settings, request, func(status daemon.Status) (bool, error) {
		if values.replace {
			return true, nil
		}
		if stdin == nil || !isatty.IsTerminal(stdin.Fd()) {
			return false, fmt.Errorf("diffx already running (PID %d); use --replace to restart here", status.PID)
		}
		fmt.Fprintf(stderr, "diffx is running (PID %d).\nStop it and open %s? [y/N] ", status.PID, label)
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
		if submitted == nil {
			return
		}
		address := status.BrowserURL + "/contexts/" + url.PathEscape(submitted.Context.ID)
		loaded := submissionInput(*submitted, time.Since(started))
		if input.Kind == "capture" {
			loaded.mode, loaded.directory = "pipe", ""
		}
		loaded.contextID, loaded.mcpURL = submitted.Context.ID, status.BrowserURL+"/mcp"
		writeStartup(stdout, loaded, address)
		openBrowser(values, address, stderr)
	})
}

func acquireLocalInput(values options, stdin *os.File) (initialInput, error) {
	piped := false
	if stdin != nil {
		var err error
		piped, err = redirected(stdin)
		if err != nil {
			return initialInput{}, err
		}
	}
	if piped && values.repositorySet && values.directory != "-" {
		return initialInput{}, errors.New("checkout path cannot be combined with piped stdin")
	}
	if piped || values.directory == "-" {
		return acquireInput(values, stdin)
	}
	path, err := filepath.Abs(values.directory)
	return initialInput{Kind: "worktree", Path: path}, err
}
