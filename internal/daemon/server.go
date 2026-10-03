package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/controlapi"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/httpapi"
	"github.com/flexdinesh/servediff/internal/mcpapi"
	"github.com/flexdinesh/servediff/internal/processlock"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	buildversion "github.com/flexdinesh/servediff/internal/version"
	"github.com/flexdinesh/servediff/internal/webui"
)

// InitialInput is acquired by a foreground CLI, never by the daemon's stdin.
type InitialInput struct {
	Kind          string
	Path          string
	Raw           []byte
	SubmittedFrom string
	CaptureID     string
}

func Run(ctx context.Context, settings Settings, runtimeDir string, initial *InitialInput, ready func(Status, *contextservice.Submission)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	instanceID := rand.Text()
	if runtimeDir != "" {
		if _, err := prepareRuntime(runtimeDir); err != nil {
			return err
		}
		owner, err := processlock.TryAcquire(OwnershipPath(runtimeDir))
		if err != nil {
			return fmt.Errorf("service ownership: %w", err)
		}
		defer owner.Close()
		defer RemoveDescriptor(runtimeDir, instanceID)
		output, err := OpenLog(runtimeDir)
		if err != nil {
			return err
		}
		defer output.Close()
		logger := log.New(output, "servediff: ", log.LstdFlags)
		return runServer(ctx, cancel, settings, runtimeDir, instanceID, initial, ready, logger)
	}
	return runServer(ctx, cancel, settings, "", instanceID, initial, ready, log.New(os.Stderr, "servediff: ", log.LstdFlags))
}

func runServer(ctx context.Context, cancel context.CancelFunc, settings Settings, runtimeDir, instanceID string, initial *InitialInput, ready func(Status, *contextservice.Submission), logger *log.Logger) error {
	if settings.State == "" {
		path, err := reviewstore.DefaultPath()
		if err != nil {
			return err
		}
		settings.State = path
	}
	statePath := settings.State
	if statePath == "memory" {
		statePath = ""
	}
	store, err := reviewstore.Open(statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	processUser, err := osuser.Current()
	if err != nil {
		return fmt.Errorf("resolve process user: %w", err)
	}
	user, err := store.User(processUser.Uid, processUser.Username)
	if err != nil {
		return err
	}
	if err := store.PruneExpired(time.Now()); err != nil {
		return err
	}
	service := contextservice.NewWithContext(ctx, store, user)
	defer service.Close()
	var submitted *contextservice.Submission
	defaultContextID := ""
	if initial != nil {
		value, err := initialSubmission(ctx, service, *initial)
		if err != nil {
			return err
		}
		submitted = &value
		defaultContextID = value.Context.ID
	}
	var assets fs.FS = webui.Assets()
	if settings.WebDir != "" {
		absolute, err := filepath.Abs(settings.WebDir)
		if err != nil {
			return err
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("web-dir must be a directory")
		}
		settings.WebDir = absolute
		assets = os.DirFS(absolute)
	}
	listener, err := listenWeb(settings)
	if err != nil {
		return err
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return errors.New("unexpected web listener address")
	}
	settings.Port = address.Port
	browserHost := settings.Host
	if ip := net.ParseIP(browserHost); ip != nil && ip.IsUnspecified() {
		browserHost = "127.0.0.1"
		if ip.To4() == nil {
			browserHost = "::1"
		}
	}
	status := Status{State: "running", InstanceID: instanceID, PID: os.Getpid(), Version: buildversion.Number(), ProtocolVersion: controlapi.ProtocolVersion,
		URL:        "http://" + net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port)),
		BrowserURL: "http://" + net.JoinHostPort(browserHost, strconv.Itoa(settings.Port)), Settings: settings}
	statusFunc := func() (Status, error) {
		value := status
		var err error
		value.Worktrees, value.Captures, err = service.Count(ctx)
		return value, err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", contextMCP(service, store, defaultContextID))
	mux.Handle("/mcp/", contextMCP(service, store, defaultContextID))
	mux.Handle("/control/", http.NotFoundHandler())
	mux.Handle("/", httpapi.NewMultiWithContext(ctx, service, store, assets, defaultContextID))
	webServer := &http.Server{Handler: publicRequests(mux, settings), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, ErrorLog: logger}
	servers := []*http.Server{webServer}
	serverErrors := make(chan error, 2)
	go func() { serverErrors <- webServer.Serve(listener) }()
	if runtimeDir != "" {
		controlListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer controlListener.Close()
		token := rand.Text() + rand.Text()
		controlServer := &http.Server{Handler: controlapi.New(token, service, statusFunc, cancel), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, ErrorLog: logger}
		servers = append(servers, controlServer)
		go func() { serverErrors <- controlServer.Serve(controlListener) }()
		descriptor := Descriptor{Endpoint: "http://" + controlListener.Addr().String(), Token: token, Status: status}
		if err := PublishDescriptor(runtimeDir, descriptor); err != nil {
			for _, server := range servers {
				_ = server.Close()
			}
			return err
		}
	}
	if ready != nil {
		ready(status, submitted)
	}
	pruneDone := make(chan struct{})
	go func() {
		defer close(pruneDone)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.PruneExpired(time.Now()); err != nil {
					logger.Printf("prune captures: %v", err)
				}
			}
		}
	}()
	var result error
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			result = errors.Join(result, err)
		}
	}
	<-pruneDone
	return result
}

func initialSubmission(ctx context.Context, service *contextservice.Service, input InitialInput) (contextservice.Submission, error) {
	switch input.Kind {
	case "worktree":
		return service.Register(ctx, rand.Text(), input.Path)
	case "capture":
		return service.Capture(ctx, rand.Text(), string(input.Raw), input.SubmittedFrom)
	case "reopen":
		return service.OpenCapture(ctx, input.CaptureID)
	default:
		return contextservice.Submission{}, errors.New("unknown diff input")
	}
}

func listenWeb(settings Settings) (net.Listener, error) {
	if settings.Port >= 0 {
		return net.Listen("tcp", net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port)))
	}
	probe, err := net.Listen("tcp", net.JoinHostPort(settings.Host, "0"))
	if err != nil {
		return nil, fmt.Errorf("bind host %s: %w", settings.Host, err)
	}
	_ = probe.Close()
	for port := 7981; port <= 7990; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort(settings.Host, strconv.Itoa(port)))
		if err == nil {
			return listener, nil
		}
	}
	return nil, errors.New("no available servediff port (7981-7990); choose --port")
}

func contextMCP(service *contextservice.Service, store *reviewstore.Store, defaultID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := defaultID
		if strings.HasPrefix(r.URL.Path, "/mcp/contexts/") {
			id = strings.TrimPrefix(r.URL.Path, "/mcp/contexts/")
			if id == "" || strings.Contains(id, "/") {
				http.NotFound(w, r)
				return
			}
		} else if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if id == "" {
			page, err := service.List(r.Context(), 2, "")
			if err != nil {
				publicProblem(w, 503, "context_unavailable", err.Error())
				return
			}
			if len(page.Contexts) != 1 {
				publicProblem(w, 409, "context_required", "Use /mcp/contexts/{contextId}")
				return
			}
			id = page.Contexts[0].ID
		}
		active, err := service.Resolve(r.Context(), id)
		if err != nil {
			status := 404
			var requestError *diffsource.RequestError
			if errors.As(err, &requestError) {
				status = requestError.Status
			}
			publicProblem(w, status, "context_unavailable", err.Error())
			return
		}
		handler := mcpapi.New(reviewservice.New(active, store), active.Capabilities.Review.Comments.Enabled(), buildversion.String())
		handler.ServeHTTP(w, r)
	})
}

func publicRequests(next http.Handler, settings Settings) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			publicProblem(w, 403, "cross_origin", "Cross-origin access denied")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			publicProblem(w, 403, "cross_site", "Cross-site access denied")
			return
		}
		if ip := net.ParseIP(settings.Host); ip != nil && ip.IsLoopback() {
			host, port, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
				port = ""
			}
			clientIP := net.ParseIP(host)
			if (host != "localhost" && (clientIP == nil || !clientIP.IsLoopback())) || (port != "" && port != strconv.Itoa(settings.Port)) {
				publicProblem(w, 403, "invalid_host", "HTTP host denied")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func publicProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(controlapi.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail})
}
