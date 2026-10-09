package review

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedCommentContracts(t *testing.T) {
	raw, err := os.ReadFile("../../test/contracts/comments.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name          string
		Comment       ReviewComment
		Repository    *RepositoryDiff
		Valid         bool
		Applicability string
		Export        string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := c.Comment.Valid(); got != c.Valid {
				t.Fatalf("valid=%v want %v", got, c.Valid)
			}
			if got := Applicability(c.Comment, c.Repository); got != c.Applicability {
				t.Fatalf("applicability=%s want %s", got, c.Applicability)
			}
			if c.Export != "" {
				var repos []RepositoryDiff
				if c.Repository != nil {
					repos = append(repos, *c.Repository)
				}
				if got := FormatComments([]ReviewComment{c.Comment}, true, repos); got != c.Export {
					t.Fatalf("export mismatch:\n%s", got)
				}
			}
		})
	}
}
