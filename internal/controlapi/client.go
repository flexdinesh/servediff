package controlapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/ingestion"
)

type Client struct {
	endpoint string
	token    string
	http     *http.Client
	initErr  error
}

func NewClient(endpoint, token string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 30 * time.Second
	client := &Client{
		endpoint: strings.TrimRight(endpoint, "/"), token: token,
		http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		client.initErr = fmt.Errorf("invalid service control endpoint: %w", err)
		return client
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" || ip == nil || !ip.IsLoopback() || parsed.Port() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		client.initErr = fmt.Errorf("service control endpoint must be an HTTP loopback address with a port")
	}
	return client
}

func (client *Client) Status(ctx context.Context) (Status, error) {
	var result Status
	err := client.do(ctx, http.MethodGet, "/control/v1/status", nil, nil, &result)
	return result, err
}

func (client *Client) Register(ctx context.Context, submissionID, path string) (contextservice.Submission, error) {
	input := struct {
		SubmissionID string `json:"submissionId"`
		Path         string `json:"path"`
	}{SubmissionID: submissionID, Path: path}
	body, err := json.Marshal(input)
	if err != nil {
		return contextservice.Submission{}, err
	}
	var result contextservice.Submission
	err = client.do(ctx, http.MethodPost, "/control/v1/worktrees", bytes.NewReader(body), http.Header{"Content-Type": []string{"application/json"}}, &result)
	return result, err
}

func (client *Client) Capture(ctx context.Context, submissionID string, raw []byte, submittedFrom string) (contextservice.Submission, error) {
	if len(raw) > MaxPatchBytes {
		return contextservice.Submission{}, &Problem{Status: http.StatusRequestEntityTooLarge, Code: "input_too_large", Detail: "Patch exceeds 16 MiB limit"}
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/octet-stream")
	headers.Set(submissionHeader, submissionID)
	headers.Set(submittedFromHeader, base64.RawURLEncoding.EncodeToString([]byte(submittedFrom)))
	var result contextservice.Submission
	err := client.do(ctx, http.MethodPost, "/control/v1/captures", bytes.NewReader(raw), headers, &result)
	return result, err
}

func (client *Client) OpenCapture(ctx context.Context, id string) (contextservice.Submission, error) {
	if id == "" || strings.ContainsAny(id, "/?#") {
		return contextservice.Submission{}, fmt.Errorf("invalid capture ID")
	}
	var result contextservice.Submission
	err := client.do(ctx, http.MethodPost, "/control/v1/captures/"+url.PathEscape(id)+"/open", nil, nil, &result)
	return result, err
}

func (client *Client) Shutdown(ctx context.Context) error {
	var result struct{}
	return client.do(ctx, http.MethodPost, "/control/v1/shutdown", nil, nil, &result)
}

func (client *Client) do(ctx context.Context, method, path string, body io.Reader, headers http.Header, result interface{}) error {
	if client.initErr != nil {
		return client.initErr
	}
	request, err := http.NewRequestWithContext(ctx, method, client.endpoint+path, body)
	if err != nil {
		return err
	}
	if headers != nil {
		request.Header = headers
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limit := int64(64 << 20)
	if path == "/control/v1/status" || response.StatusCode >= 300 {
		limit = 64 << 10
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read service response: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("service response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		problem := &Problem{Status: response.StatusCode, Detail: "Service request failed: " + http.StatusText(response.StatusCode)}
		_ = json.Unmarshal(data, problem)
		problem.Status = response.StatusCode
		return problem
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("invalid service response: %w", err)
	}
	return nil
}

func (client *Client) Ingest(ctx context.Context, input ingestion.Request) (contextservice.Submission, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return contextservice.Submission{}, err
	}
	if len(raw) > ingestion.MaxRequestBytes {
		return contextservice.Submission{}, fmt.Errorf("snapshot exceeds 64 MiB ingestion limit")
	}
	var result contextservice.Submission
	err = client.do(ctx, http.MethodPost, "/control/v1/ingestions", bytes.NewReader(raw), http.Header{"Content-Type": []string{"application/json"}}, &result)
	return result, err
}
