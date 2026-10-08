package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/browser"
	"github.com/flexdinesh/servediff/internal/collector"
	"github.com/flexdinesh/servediff/internal/config"
	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/daemon"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/hooks"
	"github.com/flexdinesh/servediff/internal/ingestion"
)

// Producer routing is resolved once: file < environment < explicit flags.
func resolvedCollectorSettings(values options) (options, error) {
	saved, err := config.LoadFile(values.configFile)
	if err != nil {
		return values, err
	}
	if !values.serverSet {
		values.server = saved.Server
	}
	if !values.tokenSet {
		values.token = saved.Token
	}
	return values, nil
}

// destination owns lifecycle and transport. A resolved destination pins every
// immutable retry to the same database; remote failure never starts locally.
type destination struct {
	queued          bool
	progress        func(ingestion.Job)
	target          hooks.Target
	endpoint, token string
	remote          *ingestion.Client
	local           *daemon.Connection
	lifecycle       *daemon.Client
}

func resolveDestination(ctx context.Context, values options, settings daemon.Settings, explicit daemon.Explicit, start func(context.Context, string, func(context.Context) error) error) (*destination, error) {
	result := &destination{endpoint: values.server, token: values.token}
	if values.server != "" {
		result.remote = ingestion.NewClient(values.server, values.token)
		health, err := result.remote.Health(ctx)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(values.token))
		result.queued = health.QueuedIngestion
		result.target = hooks.Target{Destination: "remote:" + strings.TrimRight(values.server, "/") + ":" + hex.EncodeToString(digest[:]), Identity: health.StateID}
		return result, nil
	}
	client, err := daemon.NewClient()
	if err != nil {
		return nil, err
	}
	result.lifecycle = client
	result.target.Destination = "local:" + client.RuntimeDirectory
	connect := func(ctx context.Context) error {
		result.local, err = client.EnsureConnection(ctx, settings, explicit)
		return err
	}
	if start != nil {
		status, probeErr := client.Status(ctx)
		if probeErr == nil && status.State == "running" {
			err = connect(ctx)
		} else {
			err = start(ctx, result.target.Destination, connect)
		}
	} else {
		err = connect(ctx)
	}
	if err != nil {
		return nil, err
	}
	status := result.local.Status()
	result.endpoint = status.BrowserURL
	result.target.Identity = status.StateID
	if status.Settings.State == "memory" {
		result.target.Identity += ":" + status.InstanceID
	}
	return result, nil
}

func (target *destination) current(ctx context.Context, id string) (bool, error) {
	return ingestion.NewClient(target.endpoint, target.token).ObservationCurrent(ctx, id)
}

func (target *destination) deliver(ctx context.Context, request ingestion.Request) (result ingestion.Receipt, failure error) {
	defer func() {
		if failure != nil && target.token != "" {
			failure = redactedError{cause: failure, secret: target.token}
		}
	}()
	var firstFailure error
	uncertain := false
	for attempt := 0; attempt < 2; attempt++ {
		recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "started", SubmissionID: request.SubmissionID, Attempt: attempt + 1, Destination: target.endpoint})
		var receipt ingestion.Receipt
		var err error
		if target.remote != nil {
			if target.queued {
				receipt, err = target.remote.SyncTo(ctx, request, target.target.Identity, target.progress)
			} else {
				receipt, err = target.remote.SubmitTo(ctx, request, target.target.Identity)
			}
		} else {
			var submitted contextservice.Submission
			submitted, err = target.local.Ingest(ctx, request)
			if err == nil {
				id := submitted.Context.ID
				if id == "" {
					return receipt, errors.New("receipt missing context identity")
				}
				receipt = ingestion.Receipt{ContextID: id, ReviewURL: target.endpoint + "/contexts/" + url.PathEscape(id), MCPURL: target.endpoint + "/mcp/contexts/" + url.PathEscape(id), Snapshot: submitted.Snapshot}
			}
		}
		if err == nil {
			recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "acknowledged", SubmissionID: request.SubmissionID, ContextID: receipt.ContextID, Attempt: attempt + 1, Destination: target.endpoint})
			return receipt, nil
		}
		recordCollectorActivity(hooks.Activity{Stage: "ingestion", Status: "failed", SubmissionID: request.SubmissionID, Attempt: attempt + 1, Error: err.Error()}, target.token)
		retry, ambiguous := retryableSubmission(err)
		if target.remote != nil {
			var transport *url.Error
			var problem *ingestion.Problem
			ambiguous = errors.As(err, &transport)
			retry = ambiguous || (errors.As(err, &problem) && (problem.Status == 429 || problem.Status >= 500))
		}
		uncertain = uncertain || ambiguous
		if !retry || ctx.Err() != nil || attempt == 1 {
			cause := errors.Join(firstFailure, err)
			return receipt, deliveryFailure(request.SubmissionID, uncertain, cause)
		}
		firstFailure = err
		if target.remote == nil {
			recovered, recoveryErr := target.lifecycle.RecoverConnection(ctx, target.local)
			if recoveryErr != nil {
				cause := fmt.Errorf("recovery failed: %w", errors.Join(firstFailure, recoveryErr))
				return receipt, deliveryFailure(request.SubmissionID, uncertain, cause)
			}
			target.local = recovered
			target.endpoint = recovered.Status().BrowserURL
		}
	}
	return ingestion.Receipt{}, errors.New("submission failed")
}

func collectSubmissions(ctx context.Context, command string, values options, stdin *os.File) ([]ingestion.Request, error) {
	if command != "review" || values.branch != "" {
		request, err := collectSubmission(ctx, command, values, stdin)
		if err != nil {
			return nil, err
		}
		return []ingestion.Request{request}, nil
	}
	settings, err := collectionOptions(values)
	if err != nil {
		return nil, err
	}
	discovery, err := collector.DiscoverWorktrees(ctx, values.directory, settings.SourceID, values.base)
	if err != nil {
		return nil, err
	}
	requests := make([]ingestion.Request, 0, len(discovery.Sources))
	failures := make([]error, 0)
	for _, diagnostic := range discovery.Diagnostics {
		failures = append(failures, errors.New(diagnostic))
	}
	for _, source := range discovery.Sources {
		child := values
		child.directory, child.base = source.Path, source.Base
		// Keep the triggering checkout distinct from each discovered root.
		settings.SubmissionID = newSubmissionID()
		settings.Base = source.Base
		settings.TriggerRoot = source.InputPath
		request, err := collector.Collect(ctx, child.directory, settings)
		if err != nil {
			failures = append(failures, fmt.Errorf("collect %s: %w", source.Path, err))
			continue
		}
		requests = append(requests, request)
	}
	return requests, errors.Join(failures...)
}

func submitCollected(ctx context.Context, command string, values options, settings daemon.Settings, explicit daemon.Explicit, requests []ingestion.Request, stdout, stderr io.Writer, started time.Time) error {
	target, err := resolveDestination(ctx, values, settings, explicit, nil)
	if err != nil {
		return err
	}
	target.queued = false // Legacy review output includes the synchronous snapshot receipt.
	var failures []error
	var originReceipt *ingestion.Receipt
	var originRequest ingestion.Request
	originRoot := values.directory
	if source, openErr := diffsource.OpenRepository(ctx, values.directory); openErr == nil {
		originRoot = source.Root()
	}
	for _, request := range requests {
		receipt, err := target.deliver(ctx, request)
		if err != nil {
			failures = append(failures, fmt.Errorf("submit %s: %w", request.Metadata.Root, err))
			continue
		}
		if values.branch != "" || request.Metadata.Root == originRoot {
			originReceipt = &receipt
			originRequest = request
		}
	}
	if originReceipt != nil {
		input := loadedInput{directory: originRequest.Metadata.Root, snapshot: originReceipt.Snapshot, processed: time.Since(started), mode: "git", contextID: originReceipt.ContextID, mcpURL: originReceipt.MCPURL, submitted: true, remote: target.remote != nil}
		var status *daemon.Status
		if target.local != nil {
			current := target.local.Status()
			status = &current
		}
		urls := submissionURLs(*originReceipt, status)
		writeStartup(stdout, input, urls...)
		openBrowser(values, urls[0], stderr)
	}
	return errors.Join(failures...)
}

// A lost local acknowledgement must not leave an old dirty server head current.
// Query retained captures, checking stable identity and comparison policy. An
// incomplete/error response deliberately falls back to publishing the empty state.
func (target *destination) suppressInitialEmpty(ctx context.Context, request ingestion.Request) (bool, error) {
	metadata := request.Metadata
	query := url.Values{"sourceId": {metadata.SourceID}, "limit": {"500"}}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for pageNumber := 0; pageNumber < 8; pageNumber++ {
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.endpoint+"/api/v2/contexts?"+query.Encode(), nil)
		if err != nil {
			return false, err
		}
		if target.token != "" {
			httpRequest.Header.Set("Authorization", "Bearer "+target.token)
		}
		response, err := client.Do(httpRequest)
		if err != nil {
			return false, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return false, errors.New("cannot reconcile retained captures")
		}
		var page contextservice.Page
		decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
		err = decoder.Decode(&page)
		valid := decoder.Decode(&struct{}{}) == io.EOF
		response.Body.Close()
		if err != nil || !valid || page.Contexts == nil {
			return false, errors.New("invalid retained capture response")
		}
		for _, item := range page.Contexts {
			existing := item.Observation
			if existing != nil && existing.SourceID == metadata.SourceID && existing.RepositoryKey == metadata.RepositoryKey && existing.CheckoutKey == metadata.CheckoutKey && existing.BranchID == metadata.BranchID && sameComparison(existing.Comparison, metadata.Comparison) {
				return false, nil
			}
		}
		if page.NextCursor == nil {
			return true, nil
		}
		query.Set("cursor", *page.NextCursor)
	}
	return false, errors.New("retained capture reconciliation exceeded page limit")
}

func sameComparison(left, right *ingestion.Comparison) bool {
	kind, base := func(value *ingestion.Comparison) (string, string) {
		if value == nil {
			return "working-tree", "HEAD"
		}
		return value.Kind, value.BaseRef
	}(left)
	otherKind, otherBase := func(value *ingestion.Comparison) (string, string) {
		if value == nil {
			return "working-tree", "HEAD"
		}
		return value.Kind, value.BaseRef
	}(right)
	return kind == otherKind && base == otherBase
}

// Keep error identity for retry decisions while preventing persisted credentials
// echoed by a server from reaching activity logs or CLI output.
type redactedError struct {
	cause  error
	secret string
}

func (failure redactedError) Error() string {
	return strings.ReplaceAll(failure.cause.Error(), failure.secret, "[redacted]")
}
func (failure redactedError) Unwrap() error { return failure.cause }

func deliveryFailure(id string, uncertain bool, cause error) error {
	if uncertain {
		return fmt.Errorf("submission %s may have been saved; inspect server observations before resubmitting: %w", id, cause)
	}
	return cause
}

func submissionURLs(receipt ingestion.Receipt, local *daemon.Status) []string {
	if local == nil {
		return []string{receipt.ReviewURL}
	}
	return browser.URLs(local.Settings.Host, local.Settings.Port, "/contexts/"+url.PathEscape(receipt.ContextID))
}
