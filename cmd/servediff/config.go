package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/flexdinesh/servediff/internal/config"
)

func runConfig(arguments []string, writer io.Writer) error {
	flags := flag.NewFlagSet("servediff service config", flag.ContinueOnError)
	flags.SetOutput(writer)
	path := flags.String("config-file", "", "machine JSON config path")
	if err := flags.Parse(normalizeArguments(arguments)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	arguments = flags.Args()
	if len(arguments) < 2 {
		return errors.New("usage: servediff service config {set KEY VALUE|get KEY|remove KEY}")
	}
	action, key := arguments[0], arguments[1]
	switch action {
	case "set":
		if len(arguments) != 3 {
			return errors.New("config set requires a key and value")
		}
		if err := config.EditFile(*path, key, &arguments[2]); err != nil {
			return err
		}
	case "remove":
		if len(arguments) != 2 {
			return errors.New("config remove requires a key")
		}
		if err := config.EditFile(*path, key, nil); err != nil {
			return err
		}
	case "get":
		if len(arguments) != 2 {
			return errors.New("config get requires a key")
		}
		values, err := config.ReadFile(*path)
		if err != nil {
			return err
		}
		fields := map[string]interface{}{"host": values.Host, "port": values.Port, "state": values.State, "webDir": values.WebDir}
		value, ok := fields[key]
		if !ok {
			return fmt.Errorf("unknown config key %q", key)
		}
		return json.NewEncoder(writer).Encode(value)
	default:
		return fmt.Errorf("unknown config action %q", action)
	}
	fmt.Fprintln(writer, "  config saved; applies on next service start/restart")
	return nil
}
