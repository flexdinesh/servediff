package submission

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
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
)

type Target struct{ Destination, Identity, SourceID string }
type Destination struct {
	Target          Target
	Endpoint, Token string
	client          *ingestion.Client
	Progress        func(ingestion.Job)
}

func Resolve(ctx context.Context, endpoint, token string) (*Destination, error) {
	client := ingestion.NewClient(endpoint, token)
	health, err := client.Health(ctx)
	if err != nil {
		return nil, err
	}
	if !health.QueuedIngestion {
		return nil, errors.New("server requires queued ingestion")
	}
	digest := sha256.Sum256([]byte(token))
	return &Destination{Endpoint: endpoint, Token: token, client: client, Target: Target{Destination: "remote:" + strings.TrimRight(endpoint, "/") + ":" + hex.EncodeToString(digest[:]), Identity: health.StateID}}, nil
}
func (target *Destination) Current(ctx context.Context, id string) (bool, error) {
	return target.client.ObservationCurrent(ctx, id)
}
func (target *Destination) Deliver(ctx context.Context, request ingestion.Request) (result ingestion.Receipt, failure error) {
	defer func() {
		if failure != nil && target.Token != "" {
			failure = redactedError{cause: failure, secret: target.Token}
		}
	}()
	for attempt := 0; attempt < 2; attempt++ {
		result, failure = target.client.SyncTo(ctx, request, target.Target.Identity, target.Progress)
		if failure == nil {
			return result, nil
		}
		var transport *url.Error
		var problem *ingestion.Problem
		retry := errors.As(failure, &transport) || (errors.As(failure, &problem) && (problem.Status == 429 || problem.Status >= 500))
		if !retry || ctx.Err() != nil {
			return result, failure
		}
	}
	return result, fmt.Errorf("submission %s retained for retry: %w", request.SubmissionID, failure)
}
func (target *Destination) SuppressInitialEmpty(ctx context.Context, request ingestion.Request) (bool, error) {
	metadata := request.Metadata
	query := url.Values{"sourceId": {metadata.SourceID}, "limit": {"500"}}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for pageNumber := 0; pageNumber < 8; pageNumber++ {
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Endpoint+"/api/v2/contexts?"+query.Encode(), nil)
		if err != nil {
			return false, err
		}
		if target.Token != "" {
			httpRequest.Header.Set("Authorization", "Bearer "+target.Token)
		}
		response, err := client.Do(httpRequest)
		if err != nil {
			return false, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return false, errors.New("cannot reconcile retained captures")
		}
		var page struct {
			Contexts []struct {
				Observation *ingestion.Metadata `json:"observation"`
			} `json:"contexts"`
			NextCursor *string `json:"nextCursor"`
		}
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
