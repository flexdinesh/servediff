package diffsource

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Worktree describes a checkout without reading its diff or file contents.
type Worktree struct {
	Root   string
	Branch string
	Linked bool
}

func RepositoryWorktrees(ctx context.Context, root string) (string, []Worktree, error) {
	raw, err := runGit(ctx, root, 1024*1024, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", nil, err
	}
	worktrees := parseWorktrees(raw)
	name := filepath.Base(root)
	if len(worktrees) > 0 {
		name = filepath.Base(worktrees[0].Root)
	}
	if remote, err := runGit(ctx, root, 16*1024, "config", "--get", "remote.origin.url"); err == nil {
		name = remoteRepositoryName(strings.TrimSpace(remote), name)
	}
	return name, worktrees, nil
}

// WorktreeLastChangedAt reads commit and changed-path times without loading diffs.
func WorktreeLastChangedAt(ctx context.Context, root, gitDir string) (int64, error) {
	status, err := runGit(ctx, root, 16*1024*1024, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none", "--no-renames")
	if err != nil {
		return 0, err
	}
	var latest int64
	commit, err := runGit(ctx, root, 1024, "log", "-1", "--format=%ct")
	if err == nil {
		seconds, parseErr := strconv.ParseInt(strings.TrimSpace(commit), 10, 64)
		if parseErr != nil {
			return 0, parseErr
		}
		latest = seconds * 1000
	} else {
		var failure *gitFailure
		if !errors.As(err, &failure) {
			return 0, err
		}
	}
	staged := false
	for name, state := range parseStatus(status) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		staged = staged || (state.indexStatus != " " && state.indexStatus != "?")
		changedPath := filepath.Join(root, filepath.FromSlash(name))
		for {
			info, err := os.Lstat(changedPath)
			if err == nil {
				latest = max(latest, info.ModTime().UnixMilli())
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
			if changedPath == root {
				break
			}
			// Deleted files use the nearest surviving directory's change time.
			changedPath = filepath.Dir(changedPath)
		}
	}
	if staged {
		if info, err := os.Stat(filepath.Join(gitDir, "index")); err == nil {
			latest = max(latest, info.ModTime().UnixMilli())
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	return latest, nil
}

func parseWorktrees(raw string) []Worktree {
	result := make([]Worktree, 0)
	var item Worktree
	bare := false
	first := true
	for _, field := range strings.Split(raw, "\x00") {
		switch {
		case field == "":
			if item.Root != "" {
				if !bare {
					item.Linked = !first
					result = append(result, item)
				}
				first = false
			}
			item, bare = Worktree{}, false
		case strings.HasPrefix(field, "worktree "):
			item.Root = strings.TrimPrefix(field, "worktree ")
		case strings.HasPrefix(field, "HEAD "):
			head := strings.TrimPrefix(field, "HEAD ")
			item.Branch = "Detached · " + head[:min(8, len(head))]
		case strings.HasPrefix(field, "branch "):
			item.Branch = strings.TrimPrefix(strings.TrimPrefix(field, "branch "), "refs/heads/")
		case field == "bare":
			bare = true
		}
	}
	return result
}

func remoteRepositoryName(remote, fallback string) string {
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" && parsed.Host != "" {
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
