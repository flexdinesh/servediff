package reviewstore

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/processlock"
	"github.com/flexdinesh/diffx/internal/reviewdata"
	_ "modernc.org/sqlite"
)

const schemaVersion = 9
const DefaultRetention = 7 * 24 * time.Hour

var ErrNotFound = reviewdata.ErrNotFound

type User = reviewdata.User

type Binding = reviewdata.Binding

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
	return filepath.Join(state, "diffx", "state.db"), nil
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func Open(path string) (*Store, error) {
	return OpenWithRetention(path, DefaultRetention)
}

// OpenWithRetention configures expiry for new observations.
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
	return errors.New("unrecognized or unsupported diffx state file; existing data preserved")
}

func (store *Store) initialize() error {
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("diffx state schema %d is newer than supported schema %d", version, schemaVersion)
	}
	if _, err := store.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer store.db.Exec("PRAGMA foreign_keys=ON")
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
	if version > 0 && version < schemaVersion {
		rows, err := transaction.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, name)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, name := range tables {
			if _, err := transaction.Exec("DROP TABLE \"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""); err != nil {
				return err
			}
		}
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, os_uid TEXT NOT NULL UNIQUE, name TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS diffs (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, kind TEXT NOT NULL, mode TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER)`,
		`CREATE INDEX IF NOT EXISTS diffs_owner_expiry ON diffs(owner_id, kind, expires_at)`,
		`CREATE TABLE IF NOT EXISTS diff_versions (id TEXT PRIMARY KEY, diff_id TEXT NOT NULL REFERENCES diffs(id) ON DELETE CASCADE, revision TEXT NOT NULL, manifest TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(diff_id, revision))`,
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
	if err := initializeQueue(transaction); err != nil {
		return err
	}
	if err := initializeAuthentication(transaction); err != nil {
		return err
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

func ensureReview(transaction *sql.Tx, ownerID, diffID string) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = transaction.Exec(`INSERT INTO reviews(id, diff_id, owner_id) VALUES(?, ?, ?) ON CONFLICT(diff_id) DO NOTHING`, id, diffID, ownerID)
	return err
}

func (store *Store) PruneExpired(now time.Time) error {
	transaction, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`DELETE FROM diffs WHERE id IN (SELECT s.diff_id FROM observation_scopes s JOIN contexts c ON c.id=s.context_id JOIN diffs d ON d.id=c.anchor_diff_id WHERE d.expires_at<=?)`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DELETE FROM diffs WHERE expires_at<=?`, now.UnixMilli()); err != nil {
		return err
	}

	return transaction.Commit()
}
