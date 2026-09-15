package diffsource

import (
	"context"
	"fmt"

	"github.com/flexdinesh/servediff/internal/review"
)

type Source interface {
	Root() string
	Kind() string
	Scopes() []review.DiffMode
	Live() bool
	Snapshot(context.Context, review.DiffMode) (review.RepositoryDiff, error)
	Patch(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FilePatch, error)
	Contents(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FileContents, error)
}

type RequestError struct {
	Status int
	Detail string
}

func (err *RequestError) Error() string { return err.Detail }

func Error(status int, format string, values ...any) error {
	return &RequestError{Status: status, Detail: fmt.Sprintf(format, values...)}
}

func Message(value string) *string { return &value }
