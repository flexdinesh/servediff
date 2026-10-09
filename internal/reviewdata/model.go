// Package reviewdata defines storage-independent review identities and results.
package reviewdata

import (
	"errors"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

var ErrNotFound = errors.New("diff not found")
var ErrExpired = errors.New("captured diff expired")
var ErrSubmissionConflict = errors.New("submission ID reused with different input")

func VersionID(diffID, revision string) string { return diffID + "." + revision }

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Binding struct {
	ContextID    string
	LocationID   *string
	RepositoryID *string
	DiffIDs      map[review.DiffMode]string
}

type ContextInfo struct {
	Stale            bool
	Sessions         []SessionAssociation
	Metadata         *ingestion.Metadata
	ID               string
	Kind             string
	Root             *string
	LocationID       *string
	RepositoryID     *string
	CommonDir        *string
	WorktreeKey      *string
	SubmittedFrom    *string
	CreatedAt        int64
	LastSubmittedAt  int64
	ExpiresAt        *int64
	ChangedFileCount *int
}

type SessionAssociation struct {
	SourceID        string `json:"sourceId"`
	Harness         string `json:"harness"`
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	FirstObservedAt int64  `json:"firstObservedAt"`
	LastObservedAt  int64  `json:"lastObservedAt"`
}
