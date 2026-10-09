package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
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
