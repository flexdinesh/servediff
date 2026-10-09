package contextservice

import (
	"context"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewdata"
)

// Store preserves atomic ingestion; callers cannot partially publish an observation.
type Store interface {
	IngestContext(context.Context, string, ingestion.Request) (reviewdata.Binding, error)
	ObservationSnapshot(string, string, review.DiffMode) (review.RepositoryDiff, error)
	ObservationPatch(string, string, review.DiffMode, string) (review.FilePatch, error)
	ObservationContexts(string, int, int64, string, ingestion.Filter) ([]reviewdata.ContextInfo, error)
	Context(string, string, time.Time) (reviewdata.ContextInfo, error)
	ContextBinding(string, string, time.Time) (reviewdata.Binding, error)
	Contexts(string, int, int64, string, time.Time) ([]reviewdata.ContextInfo, error)
	ContextCount(string, time.Time) (int, error)
	DeleteContext(string, string) error
}
