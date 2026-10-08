package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

// ContentIdentity preserves the established observation identity algorithm.
// Unknown full content identities remain independent unless all scopes are empty.
func ContentIdentity(request Request) (string, error) {
	if len(request.Scopes) == 0 {
		return "", errors.New("observation scopes required")
	}
	modes := make([]string, 0, len(request.Scopes))
	for _, scope := range request.Scopes {
		modes = append(modes, string(scope.Snapshot.Mode))
		if request.ContentHash == "" && len(scope.Snapshot.Files) != 0 {
			return "", nil
		}
	}
	sort.Strings(modes)
	metadata := request.Metadata
	branch := metadata.Branch
	if metadata.BranchID != "" {
		branch = metadata.BranchID
	}
	parts := []any{metadata.SourceID, metadata.RepositoryKey, metadata.CheckoutKey, branch, metadata.Head, request.Scopes[0].Snapshot.Source, modes, request.ContentHash}
	// Keep legacy HEAD identity hashes; branch policies have separate reviews.
	if policy := ComparisonPolicy(metadata); policy != "" {
		parts = append(parts, policy)
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// ComparisonPolicy identifies a stream independently of resolved commit tips.
func ComparisonPolicy(metadata Metadata) string {
	if metadata.Comparison == nil || metadata.Comparison.Kind == "working-tree" {
		return ""
	}
	return metadata.Comparison.Kind + ":" + metadata.Comparison.BaseRef
}
