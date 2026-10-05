package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/flexdinesh/servediff/internal/config"
	"github.com/flexdinesh/servediff/internal/remoteserver"
	"github.com/flexdinesh/servediff/internal/version"
)

func parseSettings(arguments []string, stderr io.Writer) (remoteserver.Settings, bool, error) {
	settings := remoteserver.Settings{Token: os.Getenv("SERVEDIFF_TOKEN")}
	flags := flag.NewFlagSet("servediff-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&settings.Listen, "listen", "0.0.0.0:7981", "HTTP listen address; terminate TLS at the deployment boundary")
	flags.StringVar(&settings.State, "state", "", "SQLite database path")
	flags.StringVar(&settings.Account, "account", "", "authenticated account name")
	path := flags.String("config-file", "", "machine JSON config path")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(arguments); err != nil {
		return settings, false, err
	}
	if *showVersion {
		return settings, true, nil
	}
	if flags.NArg() != 0 {
		return settings, false, fmt.Errorf("servediff-server takes no positional arguments")
	}
	values, err := config.LoadFile(*path)
	if err != nil {
		return settings, false, err
	}
	resolved, err := values.Settings()
	if err != nil {
		return settings, false, err
	}
	listenSet, stateSet := false, false
	flags.Visit(func(value *flag.Flag) {
		listenSet = listenSet || value.Name == "listen"
		stateSet = stateSet || value.Name == "state"
	})
	_, hostEnv := os.LookupEnv("SERVEDIFF_HOST")
	_, portEnv := os.LookupEnv("SERVEDIFF_PORT")
	if !listenSet && (*path != "" || os.Getenv("SERVEDIFF_CONFIG_PATH") != "" || hostEnv || portEnv) {
		port := resolved.Port
		if port < 0 {
			port = 7981
		}
		settings.Listen = net.JoinHostPort(resolved.Host, strconv.Itoa(port))
	}
	if !stateSet || settings.State == "" {
		settings.State = resolved.State
	}
	return settings, false, nil
}

func main() {
	settings, showVersion, err := parseSettings(os.Args[1:], os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if showVersion {
		fmt.Println(version.String())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := remoteserver.Run(ctx, settings, func(address string) { fmt.Printf("servediff-server listening on %s\n", address) }); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
