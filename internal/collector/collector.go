// Package collector produces Git-aware ingestion requests. It has no server
// or database dependency; CLI and agent hooks use the same collection entrypoint.
package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/diffsource"
	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/google/uuid"
)

const previewBudget = 16 << 20

type Options struct {
	SourceID         string
	Hostname         string
	RunID            string
	Agent            string
	SessionName      string
	Trigger          string
	TriggerRoot      string
	CollectorVersion string
	SubmissionID     string
	Base             string
	Branch           string
	OnSource         func(ingestion.Metadata, diffsource.Comparison)
}

func Collect(ctx context.Context, directory string, options Options) (ingestion.Request, error) {
	request, _, err := CollectChanged(ctx, directory, options, "")
	return request, err
}

// ErrUnchanged means the checkout still matches its acknowledged fingerprint.
var ErrUnchanged = errors.New("checkout unchanged")

// CollectChanged checks complete content identity before constructing previews.
// A nonempty fingerprint is safe to persist only after successful ingestion.
// Unknown identities always produce a request, even when a prior value exists.
func CollectChanged(ctx context.Context, directory string, options Options, previousFingerprint string) (ingestion.Request, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	options, err := defaults(options)
	if err != nil {
		return ingestion.Request{}, "", err
	}
	for attempt := 0; attempt < 2; attempt++ {
		source, err := diffsource.OpenComparison(ctx, directory, options.Base, options.Branch)
		if err != nil {
			return ingestion.Request{}, "", err
		}
		request, fingerprint, err := collectRepositoryChanged(ctx, source, options, previousFingerprint)
		if isChanged(err) {
			err = errChanged
		}
		if err == nil || !errors.Is(err, errChanged) {
			return request, fingerprint, err
		}
	}
	return ingestion.Request{}, "", errChanged
}

var errChanged = errors.New("checkout changed during collection; retry when edits have stopped")

func collectRepository(ctx context.Context, source diffsource.Source, options Options) (ingestion.Request, error) {
	request, _, err := collectRepositoryChanged(ctx, source, options, "")
	return request, err
}

func collectRepositoryChanged(ctx context.Context, source diffsource.Source, options Options, previousFingerprint string) (ingestion.Request, string, error) {
	facts, err := diffsource.Metadata(ctx, source.Root())
	if err != nil {
		return ingestion.Request{}, "", err
	}
	snapshots := make([]review.RepositoryDiff, 0, len(source.Support().Scopes))
	for _, mode := range source.Support().Scopes {
		snapshot, err := source.Snapshot(ctx, mode)
		if err != nil {
			return ingestion.Request{}, "", err
		}
		if len(snapshots) > 0 && (snapshot.Branch != snapshots[0].Branch || !sameHead(snapshot.Head, snapshots[0].Head)) {
			return ingestion.Request{}, "", errChanged
		}
		snapshots = append(snapshots, snapshot)
	}
	contentHash, err := diffsource.ContentHash(ctx, source, snapshots)
	if err != nil {
		if isChanged(err) {
			return ingestion.Request{}, "", errChanged
		}
		return ingestion.Request{}, "", err
	}
	metadata, err := repositoryMetadata(ctx, source.Root(), facts, snapshots[0], options)
	if err != nil {
		return ingestion.Request{}, "", err
	}
	comparison, compared := diffsource.ComparisonInfo(source)
	metadata.Comparison = &ingestion.Comparison{Kind: "working-tree", BaseRef: "HEAD"}
	if compared {
		metadata.Comparison = &ingestion.Comparison{Kind: "branch", BaseRef: comparison.BaseRef, BaseCommit: comparison.BaseCommit, MergeBase: comparison.BaseOID}
	} else if metadata.Head != nil {
		metadata.Comparison.BaseCommit, metadata.Comparison.MergeBase = *metadata.Head, *metadata.Head
	}
	if compared && comparison.ObjectsOnly {
		metadata.CheckoutKey = diffsource.BranchCheckoutKey(metadata.RepositoryKey, metadata.BranchID)
		metadata.WorktreeName = "branch: " + metadata.Branch
		metadata.LinkedWorktree = nil
	}
	if options.OnSource != nil {
		if !compared {
			comparison.BaseRef = "HEAD"
			if metadata.Head != nil {
				comparison.BaseOID, comparison.BaseCommit, comparison.HeadOID = *metadata.Head, *metadata.Head, *metadata.Head
			}
		}
		options.OnSource(metadata, comparison)
	}
	scopes := make([]ingestion.Scope, 0, len(snapshots))
	for _, snapshot := range snapshots {
		scopes = append(scopes, ingestion.Scope{Snapshot: snapshot})
	}
	observation := request(metadata, scopes, options)
	observation.ContentHash = contentHash
	fingerprint := Fingerprint(observation)
	unchanged := fingerprint != "" && fingerprint == previousFingerprint
	remaining := previewBudget
	for index, snapshot := range snapshots {
		if unchanged {
			break
		}
		scope, err := collectScope(ctx, source, snapshot, &remaining)
		if err != nil {
			if isChanged(err) {
				return ingestion.Request{}, "", errChanged
			}
			return ingestion.Request{}, "", err
		}
		observation.Scopes[index] = scope
	}
	for _, snapshot := range snapshots {
		latest, err := source.Snapshot(ctx, snapshot.Mode)
		if err != nil {
			return ingestion.Request{}, "", err
		}
		if latest.Revision != snapshot.Revision {
			return ingestion.Request{}, "", errChanged
		}
	}
	latestHash, err := diffsource.ContentHash(ctx, source, snapshots)
	if err != nil {
		if isChanged(err) {
			return ingestion.Request{}, "", errChanged
		}
		return ingestion.Request{}, "", err
	}
	if latestHash != contentHash {
		return ingestion.Request{}, "", errChanged
	}
	latestFacts, err := diffsource.Metadata(ctx, source.Root())
	if err != nil {
		return ingestion.Request{}, "", err
	}
	if latestFacts != facts {
		return ingestion.Request{}, "", errChanged
	}
	if unchanged {
		return ingestion.Request{}, fingerprint, ErrUnchanged
	}
	return observation, fingerprint, nil
}

// Fingerprint identifies checkout contents and stable provenance, excluding
// submission/agent/session/time and filesystem-based snapshot revisions.
// Empty means full identity cannot be proven; never use it to suppress a sync.
func Fingerprint(request ingestion.Request) string {
	if request.ContentHash == "" {
		return ""
	}
	metadata := request.Metadata
	metadata.RunID, metadata.Agent, metadata.Trigger, metadata.CollectorVersion = "", "", "", ""
	metadata.AgentSession, metadata.TriggerRoot = nil, ""
	metadata.CollectedAt = 0
	if metadata.SourceID != "" {
		metadata.Hostname = ""
	}
	if metadata.RepositoryKey != "" {
		metadata.RepositoryName, metadata.RemoteURL = "", ""
	}
	if metadata.CheckoutKey != "" {
		metadata.Root, metadata.WorktreeName, metadata.LinkedWorktree = "", "", nil
	}
	if metadata.BranchID != "" {
		metadata.Branch = ""
	}
	scopes := make([]string, 0, len(request.Scopes))
	for _, scope := range request.Scopes {
		scopes = append(scopes, scope.Snapshot.Source+":"+string(scope.Snapshot.Mode))
	}
	sort.Strings(scopes)
	identity := struct {
		Version     string
		Protocol    int
		ContentHash string
		Metadata    ingestion.Metadata
		Scopes      []string
	}{"collector-fingerprint-v1", request.ProtocolVersion, request.ContentHash, metadata, scopes}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	return hash(string(encoded))
}

func collectScope(ctx context.Context, source diffsource.Source, snapshot review.RepositoryDiff, remaining *int) (ingestion.Scope, error) {
	patches := make(map[string]review.FilePatch, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if err := ctx.Err(); err != nil {
			return ingestion.Scope{}, err
		}
		if *remaining == 0 {
			patches[file.ID] = review.FilePatch{Message: diffsource.Message("Snapshot exceeds the 16 MiB aggregate preview limit.")}
			continue
		}
		patch, err := source.Patch(ctx, snapshot.Mode, file, snapshot.Head)
		if err != nil {
			return ingestion.Scope{}, err
		}
		if source.Support().FileContents && patch.Contents == nil && !file.Binary && file.Status != "U" && !nonTextPreview(patch) {
			contents, err := source.Contents(ctx, snapshot.Mode, file, snapshot.Head)
			if err == nil {
				patch.Contents = &contents
				patch.Message = nil
			} else if diffsource.PreviewUnavailable(err) {
				if err := ctx.Err(); err != nil {
					return ingestion.Scope{}, err
				}
				if patch.Message == nil {
					patch.Message = diffsource.Message("Full contents unavailable: file is binary or exceeds the preview limit.")
				}
			} else {
				return ingestion.Scope{}, err
			}
		} else if !source.Support().FileContents && patch.Message == nil {
			patch.Message = diffsource.Message("Full contents unavailable for this snapshot.")
		}
		applyBudget(&patch, remaining)
		patches[file.ID] = patch
	}
	return ingestion.Scope{Snapshot: snapshot, Patches: patches}, nil
}

func applyBudget(patch *review.FilePatch, remaining *int) {
	size := len(patch.Patch)
	if patch.Contents != nil {
		size += len(patch.Contents.Before) + len(patch.Contents.After)
	}
	if size > *remaining {
		patch.Contents = nil
		patch.Message = diffsource.Message("Full contents unavailable: snapshot exceeds the 16 MiB aggregate preview limit.")
		size = len(patch.Patch)
		if size > *remaining {
			patch.Patch = ""
			patch.Message = diffsource.Message("Snapshot exceeds the 16 MiB aggregate preview limit.")
			size = 0
		}
	}
	*remaining -= size
}

func nonTextPreview(patch review.FilePatch) bool {
	if patch.Message == nil {
		return false
	}
	return strings.HasPrefix(*patch.Message, "Submodule or directory") || strings.HasPrefix(*patch.Message, "Special file") || strings.HasPrefix(*patch.Message, "File exceeds")
}

// CollectPatch records immutable input with its submission directory as provenance.
func CollectPatch(ctx context.Context, raw, directory string, options Options) (ingestion.Request, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ingestion.Request{}, err
	}
	source, err := diffsource.OpenPatch(raw)
	if err != nil {
		return ingestion.Request{}, err
	}
	if options.Trigger == "" {
		options.Trigger = "pipe"
	}
	options, err = defaults(options)
	if err != nil {
		return ingestion.Request{}, err
	}
	snapshot, err := source.Snapshot(ctx, review.DiffAll)
	if err != nil {
		return ingestion.Request{}, err
	}
	metadata := baseMetadata(options)
	// The submission directory is provenance, not the patch's repository.
	if directory != "" {
		metadata.Root, err = filepath.Abs(directory)
		if err != nil {
			return ingestion.Request{}, err
		}
	}
	remaining := previewBudget
	scope, err := collectScope(ctx, source, snapshot, &remaining)
	if err != nil {
		return ingestion.Request{}, err
	}
	observation := request(metadata, []ingestion.Scope{scope}, options)
	observation.ContentHash = hash("piped-content-v1", raw)
	return observation, nil
}

func request(metadata ingestion.Metadata, scopes []ingestion.Scope, options Options) ingestion.Request {
	return ingestion.Request{ProtocolVersion: ingestion.ProtocolVersion, SubmissionID: options.SubmissionID, Metadata: metadata, Scopes: scopes}
}

func defaults(options Options) (Options, error) {
	if options.Base == "" {
		options.Base = "auto"
	}
	if options.TriggerRoot != "" {
		root, err := filepath.Abs(options.TriggerRoot)
		if err != nil {
			return Options{}, err
		}
		options.TriggerRoot = root
	}
	if options.SourceID == "" {
		id, err := SourceID()
		if err != nil {
			return Options{}, err
		}
		options.SourceID = id
	}
	if options.Hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return Options{}, err
		}
		options.Hostname = hostname
	}
	if options.SubmissionID == "" {
		options.SubmissionID = uuid.NewString()
	}
	if options.Trigger == "" {
		options.Trigger = "manual"
	}
	return options, nil
}

func baseMetadata(options Options) ingestion.Metadata {
	metadata := ingestion.Metadata{SourceID: options.SourceID, Hostname: options.Hostname, RunID: options.RunID, Agent: options.Agent, Trigger: options.Trigger, TriggerRoot: options.TriggerRoot, CollectorVersion: options.CollectorVersion, CollectedAt: time.Now().UnixMilli()}
	if options.Agent != "" && options.RunID != "" {
		metadata.AgentSession = &ingestion.AgentSession{Harness: options.Agent, ID: options.RunID, Name: options.SessionName}
	}
	return metadata
}

func repositoryMetadata(ctx context.Context, root string, facts diffsource.RepositoryMetadata, snapshot review.RepositoryDiff, options Options) (ingestion.Metadata, error) {
	metadata := baseMetadata(options)
	metadata.Root, metadata.WorktreeName = root, filepath.Base(root)
	linked := facts.CommonDir != facts.GitDir
	metadata.LinkedWorktree = &linked
	metadata.Branch, metadata.Head = snapshot.Branch, snapshot.Head
	metadata.RemoteURL = facts.RemoteURL
	metadata.RepositoryName = filepath.Base(filepath.Dir(facts.CommonDir))
	metadata.RepositoryName = diffsource.RemoteRepositoryName(facts.RemoteURL, metadata.RepositoryName)
	identity, err := diffsource.StableIdentity(ctx, root, options.SourceID, snapshot.Branch)
	if err != nil {
		return ingestion.Metadata{}, err
	}
	metadata.RepositoryKey, metadata.CheckoutKey, metadata.BranchID = identity.RepositoryKey, identity.CheckoutKey, identity.BranchID
	return metadata, nil
}

func hash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func sameHead(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func isChanged(err error) bool {
	var request *diffsource.RequestError
	return errors.Is(err, os.ErrNotExist) || errors.As(err, &request) && request.Status == 409
}

// SourceID identifies this installation independently of its hostname/path.
// Ephemeral environments can provide DIFFX_SOURCE_ID explicitly.
func SourceID() (string, error) {
	if id := strings.TrimSpace(os.Getenv("DIFFX_SOURCE_ID")); id != "" {
		return id, nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(config, "diffx")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	filename := filepath.Join(directory, "source-id")
	if raw, err := os.ReadFile(filename); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
		return "", fmt.Errorf("source identity file is empty: %s", filename)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	id := uuid.NewString()
	// Publish a complete file atomically without replacing another process's ID.
	file, err := os.CreateTemp(directory, ".source-id-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(id + "\n")
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", err
	}
	if err := os.Link(file.Name(), filename); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(string(raw)); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("source identity file is empty: %s", filename)
}
