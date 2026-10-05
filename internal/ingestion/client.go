package ingestion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

type Receipt struct {
	ContextID string                `json:"contextId"`
	ReviewURL string                `json:"reviewUrl"`
	MCPURL    string                `json:"mcpUrl"`
	Snapshot  review.RepositoryDiff `json:"snapshot"`
}

// Health identifies the durable destination, including replacement databases.
type Health struct {
	StateID         string `json:"stateId"`
	ProtocolVersion int    `json:"protocolVersion"`
}

type Problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func (problem *Problem) Error() string { return problem.Detail }

type Client struct {
	endpoint string
	token    string
	http     *http.Client
	err      error
}

func NewClient(endpoint, token string) *Client {
	client := &Client{endpoint: strings.TrimRight(endpoint, "/"), token: token,
		http: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		client.err = errors.New("server must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return client
}

// Health probes authentication, database reads and ingestion compatibility.
func (client *Client) Health(ctx context.Context) (Health, error) {
	if client.err != nil {
		return Health{}, client.err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+"/api/v2/health", nil)
	if err != nil {
		return Health{}, err
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return Health{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Health{}, &Problem{Status: response.StatusCode, Detail: "Health check failed: " + http.StatusText(response.StatusCode)}
	}
	var health Health
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&health); err != nil {
		return Health{}, fmt.Errorf("invalid health response: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || health.StateID == "" || health.ProtocolVersion != ProtocolVersion {
		return Health{}, errors.New("incompatible ingestion health response; update servediff-server")
	}
	return health, nil
}

// ObservationCurrent checks the durable stream head before suppressing a sync.
// Another collector can supersede an acknowledged observation in this checkout.
func (client *Client) ObservationCurrent(ctx context.Context, id string) (bool, error) {
	if client.err != nil {
		return false, client.err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+"/api/v2/contexts/"+url.PathEscape(id), nil)
	if err != nil {
		return false, err
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, &Problem{Status: response.StatusCode, Detail: "Observation check failed: " + http.StatusText(response.StatusCode)}
	}
	var observation struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		Availability string `json:"availability"`
		Stale        *bool  `json:"stale"`
		ExpiresAt    *int64 `json:"expiresAt"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&observation); err != nil {
		return false, fmt.Errorf("invalid observation response: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || observation.ID != id || observation.Stale == nil {
		return false, errors.New("observation response missing freshness identity")
	}
	return observation.Kind == "observation" && observation.Availability == "available" && !*observation.Stale && observation.ExpiresAt != nil && *observation.ExpiresAt > time.Now().UnixMilli(), nil
}

func (client *Client) Submit(ctx context.Context, input Request) (Receipt, error) {
	return client.SubmitTo(ctx, input, "")
}

// SubmitTo refuses delivery when the destination database changed after probing.
func (client *Client) SubmitTo(ctx context.Context, input Request, stateID string) (Receipt, error) {
	if client.err != nil {
		return Receipt{}, client.err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return Receipt{}, err
	}
	if len(raw) > MaxRequestBytes {
		return Receipt{}, errors.New("snapshot exceeds the 64 MiB ingestion limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/api/v2/ingestions", bytes.NewReader(raw))
	if err != nil {
		return Receipt{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if stateID != "" {
		request.Header.Set("X-Servediff-State", stateID)
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return Receipt{}, err
	}
	defer response.Body.Close()
	limit := int64(MaxRequestBytes)
	if response.StatusCode >= 300 {
		limit = 64 << 10
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return Receipt{}, &url.Error{Op: "read ingestion response", URL: client.endpoint, Err: err}
	}
	if int64(len(body)) > limit {
		return Receipt{}, errors.New("ingestion response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		problem := &Problem{Status: response.StatusCode, Detail: "Ingestion failed: " + http.StatusText(response.StatusCode)}
		_ = json.Unmarshal(body, problem)
		problem.Status = response.StatusCode
		return Receipt{}, problem
	}
	var receipt Receipt
	if err := json.Unmarshal(body, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("invalid ingestion receipt: %w", err)
	}
	if receipt.ContextID == "" || receipt.ReviewURL == "" || receipt.MCPURL == "" {
		return Receipt{}, errors.New("ingestion receipt is missing context identity or review URLs")
	}
	origin, _ := url.Parse(client.endpoint)
	for _, value := range []*string{&receipt.ReviewURL, &receipt.MCPURL} {
		target, err := url.Parse(*value)
		if err != nil || origin == nil {
			return Receipt{}, errors.New("invalid receipt URL")
		}
		target = origin.ResolveReference(target)
		if target.Scheme != origin.Scheme || target.Host != origin.Host || target.User != nil {
			return Receipt{}, errors.New("receipt URL must belong to configured server")
		}
		*value = target.String()
	}
	return receipt, nil
}
