package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flexdinesh/diffx/internal/diffsource"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewservice"
	"github.com/flexdinesh/diffx/internal/reviewstore"
	"github.com/flexdinesh/diffx/internal/session"
)

func TestFileCommentsWithoutLineContext(t *testing.T) {
	raw := `diff --git a/empty.txt b/empty.txt
new file mode 100644
index 0000000..e69de29
diff --git a/image.bin b/image.bin
index 1111111..2222222 100644
Binary files a/image.bin and b/image.bin differ
diff --git a/old.ts b/new.ts
similarity index 100%
rename from old.ts
rename to new.ts
`
	source, err := diffsource.OpenPatch(raw)
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active := patchSession(t, store, source, raw, session.Policies{})
	service := reviewservice.New(active, store)
	server := httptest.NewServer(New(active, store, service, fstest.MapFS{"index.html": {Data: []byte("web")}}))
	defer server.Close()
	snapshot := decode[review.RepositoryDiff](t, request(t, server.Client(), http.MethodGet, server.URL+"/api/review/diffs/current?scope=all", nil))
	if len(snapshot.Files) != 3 {
		t.Fatalf("expected empty, binary, and renamed files, got %#v", snapshot.Files)
	}
	for _, file := range snapshot.Files {
		t.Run(file.Path, func(t *testing.T) {
			body := reviewservice.CreateCommentInput{DiffID: snapshot.ID, VersionID: snapshot.VersionID, FileID: file.ID, Scope: review.DiffAll, FileVersion: file.Fingerprint, Target: "file", Side: "additions", Body: "Review entire " + file.Path}
			response := request(t, server.Client(), http.MethodPost, server.URL+"/api/review/comments", body)
			if response.StatusCode != http.StatusCreated {
				detail, _ := io.ReadAll(response.Body)
				response.Body.Close()
				t.Fatalf("create status: %d: %s", response.StatusCode, detail)
			}
			created := decode[review.ReviewComment](t, response)
			if created.Target != "file" || created.Start != 0 || created.End != 0 || created.Code != "" || created.Side != "additions" || !created.Valid() || created.Origin == nil {
				t.Fatalf("file comment contains a line anchor: %#v", created)
			}
			if created.Path != file.Path || created.Fingerprint != file.Fingerprint || created.VersionID != snapshot.VersionID {
				t.Fatalf("file comment lost snapshot identity: %#v", created)
			}
			if file.Status == "R" && (created.Origin.File.OldPath == nil || *created.Origin.File.OldPath != "old.ts") {
				t.Fatalf("file comment lost rename identity: %#v", created)
			}
			for _, target := range []string{"", "lines", "unknown"} {
				invalid := body
				invalid.Target = target
				response := request(t, server.Client(), http.MethodPost, server.URL+"/api/review/comments", invalid)
				response.Body.Close()
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("zero-line target %q status: %d", target, response.StatusCode)
				}
			}
			for _, selection := range []struct {
				side       string
				start, end int
			}{
				{"additions", 1, 1},
				{"deletions", 0, 0},
			} {
				invalid := body
				invalid.Side, invalid.Start, invalid.End = selection.side, selection.start, selection.end
				response := request(t, server.Client(), http.MethodPost, server.URL+"/api/review/comments", invalid)
				response.Body.Close()
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("file target accepted line context: %d", response.StatusCode)
				}
			}
			for _, identity := range []struct{ fileVersion, versionID string }{
				{"stale", snapshot.VersionID},
				{file.Fingerprint, "stale"},
			} {
				invalid := body
				invalid.FileVersion, invalid.VersionID = identity.fileVersion, identity.versionID
				response := request(t, server.Client(), http.MethodPost, server.URL+"/api/review/comments", invalid)
				response.Body.Close()
				if response.StatusCode != http.StatusConflict {
					t.Fatalf("file target bypassed snapshot validation: %d", response.StatusCode)
				}
			}
		})
	}
	comments, err := service.ListComments(t.Context(), false)
	if err != nil || len(comments) != 3 {
		t.Fatalf("agent comments: %#v, %v", comments, err)
	}
	for _, comment := range comments {
		if comment.Target != "file" || !comment.Actionable || comment.Applicability != "anchored" {
			t.Fatalf("agent file comment: %#v", comment)
		}
	}
	exportResponse := request(t, server.Client(), http.MethodGet, server.URL+"/api/review/comments/export", nil)
	exported, _ := io.ReadAll(exportResponse.Body)
	exportResponse.Body.Close()
	if strings.Count(string(exported), `selection="file"`) != 3 || strings.Contains(string(exported), "<code>") || strings.Contains(string(exported), ` line=`) {
		t.Fatalf("unexpected file export: %s", exported)
	}
	for _, comment := range comments {
		for range 2 {
			resolution, err := service.ResolveComment(comment.ID)
			if err != nil || resolution.Status != "resolved" {
				t.Fatalf("file resolution: %#v, %v", resolution, err)
			}
		}
	}
	resolved, err := service.ListComments(t.Context(), true)
	if err != nil || len(resolved) != 3 {
		t.Fatalf("resolved file comments: %#v, %v", resolved, err)
	}
	for _, comment := range resolved {
		if comment.Target != "file" || comment.Status != "resolved" || comment.Actionable {
			t.Fatalf("file target lost during resolution: %#v", comment)
		}
	}
}
