package reviewstore

import (
	"database/sql"
	"errors"
	"time"

	"encoding/json"
	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

const SubmissionLifetime = 24 * time.Hour

var (
	ErrExpired            = errors.New("captured diff expired")
	ErrSubmissionConflict = errors.New("submission ID reused with different input")
)

type ContextInfo struct {
	Stale            bool
	Metadata         *ingestion.Metadata
	ID               string
	Kind             string
	Root             *string
	LocationID       *string
	RepositoryID     *string
	CommonDir        *string
	WorktreeKey      *string
	SubmittedFrom    *string
	CreatedAt        int64
	LastSubmittedAt  int64
	ExpiresAt        *int64
	ChangedFileCount *int
}

func initializeCatalog(transaction *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS contexts (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			kind TEXT NOT NULL CHECK(kind IN ('worktree','capture')),
			location_id TEXT UNIQUE REFERENCES locations(id) ON DELETE CASCADE,
			capture_id TEXT UNIQUE REFERENCES diffs(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			last_submitted_at INTEGER NOT NULL,
			submitted_from TEXT,
			CHECK((kind='worktree' AND location_id IS NOT NULL AND location_id=id AND capture_id IS NULL) OR (kind='capture' AND capture_id IS NOT NULL AND capture_id=id AND location_id IS NULL))
		)`,
		`CREATE INDEX IF NOT EXISTS contexts_owner_order ON contexts(owner_id,last_submitted_at DESC,id DESC)`,
		`CREATE TABLE IF NOT EXISTS submissions (
			owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('worktree','capture')),
			payload_hash TEXT NOT NULL,
			context_id TEXT NOT NULL REFERENCES contexts(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(owner_id,id)
		)`,
		`CREATE INDEX IF NOT EXISTS submissions_created ON submissions(created_at)`,
		`INSERT INTO contexts(id,owner_id,kind,location_id,created_at,last_submitted_at)
			SELECT l.id,l.owner_id,'worktree',l.id,MIN(d.created_at),MAX(d.created_at)
			FROM locations l JOIN diffs d ON d.location_id=l.id
			WHERE l.kind='git_worktree' GROUP BY l.id
			ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO contexts(id,owner_id,kind,capture_id,created_at,last_submitted_at)
			SELECT id,owner_id,'capture',id,created_at,created_at FROM diffs WHERE kind='capture'
			ON CONFLICT(id) DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func putWorktreeContext(transaction *sql.Tx, ownerID, id string, now int64) error {
	_, err := transaction.Exec(`INSERT INTO contexts(id,owner_id,kind,location_id,created_at,last_submitted_at)
		VALUES(?,?,'worktree',?,?,?) ON CONFLICT(id) DO UPDATE SET last_submitted_at=excluded.last_submitted_at`, id, ownerID, id, now, now)
	return err
}

func putCaptureContext(transaction *sql.Tx, ownerID, id, submittedFrom string, now int64) error {
	var provenance *string
	if submittedFrom != "" {
		provenance = &submittedFrom
	}
	_, err := transaction.Exec(`INSERT INTO contexts(id,owner_id,kind,capture_id,created_at,last_submitted_at,submitted_from)
		VALUES(?,?,'capture',?,?,?,?)`, id, ownerID, id, now, now, provenance)
	return err
}

// RegisterGitSubmission commits registration and retry identity atomically.
func (store *Store) RegisterGitSubmission(ownerID, requestID, payloadHash, root, commonDir, worktreeKey string) (Binding, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return Binding{}, err
	}
	defer transaction.Rollback()
	if id, err := findSubmission(transaction, ownerID, requestID, "worktree", payloadHash); err != nil {
		return Binding{}, err
	} else if id != "" {
		return bindingFor(transaction, ownerID, id, time.Now())
	}
	binding, err := registerGit(transaction, ownerID, root, commonDir, worktreeKey)
	if err != nil {
		return Binding{}, err
	}
	if err := putSubmission(transaction, ownerID, requestID, "worktree", payloadHash, binding.ContextID); err != nil {
		return Binding{}, err
	}
	return binding, transaction.Commit()
}

func (store *Store) CaptureSubmission(ownerID, requestID, payloadHash, raw, submittedFrom string, snapshot review.RepositoryDiff) (Binding, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return Binding{}, err
	}
	defer transaction.Rollback()
	if id, err := findSubmission(transaction, ownerID, requestID, "capture", payloadHash); err != nil {
		return Binding{}, err
	} else if id != "" {
		return bindingFor(transaction, ownerID, id, time.Now())
	}
	binding, err := capture(transaction, ownerID, raw, snapshot, submittedFrom)
	if err != nil {
		return Binding{}, err
	}
	if err := putSubmission(transaction, ownerID, requestID, "capture", payloadHash, binding.ContextID); err != nil {
		return Binding{}, err
	}
	return binding, transaction.Commit()
}

func findSubmission(transaction *sql.Tx, ownerID, id, kind, hash string) (string, error) {
	if id == "" {
		return "", errors.New("submission ID required")
	}
	var existingKind, existingHash, contextID string
	err := transaction.QueryRow(`SELECT kind,payload_hash,context_id FROM submissions WHERE owner_id=? AND id=? AND created_at>?`,
		ownerID, id, time.Now().Add(-SubmissionLifetime).UnixMilli()).Scan(&existingKind, &existingHash, &contextID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = transaction.Exec(`DELETE FROM submissions WHERE owner_id=? AND id=?`, ownerID, id)
		return "", err
	}
	if err != nil {
		return "", err
	}
	if existingKind != kind || existingHash != hash {
		return "", ErrSubmissionConflict
	}
	return contextID, nil
}

func putSubmission(transaction *sql.Tx, ownerID, id, kind, hash, contextID string) error {
	_, err := transaction.Exec(`INSERT INTO submissions(owner_id,id,kind,payload_hash,context_id,created_at) VALUES(?,?,?,?,?,?)`,
		ownerID, id, kind, hash, contextID, time.Now().UnixMilli())
	return err
}

const contextSelect = `SELECT c.id,c.kind,COALESCE(l.root,json_extract(o.metadata,'$.root')),c.location_id,COALESCE(l.repository_id,o.repository_id),r.common_dir,COALESCE(l.worktree_key,json_extract(o.metadata,'$.checkoutKey')),c.submitted_from,
	c.created_at,c.last_submitted_at,d.expires_at,
	(SELECT json_array_length(v.manifest, '$.files') FROM diff_versions v WHERE v.diff_id=c.capture_id LIMIT 1),o.metadata,
	COALESCE((SELECT latest.context_id FROM observation_submissions latest WHERE latest.owner_id=o.owner_id AND latest.source_id=o.source_id
		AND json_extract(o.metadata,'$.repositoryKey')<>'' AND json_extract(o.metadata,'$.checkoutKey')<>''
		AND json_extract(latest.metadata,'$.repositoryKey')=json_extract(o.metadata,'$.repositoryKey')
		AND json_extract(latest.metadata,'$.checkoutKey')=json_extract(o.metadata,'$.checkoutKey')
		AND json_extract(latest.metadata,'$.branch')=json_extract(o.metadata,'$.branch')
		ORDER BY json_extract(latest.metadata,'$.collectedAt') DESC,latest.rowid DESC LIMIT 1)<>c.id,0)
	FROM contexts c LEFT JOIN locations l ON l.id=c.location_id LEFT JOIN repositories r ON r.id=l.repository_id
	LEFT JOIN diffs d ON d.id=c.capture_id LEFT JOIN observations o ON o.context_id=c.id`

type scanner interface{ Scan(...any) error }

func scanContext(row scanner) (ContextInfo, error) {
	var item ContextInfo
	var metadata *string
	err := row.Scan(&item.ID, &item.Kind, &item.Root, &item.LocationID, &item.RepositoryID, &item.CommonDir, &item.WorktreeKey,
		&item.SubmittedFrom, &item.CreatedAt, &item.LastSubmittedAt, &item.ExpiresAt, &item.ChangedFileCount, &metadata, &item.Stale)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextInfo{}, ErrNotFound
	}
	if err == nil && metadata != nil {
		var decoded ingestion.Metadata
		if err := json.Unmarshal([]byte(*metadata), &decoded); err != nil {
			return ContextInfo{}, err
		}
		item.Metadata = &decoded
		item.Kind = "observation"
	}
	return item, err
}

func (store *Store) Context(ownerID, id string, now time.Time) (ContextInfo, error) {
	item, err := scanContext(store.db.QueryRow(contextSelect+` WHERE c.owner_id=? AND c.id=?`, ownerID, id))
	if err != nil {
		return ContextInfo{}, err
	}
	if item.ExpiresAt != nil && *item.ExpiresAt <= now.UnixMilli() {
		return ContextInfo{}, ErrExpired
	}
	return item, nil
}

// Contexts uses a keyset cursor; concurrent submissions may move an item across pages.
func (store *Store) Contexts(ownerID string, limit int, beforeTime int64, beforeID string, now time.Time) ([]ContextInfo, error) {
	query := contextSelect + ` WHERE c.owner_id=? AND (d.expires_at IS NULL OR d.expires_at>?)`
	arguments := []any{ownerID, now.UnixMilli()}
	if beforeID != "" {
		query += ` AND (c.last_submitted_at<? OR (c.last_submitted_at=? AND c.id<?))`
		arguments = append(arguments, beforeTime, beforeTime, beforeID)
	}
	query += ` ORDER BY c.last_submitted_at DESC,c.id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := store.db.Query(query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ContextInfo, 0)
	for rows.Next() {
		item, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *Store) ContextCounts(ownerID string, now time.Time) (int, int, error) {
	var worktrees, captures int
	err := store.db.QueryRow(`SELECT COALESCE(SUM(c.kind='worktree'),0),COALESCE(SUM(c.kind='capture'),0)
		FROM contexts c LEFT JOIN diffs d ON d.id=c.capture_id WHERE c.owner_id=? AND (d.expires_at IS NULL OR d.expires_at>?)`,
		ownerID, now.UnixMilli()).Scan(&worktrees, &captures)
	return worktrees, captures, err
}

func (store *Store) WorktreeContexts(ownerID string) ([]ContextInfo, error) {
	rows, err := store.db.Query(contextSelect+` WHERE c.owner_id=? AND c.kind='worktree' ORDER BY c.created_at,c.id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ContextInfo, 0)
	for rows.Next() {
		item, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DiscoverGit preserves submission order and review identity on rediscovery.
func (store *Store) DiscoverGit(ownerID, root, commonDir, worktreeKey string) (string, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	var id string
	err = transaction.QueryRow(`SELECT id FROM locations WHERE owner_id=? AND worktree_key=?`, ownerID, worktreeKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		binding, registerErr := registerGit(transaction, ownerID, root, commonDir, worktreeKey)
		if registerErr != nil {
			return "", registerErr
		}
		id = binding.ContextID
		// Discovery is not a submission; do not replace the CLI's selected context.
		if _, err := transaction.Exec(`UPDATE contexts SET last_submitted_at=0 WHERE id=?`, id); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if _, err := transaction.Exec(`UPDATE locations SET root=? WHERE id=? AND owner_id=?`, root, id, ownerID); err != nil {
		return "", err
	}
	return id, transaction.Commit()
}

type queryer interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func bindingFor(db queryer, ownerID, id string, now time.Time) (Binding, error) {
	item, err := scanContext(db.QueryRow(contextSelect+` WHERE c.owner_id=? AND c.id=?`, ownerID, id))
	if err != nil {
		return Binding{}, err
	}
	if item.ExpiresAt != nil && *item.ExpiresAt <= now.UnixMilli() {
		return Binding{}, ErrExpired
	}
	binding := Binding{ContextID: item.ID, LocationID: item.LocationID, RepositoryID: item.RepositoryID, DiffIDs: make(map[review.DiffMode]string)}
	if item.Kind == "observation" {
		return observationBinding(db, ownerID, id)
	}
	if item.Kind == "capture" {
		if err := db.QueryRow(`SELECT id FROM diff_versions WHERE diff_id=?`, id).Scan(&binding.VersionID); err != nil {
			return Binding{}, err
		}
		binding.DiffIDs[review.DiffAll] = id
		return binding, nil
	}
	rows, err := db.Query(`SELECT mode,id FROM diffs WHERE location_id=? AND owner_id=?`, id, ownerID)
	if err != nil {
		return Binding{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var mode review.DiffMode
		var diffID string
		if err := rows.Scan(&mode, &diffID); err != nil {
			return Binding{}, err
		}
		binding.DiffIDs[mode] = diffID
	}
	return binding, rows.Err()
}

func (store *Store) ContextBinding(ownerID, id string, now time.Time) (Binding, error) {
	return bindingFor(store.db, ownerID, id, now)
}
