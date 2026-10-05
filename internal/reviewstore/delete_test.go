package reviewstore

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

func TestDeleteContextCascadesEveryScopeAndAllowsResubmission(t *testing.T) {
	for _, kind := range []string{"observation", "capture", "worktree"} {
		t.Run(kind, func(t *testing.T) {
			store := openTestStore(t)
			owner := testUser(t, store, "owner")
			other := testUser(t, store, "other")
			request := observationRequest("source", "request")
			request.ContentHash = "content"
			manifest := request.Scopes[0].Snapshot
			submit := func(ownerID, requestID string) (Binding, error) {
				switch kind {
				case "observation":
					input := request
					input.SubmissionID = requestID
					return store.Ingest(ownerID, input)
				case "capture":
					return store.CaptureSubmission(ownerID, requestID, "hash", "patch", "", manifest)
				default:
					return store.RegisterGitSubmission(ownerID, requestID, "hash", "/repo/"+requestID, "/repo/.git", "/repo/.git/worktrees/"+requestID)
				}
			}
			otherBinding, err := submit(other.ID, "request")
			if err != nil {
				t.Fatal(err)
			}
			baseline := make(map[string]int)
			for _, table := range []string{"contexts", "diffs", "diff_versions", "diff_files", "reviews", "comments", "marks", "locations", "submissions", "observations", "observation_scopes", "observation_submissions", "observation_identities"} {
				var count int
				if err := store.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
					t.Fatal(err)
				}
				baseline[table] = count
			}
			binding, err := submit(owner.ID, "request")
			if err != nil {
				t.Fatal(err)
			}
			for mode, diffID := range binding.DiffIDs {
				snapshot := manifest
				snapshot.ID, snapshot.Mode = diffID, mode
				snapshot.VersionID = VersionID(diffID, snapshot.Revision)
				if kind == "observation" {
					snapshot, err = store.ObservationSnapshot(owner.ID, binding.ContextID, mode)
					if err != nil {
						t.Fatal(err)
					}
				} else if kind == "capture" {
					snapshot.VersionID = binding.VersionID
				}
				if err := store.PinVersion(snapshot, map[string]review.FilePatch{"file": {Patch: "patch"}}); err != nil {
					t.Fatal(err)
				}
				comment := review.ReviewComment{ID: "comment-" + string(mode), DiffID: diffID, VersionID: snapshot.VersionID, Scope: mode, Path: "file.txt", Body: "review"}
				if err := store.PutComment(binding.ContextID, comment); err != nil {
					t.Fatal(err)
				}
				mark := review.ReviewMark{DiffID: diffID, VersionID: snapshot.VersionID, Scope: mode, FileID: "file"}
				if err := store.PutMark(binding.ContextID, mark); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.DeleteContext(other.ID, binding.ContextID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign delete: %v", err)
			}
			if _, err := store.Context(owner.ID, binding.ContextID, time.Now()); err != nil {
				t.Fatalf("foreign delete changed context: %v", err)
			}
			if err := store.DeleteContext(owner.ID, binding.ContextID); err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteContext(owner.ID, binding.ContextID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("repeated delete: %v", err)
			}
			for table, expected := range baseline {
				var count int
				if err := store.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil || count != expected {
					t.Fatalf("%s retained %d rows; want %d: %v", table, count, expected, err)
				}
			}
			if _, err := store.Context(other.ID, otherBinding.ContextID, time.Now()); err != nil {
				t.Fatalf("other context deleted: %v", err)
			}
			resubmitted, err := submit(owner.ID, "request")
			if err != nil || resubmitted.ContextID == binding.ContextID {
				t.Fatalf("resubmit after deletion: %#v, %v", resubmitted, err)
			}
		})
	}
}

func TestDeleteLatestObservationKeepsOlderSnapshotsStale(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "owner")
	olderRequest := observationRequest("source", "older")
	olderRequest.Metadata.CollectedAt = 100
	older, err := store.Ingest(owner.ID, olderRequest)
	if err != nil {
		t.Fatal(err)
	}
	latestRequest := observationRequest("source", "latest")
	latestRequest.Metadata.CollectedAt = 200
	latest, err := store.Ingest(owner.ID, latestRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContext(owner.ID, latest.ContextID); err != nil {
		t.Fatal(err)
	}
	item, err := store.Context(owner.ID, older.ContextID, time.Now())
	if err != nil || !item.Stale {
		t.Fatalf("older snapshot promoted after deletion: %#v, %v", item, err)
	}
	lateRequest := observationRequest("source", "late")
	lateRequest.Metadata.CollectedAt = 150
	late, err := store.Ingest(owner.ID, lateRequest)
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Context(owner.ID, late.ContextID, time.Now())
	if err != nil || !item.Stale {
		t.Fatalf("late snapshot promoted after deletion: %#v, %v", item, err)
	}
}

func TestDeleteContextRollsBackWhenAnchorDeletionFails(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "owner")
	binding, err := store.Ingest(owner.ID, observationRequest("source", "request"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_anchor BEFORE DELETE ON diffs WHEN OLD.mode='all' BEGIN SELECT RAISE(ABORT,'anchor deletion failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContext(owner.ID, binding.ContextID); err == nil {
		t.Fatal("anchor failure did not abort deletion")
	}
	for mode := range binding.DiffIDs {
		if _, err := store.ObservationSnapshot(owner.ID, binding.ContextID, mode); err != nil {
			t.Fatalf("scope %s removed by failed deletion: %v", mode, err)
		}
	}
}

func TestDeleteWorktreePreservesSiblingAndRepository(t *testing.T) {
	store := openTestStore(t)
	owner := testUser(t, store, "owner")
	first, err := store.RegisterGit(owner.ID, "/repo/one", "/repo/.git", "/repo/.git/worktrees/one")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := store.RegisterGit(owner.ID, "/repo/two", "/repo/.git", "/repo/.git/worktrees/two")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContext(owner.ID, first.ContextID); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ContextBinding(owner.ID, sibling.ContextID, time.Now())
	if err != nil || len(remaining.DiffIDs) != 3 || remaining.RepositoryID == nil || sibling.RepositoryID == nil || *remaining.RepositoryID != *sibling.RepositoryID {
		t.Fatalf("sibling repository changed: %#v, %v", remaining, err)
	}
}
