package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

var errHelp = errors.New("help requested")

type options struct {
	host          string
	hostSet       bool
	port          int
	portSet       bool
	directory     string
	repositorySet bool
	fixture       string
	capture       string
	state         string
	stateSet      bool
	webDir        string
	webDirSet     bool
	runtimeDir    string
	json          bool
	noBrowser     bool
	version       bool
	server        string
	serverSet     bool
	token         string
	tokenSet      bool
	trigger       string
	agent         string
	runID         string
	sourceID      string
	pathSet       bool
	config        string
	configFile    string
	base          string
	branch        string
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	return parseOptionsMode(arguments, stderr, false)
}

func parseOptionsMode(arguments []string, stderr io.Writer, internal bool, foreground ...bool) (options, error) {
	flags := flag.NewFlagSet("servediff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	values := options{host: "127.0.0.1", directory: ".", trigger: "manual"}
	flags.StringVar(&values.directory, "path", ".", "checkout path to collect")
	flags.StringVar(&values.server, "server", values.server, "ingestion server URL; defaults to local service")
	flags.StringVar(&values.token, "token", values.token, "ingestion token; prefer SERVEDIFF_TOKEN")
	flags.StringVar(&values.trigger, "trigger", values.trigger, "collection trigger: manual or agent-hook")
	flags.StringVar(&values.agent, "agent", "", "agent name recorded with the observation")
	flags.StringVar(&values.runID, "run-id", "", "agent or container run identity")
	flags.StringVar(&values.sourceID, "source-id", "", "source identity; defaults to persistent local identity")
	flags.StringVar(&values.base, "base", "", "comparison baseline: HEAD (default), auto, or Git ref")
	flags.StringVar(&values.branch, "branch", "", "recover committed branch from Git objects; defaults base to auto")
	if internal || (len(foreground) > 0 && foreground[0]) {
		flags.StringVar(&values.host, "host", values.host, "IP address to bind (foreground/internal only)")
	}
	flags.StringVar(&values.config, "config", "", "JSON settings override for service start/restart")
	flags.StringVar(&values.configFile, "config-file", "", "machine JSON config path; defaults to SERVEDIFF_CONFIG_PATH")
	flags.IntVar(&values.port, "port", 0, "HTTP port; defaults to the first available port from 7981 to 7990")
	flags.IntVar(&values.port, "p", 0, "HTTP port; defaults to the first available port from 7981 to 7990")
	flags.StringVar(&values.fixture, "fixture", "", "read a Git patch fixture")
	flags.StringVar(&values.capture, "capture", "", "reopen a retained capture by ID")
	flags.StringVar(&values.state, "state", "", "state database path; memory disables persistence")
	flags.StringVar(&values.webDir, "web-dir", "", "serve web assets from a directory")
	if internal {
		flags.StringVar(&values.runtimeDir, "runtime-dir", "", "internal daemon runtime directory")
	}
	flags.BoolVar(&values.json, "json", false, "print service status as JSON")
	flags.BoolVar(&values.noBrowser, "no-browser", false, "do not open a browser")
	flags.BoolVar(&values.version, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "  Usage: servediff review [--path DIRECTORY] [options]")
		fmt.Fprintln(stderr, "         servediff pipe [--path DIRECTORY] [options]")
		fmt.Fprintln(stderr, "         servediff service {start|stop|restart|status} [options]")
		fmt.Fprintln(stderr, "         servediff service config {set KEY VALUE|get KEY|remove KEY}")
		fmt.Fprintln(stderr, "         servediff serve [directory | - | --fixture FILE] [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(normalizeArguments(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, errHelp
		}
		return options{}, err
	}
	minimumPort := 0
	if internal {
		minimumPort = -1
	}
	if values.port < minimumPort || values.port > 65535 {
		return options{}, errors.New("port must be between 0 and 65535")
	}
	host := net.ParseIP(values.host)
	if host == nil {
		return options{}, errors.New("host must be an IP address")
	}
	values.host = host.String()
	if flags.NArg() > 1 {
		return options{}, errors.New("expected at most one repository directory")
	}
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "path":
			values.pathSet, values.repositorySet = true, true
		case "server":
			values.serverSet = true
		case "token":
			values.tokenSet = true
		}
	})
	if !values.serverSet {
		values.server = os.Getenv("SERVEDIFF_SERVER_URL")
	}
	if !values.tokenSet {
		values.token = os.Getenv("SERVEDIFF_TOKEN")
	}
	if flags.NArg() == 1 {
		if values.pathSet {
			return options{}, errors.New("path cannot be combined with a positional directory")
		}
		values.directory, values.repositorySet = flags.Arg(0), true
	}
	if values.capture != "" && (values.repositorySet || values.fixture != "") {
		return options{}, errors.New("capture cannot be combined with a directory or fixture")
	}
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "host" {
			values.hostSet = true
		}
		if value.Name == "state" {
			values.stateSet = true
		}
		if value.Name == "web-dir" {
			values.webDirSet = true
		}
		if value.Name == "port" || value.Name == "p" {
			values.portSet = true
		}
	})
	return values, nil
}

func normalizeArguments(arguments []string) []string {
	valueOptions := map[string]bool{
		"-p": true, "--port": true, "--host": true, "--fixture": true,
		"--config": true, "--config-file": true, "--path": true, "--server": true, "--token": true, "--trigger": true, "--agent": true, "--run-id": true, "--source-id": true,
		"--state": true, "--web-dir": true, "--capture": true, "--runtime-dir": true,
		"--base": true, "--branch": true,
	}
	options, positionals := make([]string, 0, len(arguments)), make([]string, 0, 1)
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			options = append(options, "--")
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
