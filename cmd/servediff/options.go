package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
)

var errHelp = errors.New("help requested")

type options struct {
	host          string
	port          int
	portSet       bool
	directory     string
	repositorySet bool
	fixture       string
	state         string
	webDir        string
	noBrowser     bool
	version       bool
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("servediff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	values := options{host: "127.0.0.1", directory: "."}
	flags.StringVar(&values.host, "host", values.host, "IP address to bind")
	flags.IntVar(&values.port, "port", 0, "HTTP port; defaults to the first available port from 7981 to 7990")
	flags.IntVar(&values.port, "p", 0, "HTTP port; defaults to the first available port from 7981 to 7990")
	flags.StringVar(&values.fixture, "fixture", "", "read a Git patch fixture")
	flags.StringVar(&values.state, "state", "", "review state path; memory disables persistence")
	flags.StringVar(&values.webDir, "web-dir", "", "serve web assets from a directory")
	flags.BoolVar(&values.noBrowser, "no-browser", false, "do not open a browser")
	flags.BoolVar(&values.version, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "  Usage: servediff [directory | -] [options]")
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
	if flags.NArg() == 1 {
		values.directory = flags.Arg(0)
		values.repositorySet = true
	}
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "port" || value.Name == "p" {
			values.portSet = true
		}
	})
	return values, nil
}

func normalizeArguments(arguments []string) []string {
	valueOptions := map[string]bool{
		"-p": true, "--port": true, "--host": true, "--fixture": true,
		"--state": true, "--web-dir": true,
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
