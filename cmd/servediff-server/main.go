package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/flexdinesh/servediff/internal/remoteserver"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/version"
)

func main() {
	settings := remoteserver.Settings{Token: os.Getenv("SERVEDIFF_TOKEN")}
	flags := flag.NewFlagSet("servediff-server", flag.ExitOnError)
	flags.StringVar(&settings.Listen, "listen", "0.0.0.0:7981", "HTTP listen address; terminate TLS at the deployment boundary")
	flags.StringVar(&settings.State, "state", "", "SQLite database path")
	flags.StringVar(&settings.Account, "account", "", "authenticated account name")
	showVersion := flags.Bool("version", false, "print version and exit")
	_ = flags.Parse(os.Args[1:])
	if *showVersion {
		fmt.Println(version.String())
		return
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "servediff-server takes no positional arguments")
		os.Exit(1)
	}
	if settings.State == "" {
		path, err := reviewstore.DefaultPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		settings.State = path
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := remoteserver.Run(ctx, settings, func(address string) { fmt.Printf("servediff-server listening on %s\n", address) }); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
