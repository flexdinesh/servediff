package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flexdinesh/diffx/internal/browser"
	"github.com/flexdinesh/diffx/internal/config"
	"github.com/flexdinesh/diffx/internal/contextservice"
	"github.com/flexdinesh/diffx/internal/daemon"
	buildversion "github.com/flexdinesh/diffx/internal/version"
)

func run(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	if len(arguments) > 0 {
		switch arguments[0] {
		case "config":
			return runConfigInput(arguments[1:], stdin, stdout)
		case "sync":
			return runSync(ctx, arguments[1:], stdin, stdout, stderr)
		case "hook":
			return runHook(arguments[1:], stdin, stdout)
		case "collector":
			return runCollector(arguments[1:], stdout)
		case "__hook-worker":
			return runHookWorker(ctx, arguments[1:])
		case "dev":
			return runFixture(ctx, arguments[1:], stdin, stdout, stderr)
		case "--version":
			fmt.Fprintln(stdout, buildversion.String())
			return nil
		case "review", "service", "serve", "capture", "__daemon":
			return fmt.Errorf("%s removed; use diffx PATH, sync, or dev --fixture", arguments[0])
		}
	}
	return runLocal(ctx, arguments, stdin, stdout, stderr)
}
func runFixture(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	values, err := parseOptionsMode(arguments, stderr, true)
	if errors.Is(err, errHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if values.fixture == "" {
		return errors.New("dev requires --fixture")
	}
	input, err := acquireInput(values, stdin)
	if err != nil {
		return err
	}
	request, err := collectInitialInput(ctx, values, input)
	if err != nil {
		return err
	}

	settings, err := resolvedServerSettings(values)
	if err != nil {
		return err
	}
	if os.Getenv("DIFFX_EXIT_ON_STDIN_CLOSE") == "1" {
		child, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = child
		go func() { _, _ = io.Copy(io.Discard, stdin); cancel() }()
	}
	started := time.Now()
	return daemon.Run(ctx, settings, "", &request, func(status daemon.Status, submitted *contextservice.Submission) {
		address := status.BrowserURL + "/contexts/" + url.PathEscape(submitted.Context.ID)
		writeStartup(stdout, submissionInput(*submitted, time.Since(started)), address)
		openBrowser(values, address, stderr)
	})
}
func resolvedServerSettings(values options) (daemon.Settings, error) {
	saved, err := config.LoadFile(values.configFile)
	if err != nil {
		return daemon.Settings{}, err
	}
	if values.hostSet {
		saved.Host = values.host
	}
	if values.portSet {
		saved.Port = &values.port
	}
	if values.stateSet {
		saved.State = values.state
		if saved.State == "" {
			defaults, err := config.Default()
			if err != nil {
				return daemon.Settings{}, err
			}
			saved.State = defaults.State
		}
	}
	if values.webDirSet {
		saved.WebDir = values.webDir
	}
	if values.retentionDaysSet {
		saved.RetentionDays = values.retentionDays
	}
	settings, err := saved.Settings()
	return settings, err
}

func openBrowser(values options, url string, stderr io.Writer) {
	if !values.noBrowser {
		if err := browser.Open(url); err != nil {
			fmt.Fprintf(stderr, "  diffx: %v\n", err)
		}
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "  diffx: %v\n", err)
		os.Exit(1)
	}
}
