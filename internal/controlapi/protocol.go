// Package controlapi implements the private, authenticated CLI-to-daemon API.
package controlapi

import "github.com/flexdinesh/servediff/internal/diffsource"

const ProtocolVersion = 1
const MaxPatchBytes = diffsource.MaxInputBytes

const submissionHeader = "X-Servediff-Submission"
const submittedFromHeader = "X-Servediff-Submitted-From"

type Settings struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	State  string `json:"state"`
	WebDir string `json:"webDir"`
}

type Status struct {
	State           string   `json:"state"`
	InstanceID      string   `json:"instanceId"`
	PID             int      `json:"pid"`
	Version         string   `json:"version"`
	ProtocolVersion int      `json:"protocolVersion"`
	URL             string   `json:"url"`
	BrowserURL      string   `json:"browserUrl"`
	Settings        Settings `json:"settings"`
	Worktrees       int      `json:"worktrees"`
	Captures        int      `json:"captures"`
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
