package review

import (
	"errors"
	"math"
	"strings"
)

type DiffMode string

const (
	DiffAll      DiffMode = "all"
	DiffStaged   DiffMode = "staged"
	DiffUnstaged DiffMode = "unstaged"
)

func ParseDiffMode(value string) (DiffMode, error) {
	mode := DiffMode(value)
	switch mode {
	case DiffAll, DiffStaged, DiffUnstaged:
		return mode, nil
	default:
		return "", errors.New("invalid diff scope")
	}
}

type ChangedFile struct {
	ID             string  `json:"id"`
	Path           string  `json:"path"`
	OldPath        *string `json:"oldPath"`
	Status         string  `json:"status"`
	IndexStatus    string  `json:"indexStatus"`
	WorktreeStatus string  `json:"worktreeStatus"`
	Additions      int     `json:"additions"`
	Deletions      int     `json:"deletions"`
	Binary         bool    `json:"binary"`
	Fingerprint    string  `json:"fingerprint"`
	Recreated      bool    `json:"recreated,omitempty"`
}

type RepositoryDiff struct {
	ID           string        `json:"id"`
	VersionID    string        `json:"versionId"`
	LocationID   *string       `json:"locationId"`
	RepositoryID *string       `json:"repositoryId"`
	Source       string        `json:"source"`
	Root         string        `json:"root"`
	Name         string        `json:"name"`
	Branch       string        `json:"branch"`
	Head         *string       `json:"head"`
	Mode         DiffMode      `json:"mode"`
	Files        []ChangedFile `json:"files"`
	Revision     string        `json:"revision"`
}

type FileContents struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type FilePatch struct {
	Patch    string        `json:"patch"`
	Message  *string       `json:"message"`
	Contents *FileContents `json:"contents,omitempty"`
}

type ReviewFileOrigin struct {
	Status  string  `json:"status"`
	OldPath *string `json:"oldPath"`
}

type ReviewOrigin struct {
	DiffID     string           `json:"diffId"`
	VersionID  string           `json:"versionId"`
	Source     string           `json:"source"`
	Repository string           `json:"repository"`
	Branch     string           `json:"branch"`
	Head       *string          `json:"head"`
	Revision   string           `json:"revision"`
	File       ReviewFileOrigin `json:"file"`
}

type ReviewComment struct {
	ID          string        `json:"id"`
	DiffID      string        `json:"diffId"`
	VersionID   string        `json:"versionId"`
	Path        string        `json:"path"`
	Scope       DiffMode      `json:"scope"`
	Fingerprint string        `json:"fingerprint"`
	Target      string        `json:"target,omitempty"`
	Side        string        `json:"side"`
	Start       int           `json:"start"`
	End         int           `json:"end"`
	Code        string        `json:"code"`
	Body        string        `json:"body"`
	Status      string        `json:"status"`
	CreatedAt   float64       `json:"createdAt"`
	Origin      *ReviewOrigin `json:"origin,omitempty"`
}

func (comment ReviewComment) Valid() bool {
	_, modeError := ParseDiffMode(string(comment.Scope))
	validSelection := (comment.Target == "" || comment.Target == "lines") &&
		(comment.Side == "additions" || comment.Side == "deletions") &&
		comment.Start > 0 && comment.End >= comment.Start && comment.End-comment.Start < 200
	if comment.Target == "file" {
		validSelection = comment.Side == "additions" && comment.Start == 0 && comment.End == 0 && comment.Code == ""
	}
	return modeError == nil &&
		validSelection &&
		strings.TrimSpace(comment.Body) != "" && (comment.Status == "open" || comment.Status == "resolved") &&
		!math.IsNaN(comment.CreatedAt) && !math.IsInf(comment.CreatedAt, 0) &&
		(comment.Origin == nil || comment.Origin.Source == "local" || comment.Origin.Source == "stdin")
}

type ReviewMark struct {
	DiffID      string   `json:"diffId"`
	VersionID   string   `json:"versionId"`
	FileID      string   `json:"fileId"`
	FileVersion string   `json:"fileVersion"`
	Scope       DiffMode `json:"scope"`
}
