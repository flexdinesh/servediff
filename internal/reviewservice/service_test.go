package reviewservice

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/session"
)

type sourceStub struct {
	support   diffsource.Support
	snapshots map[review.DiffMode]review.RepositoryDiff
	errors    map[review.DiffMode]error
	calls     []review.DiffMode
}

func (source *sourceStub) Root() string { return "/repo" }
func (source *sourceStub) Kind() string { return "test" }
func (source *sourceStub) Support() diffsource.Support {
	return source.support
}
func (source *sourceStub) Snapshot(ctx context.Context, mode review.DiffMode) (review.RepositoryDiff, error) {
	if err := ctx.Err(); err != nil {
		return review.RepositoryDiff{}, err
	}
	source.calls = append(source.calls, mode)
	if err := source.errors[mode]; err != nil {
		return review.RepositoryDiff{}, err
	}
	return source.snapshots[mode], nil
}
func (source *sourceStub) Patch(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FilePatch, error) {
	return review.FilePatch{}, nil
}
func (source *sourceStub) Contents(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FileContents, error) {
	return review.FileContents{}, nil
}

type storeStub struct {
	comments []review.ReviewComment
	putCalls int
	putError error
}

func (store *storeStub) Comments(string) []review.ReviewComment {
	return append([]review.ReviewComment(nil), store.comments...)
}

func (store *storeStub) PutComment(_ string, comment review.ReviewComment) error {
	store.putCalls++
	if store.putError != nil {
		return store.putError
	}
	for index := range store.comments {
		if store.comments[index].ID == comment.ID {
			store.comments[index] = comment
			return nil
		}
	}
	store.comments = append(store.comments, comment)
	return nil
}

func TestListCommentsFiltersAndEnrichesInStoredOrder(t *testing.T) {
	source := &sourceStub{
		support: diffsource.Support{Scopes: []review.DiffMode{review.DiffStaged, review.DiffUnstaged}},
		snapshots: map[review.DiffMode]review.RepositoryDiff{
			review.DiffStaged:   repository(review.DiffStaged, changedFile("anchored.go", "same")),
			review.DiffUnstaged: repository(review.DiffUnstaged, changedFile("other.go", "current")),
		},
	}
	store := &storeStub{comments: []review.ReviewComment{
		comment("first", review.DiffUnstaged, "stale.go", "old", "open"),
		comment("second", review.DiffStaged, "anchored.go", "same", "open"),
		comment("resolved", review.DiffStaged, "anchored.go", "same", "resolved"),
		comment("unknown", review.DiffAll, "unknown.go", "unknown", "open"),
	}}
	service := New(session.Resolve(source, session.Policies{}), store)

	got, err := service.ListComments(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if ids := commentIDs(got); !reflect.DeepEqual(ids, []string{"first", "second", "unknown"}) {
		t.Fatalf("comment IDs = %v", ids)
	}
	if got[0].Applicability != "stale" || got[0].Actionable {
		t.Fatalf("stale comment = %#v", got[0])
	}
	if got[1].Applicability != "anchored" || !got[1].Actionable {
		t.Fatalf("anchored comment = %#v", got[1])
	}
	if got[2].Applicability != "unknown" || got[2].Actionable {
		t.Fatalf("unknown comment = %#v", got[2])
	}
	if !reflect.DeepEqual(source.calls, []review.DiffMode{review.DiffStaged, review.DiffUnstaged}) {
		t.Fatalf("snapshot calls = %v", source.calls)
	}

	got, err = service.ListComments(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if ids := commentIDs(got); !reflect.DeepEqual(ids, []string{"first", "second", "resolved", "unknown"}) {
		t.Fatalf("comment IDs with resolved = %v", ids)
	}
	if got[2].Actionable {
		t.Fatalf("resolved comment is actionable: %#v", got[2])
	}
}

func TestListCommentsReturnsNonNilEmptySliceWithoutSnapshots(t *testing.T) {
	source := &sourceStub{
		support:   diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}},
		snapshots: make(map[review.DiffMode]review.RepositoryDiff),
	}
	service := New(session.Resolve(source, session.Policies{}), &storeStub{})

	got, err := service.ListComments(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("comments = %#v", got)
	}
	if len(source.calls) != 0 {
		t.Fatalf("snapshot calls = %v", source.calls)
	}
}

func TestListCommentsPropagatesSnapshotFailure(t *testing.T) {
	want := errors.New("snapshot failed")
	source := &sourceStub{
		support:   diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}},
		snapshots: make(map[review.DiffMode]review.RepositoryDiff),
		errors:    map[review.DiffMode]error{review.DiffAll: want},
	}
	store := &storeStub{comments: []review.ReviewComment{comment("id", review.DiffAll, "file.go", "old", "open")}}
	service := New(session.Resolve(source, session.Policies{}), store)

	if _, err := service.ListComments(context.Background(), false); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestListCommentsPropagatesCancellation(t *testing.T) {
	source := &sourceStub{
		support:   diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}},
		snapshots: make(map[review.DiffMode]review.RepositoryDiff),
	}
	store := &storeStub{comments: []review.ReviewComment{comment("id", review.DiffAll, "file.go", "old", "open")}}
	service := New(session.Resolve(source, session.Policies{}), store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.ListComments(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveCommentIsIdempotentAndAllowsStaleIdentity(t *testing.T) {
	source := &sourceStub{support: diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}}}
	store := &storeStub{comments: []review.ReviewComment{comment("stale-id", review.DiffAll, "gone.go", "old", "open")}}
	service := New(session.Resolve(source, session.Policies{}), store)

	got, err := service.ResolveComment("stale-id")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Resolution{CommentID: "stale-id", Status: "resolved"}) {
		t.Fatalf("resolution = %#v", got)
	}
	if store.comments[0].Status != "resolved" || store.putCalls != 1 {
		t.Fatalf("store = %#v, put calls = %d", store.comments, store.putCalls)
	}

	got, err = service.ResolveComment("stale-id")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "resolved" || store.putCalls != 1 {
		t.Fatalf("repeat resolution = %#v, put calls = %d", got, store.putCalls)
	}
	if len(source.calls) != 0 {
		t.Fatalf("resolve snapshot calls = %v", source.calls)
	}
}

func TestResolveCommentReturnsTypedNotFoundError(t *testing.T) {
	source := &sourceStub{support: diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}}}
	service := New(session.Resolve(source, session.Policies{}), &storeStub{})

	_, err := service.ResolveComment("missing")
	if !errors.Is(err, ErrCommentNotFound) {
		t.Fatalf("error = %v", err)
	}
	var notFound *CommentNotFoundError
	if !errors.As(err, &notFound) || notFound.CommentID != "missing" {
		t.Fatalf("not-found error = %#v", err)
	}
}

func TestResolveCommentPropagatesStoreFailure(t *testing.T) {
	want := errors.New("save failed")
	source := &sourceStub{support: diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}}}
	store := &storeStub{
		comments: []review.ReviewComment{comment("id", review.DiffAll, "file.go", "old", "open")},
		putError: want,
	}
	service := New(session.Resolve(source, session.Policies{}), store)

	if _, err := service.ResolveComment("id"); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestMethodsRejectDisabledCommentCapability(t *testing.T) {
	source := &sourceStub{support: diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}}}
	store := &storeStub{comments: []review.ReviewComment{comment("id", review.DiffAll, "file.go", "old", "open")}}
	service := New(session.Resolve(source, session.Policies{Comments: session.DisablePolicy}), store)

	if _, err := service.ListComments(context.Background(), false); !capabilityError(err) {
		t.Fatalf("list error = %v", err)
	}
	if _, err := service.ResolveComment("id"); !capabilityError(err) {
		t.Fatalf("resolve error = %v", err)
	}
	if len(source.calls) != 0 || store.putCalls != 0 {
		t.Fatalf("calls after rejection: snapshots = %v, puts = %d", source.calls, store.putCalls)
	}
}

func capabilityError(err error) bool {
	var capability *session.CapabilityError
	return errors.As(err, &capability) && capability.Capability == session.ReviewComments
}

func comment(id string, scope review.DiffMode, path, fingerprint, status string) review.ReviewComment {
	return review.ReviewComment{
		ID: id, Path: path, Scope: scope, Fingerprint: fingerprint,
		Side: "additions", Start: 1, End: 1, Code: "+code", Body: "Fix it",
		Status: status, CreatedAt: 1,
	}
}

func repository(mode review.DiffMode, files ...review.ChangedFile) review.RepositoryDiff {
	return review.RepositoryDiff{Mode: mode, Files: files}
}

func changedFile(path, fingerprint string) review.ChangedFile {
	return review.ChangedFile{Path: path, Fingerprint: fingerprint}
}

func commentIDs(comments []Comment) []string {
	ids := make([]string, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	return ids
}
