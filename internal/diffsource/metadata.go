package diffsource

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"strings"
)

// RepositoryMetadata contains checkout facts without collecting its diff.
type RepositoryMetadata struct {
	CommonDir string
	GitDir    string
	RemoteURL string
}

func Metadata(ctx context.Context, root string) (RepositoryMetadata, error) {
	common, gitDir, err := RepositoryIdentity(ctx, root)
	if err != nil {
		return RepositoryMetadata{}, err
	}
	remote, err := runGit(ctx, root, 16*1024, "config", "--get", "remote.origin.url")
	if err != nil {
		var failure *gitFailure
		var exit *exec.ExitError
		if !errors.As(err, &failure) || !errors.As(failure.err, &exit) || exit.ExitCode() != 1 {
			return RepositoryMetadata{}, err
		}
		remote = ""
	}
	return RepositoryMetadata{CommonDir: common, GitDir: gitDir, RemoteURL: sanitizeRemote(strings.TrimSpace(remote))}, nil
}

func sanitizeRemote(remote string) string {
	parsed, err := url.Parse(remote)
	if err == nil && parsed.Scheme != "" && (parsed.Host != "" || parsed.Scheme == "file") {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	// SCP-style remotes have no password field, but omit the login label too.
	if at := strings.IndexByte(remote, '@'); at >= 0 {
		remote = remote[at+1:]
	}
	return remote
}

// PreviewUnavailable distinguishes intentional preview limits from failures
// requiring collection to stop (including cancellation and concurrent edits).
func PreviewUnavailable(err error) bool {
	var request *RequestError
	return errors.Is(err, errOutputLimit) || errors.As(err, &request) && (request.Status == 400 || request.Status == 413)
}
