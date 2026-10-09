// Package controlapi implements the private, authenticated CLI-to-daemon API.
package controlapi

import "github.com/flexdinesh/servediff/internal/config"

const ProtocolVersion = 5

type Settings = config.ServerSettings

type Status struct {
	State           string   `json:"state"`
	InstanceID      string   `json:"instanceId"`
	StateID         string   `json:"stateId"`
	PID             int      `json:"pid"`
	Version         string   `json:"version"`
	ProtocolVersion int      `json:"protocolVersion"`
	URL             string   `json:"url"`
	BrowserURL      string   `json:"browserUrl"`
	Settings        Settings `json:"settings"`
	Observations    int      `json:"observations"`
}

// Problem is a daemon rejection, distinct from an interrupted transport.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail"`
}

func (problem *Problem) Error() string { return problem.Detail }
