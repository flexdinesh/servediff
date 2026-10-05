package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/hooks"
	"github.com/flexdinesh/servediff/internal/ingestion"
)

// Hooks are notifications, never a reason to block or continue an agent turn.
func runHook(arguments []string, stdin io.Reader, stdout io.Writer) error {
	directory, err := hooks.StateDirectory()
	if err != nil {
		return nil
	}
	engine := newHookEngine(directory)
	event, err := parseHookEvent(arguments, stdin, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err == nil {
		var executable string
		executable, err = os.Executable()
		if err == nil {
			engine.Launch = func(job string) error { return hooks.LaunchDetached(executable, directory, job) }
			if event.Retry {
				err = engine.Retry(event)
			} else {
				err = engine.Schedule(event)
				if err == nil {
					err = engine.Retry(event)
				}
			}
		}
	}
	if err != nil {
		engine.Log(err)
	}
	return nil
}

func runCollector(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 || arguments[0] == "--help" || arguments[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: servediff collector status | retry [--config-file FILE]")
		return nil
	}
	directory, err := hooks.StateDirectory()
	if err != nil {
		return err
	}
	engine := newHookEngine(directory)
	switch arguments[0] {
	case "status":
		if len(arguments) != 1 {
			return errors.New("collector status takes no options")
		}
		statuses, err := engine.Statuses()
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(statuses)
	case "retry":
		event, err := parseHookEvent(append([]string{"--agent", "codex", "--retry"}, arguments[1:]...), nil, stdout)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		if err != nil {
			return err
		}
		return engine.Retry(event)
	default:
		return fmt.Errorf("unknown collector command %q", arguments[0])
	}
}

func parseHookEvent(arguments []string, stdin io.Reader, stdout io.Writer) (hooks.Event, error) {
	var event hooks.Event
	flags := flag.NewFlagSet("servediff hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&event.Agent, "agent", "", "codex, claude, opencode or pi")
	flags.StringVar(&event.Path, "path", "", "checkout path; defaults to hook input cwd")
	flags.StringVar(&event.RunID, "run-id", "", "agent session identity")
	flags.StringVar(&event.ConfigFile, "config-file", "", "machine JSON configuration path")
	flags.StringVar(&event.Base, "base", "auto", "comparison baseline: auto, HEAD, or Git ref")
	flags.BoolVar(&event.Retry, "retry", false, "retry queued collector work without a new collection")
	flags.Usage = func() {
		fmt.Fprintln(stdout, "Usage: servediff hook --agent {codex|claude|opencode|pi} [--path DIRECTORY] [--run-id ID] [--config-file FILE]")
	}
	if err := flags.Parse(arguments); err != nil {
		return event, err
	}
	if flags.NArg() != 0 {
		return event, errors.New("hook takes no positional arguments")
	}
	switch event.Agent {
	case "codex", "claude", "opencode", "pi":
	default:
		return event, errors.New("hook requires a supported agent name")
	}
	if event.Path == "" && !event.Retry {
		if stdin == nil {
			return event, errors.New("hook input unavailable")
		}
		var input struct {
			CWD       string `json:"cwd"`
			SessionID string `json:"session_id"`
		}
		decoder := json.NewDecoder(io.LimitReader(stdin, 1<<20))
		if err := decoder.Decode(&input); err != nil {
			return event, fmt.Errorf("invalid hook input: %w", err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF || input.CWD == "" {
			return event, errors.New("hook requires one JSON event with cwd")
		}
		event.Path = input.CWD
		if event.RunID == "" {
			event.RunID = input.SessionID
		}
	}
	event.InputPath = event.Path
	if len(event.RunID) > 32<<10 {
		return event, errors.New("hook session identity exceeds limit")
	}
	if event.ConfigFile == "" {
		event.ConfigFile = os.Getenv("SERVEDIFF_CONFIG_PATH")
	}
	if event.ConfigFile != "" {
		var err error
		event.ConfigFile, err = filepath.Abs(event.ConfigFile)
		if err != nil {
			return event, err
		}
	}
	// Workers inherit the scheduling process's environment. Different effective
	// routes must never coalesce into a worker carrying another host's settings.
	routing := sha256.New()
	encoder := json.NewEncoder(routing)
	for _, name := range []string{"SERVEDIFF_SERVER_URL", "SERVEDIFF_TOKEN", "SERVEDIFF_HOST", "SERVEDIFF_PORT", "SERVEDIFF_STATE", "SERVEDIFF_WEB_DIR", "SERVEDIFF_SOURCE_ID", "SERVEDIFF_RUNTIME_DIR", "XDG_RUNTIME_DIR", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "LOCALAPPDATA"} {
		value, exists := os.LookupEnv(name)
		if err := encoder.Encode([]interface{}{name, exists, value}); err != nil {
			return event, err
		}
	}
	event.Routing = hex.EncodeToString(routing.Sum(nil))
	return event, nil
}

func runHookWorker(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("__hook-worker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("state-dir", "", "private hook state")
	job := flags.String("job", "", "scheduled job identity")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" || *job == "" || flags.NArg() != 0 {
		return errors.New("hook worker requires state-dir and job")
	}
	engine := newHookEngine(*directory)
	return engine.Run(ctx, *job)
}

func newHookEngine(directory string) hooks.Engine {
	engine := hooks.Engine{Directory: directory, Timeout: time.Minute}
	engine.ValidateLocation = func(previous, current hooks.Event) error {
		if previous.Path == current.Path {
			return nil
		}
		oldStat, err := os.Stat(previous.Path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		newStat, err := os.Stat(current.Path)
		if err != nil {
			return err
		}
		if os.SameFile(oldStat, newStat) {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		sourceID, err := collector.SourceID()
		if err != nil {
			return err
		}
		identity, err := collector.SourceIdentity(ctx, previous.Path, sourceID, previous.Branch, previous.Base)
		if errors.Is(err, diffsource.ErrNotRepository) {
			return nil
		}
		if err != nil {
			return err
		}
		if identity == current.Identity {
			return fmt.Errorf("collector identity copied across %s and %s; preserve one source or use separate source IDs", previous.Path, current.Path)
		}
		return nil
	}
	engine.Launch = func(job string) error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		return hooks.LaunchDetached(executable, directory, job)
	}
	engine.Normalize = func(ctx context.Context, event hooks.Event) (hooks.Event, error) {
		source, err := diffsource.OpenRepository(ctx, event.Path)
		if errors.Is(err, diffsource.ErrNotRepository) || errors.Is(err, os.ErrNotExist) {
			return event, fmt.Errorf("%w: checkout %s is unavailable", hooks.ErrUnavailable, event.Path)
		}
		if err != nil {
			return event, err
		}
		event.Path = source.Root()
		if event.Identity != "" {
			sourceID, err := collector.SourceID()
			if err != nil {
				return event, err
			}
			identity, err := collector.SourceIdentity(ctx, event.Path, sourceID, event.Branch, event.Base)
			if err != nil {
				return event, err
			}
			if identity != event.Identity {
				return event, fmt.Errorf("%w: source identity changed at %s", hooks.ErrUnavailable, event.Path)
			}
		}
		return event, nil
	}
	engine.Expand = func(ctx context.Context, event hooks.Event) ([]hooks.Event, error) {
		sourceID, err := collector.SourceID()
		if err != nil {
			return nil, err
		}
		discovery, err := collector.Discover(ctx, event.Path, sourceID, event.Base)
		for _, diagnostic := range discovery.Diagnostics {
			engine.Record(hooks.Activity{Stage: "discovery", Status: "skipped", InputPath: event.Path, Reason: diagnostic})
		}
		if err != nil {
			return nil, err
		}
		events := make([]hooks.Event, 0, len(discovery.Sources))
		for _, source := range discovery.Sources {
			child := event
			child.InputPath, child.Path, child.Identity, child.Branch, child.Base, child.Resolved = source.InputPath, source.Path, source.Identity, source.Branch, source.Base, true
			events = append(events, child)
			engine.Record(hooks.Activity{Stage: "discovery", Status: "resolved", InputPath: source.InputPath, Path: source.Path, Branch: source.Branch, Base: source.Base})
		}
		return events, nil
	}
	var local *daemon.Connection
	var localClient *daemon.Client
	var remote *ingestion.Client
	var sourceID string
	engine.Resolve = func(ctx context.Context, event hooks.Event) (hooks.Target, error) {
		var err error
		sourceID, err = collector.SourceID()
		if err != nil {
			return hooks.Target{}, err
		}
		if endpoint := os.Getenv("SERVEDIFF_SERVER_URL"); endpoint != "" {
			token := os.Getenv("SERVEDIFF_TOKEN")
			remote = ingestion.NewClient(endpoint, token)
			health, err := remote.Health(ctx)
			if err != nil {
				return hooks.Target{}, err
			}
			digest := sha256.Sum256([]byte(token))
			return hooks.Target{Destination: "remote:" + strings.TrimRight(endpoint, "/") + ":" + hex.EncodeToString(digest[:]), Identity: health.StateID, SourceID: sourceID}, nil
		}
		remote = nil
		settings, explicit, err := resolvedServerSettings(options{configFile: event.ConfigFile})
		if err != nil {
			return hooks.Target{}, err
		}
		localClient, err = daemon.NewClient()
		if err != nil {
			return hooks.Target{}, err
		}
		destination := "local:" + localClient.RuntimeDirectory
		status, probeErr := localClient.Status(ctx)
		if probeErr == nil && status.State == "running" {
			local, err = localClient.EnsureConnection(ctx, settings, explicit)
		} else {
			err = engine.Start(ctx, destination, func(ctx context.Context) error {
				local, err = localClient.EnsureConnection(ctx, settings, explicit)
				return err
			})
		}
		if err != nil {
			return hooks.Target{}, err
		}
		status = local.Status()
		identity := status.StateID
		if status.Settings.State == "memory" {
			identity += ":" + status.InstanceID
		}
		return hooks.Target{Destination: destination, Identity: identity, SourceID: sourceID}, nil
	}
	engine.Collect = func(ctx context.Context, event hooks.Event, previous string) (ingestion.Request, string, error) {
		settings, err := collectionOptions(options{agent: event.Agent, runID: event.RunID, trigger: "agent-hook", base: event.Base, branch: event.Branch})
		if err != nil {
			return ingestion.Request{}, "", err
		}
		settings.OnSource = func(metadata ingestion.Metadata, comparison diffsource.Comparison) {
			activity := sourceActivity(metadata, comparison)
			activity.InputPath = event.InputPath
			engine.Record(activity)
		}
		request, fingerprint, err := collector.CollectChanged(ctx, event.Path, settings, previous)
		if errors.Is(err, collector.ErrUnchanged) {
			return request, fingerprint, hooks.ErrUnchanged
		}
		if errors.Is(err, diffsource.ErrComparisonUnavailable) || errors.Is(err, diffsource.ErrNotRepository) || errors.Is(err, os.ErrNotExist) {
			return request, fingerprint, fmt.Errorf("%w: %w", hooks.ErrUnavailable, err)
		}
		return request, fingerprint, err
	}
	engine.Confirm = func(ctx context.Context, target hooks.Target, id string) (bool, error) {
		if remote != nil {
			return remote.ObservationCurrent(ctx, id)
		}
		return ingestion.NewClient(local.Status().BrowserURL, "").ObservationCurrent(ctx, id)
	}
	engine.Deliver = func(ctx context.Context, target hooks.Target, request ingestion.Request) (string, error) {
		for attempt := 0; attempt < 2; attempt++ {
			engine.Record(hooks.Activity{Stage: "ingestion", Status: "attempt", SubmissionID: request.SubmissionID, Attempt: attempt + 1})
			if remote != nil {
				receipt, err := remote.SubmitTo(ctx, request, target.Identity)
				if err == nil {
					return receipt.ContextID, nil
				}
				engine.Record(hooks.Activity{Stage: "ingestion", Status: "failed", SubmissionID: request.SubmissionID, Attempt: attempt + 1, Error: err.Error()})
				var transport *url.Error
				var problem *ingestion.Problem
				retry := errors.As(err, &transport) || (errors.As(err, &problem) && (problem.Status == 429 || problem.Status >= 500))
				if !retry || attempt == 1 || ctx.Err() != nil {
					return "", err
				}
				continue
			}
			submitted, err := local.Ingest(ctx, request)
			if err == nil {
				if submitted.Context.ID == "" {
					return "", errors.New("hook receipt missing context identity")
				}
				return submitted.Context.ID, nil
			}
			engine.Record(hooks.Activity{Stage: "ingestion", Status: "failed", SubmissionID: request.SubmissionID, Attempt: attempt + 1, Error: err.Error()})
			retry, _ := retryableSubmission(err)
			if !retry || attempt == 1 || ctx.Err() != nil {
				return "", err
			}
			local, err = localClient.RecoverConnection(ctx, local)
			if err != nil {
				return "", err
			}
		}
		return "", errors.New("hook submission failed")
	}
	return engine
}
