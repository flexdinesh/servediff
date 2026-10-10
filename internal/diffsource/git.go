package diffsource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/review"
)

var errOutputLimit = errors.New("git output exceeds limit")

var ErrNotRepository = errors.New("not a Git working tree")

var patchFormat = []string{
	"--no-color",
	"--src-prefix=a/",
	"--dst-prefix=b/",
	"--output-indicator-new=+",
	"--output-indicator-old=-",
	"--output-indicator-context= ",
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	maximum  int
	exceeded bool
}

func (buffer *limitedBuffer) String() string { return buffer.buffer.String() }

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining < len(value) {
		if remaining > 0 {
			_, _ = buffer.buffer.Write(value[:remaining])
		}
		buffer.exceeded = true
		return len(value), errOutputLimit
	}
	return buffer.buffer.Write(value)
}

type gitFailure struct {
	err    error
	stdout string
}

func (failure *gitFailure) Error() string { return failure.err.Error() }
func (failure *gitFailure) Unwrap() error { return failure.err }

func runGit(ctx context.Context, root string, maximum int, arguments ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, err := gitProcesses.acquire(commandContext)
	if err != nil {
		return "", err
	}
	defer release()
	args := append([]string{"--literal-pathspecs", "-c", "core.quotePath=false"}, arguments...)
	command := exec.CommandContext(commandContext, "git", args...)
	command.Dir = root
	command.Env = gitEnvironment(os.Environ())
	output := &limitedBuffer{maximum: maximum}
	errorOutput := &limitedBuffer{maximum: 64 * 1024}
	command.Stdout = output
	command.Stderr = errorOutput
	err = runGitCommand(commandContext, command)
	exceeded := output.exceeded || errorOutput.exceeded
	if exceeded {
		err = errors.Join(errOutputLimit, err)
	}
	if commandContext.Err() != nil {
		return output.String(), errors.Join(commandContext.Err(), err)
	}
	if exceeded {
		return output.String(), err
	}
	if err != nil {
		// Only an ordinary Git exit can mean an absent revision or file.
		// Pipe, launch, or cleanup failures must not trigger those fallbacks.
		if !isGitExitFailure(err) {
			return output.String(), err
		}
		if detail := strings.TrimSpace(errorOutput.String()); detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
		return output.String(), &gitFailure{err: err, stdout: output.String()}
	}
	return output.String(), nil
}

func isGitExitFailure(err error) bool {
	switch failure := err.(type) {
	case *exec.ExitError:
		return true
	case interface{ Unwrap() []error }:
		causes := failure.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isGitExitFailure(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return isGitExitFailure(failure.Unwrap())
	default:
		return false
	}
}

func gitDiffArgs(mode review.DiffMode, base string) []string {
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--find-renames"}
	args = append(args, patchFormat...)
	if mode == review.DiffStaged {
		args = append(args, "--cached", base)
	} else if mode == review.DiffAll {
		args = append(args, base)
	}
	return args
}

func parseRaw(raw string) (map[string]*review.ChangedFile, error) {
	entries := strings.Split(raw, "\x00")
	files := make(map[string]*review.ChangedFile)
	for index := 0; index < len(entries); index++ {
		header := entries[index]
		if !strings.HasPrefix(header, ":") {
			continue
		}
		fields := strings.Fields(header)
		status := "M"
		if len(fields) > 0 && len(fields[len(fields)-1]) > 0 {
			status = fields[len(fields)-1][:1]
		}
		index++
		if index >= len(entries) {
			return nil, errors.New("invalid Git file record")
		}
		firstPath := entries[index]
		path := firstPath
		if status == "R" || status == "C" {
			index++
			if index >= len(entries) {
				return nil, errors.New("invalid Git rename record")
			}
			path = entries[index]
		}
		if existing := files[path]; existing != nil && existing.Status == "U" {
			continue
		}
		var oldPath *string
		if firstPath != path {
			value := firstPath
			oldPath = &value
		}
		files[path] = &review.ChangedFile{
			ID: digest(path), Path: path, OldPath: oldPath, Status: status,
			IndexStatus: " ", WorktreeStatus: " ", Fingerprint: header,
		}
	}
	return files, nil
}

func applyStats(files map[string]*review.ChangedFile, output string) {
	entries := strings.Split(output, "\x00")
	for index := 0; index < len(entries); index++ {
		record := entries[index]
		if record == "" {
			continue
		}
		first := strings.IndexByte(record, '\t')
		if first < 0 {
			continue
		}
		secondRelative := strings.IndexByte(record[first+1:], '\t')
		if secondRelative < 0 {
			continue
		}
		second := first + 1 + secondRelative
		additions, deletions := record[:first], record[first+1:second]
		path := record[second+1:]
		if path == "" && index+2 < len(entries) {
			index += 2
			path = entries[index]
		}
		file := files[path]
		if file == nil {
			continue
		}
		file.Binary = additions == "-"
		file.Additions, _ = strconv.Atoi(additions)
		file.Deletions, _ = strconv.Atoi(deletions)
	}
}

type statusPair struct {
	indexStatus    string
	worktreeStatus string
}

func parseStatus(output string) map[string]statusPair {
	renamed := make(map[string]string)
	statuses := make(map[string]statusPair)
	records := strings.Split(output, "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 3 {
			continue
		}
		path := record[3:]
		previous := statuses[path]
		indexStatus, worktreeStatus := record[:1], record[1:2]
		if indexStatus == "?" && previous.indexStatus != "" {
			indexStatus = previous.indexStatus
		}
		if previous.worktreeStatus == "?" {
			worktreeStatus = "?"
		}
		statuses[path] = statusPair{indexStatus: indexStatus, worktreeStatus: worktreeStatus}
		if strings.ContainsAny(indexStatus+worktreeStatus, "RC") && index+1 < len(records) {
			index++
			renamed[records[index]] = path
		}
	}
	for oldPath, path := range renamed {
		status, ok := statuses[path]
		if !ok {
			continue
		}
		if _, exists := statuses[oldPath]; exists {
			continue
		}
		if status.indexStatus == "R" {
			status.indexStatus = "D"
		} else {
			status.indexStatus = " "
		}
		if status.worktreeStatus == "R" {
			status.worktreeStatus = "D"
		} else {
			status.worktreeStatus = " "
		}
		statuses[oldPath] = status
	}
	return statuses
}

type gitSource struct {
	root       string
	comparison *gitComparison
}

func OpenRepository(ctx context.Context, directory string) (Source, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	root, err := runGit(ctx, absolute, 16*1024*1024, "rev-parse", "--show-toplevel")
	if err != nil {
		var failure *gitFailure
		if !errors.As(err, &failure) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, absolute)
	}
	canonical, err := filepath.EvalSymlinks(strings.TrimSuffix(root, "\n"))
	if err != nil {
		return nil, err
	}
	return &gitSource{root: filepath.Clean(canonical)}, nil
}

func RepositoryIdentity(ctx context.Context, root string) (commonDir, worktreeDir string, err error) {
	worktreeDir, err = runGit(ctx, root, 16*1024, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", err
	}
	commonDir, err = runGit(ctx, root, 16*1024, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	worktreeDir = filepath.Clean(strings.TrimSpace(worktreeDir))
	commonDir = strings.TrimSpace(commonDir)
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	if err != nil {
		return "", "", err
	}
	worktreeDir, err = filepath.EvalSymlinks(worktreeDir)
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(commonDir), filepath.Clean(worktreeDir), nil
}

func (source *gitSource) Root() string { return source.root }
func (source *gitSource) Kind() string { return "local" }
func (source *gitSource) Support() Support {
	if source.objectsOnly() {
		return Support{Scopes: []review.DiffMode{review.DiffAll}, FileContents: true}
	}
	return Support{
		Scopes:          []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged},
		Refresh:         true,
		StagingMetadata: true,
		FileContents:    true,
	}
}

func (source *gitSource) headAndBase(ctx context.Context) (*string, string, error) {
	if source.comparison != nil {
		return &source.comparison.HeadOID, source.comparison.HeadOID, nil
	}
	head, err := runGit(ctx, source.root, 16*1024*1024, "rev-parse", "--verify", "HEAD")
	if err == nil {
		head = strings.TrimSpace(head)
		return &head, head, nil
	}
	var failure *gitFailure
	if !errors.As(err, &failure) {
		return nil, "", err
	}
	base, baseError := runGit(ctx, source.root, 16*1024*1024, "hash-object", "-t", "tree", "--stdin")
	return nil, strings.TrimSpace(base), baseError
}

func (source *gitSource) branch(ctx context.Context, head *string) (string, error) {
	branch, err := runGit(ctx, source.root, 16<<10, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(branch), nil
	}
	var failure *gitFailure
	if !errors.As(err, &failure) {
		return "", err
	}
	if head == nil {
		return "detached at HEAD", nil
	}
	return "detached at " + (*head)[:7], nil
}

func (source *gitSource) recreatedContents(ctx context.Context, path string, head string) (review.FilePatch, error) {
	absolute := filepath.Join(source.root, filepath.FromSlash(path))
	stat, err := os.Lstat(absolute)
	if err != nil || (!stat.Mode().IsRegular() && stat.Mode()&os.ModeSymlink == 0) || stat.Size() > MaxFileBytes {
		return review.FilePatch{Message: Message("Recreated file exceeds the preview limit or is not a text file.")}, nil
	}
	before, err := runGit(ctx, source.root, MaxFileBytes, "show", head+":"+path)
	if err != nil {
		var failure *gitFailure
		if !errors.Is(err, errOutputLimit) && !errors.As(err, &failure) {
			return review.FilePatch{}, err
		}
		return review.FilePatch{Message: Message("Original file exceeds the 2 MiB preview limit or changed while loading.")}, nil
	}
	var raw []byte
	if stat.Mode()&os.ModeSymlink != 0 {
		value, readError := os.Readlink(absolute)
		raw, err = []byte(value), readError
	} else {
		raw, err = os.ReadFile(absolute)
	}
	if err != nil || len(raw) > MaxFileBytes {
		return review.FilePatch{Message: Message("Original file exceeds the 2 MiB preview limit or changed while loading.")}, nil
	}
	after := string(raw)
	message := (*string)(nil)
	if !validUTF8(before) || !validUTF8(after) {
		message = Message("Binary file changed. No text preview available.")
	}
	return review.FilePatch{Patch: "", Message: message, Contents: &review.FileContents{Before: before, After: after}}, nil
}

type fingerprintContent struct {
	ID          string  `json:"id"`
	Path        string  `json:"path"`
	OldPath     *string `json:"oldPath"`
	Status      string  `json:"status"`
	Additions   int     `json:"additions"`
	Deletions   int     `json:"deletions"`
	Binary      bool    `json:"binary"`
	Fingerprint string  `json:"fingerprint"`
	Recreated   bool    `json:"recreated,omitempty"`
}

func (source *gitSource) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if source.objectsOnly() && mode != review.DiffAll {
		return review.RepositoryDiff{}, Error(400, "Committed branches have no staged or unstaged scope")
	}
	if err := source.verifyComparison(ctx); err != nil {
		return review.RepositoryDiff{}, err
	}
	head, base, err := source.headAndBase(ctx)
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	args := source.diffArgs(mode, base)
	raw, err := runGit(ctx, source.root, 16*1024*1024, append(args, "--raw", "--no-abbrev", "-z", "--")...)
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	stats, err := runGit(ctx, source.root, 16*1024*1024, append(args, "--numstat", "-z", "--")...)
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	untracked := ""
	if mode != review.DiffStaged && !source.objectsOnly() {
		untracked, err = runGit(ctx, source.root, 16*1024*1024, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return review.RepositoryDiff{}, err
		}
	}
	branch := ""
	if source.comparison != nil {
		branch = source.comparison.branch
	} else {
		branch, err = source.branch(ctx, head)
		if err != nil {
			return review.RepositoryDiff{}, err
		}
	}
	statusOutput := ""
	if !source.objectsOnly() {
		statusOutput, err = runGit(ctx, source.root, 16*1024*1024, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none", "--renames")
		if err != nil {
			return review.RepositoryDiff{}, err
		}
	}
	files, err := parseRaw(raw)
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	applyStats(files, stats)
	headPaths := make(map[string]bool)
	if mode == review.DiffAll && head != nil && untracked != "" {
		paths, pathError := runGit(ctx, source.root, 16*1024*1024, "ls-tree", "-r", "--name-only", "-z", source.comparisonBase(mode, *head))
		if pathError != nil {
			return review.RepositoryDiff{}, pathError
		}
		for _, path := range strings.Split(paths, "\x00") {
			headPaths[path] = true
		}
	}
	for _, path := range strings.Split(untracked, "\x00") {
		if path == "" {
			continue
		}
		if head != nil && headPaths[path] {
			file := files[path]
			if file == nil {
				continue
			}
			preview, previewError := source.recreatedContents(ctx, path, source.comparisonBase(mode, *head))
			if previewError != nil {
				return review.RepositoryDiff{}, previewError
			}
			stat, statError := os.Lstat(filepath.Join(source.root, filepath.FromSlash(path)))
			if statError != nil {
				return review.RepositoryDiff{}, statError
			}
			fileMode := "100644"
			if stat.Mode()&os.ModeSymlink != 0 {
				fileMode = "120000"
			} else if stat.Mode()&0o111 != 0 {
				fileMode = "100755"
			}
			previousMode := strings.TrimPrefix(strings.Fields(file.Fingerprint)[0], ":")
			if preview.Contents != nil && preview.Contents.Before == preview.Contents.After && fileMode == previousMode {
				delete(files, path)
			} else {
				file.Status, file.Additions, file.Deletions, file.Recreated = "M", 0, 0, true
				file.Binary = preview.Message != nil && strings.HasPrefix(*preview.Message, "Binary")
			}
			continue
		}
		if files[path] == nil {
			files[path] = &review.ChangedFile{ID: digest(path), Path: path, Status: "?", IndexStatus: " ", WorktreeStatus: " ", Fingerprint: "untracked"}
		}
	}
	for _, file := range files {
		content := fingerprintContent{ID: file.ID, Path: file.Path, OldPath: file.OldPath, Status: file.Status, Additions: file.Additions, Deletions: file.Deletions, Binary: file.Binary, Fingerprint: file.Fingerprint, Recreated: file.Recreated}
		var size, modified, changed any
		if mode != review.DiffStaged && !source.objectsOnly() {
			path := filepath.Join(source.root, filepath.FromSlash(file.Path))
			if stat, statError := os.Lstat(path); statError == nil {
				size = stat.Size()
				modified = float64(stat.ModTime().UnixNano()) / 1_000_000
				changed, err = changedTime(path, stat)
				if err != nil {
					return review.RepositoryDiff{}, fmt.Errorf("read change time for %q: %w", file.Path, err)
				}
			}
		}
		rawFingerprint, _ := json.Marshal([]any{mode, head, source.comparisonBase(mode, base), content, size, modified, changed})
		file.Fingerprint = digest(string(rawFingerprint))
	}
	statuses := parseStatus(statusOutput)
	result := make([]review.ChangedFile, 0, len(files))
	for _, file := range files {
		status, ok := statuses[file.Path]
		if !ok && file.OldPath != nil {
			status, ok = statuses[*file.OldPath]
		}
		if ok {
			file.IndexStatus, file.WorktreeStatus = status.indexStatus, status.worktreeStatus
		}
		result = append(result, *file)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Path < result[right].Path })
	revisionData, _ := json.Marshal([]any{head, branch, source.comparisonBase(mode, base), result})
	return review.RepositoryDiff{
		Source: "local", Root: source.root, Name: filepath.Base(source.root), Branch: branch,
		Head: head, Mode: mode, Files: result, Revision: digest(string(revisionData)),
	}, nil
}

func (source *gitSource) Patch(ctx context.Context, mode review.DiffMode, file review.ChangedFile, head *string) (review.FilePatch, error) {
	if file.Recreated && head != nil {
		return source.recreatedContents(ctx, file.Path, source.comparisonBase(mode, *head))
	}
	if file.Binary {
		return review.FilePatch{Message: Message("Binary file changed. No text preview available.")}, nil
	}
	if file.Status == "U" {
		return review.FilePatch{Message: Message("Unresolved merge conflict. Resolve this file in your editor; the viewer is read-only.")}, nil
	}
	if source.objectsOnly() {
		submodule, err := source.objectSubmodule(ctx, file)
		if err != nil {
			return review.FilePatch{}, err
		}
		if submodule {
			return review.FilePatch{Message: Message("Submodule or directory changed. Open its repository to review the contents.")}, nil
		}
	}
	if mode != review.DiffStaged && !source.objectsOnly() {
		if stat, err := os.Lstat(filepath.Join(source.root, filepath.FromSlash(file.Path))); err == nil {
			if stat.IsDir() {
				return review.FilePatch{Message: Message("Submodule or directory changed. Open its repository to review the contents.")}, nil
			}
			if !stat.Mode().IsRegular() && stat.Mode()&os.ModeSymlink == 0 {
				return review.FilePatch{Message: Message("Special file. No text preview available.")}, nil
			}
			if stat.Size() > MaxFileBytes {
				return review.FilePatch{Message: Message("File exceeds the 2 MiB preview limit.")}, nil
			}
		}
	}
	_, base, err := source.headAndBase(ctx)
	if err != nil {
		return review.FilePatch{}, err
	}
	var args []string
	if file.Status == "?" {
		args = []string{"diff", "--no-index", "--no-ext-diff", "--no-textconv"}
		args = append(args, patchFormat...)
		args = append(args, "--", "/dev/null", file.Path)
	} else {
		args = append(source.diffArgs(mode, base), "--patch", "--unified=5", "--")
		if file.OldPath != nil {
			args = append(args, *file.OldPath)
		}
		args = append(args, file.Path)
	}
	output, commandError := runGit(ctx, source.root, MaxFileBytes, args...)
	if commandError != nil {
		var failure *gitFailure
		if file.Status == "?" && errors.As(commandError, &failure) {
			var exit *exec.ExitError
			if errors.As(failure.err, &exit) && exit.ExitCode() == 1 {
				return review.FilePatch{Patch: failure.stdout}, nil
			}
		}
		if errors.Is(commandError, errOutputLimit) {
			return review.FilePatch{Message: Message("Diff exceeds the 2 MiB preview limit.")}, nil
		}
		if !errors.As(commandError, &failure) {
			return review.FilePatch{}, commandError
		}
		return review.FilePatch{}, Error(409, "File changed while loading. Refresh to try again.")
	}
	if output == "" {
		return review.FilePatch{Message: Message("No text changes (file mode or metadata changed).")}, nil
	}
	return review.FilePatch{Patch: output}, nil
}

func (source *gitSource) Contents(ctx context.Context, mode review.DiffMode, file review.ChangedFile, head *string) (review.FileContents, error) {
	if file.Binary || file.Status == "U" {
		return review.FileContents{}, Error(400, "Full text is unavailable for this file")
	}
	previousPath := file.Path
	if file.OldPath != nil {
		previousPath = *file.OldPath
	}
	readBlob := func(revision, path string, missing bool) (string, error) {
		output, err := runGit(ctx, source.root, MaxFileBytes, "show", revision+":"+path)
		var failure *gitFailure
		if err != nil && missing && errors.As(err, &failure) {
			return "", nil
		}
		return output, err
	}
	beforeRevision := ""
	if mode != review.DiffUnstaged && head != nil {
		beforeRevision = source.comparisonBase(mode, *head)
	}
	before, err := readBlob(beforeRevision, previousPath, beforeRevision == "" || file.Status == "A" || file.Status == "?")
	if err != nil {
		return review.FileContents{}, err
	}
	after := ""
	if source.objectsOnly() {
		after, err = readBlob(source.comparison.HeadOID, file.Path, file.Status == "D")
	} else if mode == review.DiffStaged {
		after, err = readBlob("", file.Path, file.Status == "D")
	} else if file.Status != "D" {
		path := filepath.Join(source.root, filepath.FromSlash(file.Path))
		stat, statError := os.Lstat(path)
		if statError != nil {
			return review.FileContents{}, statError
		}
		if stat.Size() > MaxFileBytes {
			return review.FileContents{}, Error(413, "File exceeds the 2 MiB preview limit")
		}
		if stat.Mode()&os.ModeSymlink != 0 {
			after, err = os.Readlink(path)
		} else {
			var raw []byte
			raw, err = os.ReadFile(path)
			after = string(raw)
		}
	}
	if err != nil {
		return review.FileContents{}, err
	}
	if !validUTF8(before) || !validUTF8(after) {
		return review.FileContents{}, Error(400, "Full text is unavailable for binary files")
	}
	return review.FileContents{Before: before, After: after}, nil
}
