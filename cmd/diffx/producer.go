package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/flexdinesh/diffx/internal/collector"
	"github.com/flexdinesh/diffx/internal/config"
	"github.com/flexdinesh/diffx/internal/ingestion"
)

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

func collectSubmissions(ctx context.Context, values options) ([]ingestion.Request, error) {
	if values.branch != "" {
		request, err := collectSubmission(ctx, values)
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
