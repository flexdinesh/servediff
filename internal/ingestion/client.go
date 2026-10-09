package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Receipt struct {
	ContextID string `json:"contextId"`
	ReviewURL string `json:"reviewUrl"`
	MCPURL    string `json:"mcpUrl"`
}

// Health identifies the durable destination, including replacement databases.
type Health struct {
	IngestionEnabled bool   `json:"ingestionEnabled"`
	QueuedIngestion  bool   `json:"queuedIngestion"`
	StateID          string `json:"stateId"`
	ProtocolVersion  int    `json:"protocolVersion"`
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
