package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/flexdinesh/servediff/internal/browser"
	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/httpapi"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/webui"
)

type options struct {
	port      int
	fixture   string
	state     string
	webDir    string
	token     string
	noAuth    bool
	noBrowser bool
}

func run(ctx context.Context, arguments []string, stdin *os.File, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("servediff-go", flag.ContinueOnError)
	flags.SetOutput(stderr)
	values := options{}
	flags.IntVar(&values.port, "port", 3333, "HTTP port; 0 chooses a free port")
	flags.IntVar(&values.port, "p", 3333, "HTTP port; 0 chooses a free port")
	flags.StringVar(&values.fixture, "fixture", "", "read a Git patch fixture")
	flags.StringVar(&values.state, "state", "", "review state path; memory disables persistence")
	flags.StringVar(&values.webDir, "web-dir", "", "serve web assets from a directory")
	flags.StringVar(&values.token, "token", "", "fixed API token")
	flags.BoolVar(&values.noAuth, "no-auth", false, "disable API authentication")
	flags.BoolVar(&values.noBrowser, "no-browser", false, "do not open a browser")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: servediff-go [directory | -] [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(normalizeArguments(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if values.port < 0 || values.port > 65535 {
		return errors.New("port must be between 0 and 65535")
	}
	if flags.NArg() > 1 {
		return errors.New("expected at most one repository directory")
	}
	directory := "."
	if flags.NArg() == 1 {
		directory = flags.Arg(0)
	}
	var source diffsource.Source
	var err error
	if values.fixture != "" {
		raw, readError := os.ReadFile(values.fixture)
		if readError != nil {
			return readError
		}
		source, err = diffsource.OpenPatch(string(raw))
	} else {
		piped, statError := redirected(stdin)
		if statError != nil {
			return statError
		}
		if flags.Arg(0) == "-" || piped {
			raw, readError := io.ReadAll(io.LimitReader(stdin, diffsource.MaxInputBytes+1))
			if readError != nil {
				return readError
			}
			if len(raw) > diffsource.MaxInputBytes {
				return errors.New("piped diff exceeds the 16 MiB input limit")
			}
			source, err = diffsource.OpenPatch(string(raw))
		} else {
			if flags.NArg() == 0 {
				return errors.New("provide a repository path, a fixture, or pipe a Git diff")
			}
			source, err = diffsource.OpenRepository(ctx, directory)
		}
	}
	if err != nil {
		return err
	}
	token := values.token
	if values.noAuth {
		token = ""
	} else if token == "" {
		token, err = httpapi.RandomToken()
		if err != nil {
			return err
		}
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
		statePath, err = reviewstore.DefaultPath(httpapi.SessionID(source))
		if err != nil {
			return err
		}
	}
	store, err := reviewstore.Open(mapStatePath(statePath))
	if err != nil {
		return err
	}
	handler := httpapi.New(source, store, token, assets)
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", values.port))
	if err != nil {
		return err
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	local := fmt.Sprintf("http://localhost:%d", port)
	if token != "" {
		local += "#token=" + token
	}
	network := networkAddress()
	all := fmt.Sprintf("http://0.0.0.0:%d", port)
	if token != "" {
		all += "#token=" + token
	}
	networkURL := "unavailable"
	if network != "" {
		networkURL = fmt.Sprintf("http://%s:%d", network, port)
		if token != "" {
			networkURL += "#token=" + token
		}
	}
	fmt.Fprintf(stdout, "\n  servediff (Go)\n  Local    %s\n  All      %s\n  Network  %s\n  API token  %s\n  %s\n\n  Press Ctrl+C to stop.\n\n", local, all, networkURL, tokenLabel(token), source.Root())
	if !values.noBrowser {
		if browserError := browser.Open(local); browserError != nil {
			fmt.Fprintf(stderr, "servediff-go: %v\n", browserError)
		}
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
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

func normalizeArguments(arguments []string) []string {
	valueOptions := map[string]bool{
		"-p": true, "--port": true, "--fixture": true, "--state": true,
		"--web-dir": true, "--token": true,
	}
	options, positionals := make([]string, 0, len(arguments)), make([]string, 0, 1)
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			positionals = append(positionals, arguments[index+1:]...)
			break
		}
		if argument != "-" && strings.HasPrefix(argument, "-") {
			options = append(options, argument)
			name := strings.SplitN(argument, "=", 2)[0]
			if valueOptions[name] && !strings.Contains(argument, "=") && index+1 < len(arguments) {
				index++
				options = append(options, arguments[index])
			}
			continue
		}
		positionals = append(positionals, argument)
	}
	return append(options, positionals...)
}

func mapStatePath(value string) string {
	if value == "memory" {
		return ""
	}
	return value
}

func redirected(stdin *os.File) (bool, error) {
	stat, err := stdin.Stat()
	if err != nil {
		return false, err
	}
	return stat.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0 || stat.Mode().IsRegular(), nil
}

func networkAddress() string {
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}

func tokenLabel(token string) string {
	if token == "" {
		return "disabled"
	}
	return token
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "servediff-go: %v\n", err)
		os.Exit(1)
	}
}
