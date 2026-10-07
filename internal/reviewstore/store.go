package reviewstore

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/processlock"
	"github.com/flexdinesh/servediff/internal/review"
	_ "modernc.org/sqlite"
)

const schemaVersion = 7
const CaptureLifetime = 7 * 24 * time.Hour

var ErrNotFound = errors.New("diff not found")

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Binding struct {
	ContextID    string
	LocationID   *string
	RepositoryID *string
	DiffIDs      map[review.DiffMode]string
	VersionID    string
}

type CaptureInfo struct {
	ID        string `json:"id"`
	VersionID string `json:"versionId"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

type Store struct {
	db        *sql.DB
	lock      *processlock.Lock
	retention time.Duration
}

func DefaultPath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "servediff", "state.db"), nil
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func Open(path string) (*Store, error) {
	return OpenWithRetention(path, CaptureLifetime)
}

// OpenWithRetention configures expiry for new captures and fresh observations.
// Existing expiry and replay identities remain unchanged.
func OpenWithRetention(path string, retention time.Duration) (*Store, error) {
	if retention <= 0 {
		return nil, errors.New("retention must be positive")
	}
	name := ":memory:"
	var lock *processlock.Lock
	if path != "" && path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			return nil, err
		}
		// Canonicalize aliases before selecting the database ownership lock.
		parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
		if err != nil {
			return nil, err
		}
		name = filepath.Join(parent, filepath.Base(absolute))
		if existing, err := filepath.EvalSymlinks(name); err == nil {
			name = existing
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		lock, err = processlock.TryAcquire(name + ".lock")
		if err != nil {
			return nil, fmt.Errorf("state database is already in use or unavailable: %w", err)
		}
	}
	success := false
	defer func() {
		if !success && lock != nil {
			_ = lock.Close()
		}
	}()
	if name != ":memory:" {
		if err := validateStateFile(name); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, lock: lock, retention: retention}
	if _, err = db.Exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000"); err == nil {
		err = store.initialize()
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if name != ":memory:" {
		if err := os.Chmod(name, 0o600); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	success = true
	return store, nil
}

func validateStateFile(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 16)
	count, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return err
	}
	if count == 0 || string(header) == "SQLite format 3\x00" {
		return nil
	}
	return errors.New("unrecognized or unsupported servediff state file; existing data preserved")
}

func (store *Store) initialize() error {
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("servediff state schema %d is newer than supported schema %d", version, schemaVersion)
	}
	transaction, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if version == 0 {
		var existing string
		err := transaction.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' LIMIT 1`).Scan(&existing)
		if err == nil {
			return errors.New("unversioned state database")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if version > 0 && version != 2 && version != 3 && version != 4 && version != 5 && version != 6 && version != schemaVersion {
		return fmt.Errorf("servediff state schema %d is unsupported; existing data preserved", version)
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, os_uid TEXT NOT NULL UNIQUE, name TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS repositories (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, common_dir TEXT NOT NULL, UNIQUE(owner_id, common_dir))`,
		`CREATE TABLE IF NOT EXISTS locations (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, repository_id TEXT REFERENCES repositories(id) ON DELETE CASCADE, kind TEXT NOT NULL, root TEXT NOT NULL, worktree_key TEXT NOT NULL, UNIQUE(owner_id, worktree_key))`,
		`CREATE TABLE IF NOT EXISTS diffs (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, location_id TEXT REFERENCES locations(id) ON DELETE CASCADE, kind TEXT NOT NULL, mode TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER, UNIQUE(location_id, mode))`,
		`CREATE INDEX IF NOT EXISTS diffs_owner_capture ON diffs(owner_id, kind, expires_at)`,
		`CREATE TABLE IF NOT EXISTS diff_versions (id TEXT PRIMARY KEY, diff_id TEXT NOT NULL REFERENCES diffs(id) ON DELETE CASCADE, revision TEXT NOT NULL, manifest TEXT NOT NULL, raw_patch TEXT, created_at INTEGER NOT NULL, UNIQUE(diff_id, revision))`,
		`CREATE TABLE IF NOT EXISTS diff_files (version_id TEXT NOT NULL REFERENCES diff_versions(id) ON DELETE CASCADE, file_id TEXT NOT NULL, path TEXT NOT NULL, fingerprint TEXT NOT NULL, patch TEXT NOT NULL, PRIMARY KEY(version_id, file_id))`,
		`CREATE TABLE IF NOT EXISTS reviews (id TEXT PRIMARY KEY, diff_id TEXT NOT NULL UNIQUE REFERENCES diffs(id) ON DELETE CASCADE, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS comments (id TEXT PRIMARY KEY, review_id TEXT NOT NULL REFERENCES reviews(id) ON DELETE CASCADE, version_id TEXT REFERENCES diff_versions(id) ON DELETE SET NULL, file_id TEXT NOT NULL, data TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS comments_review_created ON comments(review_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS marks (review_id TEXT NOT NULL REFERENCES reviews(id) ON DELETE CASCADE, file_id TEXT NOT NULL, version_id TEXT REFERENCES diff_versions(id) ON DELETE SET NULL, data TEXT NOT NULL, PRIMARY KEY(review_id, file_id))`,
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(statement); err != nil {
			return err
		}
	}
	if err := initializeCatalog(transaction); err != nil {
		return err
	}
	if err := initializeIngestion(transaction); err != nil {
		return err
	}
	if err := initializeAuthentication(transaction); err != nil {
		return err
	}
	if version < 5 {
		// Preserve histories; begin retention from each context's last submission.
		if _, err := transaction.Exec(`UPDATE diffs SET expires_at=COALESCE(
			(SELECT c.last_submitted_at FROM contexts c WHERE c.capture_id=diffs.id OR c.location_id=diffs.location_id),
			(SELECT c.last_submitted_at FROM observation_scopes s JOIN contexts c ON c.id=s.context_id WHERE s.diff_id=diffs.id),
			created_at)+?`, CaptureLifetime.Milliseconds()); err != nil {
			return err
		}
	}
	if _, err := transaction.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *Store) Close() error {
	err := store.db.Close()
	if store.lock != nil {
		return errors.Join(err, store.lock.Close())
	}
	return err
}

func (store *Store) User(osUID, name string) (User, error) {
	if osUID == "" || name == "" {
		return User{}, errors.New("current process user is unavailable")
	}
	id, err := newID()
	if err != nil {
		return User{}, err
	}
	_, err = store.db.Exec(`INSERT INTO users(id, os_uid, name, created_at) VALUES(?, ?, ?, ?) ON CONFLICT(os_uid) DO UPDATE SET name=excluded.name`, id, osUID, name, time.Now().UnixMilli())
	if err != nil {
		return User{}, err
	}
	var result User
	err = store.db.QueryRow(`SELECT id, name FROM users WHERE os_uid=?`, osUID).Scan(&result.ID, &result.Name)
	return result, err
}

func (store *Store) RegisterGit(ownerID, root, commonDir, worktreeKey string) (Binding, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return Binding{}, err
	}
	defer transaction.Rollback()
	binding, err := registerGit(transaction, ownerID, root, commonDir, worktreeKey, store.retention)
	if err != nil {
		return Binding{}, err
	}
	return binding, transaction.Commit()
}

func registerGit(transaction *sql.Tx, ownerID, root, commonDir, worktreeKey string, retention time.Duration) (Binding, error) {
	repoID, err := newID()
	if err != nil {
		return Binding{}, err
	}
	if _, err := transaction.Exec(`INSERT INTO repositories(id, owner_id, common_dir) VALUES(?, ?, ?) ON CONFLICT(owner_id, common_dir) DO NOTHING`, repoID, ownerID, commonDir); err != nil {
		return Binding{}, err
	}
	if err := transaction.QueryRow(`SELECT id FROM repositories WHERE owner_id=? AND common_dir=?`, ownerID, commonDir).Scan(&repoID); err != nil {
		return Binding{}, err
	}
	locationID, err := newID()
	if err != nil {
		return Binding{}, err
	}
	if _, err := transaction.Exec(`INSERT INTO locations(id, owner_id, repository_id, kind, root, worktree_key) VALUES(?, ?, ?, 'git_worktree', ?, ?) ON CONFLICT(owner_id, worktree_key) DO UPDATE SET root=excluded.root`, locationID, ownerID, repoID, root, worktreeKey); err != nil {
		return Binding{}, err
	}
	if err := transaction.QueryRow(`SELECT id FROM locations WHERE owner_id=? AND worktree_key=?`, ownerID, worktreeKey).Scan(&locationID); err != nil {
		return Binding{}, err
	}
	binding := Binding{ContextID: locationID, LocationID: &locationID, RepositoryID: &repoID, DiffIDs: make(map[review.DiffMode]string)}
	for _, mode := range []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged} {
		diffID, idError := newID()
		if idError != nil {
			return Binding{}, idError
		}
		if _, err := transaction.Exec(`INSERT INTO diffs(id, owner_id, location_id, kind, mode, created_at, expires_at) VALUES(?, ?, ?, 'live', ?, ?, ?) ON CONFLICT(location_id, mode) DO UPDATE SET expires_at=excluded.expires_at`, diffID, ownerID, locationID, mode, time.Now().UnixMilli(), time.Now().Add(retention).UnixMilli()); err != nil {
			return Binding{}, err
		}
		if err := transaction.QueryRow(`SELECT id FROM diffs WHERE location_id=? AND mode=?`, locationID, mode).Scan(&diffID); err != nil {
			return Binding{}, err
		}
		binding.DiffIDs[mode] = diffID
		if err := ensureReview(transaction, ownerID, diffID); err != nil {
			return Binding{}, err
		}
	}
	if err := putWorktreeContext(transaction, ownerID, locationID, time.Now().UnixMilli()); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func ensureReview(transaction *sql.Tx, ownerID, diffID string) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = transaction.Exec(`INSERT INTO reviews(id, diff_id, owner_id) VALUES(?, ?, ?) ON CONFLICT(diff_id) DO NOTHING`, id, diffID, ownerID)
	return err
}

func (store *Store) Capture(ownerID, raw string, snapshot review.RepositoryDiff) (Binding, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return Binding{}, err
	}
	defer transaction.Rollback()
	binding, err := capture(transaction, ownerID, raw, snapshot, "", store.retention)
	if err != nil {
		return Binding{}, err
	}
	return binding, transaction.Commit()
}

func capture(transaction *sql.Tx, ownerID, raw string, snapshot review.RepositoryDiff, submittedFrom string, retention time.Duration) (Binding, error) {
	diffID, err := newID()
	if err != nil {
		return Binding{}, err
	}
	versionID, err := newID()
	if err != nil {
		return Binding{}, err
	}
	now := time.Now()
	if _, err := transaction.Exec(`INSERT INTO diffs(id, owner_id, kind, mode, created_at, expires_at) VALUES(?, ?, 'capture', 'all', ?, ?)`, diffID, ownerID, now.UnixMilli(), now.Add(retention).UnixMilli()); err != nil {
		return Binding{}, err
	}
	snapshot.ID, snapshot.VersionID = diffID, versionID
	manifest, err := json.Marshal(snapshot)
	if err != nil {
		return Binding{}, err
	}
	if _, err := transaction.Exec(`INSERT INTO diff_versions(id, diff_id, revision, manifest, raw_patch, created_at) VALUES(?, ?, ?, ?, ?, ?)`, versionID, diffID, snapshot.Revision, string(manifest), raw, now.UnixMilli()); err != nil {
		return Binding{}, err
	}
	if err := ensureReview(transaction, ownerID, diffID); err != nil {
		return Binding{}, err
	}
	if err := putCaptureContext(transaction, ownerID, diffID, submittedFrom, now.UnixMilli()); err != nil {
		return Binding{}, err
	}
	return Binding{ContextID: diffID, DiffIDs: map[review.DiffMode]string{review.DiffAll: diffID}, VersionID: versionID}, nil
}

func (store *Store) ReopenCapture(ownerID, diffID string, now time.Time) (string, Binding, error) {
	var raw, versionID string
	err := store.db.QueryRow(`SELECT v.raw_patch, v.id FROM diffs d JOIN diff_versions v ON v.diff_id=d.id WHERE d.id=? AND d.owner_id=? AND d.kind='capture' AND d.expires_at>?`, diffID, ownerID, now.UnixMilli()).Scan(&raw, &versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Binding{}, ErrNotFound
	}
	if err != nil {
		return "", Binding{}, err
	}
	return raw, Binding{ContextID: diffID, DiffIDs: map[review.DiffMode]string{review.DiffAll: diffID}, VersionID: versionID}, nil
}

func (store *Store) ListCaptures(ownerID string, now time.Time) ([]CaptureInfo, error) {
	rows, err := store.db.Query(`SELECT d.id, v.id, d.created_at, d.expires_at FROM diffs d JOIN diff_versions v ON v.diff_id=d.id WHERE d.owner_id=? AND d.kind='capture' AND d.expires_at>? ORDER BY d.created_at DESC`, ownerID, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CaptureInfo, 0)
	for rows.Next() {
		var item CaptureInfo
		if err := rows.Scan(&item.ID, &item.VersionID, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) PruneExpired(now time.Time) error {
	transaction, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`DELETE FROM diffs WHERE id IN (SELECT s.diff_id FROM observation_scopes s JOIN contexts c ON c.id=s.context_id JOIN diffs d ON d.id=c.capture_id WHERE d.expires_at<=?)`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DELETE FROM diffs WHERE expires_at<=?`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DELETE FROM submissions WHERE created_at<=?`, now.Add(-SubmissionLifetime).UnixMilli()); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *Store) CaptureAlive(ownerID, diffID string, now time.Time) (bool, error) {
	var count int
	err := store.db.QueryRow(`SELECT COUNT(*) FROM diffs WHERE owner_id=? AND id=? AND kind='capture' AND expires_at>?`, ownerID, diffID, now.UnixMilli()).Scan(&count)
	return count == 1, err
}

// RemoveLegacyReviews discards only files matching the previous per-session format.
func RemoveLegacyReviews(databasePath string) error {
	if databasePath == "" {
		return nil
	}
	directory := filepath.Join(filepath.Dir(databasePath), "reviews")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var envelope struct {
			Version  int                        `json:"version"`
			Sessions map[string]json.RawMessage `json:"sessions"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Version != 1 || envelope.Sessions == nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
