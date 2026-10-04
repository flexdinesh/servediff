// Package remoteserver adds deployment authentication to the shared server.
package remoteserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/serverapp"
	"github.com/flexdinesh/servediff/internal/webui"
)

type Settings struct {
	Listen  string
	State   string
	Account string
	Token   string
}

func Handler(ctx context.Context, store *reviewstore.Store, account, token string, assets fs.FS) (http.Handler, func() error, error) {
	if strings.TrimSpace(account) == "" || len(account) > 256 || strings.ContainsAny(account, "\r\n") || len(token) < 32 || strings.ContainsAny(token, "\r\n") {
		return nil, nil, errors.New("remote server requires an account and a token of at least 32 bytes")
	}
	user, err := store.User("account:"+account, account)
	if err != nil {
		return nil, nil, err
	}
	service := contextservice.NewWithContext(ctx, store, user)
	return authenticate(serverapp.Handler(ctx, service, store, assets, ""), account, token), service.Close, nil
}

func authenticate(next http.Handler, account, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		bearer := subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
		name, password, basic := r.BasicAuth()
		basic = basic && subtle.ConstantTimeCompare([]byte(name), []byte(account)) == 1 && subtle.ConstantTimeCompare([]byte(password), []byte(token)) == 1
		if !bearer && !basic {
			w.Header().Set("WWW-Authenticate", `Basic realm="servediff", charset="UTF-8"`)
			http.Error(w, "Authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Run(ctx context.Context, settings Settings, ready func(string)) error {
	if settings.State == "" {
		return errors.New("remote state database path required")
	}
	store, err := reviewstore.Open(settings.State)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.PruneExpired(time.Now()); err != nil {
		return err
	}
	handler, closeService, err := Handler(ctx, store, settings.Account, settings.Token, webui.Assets())
	if err != nil {
		return err
	}
	defer closeService()
	listener, err := net.Listen("tcp", settings.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	defer server.Close()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if ready != nil {
		ready(listener.Addr().String())
	}
serving:
	for {
		select {
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			break serving
		case <-ctx.Done():
			break serving
		case <-ticker.C:
			if err := store.PruneExpired(time.Now()); err != nil {
				return err
			}
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		return server.Close()
	}
	return nil
}
