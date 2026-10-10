package main

import "github.com/flexdinesh/diffx/internal/config"

func resolvedCollectorSettings(values options) (options, error) {
	saved, err := config.LoadFile(values.configFile)
	if err != nil {
		return values, err
	}
	if !values.serverSet {
		values.server = saved.Server
	}
	if !values.tokenSet {
		values.token = saved.Token
	}
	return values, nil
}
