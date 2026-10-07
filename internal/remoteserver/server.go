// Package remoteserver adds deployment authentication to the shared server.
package remoteserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/serverapp"
	"github.com/flexdinesh/servediff/internal/webui"
)

type Settings struct {
	Listen         string
	State          string
	Account        string
	Token          string
	RetentionDays  int
	BootstrapReady func(string)
}

func Handler(ctx context.Context, store *reviewstore.Store, account, token string, assets fs.FS) (http.Handler, func() error, error) {
	if _, err := store.EnsureRemoteUser(account, token); err != nil {
		return nil, nil, err
	}
	return MultiHandler(ctx, store, assets)
}

// MultiHandler composes one application service per provisioned user. Provisioning
// uses the database ownership lock, so the account set is fixed while serving.
func MultiHandler(ctx context.Context, store *reviewstore.Store, assets fs.FS) (http.Handler, func() error, error) {
	users, err := store.RemoteUsers()
	if err != nil {
		return nil, nil, err
	}
	if len(users) == 0 {
		return nil, nil, errors.New("remote server has no provisioned users")
	}
	handlers := make(map[string]http.Handler, len(users))
	services := make([]*contextservice.Service, 0, len(users))
	for _, user := range users {
		service := contextservice.NewWithContext(ctx, store, user)
		services = append(services, service)
		handlers[user.ID] = serverapp.Handler(ctx, service, store, assets, "")
	}
	closeServices := func() error {
		var err error
		for _, service := range services {
			err = errors.Join(err, service.Close())
		}
		return err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		var token, account string
		basic := false
		if value := r.Header.Get("Authorization"); strings.HasPrefix(value, "Bearer ") {
			token = strings.TrimPrefix(value, "Bearer ")
		} else if name, password, ok := r.BasicAuth(); ok {
			account, token = name, password
			basic = true
		}
		user, err := store.AuthenticateToken(token)
		if err != nil || (basic && subtle.ConstantTimeCompare([]byte(account), []byte(user.Name)) != 1) {
			unauthorized(w)
			return
		}
		handler, exists := handlers[user.ID]
		if !exists {
			unauthorized(w)
			return
		}
		handler.ServeHTTP(w, r)
	}), closeServices, nil
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="servediff", charset="UTF-8"`)
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}

func Run(ctx context.Context, settings Settings, ready func(string)) error {
	if settings.State == "" {
		return errors.New("remote state database path required")
	}
	if settings.State == "memory" {
		settings.State = ":memory:"
	}
	retention, err := Retention(settings.RetentionDays)
	if err != nil {
		return err
	}
	store, err := reviewstore.OpenWithRetention(settings.State, retention)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := bootstrap(store, settings); err != nil {
		return err
	}
	if err := store.PruneExpired(time.Now()); err != nil {
		return err
	}
	handler, closeService, err := MultiHandler(ctx, store, webui.Assets())
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

func Retention(days int) (time.Duration, error) {
	if days == 0 {
		days = 7
	}
	const day = 24 * time.Hour
	if days < 0 || int64(days) > int64((1<<63-1)/day) {
		return 0, errors.New("retention days must be positive and fit within a duration")
	}
	return time.Duration(days) * day, nil
}

// GenerateToken returns a high-entropy credential suitable for bearer or Basic auth.
func GenerateToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func bootstrap(store *reviewstore.Store, settings Settings) error {
	account := settings.Account
	if account == "" {
		account = "admin"
	}
	if settings.Token != "" {
		_, err := store.EnsureRemoteUser(account, settings.Token)
		return err
	}
	users, err := store.RemoteUsers()
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.Name == account {
			return nil
		}
	}
	if settings.State == "" || settings.State == ":memory:" || settings.State == "memory" {
		return errors.New("in-memory remote server requires SERVEDIFF_TOKEN")
	}
	path := settings.State + ".admin-token"
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		token, err := GenerateToken()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(token + "\n")
		syncErr := file.Sync()
		if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
			return err
		}
		raw = []byte(token)
	} else if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if _, err := store.EnsureRemoteUser(account, strings.TrimSpace(string(raw))); err != nil {
		return err
	}
	if settings.BootstrapReady != nil {
		settings.BootstrapReady(path)
	}
	return nil
}
