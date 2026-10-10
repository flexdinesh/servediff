package testsupport

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/flexdinesh/diffx/internal/contextservice"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/ingestionqueue"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/serverapp"
	"github.com/flexdinesh/diffx/internal/webui"
)

type TestServer struct {
	*httptest.Server
	stop func()
}

func (server *TestServer) Close() { server.Server.Close(); server.stop() }

// Server composes an unauthenticated queue and worker for producer integration tests.
func Server(t *testing.T, service *contextservice.Service, store *reviewstore.Store) *TestServer {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- ingestionqueue.Run(ctx, store.Queue(), func(ctx context.Context, _ string, input ingestion.Request) (string, error) {
			result, err := service.Ingest(ctx, input)
			return result.Context.ID, err
		})
	}()
	server := httptest.NewServer(serverapp.Handler(ctx, service, store, webui.Assets(), "", serverapp.Options{Queue: store.Queue()}))
	return &TestServer{server, func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}}
}
