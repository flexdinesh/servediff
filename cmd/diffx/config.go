package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/flexdinesh/diffx/internal/config"
)

func runConfig(arguments []string, writer io.Writer) error {
	return runConfigInput(arguments, nil, writer)
}
func runConfigInput(arguments []string, input io.Reader, writer io.Writer) error {
	flags := flag.NewFlagSet("diffx config", flag.ContinueOnError)
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
		return errors.New("usage: diffx config {set KEY VALUE|get KEY|remove KEY}")
	}
	action, key := arguments[0], arguments[1]
	switch action {
	case "set":
		if len(arguments) != 3 {
			return errors.New("config set requires a key and value")
		}
		value := arguments[2]
		if key == "token" && value == "-" {
			if input == nil {
				return errors.New("token input unavailable")
			}
			raw, err := io.ReadAll(io.LimitReader(input, 4097))
			if err != nil {
				return err
			}
			if len(raw) > 4096 {
				return errors.New("token exceeds 4096 bytes")
			}
			value = strings.TrimSpace(string(raw))
			if value == "" {
				return errors.New("token cannot be empty")
			}
		}
		if err := config.EditFile(*path, key, &value); err != nil {
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
		fields := map[string]interface{}{"host": values.Host, "port": values.Port, "state": values.State, "webDir": values.WebDir, "server": values.Server, "token": values.Token, "retentionDays": values.RetentionDays}
		value, ok := fields[key]
		if !ok {
			return fmt.Errorf("unknown config key %q", key)
		}
		if key == "token" && values.Token != "" {
			value = "[redacted]"
		}
		return json.NewEncoder(writer).Encode(value)
	default:
		return fmt.Errorf("unknown config action %q", action)
	}
	fmt.Fprintln(writer, "  config saved")
	return nil
}
