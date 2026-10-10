package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
)

type Client struct {
	*ingestion.Client
	endpoint, token string
}

func NewClient(endpoint, token string) *Client {
	return &Client{ingestion.NewClient(endpoint, token), endpoint, token}
}
func (client *Client) Submit(ctx context.Context, input ingestion.Request) (Receipt, error) {
	return client.SubmitTo(ctx, input, "")
}
func (client *Client) SubmitTo(ctx context.Context, input ingestion.Request, state string) (Receipt, error) {
	receipt, err := client.SyncTo(ctx, input, state, nil)
	result := Receipt{Receipt: receipt}
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", client.endpoint+"/api/v2/contexts/"+result.ContextID+"/diffs/current?scope=all", nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+client.token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, fmt.Errorf("snapshot status %d", response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result.Snapshot)
	return result, err
}

type Receipt struct {
	ingestion.Receipt
	Snapshot review.RepositoryDiff
}
