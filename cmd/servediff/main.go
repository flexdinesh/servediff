package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flexdinesh/servediff/internal/browser"
	"github.com/flexdinesh/servediff/internal/httpapi"
	"github.com/flexdinesh/servediff/internal/mcpapi"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
	buildversion "github.com/flexdinesh/servediff/internal/version"
	"github.com/flexdinesh/servediff/internal/webui"
)

func run(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	values, err := parseOptions(arguments, stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			return nil
		}
		return err
	}
	if values.version {
		fmt.Fprintln(stdout, buildversion.String())
		return nil
	}
	input, err := loadInput(ctx, values, stdin)
	if err != nil {
		return err
	}
	active := session.Resolve(input.source, session.Policies{})
	if os.Getenv("SERVEDIFF_EXIT_ON_STDIN_CLOSE") == "1" {
		childContext, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = childContext
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
			cancel()
		}()
	}
	assets := webui.Assets()
	if values.webDir != "" {
		absolute, pathError := filepath.Abs(values.webDir)
		if pathError != nil {
			return pathError
		}
		assets = os.DirFS(absolute)
	}
	statePath := values.state
	if statePath == "" {
		statePath, err = reviewstore.DefaultPath(active.ID)
		if err != nil {
			return err
		}
	}
	store, err := reviewstore.Open(mapStatePath(statePath))
	if err != nil {
		return err
	}
	reviews := reviewservice.New(active, store)
	rest := httpapi.New(active, store, reviews, assets)
	mcpHandler := mcpapi.New(reviews, active.Capabilities.Review.Comments.Enabled(), buildversion.String())
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/", rest)
	handler := identifyProcess(mux)
	bound, err := bind(values, stdin, stdout)
	if err != nil {
		if errors.Is(err, errStartupCancelled) {
			return nil
		}
		return err
	}
	defer bound.listener.Close()
	writeStartup(stdout, input, bound.url)
	if !values.noBrowser {
		if browserError := browser.Open(bound.browserURL); browserError != nil {
			fmt.Fprintf(stderr, "  servediff: %v\n", browserError)
		}
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(bound.listener) }()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case serverError := <-serverErrors:
		if errors.Is(serverError, http.ErrServerClosed) {
			return nil
		}
		return serverError
	}
}

func mapStatePath(value string) string {
	if value == "memory" {
		return ""
	}
	return value
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "  servediff: %v\n", err)
		os.Exit(1)
	}
}
