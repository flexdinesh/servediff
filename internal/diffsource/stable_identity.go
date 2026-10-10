package diffsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/processlock"
	"github.com/google/uuid"
)

// StableRepositoryIdentity persists provenance independently of mutable labels.
type StableRepositoryIdentity struct {
	RepositoryKey string
	CheckoutKey   string
	BranchID      string
}

type savedIdentity struct {
	Version      int               `json:"version"`
	Key          string            `json:"key"`
	Sources      map[string]string `json:"sources"`
	Branches     map[string]string `json:"branches,omitempty"`
	BranchEvents map[string]string `json:"branchEvents,omitempty"`
}

// StableIdentity adopts existing repository/checkout keys once, then preserves
// them through directory moves, remote changes, and Git branch renames. Copying
// Git metadata also copies identities; an independent clone has new metadata.
func StableIdentity(ctx context.Context, root, sourceID, branch string) (StableRepositoryIdentity, error) {
	facts, err := Metadata(ctx, root)
	if err != nil {
		return StableRepositoryIdentity{}, err
	}
	directory := filepath.Join(facts.CommonDir, "diffx")
	lock, err := identityLock(ctx, filepath.Join(directory, "identity.lock"))
	if err != nil {
		return StableRepositoryIdentity{}, err
	}
	defer lock.Close()
	repositoryKey := identityHash("repository", sourceID, facts.CommonDir)
	if facts.RemoteURL != "" {
		repositoryKey = identityHash("remote", facts.RemoteURL)
	}
	repositoryPath := filepath.Join(directory, "repository.json")
	repository, err := readSavedIdentity(repositoryPath, sourceID, repositoryKey)
	if err != nil {
		return StableRepositoryIdentity{}, err
	}
	checkoutPath := filepath.Join(facts.GitDir, "diffx", "checkout.json")
	checkout, err := readSavedIdentity(checkoutPath, sourceID, identityHash("checkout", sourceID, facts.GitDir))
	if err != nil {
		return StableRepositoryIdentity{}, err
	}
	branchID, err := stableBranchID(ctx, root, branch, &repository)
	if err != nil {
		return StableRepositoryIdentity{}, err
	}
	if err := writeSavedIdentity(repositoryPath, repository); err != nil {
		return StableRepositoryIdentity{}, err
	}
	if err := writeSavedIdentity(checkoutPath, checkout); err != nil {
		return StableRepositoryIdentity{}, err
	}
	return StableRepositoryIdentity{RepositoryKey: repository.Sources[sourceID], CheckoutKey: checkout.Sources[sourceID], BranchID: branchID}, nil
}

// BranchCheckoutKey identifies an object-only branch source after its checkout
// disappears. It never depends on the surviving checkout's path or branch name.
func BranchCheckoutKey(repositoryKey, branchID string) string {
	return identityHash("branch-checkout", repositoryKey, branchID)
}

func identityHash(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func identityLock(ctx context.Context, path string) (*processlock.Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lock, err := processlock.TryAcquire(path)
		if !errors.Is(err, processlock.ErrLocked) {
			return lock, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func readSavedIdentity(path, sourceID, initial string) (savedIdentity, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return savedIdentity{Version: 1, Key: initial, Sources: map[string]string{sourceID: initial}, Branches: make(map[string]string)}, nil
	}
	if err != nil {
		return savedIdentity{}, err
	}
	var identity savedIdentity
	if err := json.Unmarshal(raw, &identity); err != nil {
		return savedIdentity{}, fmt.Errorf("invalid collector identity %s: %w", path, err)
	}
	if identity.Version != 1 || identity.Key == "" {
		return savedIdentity{}, fmt.Errorf("invalid collector identity %s", path)
	}
	if identity.Branches == nil {
		identity.Branches = make(map[string]string)
	}
	if identity.BranchEvents == nil {
		identity.BranchEvents = make(map[string]string)
	}
	if identity.Sources == nil {
		identity.Sources = map[string]string{sourceID: identity.Key}
	}
	if identity.Sources[sourceID] == "" {
		identity.Sources[sourceID] = initial
	}
	return identity, nil
}

func writeSavedIdentity(path string, identity savedIdentity) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	// Avoid rewriting unchanged files on every collection.
	if previous, err := os.ReadFile(path); err == nil && string(previous) == string(raw)+"\n" {
		return nil
	}
	file, err := os.CreateTemp(directory, ".identity-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(raw, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func stableBranchID(ctx context.Context, root, branch string, repository *savedIdentity) (string, error) {
	if branch == "" || branch == "HEAD" || strings.HasPrefix(branch, "detached at ") {
		return "", nil
	}
	// Ref validation keeps a branch label from becoming an arbitrary config key.
	if _, err := runGit(ctx, root, 4096, "check-ref-format", "refs/heads/"+branch); err != nil {
		return "", fmt.Errorf("invalid branch identity label %q: %w", branch, err)
	}
	key := "branch." + branch + ".diffx-id"
	raw, err := runGit(ctx, root, 1<<20, "config", "--local", "--get-regexp", `^branch\..*\.diffx-id$`)
	if err != nil && !stableGitExitCode(err, 1) {
		return "", err
	}
	ids := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		name, id, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		name = strings.TrimSuffix(strings.TrimPrefix(name, "branch."), ".diffx-id")
		if _, err := uuid.Parse(id); err != nil {
			return "", fmt.Errorf("invalid collector branch identity for %q", name)
		}
		ids[name] = id
	}
	id := ids[branch]
	if id == "" {
		id = uuid.NewString()
	} else {
		duplicates := make([]string, 0)
		for name, candidate := range ids {
			if candidate == id {
				duplicates = append(duplicates, name)
			}
		}
		if len(duplicates) > 1 {
			original := repository.Branches[id]
			if original == "" || ids[original] != id {
				return "", fmt.Errorf("ambiguous copied collector branch identity %s: %s", id, strings.Join(duplicates, ", "))
			}
			// A matching name can itself be a copy recreated after a rename.
			// Require the known branch's enrollment event before assigning IDs.
			if err := verifyOriginalBranch(ctx, root, original, repository.BranchEvents[id]); err != nil {
				return "", fmt.Errorf("ambiguous copied collector branch identity %s: %w", id, err)
			}
			if original != branch {
				id = uuid.NewString()
			}
		}
	}
	if ids[branch] != id {
		if _, err := runGit(ctx, root, 4096, "config", "--local", "--replace-all", key, id); err != nil {
			return "", err
		}
	}
	repository.Branches[id] = branch
	if repository.BranchEvents == nil {
		repository.BranchEvents = make(map[string]string)
	}
	events, err := branchIdentityEvents(ctx, root, branch)
	if err != nil {
		return "", err
	}
	if len(events) > 0 {
		repository.BranchEvents[id] = identityHash("branch-event", events[0])
	}
	return id, nil
}

func branchIdentityEvents(ctx context.Context, root, branch string) ([]string, error) {
	raw, err := runGit(ctx, root, 32<<10, "reflog", "show", "-n", "32", "--date=raw", "--format=%H%x09%gD%x09%gs", "refs/heads/"+branch)
	if err != nil {
		// An unborn branch has no reflog; ordinary identity creation remains safe.
		if stableGitExitCode(err, 128) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimSpace(raw), "\n"), nil
}

func verifyOriginalBranch(ctx context.Context, root, branch, enrolled string) error {
	if enrolled == "" {
		return fmt.Errorf("original branch %q has no enrollment reflog evidence", branch)
	}
	events, err := branchIdentityEvents(ctx, root, branch)
	if err != nil {
		return err
	}
	for _, event := range events {
		if identityHash("branch-event", event) == enrolled {
			return nil
		}
		if strings.Contains(event, "\tBranch: copied ") {
			return fmt.Errorf("original branch name %q was recreated by a copy", branch)
		}
	}
	return fmt.Errorf("original branch %q enrollment falls outside available reflog evidence", branch)
}

func stableGitExitCode(err error, code int) bool {
	var failure *gitFailure
	var exit *exec.ExitError
	return errors.As(err, &failure) && errors.As(failure.err, &exit) && exit.ExitCode() == code
}
