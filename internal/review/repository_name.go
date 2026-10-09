package review

import (
	"net/url"
	"path"
	"strings"
)

func RemoteRepositoryName(remote, fallback string) string {
	if remote == "" {
		return fallback
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" && (parsed.Host != "" || parsed.Scheme == "file") {
		remote = parsed.Path
	} else if _, suffix, ok := strings.Cut(remote, ":"); ok {
		remote = suffix
	}
	name := strings.TrimSuffix(path.Base(strings.TrimRight(remote, "/")), ".git")
	if name == "" || name == "." || name == "/" {
		return fallback
	}
	return name
}
