package reviewstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/reviewdata"
)

var (
	ErrExpired            = reviewdata.ErrExpired
	ErrSubmissionConflict = reviewdata.ErrSubmissionConflict
)

type ContextInfo = reviewdata.ContextInfo

func initializeCatalog(transaction *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS contexts(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,kind TEXT NOT NULL CHECK(kind='observation'),anchor_diff_id TEXT NOT NULL UNIQUE REFERENCES diffs(id) ON DELETE CASCADE,created_at INTEGER NOT NULL,last_submitted_at INTEGER NOT NULL,submitted_from TEXT)`,
		`CREATE INDEX IF NOT EXISTS contexts_owner_order ON contexts(owner_id,last_submitted_at DESC,id DESC)`,
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func putObservationContext(transaction *sql.Tx, ownerID, id, submittedFrom string, now int64) error {
	var provenance *string
	if submittedFrom != "" {
		provenance = &submittedFrom
	}
	_, err := transaction.Exec(`INSERT INTO contexts(id,owner_id,kind,anchor_diff_id,created_at,last_submitted_at,submitted_from)
		VALUES(?,?,'observation',?,?,?,?)`, id, ownerID, id, now, now, provenance)
	return err
}

var contextSelect = `SELECT c.id,c.kind,json_extract(o.metadata,'$.root'),NULL,o.repository_id,NULL,json_extract(o.metadata,'$.checkoutKey'),c.submitted_from,
	c.created_at,c.last_submitted_at,d.expires_at,
	(SELECT json_array_length(v.manifest, '$.files') FROM diff_versions v WHERE v.diff_id=c.anchor_diff_id LIMIT 1),o.metadata,
	COALESCE(head.context_id<>c.id,0),
	(SELECT json_group_array(json_object('sourceId',s.source_id,'harness',s.harness,'id',s.session_id,'name',s.name,'firstObservedAt',a.first_observed_at,'lastObservedAt',a.last_observed_at))
	 FROM observation_sessions a JOIN agent_sessions s ON s.owner_id=a.owner_id AND s.source_id=a.source_id AND s.harness=a.harness AND s.session_id=a.session_id WHERE a.context_id=c.id)
	FROM contexts c
	LEFT JOIN diffs d ON d.id=c.anchor_diff_id LEFT JOIN observations o ON o.context_id=c.id
	LEFT JOIN observation_stream_heads head ON head.owner_id=o.owner_id AND head.source_id=o.source_id
		AND head.repository_key=json_extract(o.metadata,'$.repositoryKey') AND head.checkout_key=json_extract(o.metadata,'$.checkoutKey')
		AND head.comparison_policy=` + comparisonPolicySQL("o.metadata") + `
		AND head.branch=COALESCE(NULLIF(json_extract(o.metadata,'$.branchId'),''),json_extract(o.metadata,'$.branch'))`

type scanner interface{ Scan(...any) error }

func scanContext(row scanner) (ContextInfo, error) {
	var item ContextInfo
	var metadata *string
	var sessions string
	err := row.Scan(&item.ID, &item.Kind, &item.Root, &item.LocationID, &item.RepositoryID, &item.CommonDir, &item.WorktreeKey,
		&item.SubmittedFrom, &item.CreatedAt, &item.LastSubmittedAt, &item.ExpiresAt, &item.ChangedFileCount, &metadata, &item.Stale, &sessions)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextInfo{}, ErrNotFound
	}
	if err == nil {
		if err := json.Unmarshal([]byte(sessions), &item.Sessions); err != nil {
			return ContextInfo{}, err
		}
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

// DeleteContext removes the context and every scope's retained review data.
// Stream heads intentionally survive so older observations remain stale.
func (store *Store) DeleteContext(ownerID, id string) error {
	transaction, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var anchor string
	err = transaction.QueryRow("SELECT anchor_diff_id FROM contexts WHERE owner_id=? AND id=?", ownerID, id).Scan(&anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = transaction.Exec("DELETE FROM diffs WHERE owner_id=? AND id<>? AND id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)", ownerID, anchor, id); err != nil {
		return err
	}
	if _, err = transaction.Exec("DELETE FROM diffs WHERE owner_id=? AND id=?", ownerID, anchor); err != nil {
		return err
	}
	return transaction.Commit()
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

func (store *Store) ContextCount(ownerID string, now time.Time) (int, error) {
	var count int
	err := store.db.QueryRow(`SELECT COUNT(*)
		FROM contexts c LEFT JOIN diffs d ON d.id=c.anchor_diff_id WHERE c.owner_id=? AND (d.expires_at IS NULL OR d.expires_at>?)`,
		ownerID, now.UnixMilli()).Scan(&count)
	return count, err
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
	return observationBinding(db, ownerID, id)
}

func (store *Store) ContextBinding(ownerID, id string, now time.Time) (Binding, error) {
	return bindingFor(store.db, ownerID, id, now)
}
