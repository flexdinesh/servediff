package reviewstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/review"
)

func initializeIngestion(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS observation_repositories (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, repository_key TEXT NOT NULL, UNIQUE(owner_id,repository_key))`,
		`CREATE TABLE IF NOT EXISTS observations (context_id TEXT PRIMARY KEY REFERENCES contexts(id) ON DELETE CASCADE, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, submission_id TEXT NOT NULL, payload_hash TEXT NOT NULL, repository_id TEXT REFERENCES observation_repositories(id), metadata TEXT NOT NULL, UNIQUE(owner_id,source_id,submission_id))`,
		`CREATE TABLE IF NOT EXISTS observation_scopes (context_id TEXT NOT NULL REFERENCES observations(context_id) ON DELETE CASCADE, mode TEXT NOT NULL, diff_id TEXT NOT NULL UNIQUE REFERENCES diffs(id) ON DELETE CASCADE, version_id TEXT NOT NULL REFERENCES diff_versions(id) ON DELETE CASCADE, PRIMARY KEY(context_id,mode))`,
		`CREATE TABLE IF NOT EXISTS observation_submissions (owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, submission_id TEXT NOT NULL, context_id TEXT NOT NULL REFERENCES observations(context_id) ON DELETE CASCADE, payload_hash TEXT NOT NULL, metadata TEXT NOT NULL, PRIMARY KEY(owner_id,source_id,submission_id))`,
		// Stream heads outlive snapshot retention. Backfill newest arrivals first;
		// an existing head must never regress when its snapshot has been pruned.
		`CREATE TABLE IF NOT EXISTS observation_stream_heads (owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, repository_key TEXT NOT NULL, checkout_key TEXT NOT NULL, branch TEXT NOT NULL, comparison_policy TEXT NOT NULL DEFAULT '', context_id TEXT NOT NULL, collected_at INTEGER NOT NULL, arrival_sequence INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(owner_id,source_id,repository_key,checkout_key,branch,comparison_policy))`,
		`CREATE TABLE IF NOT EXISTS observation_identities (owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, identity_hash TEXT NOT NULL, context_id TEXT NOT NULL REFERENCES observations(context_id) ON DELETE CASCADE, PRIMARY KEY(owner_id,identity_hash))`,
		`CREATE INDEX IF NOT EXISTS observations_source ON observations(owner_id,source_id)`,
		`CREATE INDEX IF NOT EXISTS observations_branch ON observations(owner_id,json_extract(metadata,'$.branch'))`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return initializeSessions(tx)
}

// Ingest atomically commits an immutable observation and its retry identity.
// Checkout paths are labels; this method never accesses a producer filesystem.
func (store *Store) Ingest(ownerID string, request ingestion.Request) (Binding, error) {
	return store.IngestContext(context.Background(), ownerID, request)
}
func (store *Store) IngestContext(ctx context.Context, ownerID string, request ingestion.Request) (Binding, error) {
	if request.SubmissionID == "" || request.Metadata.SourceID == "" {
		return Binding{}, errors.New("submission and source identity required")
	}
	if len(request.Scopes) == 0 {
		return Binding{}, errors.New("observation scopes required")
	}
	_, hash, err := payloadBytes(request)
	if err != nil {
		return Binding{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	sequence, id, err := recordSubmission(tx, ownerID, request, hash)
	if err != nil {
		return Binding{}, err
	}
	if id != "" {
		binding, err := observationBinding(tx, ownerID, id)
		if err != nil {
			return Binding{}, err
		}
		return binding, tx.Commit()
	}
	now := time.Now().UnixMilli()
	branch := branchIdentity(request.Metadata)
	identityRequest := request
	identityRequest.Metadata.BranchID = branch
	identity, err := observationIdentity(identityRequest)
	if err != nil {
		return Binding{}, err
	}
	if identity != "" {
		err = tx.QueryRow(`SELECT i.context_id FROM observation_identities i JOIN contexts c ON c.id=i.context_id JOIN diffs d ON d.id=c.anchor_diff_id WHERE i.owner_id=? AND i.identity_hash=? AND d.expires_at>?`, ownerID, identity, now).Scan(&id)

		if err == nil {
			if _, err := tx.Exec(`UPDATE contexts SET last_submitted_at=? WHERE id=?`, now, id); err != nil {
				return Binding{}, err
			}
			if _, err := tx.Exec(`UPDATE diffs SET expires_at=? WHERE id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)`, now+store.retention.Milliseconds(), id); err != nil {
				return Binding{}, err
			}
			if err := putObservationSubmission(tx, ownerID, id, hash, request, sequence); err != nil {
				return Binding{}, err
			}
			binding, err := observationBinding(tx, ownerID, id)
			if err != nil {
				return Binding{}, err
			}
			return binding, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Binding{}, err
		}
		// Expired identity may still await pruning. It must not hide a new review.
		if _, err := tx.Exec(`DELETE FROM observation_identities WHERE owner_id=? AND identity_hash=?`, ownerID, identity); err != nil {
			return Binding{}, err
		}
	}
	id, err = newID()
	if err != nil {
		return Binding{}, err
	}
	var repositoryID *string
	if request.Metadata.RepositoryKey != "" {
		repoID, err := newID()
		if err != nil {
			return Binding{}, err
		}
		if _, err := tx.Exec(`INSERT INTO observation_repositories(id,owner_id,repository_key) VALUES(?,?,?) ON CONFLICT(owner_id,repository_key) DO NOTHING`, repoID, ownerID, request.Metadata.RepositoryKey); err != nil {
			return Binding{}, err
		}
		if err := tx.QueryRow(`SELECT id FROM observation_repositories WHERE owner_id=? AND repository_key=?`, ownerID, request.Metadata.RepositoryKey).Scan(&repoID); err != nil {
			return Binding{}, err
		}
		repositoryID = &repoID
	}
	binding := Binding{ContextID: id, RepositoryID: repositoryID, DiffIDs: make(map[review.DiffMode]string)}
	// The all-scope diff anchors the observation; extra scopes have
	// independent review identities associated through observation_scopes.
	seen := make(map[review.DiffMode]bool)
	for _, scope := range request.Scopes {
		if _, err := review.ParseDiffMode(string(scope.Snapshot.Mode)); err != nil {
			return Binding{}, err
		}
		if seen[scope.Snapshot.Mode] {
			return Binding{}, errors.New("duplicate observation scope")
		}
		seen[scope.Snapshot.Mode] = true
	}
	if !seen[review.DiffAll] {
		return Binding{}, errors.New("all observation scope required")
	}
	for _, scope := range request.Scopes {
		snapshot := scope.Snapshot
		diffID := id
		if snapshot.Mode != review.DiffAll {
			diffID, err = newID()
			if err != nil {
				return Binding{}, err
			}
		}
		versionID, err := newID()
		if err != nil {
			return Binding{}, err
		}
		if _, err := tx.Exec(`INSERT INTO diffs(id,owner_id,kind,mode,created_at,expires_at) VALUES(?,?,'observation',?,?,?)`, diffID, ownerID, snapshot.Mode, now, now+store.retention.Milliseconds()); err != nil {
			return Binding{}, err
		}
		snapshot.ID, snapshot.VersionID = diffID, versionID
		snapshot.LocationID, snapshot.RepositoryID = nil, repositoryID
		if err := pinObservationVersion(tx, snapshot, scope.Patches, now); err != nil {
			return Binding{}, err
		}
		if err := ensureReview(tx, ownerID, diffID); err != nil {
			return Binding{}, err
		}
		binding.DiffIDs[snapshot.Mode] = diffID
		if len(request.Scopes) == 1 {
		}
	}
	if err := putObservationContext(tx, ownerID, id, request.Metadata.Root, now); err != nil {
		return Binding{}, err
	}
	metadata, err := json.Marshal(request.Metadata)
	if err != nil {
		return Binding{}, err
	}
	if _, err := tx.Exec(`INSERT INTO observations(context_id,owner_id,source_id,submission_id,payload_hash,repository_id,metadata) VALUES(?,?,?,?,?,?,?)`, id, ownerID, request.Metadata.SourceID, request.SubmissionID, hash, repositoryID, string(metadata)); err != nil {
		return Binding{}, err
	}
	for mode, diffID := range binding.DiffIDs {
		if _, err := tx.Exec(`INSERT INTO observation_scopes(context_id,mode,diff_id,version_id) SELECT ?,?,id,(SELECT id FROM diff_versions WHERE diff_id=diffs.id) FROM diffs WHERE id=?`, id, mode, diffID); err != nil {
			return Binding{}, err
		}
	}
	if err := putObservationSubmission(tx, ownerID, id, hash, request, sequence); err != nil {
		return Binding{}, err
	}
	if identity != "" {
		if _, err := tx.Exec(`INSERT INTO observation_identities(owner_id,identity_hash,context_id) VALUES(?,?,?)`, ownerID, identity, id); err != nil {
			return Binding{}, err
		}
	}
	return binding, tx.Commit()
}

// Missing hashes from older producers dedupe only known empty snapshots.
func observationIdentity(request ingestion.Request) (string, error) {
	return ingestion.ContentIdentity(request)
}

func putObservationSubmission(tx *sql.Tx, ownerID, contextID, hash string, request ingestion.Request, sequence int64) error {
	metadata, err := json.Marshal(request.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO observation_submissions(owner_id,source_id,submission_id,context_id,payload_hash,metadata) VALUES(?,?,?,?,?,?)`, ownerID, request.Metadata.SourceID, request.SubmissionID, contextID, hash, string(metadata))
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE ingestion_records SET context_id=? WHERE sequence=?", contextID, sequence); err != nil {
		return err
	}
	if err := putSessionAssociation(tx, ownerID, contextID, request.Metadata); err != nil {
		return err
	}
	if request.Metadata.RepositoryKey == "" || request.Metadata.CheckoutKey == "" {
		return err
	}
	branch := branchIdentity(request.Metadata)
	// Equal collection times use arrival order. Retries bypass this write.
	_, err = tx.Exec(`INSERT INTO observation_stream_heads(owner_id,source_id,repository_key,checkout_key,branch,comparison_policy,context_id,collected_at,arrival_sequence) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(owner_id,source_id,repository_key,checkout_key,branch,comparison_policy) DO UPDATE SET context_id=excluded.context_id,collected_at=excluded.collected_at,arrival_sequence=excluded.arrival_sequence
		WHERE excluded.collected_at>observation_stream_heads.collected_at OR (excluded.collected_at=observation_stream_heads.collected_at AND excluded.arrival_sequence>observation_stream_heads.arrival_sequence)`, ownerID, request.Metadata.SourceID, request.Metadata.RepositoryKey, request.Metadata.CheckoutKey, branch, comparisonPolicy(request.Metadata), contextID, request.Metadata.CollectedAt, sequence)
	return err
}

func pinObservationVersion(tx *sql.Tx, snapshot review.RepositoryDiff, patches map[string]review.FilePatch, now int64) error {
	manifest, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO diff_versions(id,diff_id,revision,manifest,created_at) VALUES(?,?,?,?,?)`, snapshot.VersionID, snapshot.ID, snapshot.Revision, string(manifest), now); err != nil {
		return err
	}
	for _, file := range snapshot.Files {
		patch, exists := patches[file.ID]
		if !exists {
			return errors.New("observation file patch missing")
		}
		encoded, err := json.Marshal(patch)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO diff_files(version_id,file_id,path,fingerprint,patch) VALUES(?,?,?,?,?)`, snapshot.VersionID, file.ID, file.Path, file.Fingerprint, string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func observationBinding(db queryer, ownerID, contextID string) (Binding, error) {
	binding := Binding{ContextID: contextID, DiffIDs: make(map[review.DiffMode]string)}
	if err := db.QueryRow(`SELECT o.repository_id FROM observations o JOIN contexts c ON c.id=o.context_id JOIN diffs d ON d.id=c.anchor_diff_id WHERE o.owner_id=? AND o.context_id=? AND d.expires_at>?`, ownerID, contextID, time.Now().UnixMilli()).Scan(&binding.RepositoryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Binding{}, ErrNotFound
		}
		return Binding{}, err
	}
	rows, err := db.Query(`SELECT mode,diff_id FROM observation_scopes WHERE context_id=?`, contextID)
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

func (store *Store) Observation(ownerID, contextID string) (ingestion.Metadata, error) {
	var raw string
	err := store.db.QueryRow(`SELECT o.metadata FROM observations o JOIN contexts c ON c.id=o.context_id JOIN diffs d ON d.id=c.anchor_diff_id WHERE o.owner_id=? AND o.context_id=? AND d.expires_at>?`, ownerID, contextID, time.Now().UnixMilli()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ingestion.Metadata{}, ErrNotFound
	}
	if err != nil {
		return ingestion.Metadata{}, err
	}
	var metadata ingestion.Metadata
	err = json.Unmarshal([]byte(raw), &metadata)
	return metadata, err
}

func (store *Store) ObservationSnapshot(ownerID, contextID string, mode review.DiffMode) (review.RepositoryDiff, error) {
	var raw string
	err := store.db.QueryRow(`SELECT v.manifest FROM observations o JOIN observation_scopes s ON s.context_id=o.context_id JOIN diff_versions v ON v.id=s.version_id JOIN diffs d ON d.id=s.diff_id WHERE o.owner_id=? AND o.context_id=? AND s.mode=? AND d.expires_at>?`, ownerID, contextID, mode, time.Now().UnixMilli()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return review.RepositoryDiff{}, ErrNotFound
	}
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	var snapshot review.RepositoryDiff
	err = json.Unmarshal([]byte(raw), &snapshot)
	return snapshot, err
}

func (store *Store) ObservationPatch(ownerID, contextID string, mode review.DiffMode, fileID string) (review.FilePatch, error) {
	var raw string
	err := store.db.QueryRow(`SELECT f.patch FROM observations o JOIN observation_scopes s ON s.context_id=o.context_id JOIN diff_files f ON f.version_id=s.version_id JOIN diffs d ON d.id=s.diff_id WHERE o.owner_id=? AND o.context_id=? AND s.mode=? AND f.file_id=? AND d.expires_at>?`, ownerID, contextID, mode, fileID, time.Now().UnixMilli()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return review.FilePatch{}, ErrNotFound
	}
	if err != nil {
		return review.FilePatch{}, err
	}
	var patch review.FilePatch
	err = json.Unmarshal([]byte(raw), &patch)
	return patch, err
}

// ObservationContexts filters immutable observations using captured metadata.
func (store *Store) ObservationContexts(ownerID string, limit int, beforeTime int64, beforeID string, filter ingestion.Filter) ([]ContextInfo, error) {
	query := contextSelect + ` WHERE c.owner_id=? AND o.context_id IS NOT NULL AND d.expires_at>?`
	arguments := []any{ownerID, time.Now().UnixMilli()}
	// All predicates match one collection, rather than mixing facts from
	// unrelated submissions of the same immutable snapshot.
	query += ` AND EXISTS (SELECT 1 FROM observation_submissions p
		LEFT JOIN agent_sessions session ON session.owner_id=p.owner_id AND session.source_id=p.source_id
		AND session.harness=COALESCE(json_extract(p.metadata,'$.agentSession.harness'),json_extract(p.metadata,'$.agent'))
		AND session.session_id=COALESCE(json_extract(p.metadata,'$.agentSession.id'),json_extract(p.metadata,'$.runId'))
		WHERE p.owner_id=c.owner_id AND p.context_id=c.id`
	fields := []struct{ expression, value string }{
		{"json_extract(p.metadata,'$.repositoryName')", filter.Repository},
		{"json_extract(p.metadata,'$.branch')", filter.Branch},
		{"json_extract(p.metadata,'$.worktreeName')", filter.Worktree},
		{"json_extract(p.metadata,'$.hostname')", filter.Hostname},
		{"p.source_id", filter.SourceID},
		{"json_extract(p.metadata,'$.runId')", filter.RunID},
		{"session.harness", filter.Harness},
		{"session.session_id", filter.SessionID},
		{"session.name", filter.SessionName},
	}
	for _, field := range fields {
		if field.value != "" {
			query += ` AND ` + field.expression + `=?`
			arguments = append(arguments, field.value)
		}
	}
	if filter.Query != "" {
		terms := []string{}
		for _, field := range []string{"repositoryName", "remoteUrl", "branch", "worktreeName", "hostname", "sourceId", "runId", "root", "triggerRoot", "agent"} {
			terms = append(terms, `instr(lower(COALESCE(json_extract(p.metadata,'$.`+field+`'),'')),lower(?))>0`)
			arguments = append(arguments, filter.Query)
		}
		for _, field := range []string{"harness", "session_id", "name"} {
			terms = append(terms, `instr(lower(COALESCE(session.`+field+`,'')),lower(?))>0`)
			arguments = append(arguments, filter.Query)
		}
		query += ` AND (` + strings.Join(terms, " OR ") + `)`
	}
	query += `)`
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
	result := make([]ContextInfo, 0)
	for rows.Next() {
		item, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func branchIdentity(metadata ingestion.Metadata) string {
	if metadata.BranchID != "" {
		return metadata.BranchID
	}
	return metadata.Branch
}
