package review

import (
	"context"
	"fmt"
)

type Source interface {
	Root() string
	Kind() string
	Support() Support
	Snapshot(context.Context, DiffMode) (RepositoryDiff, error)
	Patch(context.Context, DiffMode, ChangedFile, *string) (FilePatch, error)
	Contents(context.Context, DiffMode, ChangedFile, *string) (FileContents, error)
}

type Support struct {
	Scopes          []DiffMode
	Refresh         bool
	StagingMetadata bool
	FileContents    bool
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
