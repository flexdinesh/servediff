package ingestion

import (
	"strings"
	"testing"

	"github.com/flexdinesh/servediff/internal/review"
)

func validRequest() Request {
	return Request{ProtocolVersion: ProtocolVersion, SubmissionID: "turn-1", Metadata: Metadata{SourceID: "sandbox-1", Trigger: "agent-hook", CollectedAt: 1, RepositoryKey: "repository", CheckoutKey: "checkout"}, Scopes: []Scope{{Snapshot: review.RepositoryDiff{Source: "local", Mode: review.DiffAll, Revision: "revision", Files: []review.ChangedFile{{ID: "file", Path: "file.txt", Fingerprint: "fingerprint"}}}, Patches: map[string]review.FilePatch{"file": {Patch: "diff"}}}}}
}

func TestValidateObservationContracts(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Request)
	}{
		{"unknown protocol", func(r *Request) { r.ProtocolVersion++ }},
		{"blank submission", func(r *Request) { r.SubmissionID = " " }},
		{"oversized submission", func(r *Request) { r.SubmissionID = strings.Repeat("x", 129) }},
		{"short content hash", func(r *Request) { r.ContentHash = "abc" }},
		{"invalid content hash", func(r *Request) { r.ContentHash = strings.Repeat("z", 64) }},
		{"uppercase content hash", func(r *Request) { r.ContentHash = strings.Repeat("A", 64) }},
		{"blank source", func(r *Request) { r.Metadata.SourceID = " " }},
		{"oversized source", func(r *Request) { r.Metadata.SourceID = strings.Repeat("x", 257) }},
		{"missing collection time", func(r *Request) { r.Metadata.CollectedAt = 0 }},
		{"unknown trigger", func(r *Request) { r.Metadata.Trigger = "watcher" }},
		{"working-tree non-HEAD baseline", func(r *Request) {
			r.Metadata.Comparison = &Comparison{Kind: "working-tree", BaseRef: "main"}
		}},
		{"oversized metadata", func(r *Request) { r.Metadata.Hostname = strings.Repeat("x", (32<<10)+1) }},
		{"oversized trigger root", func(r *Request) { r.Metadata.TriggerRoot = strings.Repeat("x", (32<<10)+1) }},
		{"unknown comparison", func(r *Request) { r.Metadata.Comparison = &Comparison{Kind: "unknown", BaseRef: "HEAD"} }},
		{"blank baseline", func(r *Request) { r.Metadata.Comparison = &Comparison{Kind: "working-tree", BaseRef: " "} }},
		{"missing branch commits", func(r *Request) { r.Metadata.Comparison = &Comparison{Kind: "branch", BaseRef: "refs/heads/main"} }},
		{"invalid comparison commit", func(r *Request) {
			r.Metadata.Comparison = &Comparison{Kind: "working-tree", BaseRef: "HEAD", BaseCommit: "invalid"}
		}},
		{"uppercase comparison commit", func(r *Request) {
			r.Metadata.Comparison = &Comparison{Kind: "working-tree", BaseRef: "HEAD", MergeBase: strings.Repeat("A", 40)}
		}},
		{"blank session harness", func(r *Request) { r.Metadata.AgentSession = &AgentSession{Harness: " ", ID: "id"} }},
		{"blank session ID", func(r *Request) { r.Metadata.AgentSession = &AgentSession{Harness: "codex", ID: " "} }},
		{"oversized session name", func(r *Request) {
			r.Metadata.AgentSession = &AgentSession{Harness: "codex", ID: "id", Name: strings.Repeat("x", (32<<10)+1)}
		}},
		{"missing repository", func(r *Request) { r.Metadata.RepositoryKey = "" }},
		{"missing checkout", func(r *Request) { r.Metadata.CheckoutKey = "" }},
		{"metadata branch differs", func(r *Request) { r.Metadata.Branch = "different" }},
		{"metadata root differs", func(r *Request) { r.Metadata.Root = "/different" }},
		{"metadata HEAD differs", func(r *Request) { head := "different"; r.Metadata.Head = &head }},
		{"mixed source scopes", func(r *Request) {
			scope := r.Scopes[0]
			scope.Snapshot.Mode = review.DiffStaged
			scope.Snapshot.Source = "stdin"
			r.Scopes = append(r.Scopes, scope)
		}},
		{"no scopes", func(r *Request) { r.Scopes = nil }},
		{"repeated scope", func(r *Request) { r.Scopes = append(r.Scopes, r.Scopes[0]) }},
		{"unknown scope", func(r *Request) { r.Scopes[0].Snapshot.Mode = "unknown" }},
		{"missing all scope", func(r *Request) { r.Scopes[0].Snapshot.Mode = review.DiffStaged }},
		{"unknown source", func(r *Request) { r.Scopes[0].Snapshot.Source = "remote" }},
		{"missing revision", func(r *Request) { r.Scopes[0].Snapshot.Revision = "" }},
		{"missing file list", func(r *Request) { r.Scopes[0].Snapshot.Files = nil }},
		{"missing preview", func(r *Request) { r.Scopes[0].Patches = nil }},
		{"foreign preview", func(r *Request) { r.Scopes[0].Patches["foreign"] = review.FilePatch{} }},
		{"duplicate file", func(r *Request) {
			r.Scopes[0].Snapshot.Files = append(r.Scopes[0].Snapshot.Files, r.Scopes[0].Snapshot.Files[0])
		}},
		{"duplicate path", func(r *Request) {
			f := r.Scopes[0].Snapshot.Files[0]
			f.ID = "other"
			r.Scopes[0].Snapshot.Files = append(r.Scopes[0].Snapshot.Files, f)
			r.Scopes[0].Patches[f.ID] = review.FilePatch{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest()
			tc.change(&r)
			if err := Validate(r); err == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
}

func TestValidateStructuredMetadataAndLegacyFallback(t *testing.T) {
	for _, comparison := range []*Comparison{
		nil,
		{Kind: "working-tree", BaseRef: "HEAD"},
		{Kind: "working-tree", BaseRef: "HEAD", BaseCommit: strings.Repeat("a", 40), MergeBase: strings.Repeat("a", 40)},
		{Kind: "branch", BaseRef: "refs/heads/main", BaseCommit: strings.Repeat("a", 40), MergeBase: strings.Repeat("b", 40)},
		{Kind: "branch", BaseRef: "refs/heads/main", BaseCommit: strings.Repeat("a", 64), MergeBase: strings.Repeat("b", 64)},
	} {
		r := validRequest()
		r.Metadata.Comparison = comparison
		r.Metadata.AgentSession = &AgentSession{Harness: "codex", ID: "session", Name: "Feature work"}
		r.Metadata.TriggerRoot = "/workspace"
		if err := Validate(r); err != nil {
			t.Fatalf("valid metadata %+v: %v", comparison, err)
		}
	}
}

func TestValidateEmptyAndUnavailableSnapshots(t *testing.T) {
	r := validRequest()
	r.ContentHash = strings.Repeat("a", 64)
	message := "Binary contents unavailable"
	r.Scopes[0].Patches["file"] = review.FilePatch{Message: &message}
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.Scopes[0].Snapshot.Files = []review.ChangedFile{}
	r.Scopes[0].Patches = map[string]review.FilePatch{}
	if err := Validate(r); err != nil {
		t.Fatalf("empty successful snapshot: %v", err)
	}
	r.Scopes[0].Snapshot.Source = "stdin"
	r.Metadata.RepositoryKey, r.Metadata.CheckoutKey = "", ""
	if err := Validate(r); err != nil {
		t.Fatalf("unassociated piped snapshot: %v", err)
	}
}

func TestValidateLegacyProtocol(t *testing.T) {
	r := validRequest()
	r.ProtocolVersion = 1
	if err := Validate(r); err != nil {
		t.Fatalf("legacy producer: %v", err)
	}
}
