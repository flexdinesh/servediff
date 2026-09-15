package review

import (
	"fmt"
	"strings"
)

func Applicability(comment ReviewComment, repository *RepositoryDiff) string {
	if repository == nil {
		return "unknown"
	}
	if comment.Scope != repository.Mode {
		return "other-scope"
	}
	for _, file := range repository.Files {
		if file.Path == comment.Path && file.Fingerprint == comment.Fingerprint {
			return "anchored"
		}
	}
	return "stale"
}

func changeName(status string) string {
	return map[string]string{
		"A": "added", "M": "modified", "D": "deleted", "R": "renamed",
		"C": "copied", "T": "type-changed", "U": "conflicted", "?": "untracked",
	}[status]
}

func attribute(value string) string {
	return strings.NewReplacer("\n", "&#10;", "\r", "&#13;", "\t", "&#9;").Replace(xml(value))
}

func xml(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(value)
}

type exportReview struct {
	origin *ReviewOrigin
	files  []exportFile
}

type exportFile struct {
	path     string
	comments []exportComment
}

type exportComment struct {
	id      string
	comment ReviewComment
}

func FormatComments(comments []ReviewComment, includeResolved bool, repositories []RepositoryDiff) string {
	repositoryFor := func(comment ReviewComment) *RepositoryDiff {
		for index := range repositories {
			if repositories[index].Mode == comment.Scope {
				return &repositories[index]
			}
		}
		return nil
	}
	selected := make([]ReviewComment, 0, len(comments))
	for _, comment := range comments {
		if includeResolved || (comment.Status == "open" && Applicability(comment, repositoryFor(comment)) != "stale") {
			selected = append(selected, comment)
		}
	}
	if len(selected) == 0 {
		return ""
	}
	reviews := make([]exportReview, 0)
	keys := make(map[string]int)
	for index, comment := range selected {
		key := "unknown"
		if comment.Origin != nil {
			key = strings.Join([]string{comment.Origin.Source, comment.Origin.Repository, comment.Origin.Branch, value(comment.Origin.Head), comment.Origin.Revision}, "\x00")
		}
		reviewIndex, ok := keys[key]
		if !ok {
			reviewIndex = len(reviews)
			keys[key] = reviewIndex
			reviews = append(reviews, exportReview{origin: comment.Origin})
		}
		fileIndex := -1
		for candidate := range reviews[reviewIndex].files {
			if reviews[reviewIndex].files[candidate].path == comment.Path {
				fileIndex = candidate
				break
			}
		}
		if fileIndex < 0 {
			fileIndex = len(reviews[reviewIndex].files)
			reviews[reviewIndex].files = append(reviews[reviewIndex].files, exportFile{path: comment.Path})
		}
		reviews[reviewIndex].files[fileIndex].comments = append(reviews[reviewIndex].files[fileIndex].comments, exportComment{id: fmt.Sprintf("C%d", index+1), comment: comment})
	}
	instruction := "Address every unresolved review comment. Inspect the current working tree before editing because code and line numbers describe the reviewed snapshot. Preserve unrelated changes."
	if includeResolved {
		instruction = "Address every anchored open review comment. Inspect the current working tree before editing because code and line numbers describe the reviewed snapshot. Preserve unrelated changes. Resolved and stale comments are context only; do not act on them. If an anchored open comment cannot be applied, report it using its comment ID."
	}
	lines := []string{"<code-review-comments version=\"2\">", "  <instructions>" + xml(instruction) + "</instructions>"}
	for _, item := range reviews {
		if item.origin == nil {
			lines = append(lines, "  <review origin=\"unknown\">")
		} else {
			head := ""
			if item.origin.Head != nil {
				head = ` head="` + attribute(*item.origin.Head) + `"`
			}
			lines = append(lines, fmt.Sprintf(`  <review source="%s" repository="%s" branch="%s"%s revision="%s">`, item.origin.Source, attribute(item.origin.Repository), attribute(item.origin.Branch), head, attribute(item.origin.Revision)))
		}
		for _, file := range item.files {
			metadata := ""
			if origin := file.comments[0].comment.Origin; origin != nil {
				if origin.File.OldPath != nil {
					metadata += ` old-path="` + attribute(*origin.File.OldPath) + `"`
				}
				name := changeName(origin.File.Status)
				if name == "" {
					name = origin.File.Status
				}
				metadata += ` change="` + attribute(name) + `"`
			}
			lines = append(lines, `    <file path="`+attribute(file.path)+`"`+metadata+`>`)
			for _, exported := range file.comments {
				comment := exported.comment
				selection := "range"
				if comment.Start == comment.End {
					selection = "single-line"
				}
				lines = append(lines,
					fmt.Sprintf(`      <comment id="%s" selection="%s" line="%d" end-line="%d" side="%s" scope="%s" status="%s" applicability="%s">`, exported.id, selection, comment.Start, comment.End, comment.Side, comment.Scope, comment.Status, Applicability(comment, repositoryFor(comment))),
					"        <code>"+xml(comment.Code)+"</code>",
					"        <body>"+xml(comment.Body)+"</body>",
					"      </comment>",
				)
			}
			lines = append(lines, "    </file>")
		}
		lines = append(lines, "  </review>")
	}
	return strings.Join(append(lines, "</code-review-comments>"), "\n")
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}
