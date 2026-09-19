package reviewservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/session"
)

var ErrCommentNotFound = errors.New("review comment not found")

type CommentNotFoundError struct {
	CommentID string
}

func (err *CommentNotFoundError) Error() string {
	return fmt.Sprintf("review comment %q not found", err.CommentID)
}

func (err *CommentNotFoundError) Unwrap() error {
	return ErrCommentNotFound
}

type CommentStore interface {
	Comments(string) []review.ReviewComment
	PutComment(string, review.ReviewComment) error
}

type Comment struct {
	review.ReviewComment
	Applicability string `json:"applicability"`
	Actionable    bool   `json:"actionable"`
}

type Resolution struct {
	CommentID string `json:"comment_id"`
	Status    string `json:"status"`
}

type Service struct {
	session session.Session
	store   CommentStore
}

func New(active session.Session, store CommentStore) *Service {
	return &Service{session: active, store: store}
}

func (service *Service) ListComments(ctx context.Context, includeResolved bool) ([]Comment, error) {
	if err := service.requireComments(); err != nil {
		return nil, err
	}
	selected := make([]review.ReviewComment, 0)
	needed := make(map[review.DiffMode]bool)
	for _, comment := range service.store.Comments(service.session.ID) {
		if !includeResolved && comment.Status != "open" {
			continue
		}
		selected = append(selected, comment)
		needed[comment.Scope] = true
	}

	repositories := make(map[review.DiffMode]review.RepositoryDiff)
	for _, scope := range service.session.Capabilities.Diff.Scopes.Values {
		if !needed[scope] || !service.session.Capabilities.Diff.Scopes.Allows(scope) {
			continue
		}
		if _, exists := repositories[scope]; exists {
			continue
		}
		repository, err := service.session.Source.Snapshot(ctx, scope)
		if err != nil {
			return nil, err
		}
		repositories[scope] = repository
	}

	comments := make([]Comment, 0, len(selected))
	for _, stored := range selected {
		var repository *review.RepositoryDiff
		if current, ok := repositories[stored.Scope]; ok {
			repository = &current
		}
		applicability := review.Applicability(stored, repository)
		comments = append(comments, Comment{
			ReviewComment: stored,
			Applicability: applicability,
			Actionable:    stored.Status == "open" && applicability == "anchored",
		})
	}
	return comments, nil
}

func (service *Service) ResolveComment(commentID string) (Resolution, error) {
	if err := service.requireComments(); err != nil {
		return Resolution{}, err
	}
	for _, comment := range service.store.Comments(service.session.ID) {
		if comment.ID != commentID {
			continue
		}
		if comment.Status != "resolved" {
			comment.Status = "resolved"
			if err := service.store.PutComment(service.session.ID, comment); err != nil {
				return Resolution{}, err
			}
		}
		return Resolution{CommentID: comment.ID, Status: "resolved"}, nil
	}
	return Resolution{}, &CommentNotFoundError{CommentID: commentID}
}

func (service *Service) requireComments() error {
	if !service.session.Capabilities.Review.Comments.Enabled() {
		return session.NotEnabled(session.ReviewComments)
	}
	return nil
}
