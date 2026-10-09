package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flexdinesh/servediff/internal/browser"
	"github.com/flexdinesh/servediff/internal/config"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/hooks"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	buildversion "github.com/flexdinesh/servediff/internal/version"
)

var errServiceStopped = errors.New("service is stopped")

func run(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) (failure error) {
	if len(arguments) == 0 {
		return runLocal(ctx, arguments, stdin, stdout, stderr)
	}
	if arguments[0] == "config" {
		return runConfigInput(arguments[1:], stdin, stdout)
	}
	if arguments[0] == "sync" {
		return runSync(ctx, arguments[1:], stdin, stdout, stderr)
	}
	switch arguments[0] {
	case "hook", "collector", "__hook-worker", "review", "service", "serve", "__daemon", "--help", "-h", "--version":
	default:
		return runLocal(ctx, arguments, stdin, stdout, stderr)
	}
	if arguments[0] == "hook" {
		return runHook(arguments[1:], stdin, stdout)
	}
	if arguments[0] == "collector" {
		return runCollector(arguments[1:], stdout)
	}
	if arguments[0] == "__hook-worker" {
		return runHookWorker(ctx, arguments[1:])
	}
	command := ""
	if len(arguments) > 0 {
		switch arguments[0] {
		case "review", "service", "serve", "__daemon":
			command, arguments = arguments[0], arguments[1:]
		}
	}
	if command == "" && arguments[0] != "--help" && arguments[0] != "-h" && arguments[0] != "--version" {
		return fmt.Errorf("unknown command %q; use servediff PATH or pipe a Git diff into servediff", arguments[0])
	}
	action := ""
	if command == "service" {
		if len(arguments) == 0 || arguments[0] == "--help" || arguments[0] == "-h" {
			fmt.Fprintln(stdout, "  Usage: servediff service {start|stop|restart|status} [options]")
			fmt.Fprintln(stdout, "         servediff service config {set KEY VALUE|get KEY|remove KEY}")
			return nil
		}
		action, arguments = arguments[0], arguments[1:]
		if action == "config" {
			return runConfig(arguments, stdout)
		}
		if action != "start" && action != "stop" && action != "restart" && action != "status" {
			return fmt.Errorf("unknown service command %q", action)
		}
	}
	values, err := parseOptionsMode(arguments, stderr, command == "__daemon", command == "serve")
	if err != nil {
		if errors.Is(err, errHelp) {
			return nil
		}
		return err
	}
	if command == "review" {
		values, err = resolvedCollectorSettings(values)
		if err != nil {
			return err
		}
	}
	if values.version {
		fmt.Fprintln(stdout, buildversion.String())
		return nil
	}
	if command != "__daemon" && values.runtimeDir != "" {
		return errors.New("runtime-dir is an internal option; use SERVEDIFF_RUNTIME_DIR for isolation")
	}
	if values.config != "" && (command != "service" || (action != "start" && action != "restart")) {
		return errors.New("config overrides are only supported by service start/restart")
	}
	if values.json && (command != "service" || action != "status") {
		return errors.New("json is only supported by service status")
	}
	if command == "service" && (values.repositorySet || values.fixture != "" || values.capture != "") {
		return errors.New("service commands do not accept diff input")
	}
	if command == "service" && (action == "stop" || action == "status") && (values.hostSet || values.portSet || values.stateSet || values.webDirSet || values.retentionDaysSet) {
		return errors.New("stop and status do not accept server settings")
	}
	if (command == "service" || command == "serve" || command == "__daemon") && (values.serverSet || values.tokenSet || values.agent != "" || values.runID != "" || values.sessionName != "" || values.sourceID != "" || values.trigger != "manual") {
		return errors.New("server lifecycle commands do not accept collector options")
	}
	if command == "review" && values.server != "" && (values.hostSet || values.portSet || values.stateSet || values.webDirSet || values.retentionDaysSet) {
		return errors.New("remote ingestion does not accept local server settings")
	}
	if command == "review" && (values.fixture != "" || values.directory == "-") {
		return errors.New("review collects Git data; pipe a Git diff into servediff for stdin")
	}
	if command != "review" && (values.base != "" || values.branch != "") {
		return errors.New("base and branch are only supported by review")
	}
	if values.capture != "" && (values.base != "" || values.branch != "") {
		return errors.New("capture cannot be combined with base or branch")
	}
	if values.capture != "" && values.server != "" && command == "review" {
		return errors.New("capture reopening is only available on the local service")
	}
	if values.trigger == "agent-hook" {
		values.noBrowser = true
	}
	if values.trigger != "manual" && values.trigger != "agent-hook" {
		return errors.New("trigger must be manual or agent-hook")
	}
	settings, explicit := daemon.DefaultSettings(), daemon.Explicit{}
	if command == "__daemon" {
		// The parent passes a resolved snapshot; never reread file/environment.
		settings, explicit, err = serverSettings(values)
	} else if (command == "service" && (action == "start" || action == "restart")) || command == "serve" || (command == "review" && values.server == "") {
		settings, explicit, err = resolvedServerSettings(values)
		if command == "service" {
			explicit = daemon.Explicit{Host: true, Port: true, State: true, WebDir: true, RetentionDays: true}
		}
	}
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
		input.IngestionDisabled = true
		if input.Kind != "reopen" {
			request, collectErr := collectInitialInput(ctx, values, input)
			if collectErr != nil {
				return collectErr
			}
			input.Ingestion = &request
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
			address := status.BrowserURL + "/contexts/" + url.PathEscape(submitted.Context.ID)
			writeStartup(stdout, submissionInput(*submitted, time.Since(started)), address)
			openBrowser(values, address, stderr)
		})
	}
	if command == "service" {
		client, err := daemon.NewClient()
		if err != nil {
			return err
		}
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
	started := time.Now()
	defer func() {
		if failure != nil {
			recordCollectorActivity(hooks.Activity{Stage: "submission", Status: "failed", InputPath: values.directory, Destination: values.server, Error: failure.Error()}, values.token)
		}
	}()
	operationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if values.capture == "" {
		requests, collectErr := collectSubmissions(operationCtx, command, values, stdin)
		if len(requests) == 0 {
			return collectErr
		}
		submitErr := submitCollected(operationCtx, command, values, settings, explicit, requests, stdout, stderr, started)
		return errors.Join(collectErr, submitErr)
	}
	input := daemon.InitialInput{Kind: "reopen", CaptureID: values.capture}
	client, err := daemon.NewClient()
	if err != nil {
		return err
	}
	connection, err := client.EnsureConnection(operationCtx, settings, explicit)
	if err != nil {
		recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "waiting", Reason: "local service unavailable", Error: err.Error()})
		return err
	}
	requestID := newSubmissionID()
	if input.Ingestion != nil {
		requestID = input.Ingestion.SubmissionID
	}
	var submitted contextservice.Submission
	var firstFailure error
	uncertain := false
	for attempt := 0; attempt < 2; attempt++ {
		recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "started", SubmissionID: requestID, Attempt: attempt + 1, Destination: connection.Status().BrowserURL})
		submitted, err = submitInput(operationCtx, connection, requestID, input)
		if err == nil {
			recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "acknowledged", SubmissionID: requestID, ContextID: submitted.Context.ID, Attempt: attempt + 1, Destination: connection.Status().BrowserURL})
			break
		}
		recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "failed", SubmissionID: requestID, Attempt: attempt + 1, Error: err.Error()})
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
	urls := browser.URLs(status.Settings.Host, status.Settings.Port, "/contexts/"+url.PathEscape(submitted.Context.ID))
	if input.Ingestion != nil {
		loaded := submissionInput(submitted, time.Since(started))
		loaded.mode = "git"
		loaded.directory = input.Ingestion.Metadata.Root
		loaded.contextID, loaded.mcpURL, loaded.submitted = submitted.Context.ID, status.BrowserURL+"/mcp/contexts/"+submitted.Context.ID, true
		writeStartup(stdout, loaded, urls...)
	} else {
		writeSubmission(stdout, submitted, time.Since(started), urls[0], status.BrowserURL+"/mcp/contexts/"+submitted.Context.ID, urls[1:]...)
	}
	openBrowser(values, urls[0], stderr)
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

func resolvedServerSettings(values options) (daemon.Settings, daemon.Explicit, error) {
	saved, err := config.LoadFile(values.configFile)
	if err != nil {
		return daemon.Settings{}, daemon.Explicit{}, err
	}
	if values.config != "" {
		saved, err = config.Merge(saved, values.config)
		if err != nil {
			return daemon.Settings{}, daemon.Explicit{}, err
		}
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
				return daemon.Settings{}, daemon.Explicit{}, err
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
	return settings, daemon.Explicit{Host: values.hostSet, Port: values.portSet, State: values.stateSet, WebDir: values.webDirSet, RetentionDays: values.retentionDaysSet}, err
}

func serverSettings(values options) (daemon.Settings, daemon.Explicit, error) {
	settings := daemon.DefaultSettings()
	settings.Host, settings.Port = values.host, -1
	settings.RetentionDays = values.retentionDays
	if settings.RetentionDays == 0 {
		settings.RetentionDays = 7
	}
	if settings.RetentionDays < 1 || settings.RetentionDays > 106751 {
		return settings, daemon.Explicit{}, errors.New("retention-days must be between 1 and 106751")
	}
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
	return settings, daemon.Explicit{Host: values.hostSet, Port: values.portSet, State: values.stateSet, WebDir: values.webDirSet, RetentionDays: values.retentionDaysSet}, nil
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

func submitRemote(ctx context.Context, command string, values options, request ingestion.Request, stdout, stderr io.Writer, started time.Time) error {
	return submitCollected(ctx, command, values, daemon.Settings{}, daemon.Explicit{}, []ingestion.Request{request}, stdout, stderr, started)
}
