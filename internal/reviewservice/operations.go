package reviewservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewdata"
	"github.com/flexdinesh/diffx/internal/session"
)

type MutationStore interface {
	CommentStore
	DeleteComment(string, string) (bool, error)
	DeleteComments(string, map[string]bool) (int, error)
	ImportComments(string, []review.ReviewComment) ([]review.ReviewComment, error)
	Marks(string, review.DiffMode) ([]review.ReviewMark, error)
	ClearMarks(string, review.DiffMode) error
	DeleteMark(string, review.DiffMode, string) error
	PutMark(string, review.ReviewMark) error
	StoredVersion(string, string, string, time.Time) (review.RepositoryDiff, error)
}

// Snapshot reads and capability checks belong to the application, shared by transports.
func (service *Service) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if !service.session.Capabilities.Diff.Scopes.Allows(mode) {
		return review.RepositoryDiff{}, session.NotEnabled(session.DiffScopes)
	}
	return service.session.Source.Snapshot(ctx, mode)
}

func (service *Service) ownsDiff(id string) bool {
	for _, owned := range service.session.DiffIDs {
		if owned == id {
			return true
		}
	}
	return false
}
func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

type UpdateCommentInput struct {
	Body   *string `json:"body"`
	Status *string `json:"status"`
}
type DeletedComments struct {
	Comments []review.ReviewComment `json:"comments"`
	Deleted  int                    `json:"deleted"`
}
type ExportCommentsInput struct {
	Revision, Scope, CommentID string
	IncludeResolved            bool
}

func (service *Service) Comments() ([]review.ReviewComment, error) {
	if err := service.requireComments(); err != nil {
		return nil, err
	}
	return service.store.Comments(service.session.ContextID)
}
func (service *Service) DeleteComment(id string) (bool, error) {
	if err := service.requireComments(); err != nil {
		return false, err
	}
	return service.store.DeleteComment(service.session.ContextID, id)
}
func (service *Service) Marks(mode review.DiffMode) ([]review.ReviewMark, error) {
	if !service.session.Capabilities.Diff.Scopes.Allows(mode) {
		return nil, session.NotEnabled(session.DiffScopes)
	}
	return service.store.Marks(service.session.ContextID, mode)
}
func (service *Service) ClearMarks(mode review.DiffMode) error {
	if !service.session.Capabilities.Diff.Scopes.Allows(mode) {
		return session.NotEnabled(session.DiffScopes)
	}
	return service.store.ClearMarks(service.session.ContextID, mode)
}
func (service *Service) DeleteMark(mode review.DiffMode, id string) error {
	if !service.session.Capabilities.Diff.Scopes.Allows(mode) {
		return session.NotEnabled(session.DiffScopes)
	}
	return service.store.DeleteMark(service.session.ContextID, mode, id)
}
func contentContext(contents string, start, end int) (string, bool) {
	if start > end {
		start, end = end, start
	}
	if start < 1 || end-start >= 200 {
		return "", false
	}
	lines := strings.Split(contents, "\n")
	if end > len(lines) {
		return "", false
	}
	selected := make([]string, 0, end-start+1)
	for line := start; line <= end; line++ {
		selected = append(selected, "  "+strings.TrimSuffix(lines[line-1], "\r"))
	}
	return strings.Join(selected, "\n"), true
}

func repositoryFor(comment review.ReviewComment, repositories []review.RepositoryDiff) *review.RepositoryDiff {
	for index := range repositories {
		if repositories[index].Mode == comment.Scope {
			return &repositories[index]
		}
	}
	return nil
}

func (service *Service) CurrentFile(ctx context.Context, mode review.DiffMode, diffID, versionID, fileID, fileVersion string) (review.RepositoryDiff, review.ChangedFile, error) {
	snapshot, err := service.Snapshot(ctx, mode)
	if err != nil {
		return review.RepositoryDiff{}, review.ChangedFile{}, err
	}
	if snapshot.ID != diffID || snapshot.VersionID != versionID {
		return review.RepositoryDiff{}, review.ChangedFile{}, review.Error(409, "Diff changed. Refresh to load the latest version.")
	}
	for _, file := range snapshot.Files {
		if file.ID != fileID {
			continue
		}
		if file.Fingerprint != fileVersion {
			return review.RepositoryDiff{}, review.ChangedFile{}, review.Error(409, "File changed. Refresh to load the latest version.")
		}
		return snapshot, file, nil
	}
	return review.RepositoryDiff{}, review.ChangedFile{}, review.Error(404, "File is not in the current diff")
}

func (service *Service) repositories(ctx context.Context, comments []review.ReviewComment) ([]review.RepositoryDiff, error) {
	needed := make(map[review.DiffMode]bool)
	allowed := make(map[review.DiffMode]bool)
	for _, scope := range service.session.Capabilities.Diff.Scopes.Values {
		allowed[scope] = true
	}
	for _, comment := range comments {
		needed[comment.Scope] = allowed[comment.Scope]
	}
	result := make([]review.RepositoryDiff, 0, len(needed))
	for mode, include := range needed {
		if !include {
			continue
		}
		snapshot, err := service.Snapshot(ctx, mode)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func (service *Service) validateReference(ctx context.Context, diffID, versionID string) error {
	if diffID == "" && versionID == "" {
		return nil
	}
	if !service.ownsDiff(diffID) {
		return review.Error(404, "Comment diff not found")
	}
	if versionID == "" {
		return nil
	}
	_, err := service.store.StoredVersion(service.session.User.ID, diffID, versionID, time.Now())
	if err == nil {
		return nil
	}
	if !errors.Is(err, reviewdata.ErrNotFound) {
		return err
	}
	for mode, id := range service.session.DiffIDs {
		if id != diffID {
			continue
		}
		current, err := service.Snapshot(ctx, mode)
		if err == nil && current.VersionID == versionID {
			return nil
		}
	}
	return review.Error(404, "Comment version not found")
}

type CreateCommentInput struct {
	DiffID      string          `json:"diffId"`
	VersionID   string          `json:"versionId"`
	FileID      string          `json:"fileId"`
	Scope       review.DiffMode `json:"scope"`
	FileVersion string          `json:"fileVersion"`
	Target      string          `json:"target,omitempty"`
	Side        string          `json:"side"`
	Start       int             `json:"start"`
	End         int             `json:"end"`
	Body        string          `json:"body"`
}

func (service *Service) CreateComment(ctx context.Context, body CreateCommentInput) (review.ReviewComment, error) {
	if err := service.requireComments(); err != nil {
		return review.ReviewComment{}, err
	}
	validSelection := (body.Target == "" || body.Target == "lines") && (body.Side == "additions" || body.Side == "deletions") && body.Start > 0 && body.End > 0
	if body.Target == "file" {
		validSelection = body.Side == "additions" && body.Start == 0 && body.End == 0
	}
	if _, err := review.ParseDiffMode(string(body.Scope)); err != nil || !validSelection || strings.TrimSpace(body.Body) == "" {
		return review.ReviewComment{}, review.Error(400, "Invalid comment")
	}
	if !service.session.Capabilities.Diff.Scopes.Allows(body.Scope) {
		return review.ReviewComment{}, session.NotEnabled(session.DiffScopes)
	}
	snapshot, file, err := service.CurrentFile(ctx, body.Scope, body.DiffID, body.VersionID, body.FileID, body.FileVersion)
	if err != nil {
		return review.ReviewComment{}, err
	}
	code := ""
	if body.Target != "file" {
		preview, err := service.session.Source.Patch(ctx, body.Scope, file, snapshot.Head)
		if err != nil {
			return review.ReviewComment{}, err
		}
		var ok bool
		code, ok = review.PatchContext(preview.Patch, body.Side, body.Start, body.End)
		if !ok && preview.Contents != nil {
			contents := preview.Contents.After
			if body.Side == "deletions" {
				contents = preview.Contents.Before
			}
			code, ok = contentContext(contents, body.Start, body.End)
		}
		if !ok {
			return review.ReviewComment{}, review.Error(400, "Select up to 200 visible lines on one side")
		}
	}
	start, end := body.Start, body.End
	if start > end {
		start, end = end, start
	}
	id, err := randomID()
	if err != nil {
		return review.ReviewComment{}, err
	}
	comment := review.ReviewComment{
		ID: id, DiffID: snapshot.ID, VersionID: snapshot.VersionID, Path: file.Path, Scope: body.Scope, Fingerprint: file.Fingerprint,
		Target: body.Target, Side: body.Side, Start: start, End: end, Code: code, Body: strings.TrimSpace(body.Body), Status: "open", CreatedAt: float64(time.Now().UnixMilli()),
		Origin: &review.ReviewOrigin{DiffID: snapshot.ID, VersionID: snapshot.VersionID, Source: snapshot.Source, Repository: snapshot.Name, Branch: snapshot.Branch, Head: snapshot.Head, Revision: snapshot.Revision, File: review.ReviewFileOrigin{Status: file.Status, OldPath: file.OldPath}},
	}
	if err := service.store.PutComment(service.session.ContextID, comment); err != nil {
		return review.ReviewComment{}, err
	}
	return comment, nil
}

func (service *Service) UpdateComment(commentID string, body UpdateCommentInput) (review.ReviewComment, error) {
	if err := service.requireComments(); err != nil {
		return review.ReviewComment{}, err
	}
	if body.Body == nil && body.Status == nil || body.Body != nil && strings.TrimSpace(*body.Body) == "" || body.Status != nil && *body.Status != "open" && *body.Status != "resolved" {
		return review.ReviewComment{}, review.Error(400, "Invalid comment update")
	}
	comments, err := service.store.Comments(service.session.ContextID)
	if err != nil {
		return review.ReviewComment{}, err
	}
	for _, comment := range comments {
		if comment.ID != commentID {
			continue
		}
		if body.Body != nil {
			comment.Body = strings.TrimSpace(*body.Body)
		}
		if body.Status != nil {
			comment.Status = *body.Status
		}
		if err := service.store.PutComment(service.session.ContextID, comment); err != nil {
			return review.ReviewComment{}, err
		}
		return comment, nil
	}
	return review.ReviewComment{}, review.Error(404, "Comment not found")
}

func (service *Service) DeleteComments(ctx context.Context, selection string) (DeletedComments, error) {
	if err := service.requireComments(); err != nil {
		return DeletedComments{}, err
	}
	if selection != "all" && selection != "open" && selection != "resolved" && selection != "stale" {
		return DeletedComments{}, review.Error(400, "Invalid comment status")
	}
	comments, err := service.store.Comments(service.session.ContextID)
	if err != nil {
		return DeletedComments{}, err
	}
	repositories, err := service.repositories(ctx, comments)
	if err != nil {
		return DeletedComments{}, err
	}
	selected := make(map[string]bool)
	for _, comment := range comments {
		stale := review.Applicability(comment, repositoryFor(comment, repositories)) == "stale"
		if selection == "all" || (selection == "stale" && stale) || (!stale && comment.Status == selection) {
			selected[comment.ID] = true
		}
	}
	deleted, err := service.store.DeleteComments(service.session.ContextID, selected)
	if err != nil {
		return DeletedComments{}, err
	}
	remaining, err := service.store.Comments(service.session.ContextID)
	return DeletedComments{Comments: remaining, Deleted: deleted}, err
}

func (service *Service) ImportComments(ctx context.Context, input []review.ReviewComment) ([]review.ReviewComment, error) {
	if err := service.requireComments(); err != nil {
		return nil, err
	}
	for _, comment := range input {
		if !comment.Valid() {
			return nil, review.Error(400, "Invalid comment import")
		}
		if comment.DiffID != service.session.DiffIDs[comment.Scope] {
			return nil, review.Error(404, "Comment diff not found")
		}
		if err := service.validateReference(ctx, comment.DiffID, comment.VersionID); err != nil {
			return nil, err
		}
		if comment.Origin != nil {
			if err := service.validateReference(ctx, comment.Origin.DiffID, comment.Origin.VersionID); err != nil {
				return nil, err
			}
		}
	}
	comments, err := service.store.ImportComments(service.session.ContextID, input)
	if err != nil {
		return nil, err
	}
	return comments, nil
}

func (service *Service) ExportComments(ctx context.Context, input ExportCommentsInput) (string, error) {
	if err := service.requireComments(); err != nil {
		return "", err
	}
	revision, requestedScope := input.Revision, input.Scope
	var mode review.DiffMode
	var current *review.RepositoryDiff
	if requestedScope != "" {
		var err error
		mode, err = review.ParseDiffMode(requestedScope)
		if err != nil {
			return "", review.Error(400, "Invalid diff scope")
		}
		snapshot, err := service.Snapshot(ctx, mode)
		if err != nil {
			return "", err
		}
		current = &snapshot
	}
	selected := make([]review.ReviewComment, 0)
	comments, err := service.store.Comments(service.session.ContextID)
	if err != nil {
		return "", err
	}
	for _, comment := range comments {
		if id := input.CommentID; id != "" && comment.ID != id {
			continue
		}
		if revision != "" {
			matches := comment.Scope == mode && comment.Origin != nil && comment.Origin.Revision == revision
			if comment.Origin == nil && current != nil && current.Revision == revision {
				for _, file := range current.Files {
					matches = matches || (file.Path == comment.Path && file.Fingerprint == comment.Fingerprint)
				}
			}
			if !matches {
				continue
			}
		}
		selected = append(selected, comment)
	}
	repositories, err := service.repositories(ctx, selected)
	if err != nil {
		return "", err
	}
	return review.FormatComments(selected, input.IncludeResolved, repositories), nil
}

func (service *Service) PutMark(ctx context.Context, mode review.DiffMode, fileID, versionID, fileVersion string) (review.ReviewMark, error) {
	if !service.session.Capabilities.Diff.Scopes.Allows(mode) {
		return review.ReviewMark{}, session.NotEnabled(session.DiffScopes)
	}
	snapshot, err := service.Snapshot(ctx, mode)
	if err != nil {
		return review.ReviewMark{}, err
	}
	if snapshot.VersionID != versionID {
		return review.ReviewMark{}, review.Error(409, "Diff changed. Refresh to try again.")
	}
	for _, file := range snapshot.Files {
		if file.ID != fileID {
			continue
		}
		if file.Fingerprint != fileVersion {
			return review.ReviewMark{}, review.Error(409, "File changed. Refresh to try again.")
		}
		mark := review.ReviewMark{DiffID: snapshot.ID, VersionID: snapshot.VersionID, FileID: fileID, FileVersion: fileVersion, Scope: mode}
		if err := service.store.PutMark(service.session.ContextID, mark); err != nil {
			return review.ReviewMark{}, err
		}
		return mark, nil
	}
	return review.ReviewMark{}, review.Error(404, "File is not in the current diff")
}
