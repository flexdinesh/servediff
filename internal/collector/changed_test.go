package collector

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
)

func changed(t *testing.T, root, previous string) (ingestion.Request, string) {
	t.Helper()
	request, fingerprint, err := CollectChanged(t.Context(), root, Options{SourceID: "machine", Hostname: "host"}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint == "" || fingerprint == previous || Fingerprint(request) != fingerprint {
		t.Fatalf("missing or unchanged identity: previous %q, current %q", previous, fingerprint)
	}
	return request, fingerprint
}

func unchanged(t *testing.T, root, previous string) {
	t.Helper()
	request, fingerprint, err := CollectChanged(t.Context(), root, Options{SourceID: "machine", Hostname: "host", Agent: "another", RunID: "another", SubmissionID: "another"}, previous)
	if !errors.Is(err, ErrUnchanged) || fingerprint != previous || request.SubmissionID != "" || len(request.Scopes) != 0 {
		t.Fatalf("unchanged checkout: fingerprint %q, request %+v, error %v", fingerprint, request, err)
	}
}

func TestCollectChangedTracksLatestCheckout(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	_, clean := changed(t, root, "")
	unchanged(t, root, clean)

	write(t, root, "tracked", "working!\n")
	first, dirty := changed(t, root, clean)
	patch := patchFor(t, first, review.DiffAll, "tracked")
	if patch.Contents == nil || patch.Contents.Before != "original\n" || patch.Contents.After != "working!\n" {
		t.Fatalf("full contents: %+v", patch)
	}
	unchanged(t, root, dirty)

	filename := filepath.Join(root, "tracked")
	stat, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "tracked", "changed!\n")
	if err := os.Chtimes(filename, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, edited := changed(t, root, dirty)
	if patch := patchFor(t, second, review.DiffAll, "tracked"); patch.Contents == nil || patch.Contents.After != "changed!\n" {
		t.Fatalf("same size/timestamp edit lost: %+v", patch)
	}
	// Rewriting the same bytes and changing timestamps must not ingest again.
	write(t, root, "tracked", "changed!\n")
	if err := os.Chtimes(filename, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	unchanged(t, root, edited)

	git(t, root, "add", "tracked")
	_, staged := changed(t, root, edited)
	unchanged(t, root, staged)
	git(t, root, "reset", "--", "tracked")
	_, unstaged := changed(t, root, staged)
	if unstaged != edited {
		t.Fatal("reset did not restore content identity")
	}
	git(t, root, "restore", "tracked")
	cleared, clearedFingerprint := changed(t, root, unstaged)
	if clearedFingerprint != clean {
		t.Fatal("clean checkout identity did not restore")
	}
	for _, scope := range cleared.Scopes {
		if len(scope.Snapshot.Files) != 0 || len(scope.Patches) != 0 {
			t.Fatal("dirty-to-clean did not emit an empty observation")
		}
	}
}

func TestCollectChangedTracksBranchHeadAndRepositoryMetadata(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	_, initial := changed(t, root, "")
	git(t, root, "switch", "-c", "feature")
	branchRequest, branch := changed(t, root, initial)
	if branchRequest.Metadata.Branch != "feature" {
		t.Fatal("branch transition missing")
	}
	git(t, root, "commit", "--allow-empty", "-m", "new head")
	headRequest, head := changed(t, root, branch)
	if sameHead(branchRequest.Metadata.Head, headRequest.Metadata.Head) {
		t.Fatal("HEAD transition missing")
	}
	git(t, root, "remote", "add", "origin", "https://example.com/org/repository.git")
	remoteRequest, err := Collect(t.Context(), root, Options{SourceID: "machine", Hostname: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if remoteRequest.Metadata.RepositoryKey != headRequest.Metadata.RepositoryKey || remoteRequest.Metadata.RepositoryName != "repository" || remoteRequest.Metadata.RemoteURL == headRequest.Metadata.RemoteURL {
		t.Fatal("repository metadata transition missing")
	}
	unchanged(t, root, head)
}

func TestCollectChangedTracksUntrackedAndDeletedFiles(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	_, initial := changed(t, root, "")
	write(t, root, "untracked", "new\n")
	added, untracked := changed(t, root, initial)
	if patch := patchFor(t, added, review.DiffAll, "untracked"); patch.Contents == nil || patch.Contents.After != "new\n" {
		t.Fatalf("untracked contents: %+v", patch)
	}
	if err := os.Remove(filepath.Join(root, "tracked")); err != nil {
		t.Fatal(err)
	}
	deleted, removed := changed(t, root, untracked)
	if patch := patchFor(t, deleted, review.DiffAll, "tracked"); patch.Contents == nil || patch.Contents.Before != "original\n" || patch.Contents.After != "" {
		t.Fatalf("deleted contents: %+v", patch)
	}
	unchanged(t, root, removed)
}

func TestCollectChangedNeverSkipsUnknownIdentity(t *testing.T) {
	root := repository(t)
	write(t, root, "tracked", "original\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",module")
	if err := os.Mkdir(filepath.Join(root, "module"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, previous := range []string{"", "prior-known-fingerprint"} {
		request, fingerprint, err := CollectChanged(t.Context(), root, Options{SourceID: "machine", Hostname: "host"}, previous)
		if err != nil || fingerprint != "" || request.ContentHash != "" || Fingerprint(request) != "" {
			t.Fatalf("unknown identity: fingerprint %q, hash %q, error %v", fingerprint, request.ContentHash, err)
		}
		if err := ingestion.Validate(request); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectChangedVerifiesIdentityWhileSkippingPreviews(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git wrapper requires a POSIX shell")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []string{"", "yes"} {
		t.Run("mutate="+mutate, func(t *testing.T) {
			root := repository(t)
			write(t, root, "tracked", "original\n")
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "initial")
			write(t, root, "tracked", "working!\n")
			_, initial := changed(t, root, "")
			bin := t.TempDir()
			// Fail preview commands. Optionally edit during the second complete
			// identity read, after snapshot verification in the unchanged path.
			script := `#!/bin/sh
case " $* " in
  *" --patch "*) exit 9 ;;
  *" ls-files --stage -z "*)
    count=0
    if [ -f "$DIFFX_TEST_HASH_COUNT" ]; then
      read -r count < "$DIFFX_TEST_HASH_COUNT"
    fi
    count=$((count + 1))
    printf '%s\n' "$count" > "$DIFFX_TEST_HASH_COUNT"
    if [ "$DIFFX_TEST_MUTATE" = yes ] && [ "$count" -eq 2 ]; then
      printf 'changed!\n' > "$DIFFX_TEST_CHANGED_FILE"
    fi
    ;;
esac
exec "$DIFFX_TEST_REAL_GIT" "$@"
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DIFFX_TEST_REAL_GIT", realGit)
			t.Setenv("DIFFX_TEST_HASH_COUNT", filepath.Join(bin, "count"))
			t.Setenv("DIFFX_TEST_CHANGED_FILE", filepath.Join(root, "tracked"))
			t.Setenv("DIFFX_TEST_MUTATE", mutate)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			_, fingerprint, err := CollectChanged(t.Context(), root, Options{SourceID: "machine", Hostname: "host"}, initial)
			if mutate == "" {
				if !errors.Is(err, ErrUnchanged) || fingerprint != initial {
					t.Fatalf("unchanged path constructed previews: %q, %v", fingerprint, err)
				}
			} else if !errors.Is(err, errChanged) || fingerprint != "" {
				t.Fatalf("concurrent edit skipped: %q, %v", fingerprint, err)
			}
		})
	}
}

func TestFingerprintUsesStableProvenance(t *testing.T) {
	root := repository(t)
	request := collect(t, root)
	initial := Fingerprint(request)
	request.SubmissionID = "another"
	request.Metadata.RunID, request.Metadata.Agent, request.Metadata.Trigger = "another", "another", "another"
	request.Metadata.CollectorVersion = "another"
	request.Metadata.CollectedAt++
	request.Metadata.Hostname, request.Metadata.Root, request.Metadata.WorktreeName = "another", "another", "another"
	request.Metadata.RepositoryName, request.Metadata.RemoteURL = "another", "another"
	request.Metadata.Branch = "another"
	for index := range request.Scopes {
		request.Scopes[index].Snapshot.Revision = "another"
	}
	request.Scopes[0], request.Scopes[2] = request.Scopes[2], request.Scopes[0]
	if Fingerprint(request) != initial {
		t.Fatal("ephemeral producer fields changed fingerprint")
	}
	for _, change := range []func(*ingestion.Metadata){
		func(metadata *ingestion.Metadata) { metadata.SourceID = "another" },
		func(metadata *ingestion.Metadata) { metadata.RepositoryKey = "another" },
		func(metadata *ingestion.Metadata) { metadata.CheckoutKey = "another" },
		func(metadata *ingestion.Metadata) { metadata.BranchID = "another" },
	} {
		changed := request
		change(&changed.Metadata)
		if Fingerprint(changed) == initial {
			t.Fatal("stable provenance omitted from fingerprint")
		}
	}
}
