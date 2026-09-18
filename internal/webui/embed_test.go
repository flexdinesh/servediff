package webui

import (
	"io/fs"
	"testing"
)

func TestAssetsContainBuiltApplication(t *testing.T) {
	contents, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) == 0 {
		t.Fatal("embedded index.html is empty")
	}
}
