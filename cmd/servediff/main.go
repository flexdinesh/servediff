package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flexdinesh/servediff/internal/browser"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	buildversion "github.com/flexdinesh/servediff/internal/version"
)

var errServiceStopped = errors.New("service is stopped")

func run(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	command := "submit"
	if len(arguments) > 0 {
		switch arguments[0] {
		case "service", "serve", "__daemon":
			command, arguments = arguments[0], arguments[1:]
		}
	}
	action := ""
	if command == "service" {
		if len(arguments) == 0 || arguments[0] == "--help" || arguments[0] == "-h" {
			fmt.Fprintln(stdout, "  Usage: servediff service {start|stop|restart|status} [options]")
			return nil
		}
		action, arguments = arguments[0], arguments[1:]
		if action != "start" && action != "stop" && action != "restart" && action != "status" {
			return fmt.Errorf("unknown service command %q", action)
		}
	}
	values, err := parseOptionsMode(arguments, stderr, command == "__daemon")
	if err != nil {
		if errors.Is(err, errHelp) {
			return nil
		}
		return err
	}
	if values.version {
		fmt.Fprintln(stdout, buildversion.String())
		return nil
	}
	if command != "__daemon" && values.runtimeDir != "" {
		return errors.New("runtime-dir is an internal option; use SERVEDIFF_RUNTIME_DIR for isolation")
	}
	if values.json && (command != "service" || action != "status") {
		return errors.New("json is only supported by service status")
	}
	if command == "service" && (values.repositorySet || values.fixture != "" || values.capture != "") {
		return errors.New("service commands do not accept diff input")
	}
	if command == "service" && (action == "stop" || action == "status") && (values.hostSet || values.portSet || values.stateSet || values.webDirSet) {
		return errors.New("stop and status do not accept server settings")
	}
	settings, explicit, err := serverSettings(values)
	if err != nil {
		return err
	}
	if command == "__daemon" {
		if values.runtimeDir == "" {
			return errors.New("daemon runtime directory is required")
		}
		return daemon.Run(ctx, settings, values.runtimeDir, nil, nil)
	}
	if command == "serve" {
		input, err := acquireInput(values, stdin)
		if err != nil {
			return err
		}
		if os.Getenv("SERVEDIFF_EXIT_ON_STDIN_CLOSE") == "1" {
			child, cancel := context.WithCancel(ctx)
			defer cancel()
			ctx = child
			go func() { _, _ = io.Copy(io.Discard, stdin); cancel() }()
		}
		started := time.Now()
		return daemon.Run(ctx, settings, "", &input, func(status controlapi.Status, submitted *contextservice.Submission) {
			if submitted == nil {
				return
			}
			writeStartup(stdout, submissionInput(*submitted, time.Since(started)), status.BrowserURL)
			openBrowser(values, status.BrowserURL, stderr)
		})
	}
	client, err := daemon.NewClient()
	if err != nil {
		return err
	}
	if command == "service" {
		var status controlapi.Status
		switch action {
		case "stop":
			if err := client.Stop(ctx); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "  servediff service stopped")
			return nil
		case "start":
			status, err = client.Start(ctx, settings, explicit)
		case "restart":
			status, err = client.Restart(ctx, settings, explicit)
		case "status":
			status, err = client.Status(ctx)
		}
		if err != nil {
			if values.json {
				_ = json.NewEncoder(stdout).Encode(status)
			}
			return err
		}
		writeServiceStatus(stdout, status, values.json)
		if status.State != "running" {
			return errServiceStopped
		}
		return nil
	}
	input, err := acquireInput(values, stdin)
	if err != nil {
		return err
	}
	started := time.Now()
	operationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	connection, err := client.EnsureConnection(operationCtx, settings, explicit)
	if err != nil {
		return err
	}
	requestID := newSubmissionID()
	var submitted contextservice.Submission
	var firstFailure error
	uncertain := false
	for attempt := 0; attempt < 2; attempt++ {
		submitted, err = submitInput(operationCtx, connection, requestID, input)
		if err == nil {
			break
		}
		retry, ambiguous := retryableSubmission(err)
		uncertain = uncertain || ambiguous
		if !retry || operationCtx.Err() != nil || attempt == 1 {
			return submissionFailure(input, connection.Status(), requestID, uncertain, errors.Join(firstFailure, err))
		}
		firstFailure = err
		recovered, recoveryError := client.RecoverConnection(operationCtx, connection)
		if recoveryError != nil {
			cause := fmt.Errorf("%w; recovery failed: %w", err, recoveryError)
			return submissionFailure(input, connection.Status(), requestID, uncertain, cause)
		}
		connection = recovered
	}
	status := connection.Status()
	url := status.BrowserURL + "/contexts/" + submitted.Context.ID
	writeSubmission(stdout, submitted, time.Since(started), url, status.BrowserURL+"/mcp/contexts/"+submitted.Context.ID)
	openBrowser(values, url, stderr)
	return nil
}

// Only transport failures and explicit pre-submission lifecycle rejections can
// be replayed. A transport failure leaves the database commit outcome unknown.
func retryableSubmission(err error) (bool, bool) {
	var problem *controlapi.Problem
	if errors.As(err, &problem) {
		return (problem.Status == 401 && problem.Code == "unauthorized") ||
			(problem.Status == 503 && problem.Code == "service_draining"), false
	}
	return true, true
}

func submissionFailure(input daemon.InitialInput, status controlapi.Status, id string, uncertain bool, cause error) error {
	if !uncertain || input.Kind == "reopen" {
		return cause
	}
	return fmt.Errorf("submission %s may have been saved in %q; inspect its contexts before resubmitting: %w", id, status.Settings.State, cause)
}

func serverSettings(values options) (daemon.Settings, daemon.Explicit, error) {
	settings := daemon.DefaultSettings()
	settings.Host, settings.Port = values.host, -1
	if values.portSet {
		settings.Port = values.port
	}
	settings.State = values.state
	if settings.State == "" {
		path, err := reviewstore.DefaultPath()
		if err != nil {
			return settings, daemon.Explicit{}, err
		}
		settings.State = path
	}
	if settings.State != "memory" {
		path, err := filepath.Abs(settings.State)
		if err != nil {
			return settings, daemon.Explicit{}, err
		}
		settings.State = filepath.Clean(path)
	}
	if values.webDir != "" {
		path, err := filepath.Abs(values.webDir)
		if err != nil {
			return settings, daemon.Explicit{}, err
		}
		settings.WebDir = filepath.Clean(path)
	}
	return settings, daemon.Explicit{Host: values.hostSet, Port: values.portSet, State: values.stateSet, WebDir: values.webDirSet}, nil
}

func openBrowser(values options, url string, stderr io.Writer) {
	if !values.noBrowser {
		if err := browser.Open(url); err != nil {
			fmt.Fprintf(stderr, "  servediff: %v\n", err)
		}
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errServiceStopped) {
			fmt.Fprintf(os.Stderr, "  servediff: %v\n", err)
		}
		os.Exit(1)
	}
}
