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
	"time"
)

// SyncTo distinguishes durable acceptance from committed publication. Cancelling
// this wait does not cancel a job already accepted by the server.
func (client *Client) SyncTo(ctx context.Context, input Request, stateID string, progress func(Job)) (Receipt, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Receipt{}, err
	}
	if len(raw) > MaxRequestBytes {
		return Receipt{}, errors.New("snapshot exceeds the 64 MiB ingestion limit")
	}
	job, err := client.jobRequest(ctx, http.MethodPost, "/api/v2/ingestion-jobs", raw, stateID)
	if err != nil {
		return Receipt{}, err
	}
	id := job.ID
	for {
		if job.ID != id || job.SubmissionID != input.SubmissionID {
			return Receipt{}, errors.New("ingestion job identity changed")
		}
		if progress != nil {
			progress(job)
		}
		switch job.State {
		case "succeeded":
			if job.ContextID == "" {
				return Receipt{}, errors.New("completed job missing context identity")
			}
			return Receipt{ContextID: job.ContextID, ReviewURL: client.endpoint + "/contexts/" + url.PathEscape(job.ContextID), MCPURL: client.endpoint + "/mcp/contexts/" + url.PathEscape(job.ContextID)}, nil
		case "failed":
			return Receipt{}, fmt.Errorf("ingestion job %s failed: %s", id, job.Detail)
		case "queued", "running", "retrying":
		default:
			return Receipt{}, errors.New("invalid ingestion job state")
		}
		select {
		case <-ctx.Done():
			return Receipt{}, fmt.Errorf("job %s accepted; server processing continues: %w", id, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
		job, err = client.jobRequest(ctx, http.MethodGet, "/api/v2/ingestion-jobs/"+url.PathEscape(id), nil, stateID)
		if err != nil {
			return Receipt{}, fmt.Errorf("job %s accepted; retry the same submission to resume: %w", id, err)
		}
	}
}

func (client *Client) jobRequest(ctx context.Context, method, path string, raw []byte, stateID string) (Job, error) {
	if client.err != nil {
		return Job{}, client.err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return Job{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("X-Diffx-State", stateID)
	response, err := client.http.Do(request)
	if err != nil {
		return Job{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return Job{}, err
	}
	if len(body) > 64<<10 {
		return Job{}, errors.New("job response exceeds limit")
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		problem := &Problem{Status: response.StatusCode, Detail: http.StatusText(response.StatusCode)}
		_ = json.Unmarshal(body, problem)
		problem.Status = response.StatusCode
		return Job{}, problem
	}
	var job Job
	if err := json.Unmarshal(body, &job); err != nil {
		return Job{}, err
	}
	if job.ID == "" {
		return Job{}, errors.New("job response missing identity")
	}
	return job, nil
}
