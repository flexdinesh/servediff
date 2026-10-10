package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/flexdinesh/diffx/internal/config"
	"github.com/flexdinesh/diffx/internal/remoteserver"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/version"
)

func parseSettings(arguments []string, stderr io.Writer) (remoteserver.Settings, bool, error) {
	settings := remoteserver.Settings{Token: os.Getenv("DIFFX_TOKEN")}
	flags := flag.NewFlagSet("diffx-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&settings.Listen, "listen", "127.0.0.1:7981", "HTTP listen address; terminate TLS at the deployment boundary")
	flags.StringVar(&settings.State, "state", "", "SQLite database path")
	flags.StringVar(&settings.Account, "account", "admin", "bootstrap account name; persisted credentials are never replaced")
	flags.IntVar(&settings.RetentionDays, "retention-days", 7, "snapshot retention in days")
	path := flags.String("config-file", "", "optional read-only server JSON config path")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(arguments); err != nil {
		return settings, false, err
	}
	if *showVersion {
		return settings, true, nil
	}
	if flags.NArg() != 0 {
		return settings, false, fmt.Errorf("diffx-server takes no positional arguments")
	}
	values, err := config.LoadRemoteFile(*path)
	if err != nil {
		return settings, false, err
	}
	listenSet, stateSet, accountSet, retentionSet := false, false, false, false
	flags.Visit(func(value *flag.Flag) {
		listenSet = listenSet || value.Name == "listen"
		stateSet = stateSet || value.Name == "state"
		accountSet = accountSet || value.Name == "account"
		retentionSet = retentionSet || value.Name == "retention-days"
	})
	if !listenSet {
		settings.Listen, err = values.ListenAddress()
		if err != nil {
			return settings, false, err
		}
	}
	if !stateSet || settings.State == "" {
		settings.State = values.State
	}
	if !accountSet {
		settings.Account = values.Account
	}
	if !retentionSet {
		settings.RetentionDays = values.RetentionDays
	}
	if settings.RetentionDays <= 0 {
		return settings, false, fmt.Errorf("retention days must be positive")
	}
	if settings.State == "" {
		return settings, false, fmt.Errorf("state must be a database path or memory")
	}
	if strings.TrimSpace(settings.Account) == "" || len(settings.Account) > 256 || strings.ContainsAny(settings.Account, "\r\n:") {
		return settings, false, fmt.Errorf("invalid bootstrap account name")
	}
	if _, err := remoteserver.Retention(settings.RetentionDays); err != nil {
		return settings, false, err
	}
	return settings, false, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "user" {
		if err := createUser(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
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
	settings.BootstrapReady = func(path string) { fmt.Fprintf(os.Stderr, "Admin credential saved to %q\n", path) }
	if err := remoteserver.Run(ctx, settings, func(address string) { fmt.Printf("diffx-server listening on %s\n", address) }); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Provisioning opens the same exclusive state lock as the server. Stop the
// server before creating an account, then restart to activate its handler.
func createUser(arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "create" {
		return fmt.Errorf("usage: diffx-server user create --name NAME [--state PATH] [--config-file PATH]")
	}
	flags := flag.NewFlagSet("diffx-server user create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "new account name")
	state := flags.String("state", "", "SQLite database path; server must be stopped")
	configPath := flags.String("config-file", "", "optional read-only server JSON config path")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*name) == "" {
		return fmt.Errorf("user create requires --name NAME and no positional arguments")
	}
	settings, _, err := parseSettings([]string{"--state", *state, "--config-file", *configPath}, stderr)
	if err != nil {
		return err
	}
	if settings.State == ":memory:" || settings.State == "memory" {
		return fmt.Errorf("user create requires a persistent state database")
	}
	store, err := reviewstore.Open(settings.State)
	if err != nil {
		return err
	}
	defer store.Close()
	token, err := remoteserver.GenerateToken()
	if err != nil {
		return err
	}
	if _, err := store.ProvisionUser(*name, token); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Created user %s\nBearer token: %s\n", *name, token)
	return err
}
