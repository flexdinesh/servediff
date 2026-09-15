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
	if _, err := OpenPatch("diff --cc file.txt\n"); err == nil {
		t.Fatal("expected combined diff error")
	}
	broken := strings.Replace(fixturePatch(t), "@@ -1,3 +1,3 @@", "@@ -20,5 +20,5 @@", 1)
	if _, err := OpenPatch(broken); err == nil {
		t.Fatal("expected invalid hunk error")
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
