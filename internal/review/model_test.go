package review

import (
	"strings"
	"testing"
)

func TestCommentTargets(t *testing.T) {
	file := ReviewComment{ID: "file-comment", DiffID: "diff", Path: "new.ts", Scope: DiffAll, Fingerprint: "current", Target: "file", Side: "additions", Body: "Review the whole file", Status: "open"}
	if !file.Valid() {
		t.Fatal("whole-file comment should be valid without line context")
	}
	for _, selection := range []struct {
		target, side string
		start, end   int
		code         string
	}{
		{"", "additions", 0, 0, ""},
		{"lines", "additions", 0, 0, ""},
		{"other", "additions", 0, 0, ""},
		{"file", "additions", 1, 1, ""},
		{"file", "deletions", 0, 0, ""},
		{"file", "additions", 0, 0, "+ synthetic context"},
	} {
		invalid := file
		invalid.Target, invalid.Side, invalid.Start, invalid.End, invalid.Code = selection.target, selection.side, selection.start, selection.end, selection.code
		if invalid.Valid() {
			t.Fatalf("invalid selection accepted: %#v", invalid)
		}
	}
	for _, target := range []string{"", "lines"} {
		line := file
		line.Target, line.Start, line.End, line.Code = target, 1, 1, "+ value"
		if !line.Valid() {
			t.Fatalf("line comment rejected: %#v", line)
		}
	}
}

func TestFileCommentExportAndApplicability(t *testing.T) {
	comment := ReviewComment{ID: "file-comment", DiffID: "diff", Path: "new.ts", Scope: DiffAll, Fingerprint: "current", Target: "file", Side: "additions", Body: "Review <file>", Status: "open"}
	oldPath := "old.ts"
	comment.Origin = &ReviewOrigin{Source: "stdin", File: ReviewFileOrigin{Status: "R", OldPath: &oldPath}}
	repository := RepositoryDiff{ID: "diff", Mode: DiffAll, Files: []ChangedFile{{Path: "new.ts", Fingerprint: "current"}}}
	output := FormatComments([]ReviewComment{comment}, false, []RepositoryDiff{repository})
	for _, expected := range []string{`selection="file"`, `applicability="anchored"`, `old-path="old.ts" change="renamed"`, "<body>Review &lt;file&gt;</body>"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q in export: %s", expected, output)
		}
	}
	for _, unexpected := range []string{` line=`, ` end-line=`, ` side=`, "<code>"} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("synthetic line context %q in export: %s", unexpected, output)
		}
	}
	repository.Files[0].Fingerprint = "changed"
	if Applicability(comment, &repository) != "stale" || FormatComments([]ReviewComment{comment}, false, []RepositoryDiff{repository}) != "" {
		t.Fatal("changed file comment must become stale and leave actionable export")
	}
	output = FormatComments([]ReviewComment{comment}, true, []RepositoryDiff{repository})
	if !strings.Contains(output, `selection="file"`) || !strings.Contains(output, `applicability="stale"`) {
		t.Fatalf("full export must preserve stale file comment: %s", output)
	}
}
