package ingestion

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/flexdinesh/diffx/internal/review"
)

func Validate(input Request) error {
	if input.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported ingestion protocol %d", input.ProtocolVersion)
	}
	if strings.TrimSpace(input.SubmissionID) == "" || len(input.SubmissionID) > 128 {
		return fmt.Errorf("submissionId required; maximum 128 bytes")
	}
	if input.ContentHash != "" {
		if len(input.ContentHash) != 64 || input.ContentHash != strings.ToLower(input.ContentHash) {
			return fmt.Errorf("contentHash must be a lowercase SHA256 hex digest")
		}
		if _, err := hex.DecodeString(input.ContentHash); err != nil {
			return fmt.Errorf("contentHash must be a lowercase SHA256 hex digest")
		}
	}
	if strings.TrimSpace(input.Metadata.SourceID) == "" || len(input.Metadata.SourceID) > 256 || input.Metadata.CollectedAt <= 0 {
		return fmt.Errorf("sourceId and positive collectedAt required")
	}
	if input.Metadata.Trigger != "manual" && input.Metadata.Trigger != "agent-hook" && input.Metadata.Trigger != "pipe" {
		return fmt.Errorf("trigger must be manual, agent-hook, or pipe")
	}
	values := []string{input.Metadata.Hostname, input.Metadata.RunID, input.Metadata.Agent, input.Metadata.RepositoryKey, input.Metadata.RepositoryName, input.Metadata.RemoteURL, input.Metadata.CheckoutKey, input.Metadata.Root, input.Metadata.WorktreeName, input.Metadata.Branch, input.Metadata.BranchID, input.Metadata.CollectorVersion, input.Metadata.TriggerRoot}
	if comparison := input.Metadata.Comparison; comparison != nil {
		if comparison.Kind != "working-tree" && comparison.Kind != "branch" {
			return fmt.Errorf("comparison kind must be working-tree or branch")
		}
		if strings.TrimSpace(comparison.BaseRef) == "" {
			return fmt.Errorf("comparison baseRef required")
		}
		if comparison.Kind == "working-tree" && comparison.BaseRef != "HEAD" {
			return fmt.Errorf("working-tree comparison requires HEAD baseline")
		}
		if comparison.Kind == "branch" && (comparison.BaseCommit == "" || comparison.MergeBase == "") {
			return fmt.Errorf("branch comparison requires baseCommit and mergeBase")
		}
		for _, commit := range []string{comparison.BaseCommit, comparison.MergeBase} {
			if commit != "" && !validCommit(commit) {
				return fmt.Errorf("comparison commits must be lowercase Git object IDs")
			}
		}
		values = append(values, comparison.BaseRef)
	}
	if session := input.Metadata.AgentSession; session != nil {
		if strings.TrimSpace(session.Harness) == "" || strings.TrimSpace(session.ID) == "" {
			return fmt.Errorf("agentSession harness and id required")
		}
		values = append(values, session.Harness, session.ID, session.Name)
	}
	for _, value := range values {
		if len(value) > 32<<10 {
			return fmt.Errorf("metadata value exceeds 32 KiB")
		}
	}
	if len(input.Scopes) < 1 || len(input.Scopes) > 3 {
		return fmt.Errorf("one to three scopes required")
	}
	seen := make(map[review.DiffMode]bool)
	for _, scope := range input.Scopes {
		if _, err := review.ParseDiffMode(string(scope.Snapshot.Mode)); err != nil || seen[scope.Snapshot.Mode] {
			return fmt.Errorf("invalid or repeated scope")
		}
		seen[scope.Snapshot.Mode] = true
		if scope.Snapshot.Source != "local" && scope.Snapshot.Source != "stdin" {
			return fmt.Errorf("snapshot source must be local or stdin")
		}
		if scope.Snapshot.Source == "local" && (input.Metadata.RepositoryKey == "" || input.Metadata.CheckoutKey == "") {
			return fmt.Errorf("Git snapshots require repositoryKey and checkoutKey")
		}
		if scope.Snapshot.Source == "local" && (scope.Snapshot.Root != input.Metadata.Root || scope.Snapshot.Branch != input.Metadata.Branch || !sameHead(scope.Snapshot.Head, input.Metadata.Head)) {
			return fmt.Errorf("Git snapshot and observation metadata must describe the same checkout state")
		}
		first := input.Scopes[0].Snapshot
		if scope.Snapshot.Source != first.Source || scope.Snapshot.Root != first.Root || scope.Snapshot.Branch != first.Branch || !sameHead(scope.Snapshot.Head, first.Head) {
			return fmt.Errorf("all scopes must describe the same checkout state")
		}
		if scope.Snapshot.Revision == "" || scope.Snapshot.Files == nil {
			return fmt.Errorf("snapshot revision and files required")
		}
		files := make(map[string]bool)
		paths := make(map[string]bool)
		for _, file := range scope.Snapshot.Files {
			if file.ID == "" || file.Path == "" || file.Fingerprint == "" || files[file.ID] || paths[file.Path] {
				return fmt.Errorf("unique file identities, paths, and fingerprints required")
			}
			files[file.ID], paths[file.Path] = true, true
			if _, ok := scope.Patches[file.ID]; !ok {
				return fmt.Errorf("preview required for every file, including unavailable previews")
			}
		}
		if len(files) != len(scope.Patches) {
			return fmt.Errorf("preview identities must match snapshot files")
		}
	}
	if !seen[review.DiffAll] {
		return fmt.Errorf("all scope required")
	}
	return nil
}

func validCommit(value string) bool {
	if (len(value) != 40 && len(value) != 64) || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameHead(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
