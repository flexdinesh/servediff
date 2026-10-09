package diffsource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/flexdinesh/servediff/internal/review"
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
		name = RemoteRepositoryName(strings.TrimSpace(remote), name)
	}
	return name, worktrees, nil
}

type WorktreeChangeSummary struct {
	LastChangedAt    int64
	ChangedFileCount int
}

// WorktreeChanges reads local change counts and times without loading diffs.
func WorktreeChanges(ctx context.Context, root, gitDir string) (WorktreeChangeSummary, error) {
	status, err := runGit(ctx, root, 16*1024*1024, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none", "--renames")
	if err != nil {
		return WorktreeChangeSummary{}, err
	}
	var latest int64
	commit, err := runGit(ctx, root, 1024, "log", "-1", "--format=%ct")
	if err == nil {
		seconds, parseErr := strconv.ParseInt(strings.TrimSpace(commit), 10, 64)
		if parseErr != nil {
			return WorktreeChangeSummary{}, parseErr
		}
		latest = seconds * 1000
	} else {
		var failure *gitFailure
		if !errors.As(err, &failure) {
			return WorktreeChangeSummary{}, err
		}
	}
	staged := false
	for name, state := range parseStatus(status) {
		if err := ctx.Err(); err != nil {
			return WorktreeChangeSummary{}, err
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
				return WorktreeChangeSummary{}, err
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
			return WorktreeChangeSummary{}, err
		}
	}
	return WorktreeChangeSummary{LastChangedAt: latest, ChangedFileCount: changedFileCount(status)}, nil
}

func changedFileCount(status string) int {
	paths := make(map[string]bool)
	records := strings.Split(status, "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 3 {
			continue
		}
		paths[record[3:]] = true
		if strings.ContainsAny(record[:2], "RC") {
			index++ // Renames/copies include a second record containing the old path.
		}
	}
	return len(paths)
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

// RemoteRepositoryName extracts the display name from URL and SCP-style remotes.
func RemoteRepositoryName(remote, fallback string) string {
	return review.RemoteRepositoryName(remote, fallback)
}
