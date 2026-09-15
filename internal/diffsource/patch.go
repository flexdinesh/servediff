package diffsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/flexdinesh/servediff/internal/review"
)

const (
	MaxInputBytes = 16 * 1024 * 1024
	MaxFileBytes  = 2 * 1024 * 1024
)

var (
	ansiPattern    = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
	commitBoundary = regexp.MustCompile(`(?m)^(?:commit [a-f\d]{40,64}(?: .*)?|From [a-f\d]{40,64} .*)$`)
	commitID       = regexp.MustCompile(`(?m)^(?:commit|From) ([a-f\d]{40,64})`)
	fileBoundary   = regexp.MustCompile(`(?m)^diff --git `)
	hunkHeader     = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)
)

type patchSource struct {
	root     string
	manifest review.RepositoryDiff
	previews map[string]review.FilePatch
}

func OpenPatch(input string) (Source, error) {
	if len(input) > MaxInputBytes {
		return nil, Error(400, "Piped diff exceeds the 16 MiB input limit")
	}
	data := ansiPattern.ReplaceAllString(input, "")
	if strings.Contains(data, "diff --cc ") || strings.Contains(data, "diff --combined ") {
		return nil, Error(400, "Combined merge diffs are not supported. Use git show --diff-merges=separate | servediff instead.")
	}
	revision := digest(data)
	root := "stdin:" + revision
	sections := splitAt(data, commitBoundary)
	commits := make([]string, 0, len(sections))
	for _, section := range sections {
		if fileBoundary.MatchString(section) {
			commits = append(commits, section)
		}
	}
	files := make([]review.ChangedFile, 0)
	previews := make(map[string]review.FilePatch)
	for section, commit := range commits {
		prefix := ""
		if len(commits) > 1 {
			name := "patch"
			if match := commitID.FindStringSubmatch(commit); len(match) == 2 {
				name = match[1][:8]
			}
			prefix = strconv.Itoa(section+1) + " · " + name + "/"
		}
		for _, patch := range splitAt(commit, fileBoundary) {
			if !strings.HasPrefix(patch, "diff --git ") {
				continue
			}
			if len(patch) > MaxFileBytes {
				return nil, Error(400, "A file in the piped diff exceeds the 2 MiB preview limit")
			}
			file, err := parsePatchFile(patch, prefix)
			if err != nil {
				return nil, err
			}
			if _, exists := previews[file.Path]; exists {
				return nil, Error(400, "Repeated file in patch: %s. Pipe standard git show or git diff output.", file.Path)
			}
			files = append(files, file)
			message := (*string)(nil)
			body := patch
			if file.Binary {
				body = ""
				message = Message("Binary file changed. No text preview available.")
			}
			previews[file.Path] = review.FilePatch{Patch: body, Message: message}
		}
	}
	if len(files) == 0 && strings.TrimSpace(data) != "" && !commitBoundary.MatchString(data) {
		return nil, Error(400, "No Git patch found on stdin. Pipe git diff or git show output into servediff.")
	}
	return &patchSource{
		root: root,
		manifest: review.RepositoryDiff{
			Source: "stdin", Root: root, Name: "Piped diff", Branch: "stdin",
			Mode: review.DiffAll, Files: files, Revision: revision,
		},
		previews: previews,
	}, nil
}

func splitAt(value string, pattern *regexp.Regexp) []string {
	indices := pattern.FindAllStringIndex(value, -1)
	if len(indices) == 0 {
		return []string{value}
	}
	result := make([]string, 0, len(indices)+1)
	if indices[0][0] > 0 {
		result = append(result, value[:indices[0][0]])
	}
	for index, position := range indices {
		end := len(value)
		if index+1 < len(indices) {
			end = indices[index+1][0]
		}
		result = append(result, value[position[0]:end])
	}
	return result
}

func parsePatchFile(patch, prefix string) (review.ChangedFile, error) {
	if !validPatchHunks(patch) {
		return review.ChangedFile{}, Error(400, "Could not parse a file in the piped diff")
	}
	path := ""
	var oldPath *string
	status := "M"
	if value := headerValue(patch, "rename to "); value != "" {
		path = value
		previous := prefix + headerValue(patch, "rename from ")
		oldPath = &previous
		status = "R"
	} else if value := headerValue(patch, "copy to "); value != "" {
		path = value
		previous := prefix + headerValue(patch, "copy from ")
		oldPath = &previous
		status = "C"
	} else {
		path = patchPath(patch, "+++ ", "b/")
		previous := patchPath(patch, "--- ", "a/")
		if path == "" {
			path = diffGitPath(patch)
		}
		if path == "" && previous != "" {
			path = previous
		}
		if previous != "" && previous != path {
			value := prefix + previous
			oldPath = &value
		}
	}
	if strings.Contains(patch, "\nnew file mode ") {
		status = "A"
		oldPath = nil
	} else if strings.Contains(patch, "\ndeleted file mode ") {
		status = "D"
	} else if status == "M" {
		oldMode, newMode := headerValue(patch, "old mode "), headerValue(patch, "new mode ")
		if len(oldMode) >= 3 && len(newMode) >= 3 && oldMode[:3] != newMode[:3] {
			status = "T"
		}
	}
	if path == "" {
		return review.ChangedFile{}, Error(400, "Could not parse a file in the piped diff")
	}
	additions, deletions := patchStats(patch)
	binary := regexp.MustCompile(`(?m)^(?:Binary files .* differ|GIT binary patch)$`).MatchString(patch)
	path = prefix + path
	return review.ChangedFile{
		ID: digest(path), Path: path, OldPath: oldPath, Status: status,
		IndexStatus: "", WorktreeStatus: "", Additions: additions, Deletions: deletions,
		Binary: binary, Fingerprint: digest(patch),
	}, nil
}

func validPatchHunks(patch string) bool {
	expectedOld, expectedNew, actualOld, actualNew := 0, 0, 0, 0
	inHunk := false
	complete := func() bool { return !inHunk || expectedOld == actualOld && expectedNew == actualNew }
	for _, line := range strings.Split(patch, "\n") {
		if match := hunkHeader.FindStringSubmatch(line); len(match) == 5 {
			if !complete() {
				return false
			}
			expectedOld, expectedNew = count(match[2]), count(match[4])
			actualOld, actualNew, inHunk = 0, 0, true
			continue
		}
		if inHunk && expectedOld == actualOld && expectedNew == actualNew {
			inHunk = false
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ No newline") {
			continue
		}
		switch line[0] {
		case ' ':
			actualOld++
			actualNew++
		case '+':
			actualNew++
		case '-':
			actualOld++
		default:
			return false
		}
	}
	return complete()
}

func count(value string) int {
	if value == "" {
		return 1
	}
	result, _ := strconv.Atoi(value)
	return result
}

func headerValue(patch, prefix string) string {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func patchPath(patch, header, gitPrefix string) string {
	value := headerValue(patch, header)
	if value == "" || value == "/dev/null" {
		return ""
	}
	value = strings.SplitN(value, "\t", 2)[0]
	value = strings.Trim(value, `"`)
	return strings.TrimPrefix(value, gitPrefix)
}

func diffGitPath(patch string) string {
	line := headerValue(patch, "diff --git ")
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, `"`) {
		separator := strings.LastIndex(line, ` "b/`)
		if separator < 0 {
			return ""
		}
		value, err := strconv.Unquote(line[separator+1:])
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(value, "b/")
	}
	separator := strings.LastIndex(line, " b/")
	if separator < 0 {
		return ""
	}
	return strings.TrimPrefix(line[separator+1:], "b/")
}

func patchStats(patch string) (int, int) {
	additions, deletions, inHunk := 0, 0, false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		if strings.HasPrefix(line, "+") {
			additions++
		} else if strings.HasPrefix(line, "-") {
			deletions++
		}
	}
	return additions, deletions
}

func PatchContext(patch, side string, start, end int) (string, bool) {
	if start > end {
		start, end = end, start
	}
	if start < 1 || end-start >= 200 || (side != "additions" && side != "deletions") {
		return "", false
	}
	selected := make(map[int]string)
	oldLine, newLine, inHunk := 0, 0, false
	for _, line := range strings.Split(patch, "\n") {
		if match := hunkHeader.FindStringSubmatch(line); len(match) == 5 {
			oldLine, _ = strconv.Atoi(match[1])
			newLine, _ = strconv.Atoi(match[3])
			inHunk = true
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ No newline") {
			continue
		}
		prefix := line[0]
		switch prefix {
		case ' ':
			if side == "additions" {
				selected[newLine] = "  " + line[1:]
			} else {
				selected[oldLine] = "  " + line[1:]
			}
			oldLine++
			newLine++
		case '+':
			if side == "additions" {
				selected[newLine] = "+ " + line[1:]
			}
			newLine++
		case '-':
			if side == "deletions" {
				selected[oldLine] = "- " + line[1:]
			}
			oldLine++
		}
	}
	lines := make([]string, 0, end-start+1)
	for line := start; line <= end; line++ {
		value, ok := selected[line]
		if !ok {
			return "", false
		}
		lines = append(lines, value)
	}
	return strings.Join(lines, "\n"), true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validUTF8(value string) bool { return utf8.ValidString(value) && !strings.ContainsRune(value, 0) }

func (source *patchSource) Root() string              { return source.root }
func (source *patchSource) Kind() string              { return "stdin" }
func (source *patchSource) Scopes() []review.DiffMode { return []review.DiffMode{review.DiffAll} }
func (source *patchSource) Live() bool                { return false }
func (source *patchSource) Snapshot(_ context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if mode != review.DiffAll {
		return review.RepositoryDiff{}, Error(400, "Piped diffs have no staged or unstaged scope")
	}
	return source.manifest, nil
}
func (source *patchSource) Patch(_ context.Context, _ review.DiffMode, file review.ChangedFile, _ *string) (review.FilePatch, error) {
	preview, ok := source.previews[file.Path]
	if !ok {
		return review.FilePatch{}, Error(404, "File is not in the piped diff")
	}
	return preview, nil
}
func (source *patchSource) Contents(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FileContents, error) {
	return review.FileContents{}, Error(400, "Full context is unavailable for piped diffs")
}
