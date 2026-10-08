package serverapp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

type Endpoint struct {
	Server   *http.Server
	Listener net.Listener
}
type Task func(context.Context) error

// Run ties HTTP listeners and background work to one foreground lifetime.
// All tasks must honor cancellation before returning; storage closes afterwards.
func Run(ctx context.Context, endpoints []Endpoint, tasks []Task, ready func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failures := make(chan error, len(endpoints)+len(tasks))
	var workers sync.WaitGroup
	for _, endpoint := range endpoints {
		workers.Add(1)
		go func() { defer workers.Done(); failures <- endpoint.Server.Serve(endpoint.Listener) }()
	}
	for _, task := range tasks {
		workers.Add(1)
		go func() { defer workers.Done(); failures <- task(ctx) }()
	}
	if ready != nil {
		ready()
	}
	var result error
	select {
	case <-ctx.Done():
	case err := <-failures:
		if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			result = err
		}
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for _, endpoint := range endpoints {
		if err := endpoint.Server.Shutdown(shutdown); err != nil {
			result = errors.Join(result, endpoint.Server.Close())
		}
	}
	workers.Wait()
	return result
}

func Prune(prune func(time.Time) error) Task {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case now := <-ticker.C:
				if err := prune(now); err != nil {
					return err
				}
			}
		}
	}
}
