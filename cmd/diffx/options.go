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
	replace          bool
	print            bool
	debug            bool
	retry            bool
	progress         func(string, string)
	host             string
	hostSet          bool
	port             int
	portSet          bool
	directory        string
	repositorySet    bool
	fixture          string
	state            string
	stateSet         bool
	webDir           string
	webDirSet        bool
	noBrowser        bool
	version          bool
	server           string
	serverSet        bool
	token            string
	tokenSet         bool
	trigger          string
	agent            string
	sessionName      string
	retentionDays    int
	retentionDaysSet bool
	runID            string
	sourceID         string
	pathSet          bool
	configFile       string
	base             string
	branch           string
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	return parseOptionsMode(arguments, stderr, false)
}

func parseOptionsMode(arguments []string, stderr io.Writer, allowHost bool) (options, error) {
	flags := flag.NewFlagSet("diffx", flag.ContinueOnError)
	flags.SetOutput(stderr)
	values := options{host: "127.0.0.1", directory: ".", trigger: "manual"}
	flags.StringVar(&values.directory, "path", ".", "checkout path to collect")
	flags.StringVar(&values.server, "server", values.server, "ingestion server URL; required for sync")
	flags.StringVar(&values.token, "token", values.token, "ingestion token; prefer DIFFX_TOKEN")
	flags.StringVar(&values.trigger, "trigger", values.trigger, "collection trigger: manual or agent-hook")
	flags.StringVar(&values.agent, "harness", "", "harness name recorded with the observation")
	flags.StringVar(&values.runID, "run-id", "", "agent or container run identity")
	flags.StringVar(&values.sessionName, "session-name", "", "optional agent session name")
	flags.IntVar(&values.retentionDays, "retention-days", 7, "retention in days; positive, defaults to seven")
	flags.StringVar(&values.sourceID, "source-id", "", "source identity; defaults to persistent local identity")
	flags.StringVar(&values.base, "base", "", "comparison baseline: auto (default), HEAD, or Git ref")
	flags.StringVar(&values.branch, "branch", "", "recover committed branch from Git objects; defaults base to auto")
	if allowHost {
		flags.StringVar(&values.host, "host", values.host, "IP address to bind")
	}
	flags.StringVar(&values.configFile, "config-file", "", "machine JSON config path; defaults to DIFFX_CONFIG_PATH")
	flags.IntVar(&values.port, "port", 0, "HTTP port; defaults to the first available port from 7981 to 7990")
	flags.IntVar(&values.port, "p", 0, "HTTP port; defaults to the first available port from 7981 to 7990")
	flags.StringVar(&values.fixture, "fixture", "", "read a Git patch fixture")
	flags.StringVar(&values.state, "state", "", "state database path; memory disables persistence")
	flags.StringVar(&values.webDir, "web-dir", "", "serve web assets from a directory")
	flags.BoolVar(&values.replace, "replace", false, "replace an existing foreground local instance")
	flags.BoolVar(&values.print, "print", false, "print only the configured server URL after sync")
	flags.BoolVar(&values.debug, "debug", false, "show sync progress on stderr")
	flags.BoolVar(&values.retry, "retry", false, "retry saved sync submissions without collecting")
	flags.BoolVar(&values.noBrowser, "no-browser", false, "do not open a browser")
	flags.BoolVar(&values.version, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "  Usage: diffx [PATH] [--host IP] [--replace]")
		fmt.Fprintln(stderr, "         git diff | diffx [options]")
		fmt.Fprintln(stderr, "         diffx sync [--path DIRECTORY] [--print] [--debug] [--retry]")
		fmt.Fprintln(stderr, "         diffx config {set|get|remove} KEY [VALUE]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(normalizeArguments(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, errHelp
		}
		return options{}, err
	}
	if values.port < 0 || values.port > 65535 {
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
		case "retention-days":
			values.retentionDaysSet = true
		}
	})
	if !values.serverSet {
		values.server = os.Getenv("DIFFX_SERVER_URL")
	}
	if !values.tokenSet {
		values.token = os.Getenv("DIFFX_TOKEN")
	}
	if flags.NArg() == 1 {
		if values.pathSet {
			return options{}, errors.New("path cannot be combined with a positional directory")
		}
		values.directory, values.repositorySet = flags.Arg(0), true
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
		"--config-file": true, "--path": true, "--server": true, "--token": true, "--trigger": true, "--harness": true, "--run-id": true, "--source-id": true,
		"--state": true, "--web-dir": true,
		"--base": true, "--branch": true, "--session-name": true, "--retention-days": true,
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
