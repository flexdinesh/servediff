// Package ingestion defines the producer-to-server snapshot contract.
// It contains no filesystem, transport, or persistence behavior.
package ingestion

import "github.com/flexdinesh/servediff/internal/review"

const ProtocolVersion = 1
const MaxRequestBytes = 64 << 20

// Comparison records the interpretation and resolved Git baseline of a capture.
// Kind is working-tree or branch; absent comparisons retain legacy HEAD semantics.
type Comparison struct {
	Kind       string `json:"kind"`
	BaseRef    string `json:"baseRef"`
	BaseCommit string `json:"baseCommit,omitempty"`
	MergeBase  string `json:"mergeBase,omitempty"`
}

// AgentSession identifies the session observing a capture, never its authorship.
type AgentSession struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
}

// Metadata records facts at collection time. Paths and hostnames are labels,
// not globally unique identities. Account identity comes from the server.
type Metadata struct {
	SourceID         string        `json:"sourceId"`
	Hostname         string        `json:"hostname"`
	RunID            string        `json:"runId"`
	Agent            string        `json:"agent"`
	Trigger          string        `json:"trigger"`
	RepositoryKey    string        `json:"repositoryKey"`
	RepositoryName   string        `json:"repositoryName"`
	RemoteURL        string        `json:"remoteUrl"`
	CheckoutKey      string        `json:"checkoutKey"`
	Root             string        `json:"root"`
	WorktreeName     string        `json:"worktreeName"`
	LinkedWorktree   *bool         `json:"linkedWorktree,omitempty"`
	Branch           string        `json:"branch"`
	BranchID         string        `json:"branchId,omitempty"`
	Head             *string       `json:"head"`
	CollectedAt      int64         `json:"collectedAt"`
	CollectorVersion string        `json:"collectorVersion"`
	Comparison       *Comparison   `json:"comparison,omitempty"`
	AgentSession     *AgentSession `json:"agentSession,omitempty"`
	TriggerRoot      string        `json:"triggerRoot,omitempty"`
}

type Scope struct {
	Snapshot review.RepositoryDiff       `json:"snapshot"`
	Patches  map[string]review.FilePatch `json:"patches"`
}

// Request is one immutable observation. Replays retain the submission ID;
// fresh observations may reuse an existing review when ContentHash matches.
type Request struct {
	ProtocolVersion int      `json:"protocolVersion"`
	SubmissionID    string   `json:"submissionId"`
	ContentHash     string   `json:"contentHash,omitempty"`
	Metadata        Metadata `json:"metadata"`
	Scopes          []Scope  `json:"scopes"`
}

type Filter struct {
	Query       string
	Repository  string
	Branch      string
	Worktree    string
	Hostname    string
	SourceID    string
	RunID       string
	Harness     string
	SessionID   string
	SessionName string
}
