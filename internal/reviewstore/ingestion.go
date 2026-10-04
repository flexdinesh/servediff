package reviewstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/review"
)

func initializeIngestion(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS observation_repositories (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, repository_key TEXT NOT NULL, UNIQUE(owner_id,repository_key))`,
		`CREATE TABLE IF NOT EXISTS observations (context_id TEXT PRIMARY KEY REFERENCES contexts(id) ON DELETE CASCADE, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, submission_id TEXT NOT NULL, payload_hash TEXT NOT NULL, repository_id TEXT REFERENCES observation_repositories(id), metadata TEXT NOT NULL, UNIQUE(owner_id,source_id,submission_id))`,
		`CREATE TABLE IF NOT EXISTS observation_scopes (context_id TEXT NOT NULL REFERENCES observations(context_id) ON DELETE CASCADE, mode TEXT NOT NULL, diff_id TEXT NOT NULL UNIQUE REFERENCES diffs(id) ON DELETE CASCADE, version_id TEXT NOT NULL REFERENCES diff_versions(id) ON DELETE CASCADE, PRIMARY KEY(context_id,mode))`,
		`CREATE INDEX IF NOT EXISTS observations_source ON observations(owner_id,source_id)`,
		`CREATE INDEX IF NOT EXISTS observations_branch ON observations(owner_id,json_extract(metadata,'$.branch'))`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// Ingest atomically commits an immutable observation and its retry identity.
// Checkout paths are labels; this method never accesses a producer filesystem.
func (store *Store) Ingest(ownerID string, request ingestion.Request) (Binding, error) {
	if request.SubmissionID == "" || request.Metadata.SourceID == "" {
		return Binding{}, errors.New("submission and source identity required")
	}
	if len(request.Scopes) == 0 {
		return Binding{}, errors.New("observation scopes required")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Binding{}, err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	tx, err := store.db.Begin()
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	var id, previousHash string
	err = tx.QueryRow(`SELECT context_id,payload_hash FROM observations WHERE owner_id=? AND source_id=? AND submission_id=?`, ownerID, request.Metadata.SourceID, request.SubmissionID).Scan(&id, &previousHash)
	if err == nil {
		if hash != previousHash {
			return Binding{}, ErrSubmissionConflict
		}
		return observationBinding(tx, ownerID, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Binding{}, err
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
	now := time.Now().UnixMilli()
	binding := Binding{ContextID: id, RepositoryID: repositoryID, DiffIDs: make(map[review.DiffMode]string)}
	// The all-scope diff anchors the legacy capture context; extra scopes have
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
		if _, err := tx.Exec(`INSERT INTO diffs(id,owner_id,kind,mode,created_at) VALUES(?,?,'observation',?,?)`, diffID, ownerID, snapshot.Mode, now); err != nil {
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
			binding.VersionID = versionID
		}
	}
	if err := putCaptureContext(tx, ownerID, id, request.Metadata.Root, now); err != nil {
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
	return binding, tx.Commit()
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
	if err := db.QueryRow(`SELECT repository_id FROM observations WHERE owner_id=? AND context_id=?`, ownerID, contextID).Scan(&binding.RepositoryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Binding{}, ErrNotFound
		}
		return Binding{}, err
	}
	rows, err := db.Query(`SELECT mode,diff_id,version_id FROM observation_scopes WHERE context_id=?`, contextID)
	if err != nil {
		return Binding{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var mode review.DiffMode
		var diffID, versionID string
		if err := rows.Scan(&mode, &diffID, &versionID); err != nil {
			return Binding{}, err
		}
		binding.DiffIDs[mode] = diffID
		binding.VersionID = versionID
	}
	if len(binding.DiffIDs) != 1 {
		binding.VersionID = ""
	}
	return binding, rows.Err()
}

func (store *Store) Observation(ownerID, contextID string) (ingestion.Metadata, error) {
	var raw string
	err := store.db.QueryRow(`SELECT metadata FROM observations WHERE owner_id=? AND context_id=?`, ownerID, contextID).Scan(&raw)
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
	err := store.db.QueryRow(`SELECT v.manifest FROM observations o JOIN observation_scopes s ON s.context_id=o.context_id JOIN diff_versions v ON v.id=s.version_id WHERE o.owner_id=? AND o.context_id=? AND s.mode=?`, ownerID, contextID, mode).Scan(&raw)
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
	err := store.db.QueryRow(`SELECT f.patch FROM observations o JOIN observation_scopes s ON s.context_id=o.context_id JOIN diff_files f ON f.version_id=s.version_id WHERE o.owner_id=? AND o.context_id=? AND s.mode=? AND f.file_id=?`, ownerID, contextID, mode, fileID).Scan(&raw)
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
	query := contextSelect + ` WHERE c.owner_id=? AND o.context_id IS NOT NULL`
	arguments := []any{ownerID}
	fields := []struct{ key, value string }{
		{"repositoryName", filter.Repository}, {"branch", filter.Branch}, {"worktreeName", filter.Worktree}, {"hostname", filter.Hostname}, {"sourceId", filter.SourceID}, {"runId", filter.RunID},
	}
	for _, field := range fields {
		if field.value != "" {
			query += ` AND json_extract(o.metadata,'$.` + field.key + `')=?`
			arguments = append(arguments, field.value)
		}
	}
	if filter.Query != "" {
		terms := []string{}
		for _, field := range []string{"repositoryName", "remoteUrl", "branch", "worktreeName", "hostname", "sourceId", "runId", "root"} {
			terms = append(terms, `instr(lower(COALESCE(json_extract(o.metadata,'$.`+field+`'),'')),lower(?))>0`)
			arguments = append(arguments, filter.Query)
		}
		query += ` AND (` + strings.Join(terms, " OR ") + `)`
	}
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
