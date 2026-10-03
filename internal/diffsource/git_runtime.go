package diffsource

import (
	"context"
	"strings"
)

// Admission bounds both running processes and callers waiting for a process.
type gitProcessLimit struct {
	active   chan struct{}
	admitted chan struct{}
}

func newGitProcessLimit(active, waiting int) *gitProcessLimit {
	return &gitProcessLimit{active: make(chan struct{}, active), admitted: make(chan struct{}, active+waiting)}
}

var gitProcesses = newGitProcessLimit(4, 16)

func (limit *gitProcessLimit) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case limit.admitted <- struct{}{}:
	default:
		return nil, Error(503, "Git process queue is full. Try again shortly.")
	}
	select {
	case limit.active <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-limit.active
			<-limit.admitted
			return nil, err
		}
		return func() { <-limit.active; <-limit.admitted }, nil
	case <-ctx.Done():
		<-limit.admitted
		return nil, ctx.Err()
	}
}

func gitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CEILING_DIRECTORIES",
			"GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_PREFIX", "GIT_IMPLICIT_WORK_TREE", "GIT_SUPER_PREFIX",
			"GIT_SHALLOW_FILE", "GIT_GRAFT_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_OPTIONAL_LOCKS", "GIT_TERMINAL_PROMPT":
			continue
		}
		if strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}
