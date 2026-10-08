package httpapi

import (
	"time"

	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewdata"
	"github.com/flexdinesh/servediff/internal/reviewservice"
)

// Store combines only the storage operations used by REST review handlers.
type Store interface {
	reviewservice.MutationStore
	CaptureAlive(string, string, time.Time) (bool, error)
	ListCaptures(string, time.Time) ([]reviewdata.CaptureInfo, error)
	StoredPatch(string, string, string) (review.FilePatch, error)
	ReopenCapture(string, string, time.Time) (string, reviewdata.Binding, error)
}
