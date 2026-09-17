package diffsource

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/review"
)

func fixturePatch(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../test/fixtures/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOpenPatchMatchesSharedFixture(t *testing.T) {
	source, err := OpenPatch(fixturePatch(t))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != "25bec613f1ae9a88b5d5e29927eb9ca697a2a8c8ecdb42985feeee35e3483198" {
		t.Fatalf("unexpected revision: %s", snapshot.Revision)
	}
	if len(snapshot.Files) != 12 {
		t.Fatalf("got %d files, want 12", len(snapshot.Files))
	}
	first := snapshot.Files[0]
	if first.Path != "src/value.ts" || first.Status != "M" || first.Additions != 1 || first.Deletions != 1 {
		t.Fatalf("unexpected first file: %#v", first)
	}
	preview, err := source.Patch(context.Background(), review.DiffAll, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, ok := PatchContext(preview.Patch, "additions", 1, 1)
	if !ok || code != "+ export const value = 2;" {
		t.Fatalf("unexpected context: %q, %v", code, ok)
	}
}

func TestOpenPatchRejectsUnsupportedInput(t *testing.T) {
	if _, err := OpenPatch("not a patch\n"); err == nil {
		t.Fatal("expected invalid patch error")
	}
	broken := strings.Replace(fixturePatch(t), "@@ -1,3 +1,3 @@", "@@ -20,5 +20,5 @@", 1)
	if _, err := OpenPatch(broken); err == nil {
		t.Fatal("expected invalid hunk error")
	}
	if _, err := OpenPatch("diff --cc file.txt\n@@@ broken @@@\n"); err == nil {
		t.Fatal("expected invalid combined diff error")
	}
}

func TestOpenPatchNormalizesCombinedMergeDiff(t *testing.T) {
	input := `commit 061cb20c17067fefb691086dd12c6326a2ad5c5d
Merge: 1c3df91 c95742f

diff --cc file.txt
index 5eadfe9,36021c1..2d7d9a8
--- a/file.txt
+++ b/file.txt
@@@ -1,3 -1,3 +1,3 @@@ resolve value
  one
- left
 -right
++merge
  three
`
	source, err := OpenPatch(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(snapshot.Files))
	}
	file := snapshot.Files[0]
	if file.Path != "file.txt" || file.Additions != 1 || file.Deletions != 1 {
		t.Fatalf("unexpected combined file: %#v", file)
	}
	preview, err := source.Patch(context.Background(), review.DiffAll, file, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `diff --git a/file.txt b/file.txt
index 5eadfe9..2d7d9a8
--- a/file.txt
+++ b/file.txt
@@ -1,3 +1,3 @@ resolve value
 one
-left
+merge
 three
`
	if preview.Patch != want {
		t.Fatalf("unexpected normalized patch:\n%s", preview.Patch)
	}
	if code, ok := PatchContext(preview.Patch, "deletions", 2, 2); !ok || code != "- left" {
		t.Fatalf("unexpected deletion context: %q, %v", code, ok)
	}
	if code, ok := PatchContext(preview.Patch, "additions", 2, 2); !ok || code != "+ merge" {
		t.Fatalf("unexpected addition context: %q, %v", code, ok)
	}
}

func TestOpenPatchNormalizesCombinedMetadata(t *testing.T) {
	input := `diff --combined script.sh
index 1111111,2222222..3333333
mode 100644,100755..100755
--- a/script.sh
+++ b/script.sh
@@@ -1,1 -1,1 +1,1 @@@
- old
 -other
++new
`
	source, err := OpenPatch(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Status != "M" {
		t.Fatalf("unexpected metadata file: %#v", snapshot.Files)
	}
	preview, err := source.Patch(context.Background(), review.DiffAll, snapshot.Files[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Patch, "old mode 100644\nnew mode 100755") {
		t.Fatalf("mode metadata not normalized:\n%s", preview.Patch)
	}
}

func TestOpenPatchPreservesMetadataOnlyChanges(t *testing.T) {
	input := `diff --git a/old.ts b/new.ts
similarity index 100%
rename from old.ts
rename to new.ts
diff --git a/image.bin b/image.bin
index 1111111..2222222 100644
Binary files a/image.bin and b/image.bin differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git a/link b/link
old mode 100644
new mode 120000
`
	source, err := OpenPatch(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(context.Background(), review.DiffAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 4 || snapshot.Files[0].Status != "R" || snapshot.Files[1].Binary != true || snapshot.Files[2].Status != "M" || snapshot.Files[3].Status != "T" {
		t.Fatalf("unexpected metadata files: %#v", snapshot.Files)
	}
}
