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

func (client *Client) Submit(ctx context.Context, input Request) (Receipt, error) {
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
