package reviewstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
)

type Repository struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Root            string `json:"root"`
	LastSubmittedAt int64  `json:"lastSubmittedAt"`
}

func (store *Store) Repositories(ownerID string) ([]Repository, error) {
	rows, err := store.db.Query("SELECT r.id,d.name,d.root,d.submitted_at FROM repository_details d JOIN repositories r ON r.id=d.repository_id WHERE r.owner_id=? ORDER BY d.submitted_at DESC,r.id", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Repository, 0)
	for rows.Next() {
		var item Repository
		if err := rows.Scan(&item.ID, &item.Name, &item.Root, &item.LastSubmittedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) Repository(ownerID, id string) (Repository, error) {
	items, err := store.Repositories(ownerID)
	if err != nil {
		return Repository{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Repository{}, ErrNotFound
}

func (store *Store) RepositoryByCommonDir(ownerID, commonDir string) (string, error) {
	var id string
	err := store.db.QueryRow("SELECT r.id FROM repositories r JOIN repository_details d ON d.repository_id=r.id WHERE r.owner_id=? AND r.common_dir=?", ownerID, commonDir).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (store *Store) RepositoryContexts(ownerID, id string) ([]ContextInfo, error) {
	if _, err := store.Repository(ownerID, id); err != nil {
		return nil, err
	}
	rows, err := store.db.Query(contextSelect+" WHERE c.owner_id=? AND l.repository_id=? ORDER BY c.last_submitted_at DESC,c.id", ownerID, id)
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

func (store *Store) WorktreeMetadata(id, name, branch string, worktreeName *string) error {
	_, err := store.db.Exec("INSERT INTO context_metadata(context_id,name,branch,worktree_name) VALUES(?,?,?,?) ON CONFLICT(context_id) DO UPDATE SET name=excluded.name,branch=excluded.branch,worktree_name=excluded.worktree_name", id, name, branch, worktreeName)
	return err
}

func (store *Store) RepositoryName(id, name string) error {
	_, err := store.db.Exec("UPDATE repository_details SET name=? WHERE repository_id=?", name, id)
	return err
}

func (store *Store) MarkChanged(id string) (int64, error) {
	var generation int64
	err := store.db.QueryRow("INSERT INTO context_metadata(context_id,generation) VALUES(?,1) ON CONFLICT(context_id) DO UPDATE SET generation=generation+1 RETURNING generation", id).Scan(&generation)
	return generation, err
}

// Publish exposes a version only after its manifest and previews have committed together.
func (store *Store) Publish(snapshot review.RepositoryDiff, previews map[string]review.FilePatch, generation int64) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := pinVersion(tx, snapshot, previews); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	var previous string
	if err := tx.QueryRow("SELECT version_id FROM current_versions WHERE diff_id=?", snapshot.ID).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec("INSERT INTO current_versions VALUES(?,?,?,?) ON CONFLICT(diff_id) DO UPDATE SET version_id=excluded.version_id,collected_at=excluded.collected_at,generation=excluded.generation", snapshot.ID, snapshot.VersionID, now, generation); err != nil {
		return err
	}
	if snapshot.LocationID != nil {
		if _, err := tx.Exec("INSERT INTO context_metadata(context_id,branch,last_changed_at) VALUES(?,?,?) ON CONFLICT(context_id) DO UPDATE SET branch=excluded.branch,last_changed_at=CASE WHEN ? THEN excluded.last_changed_at ELSE context_metadata.last_changed_at END", *snapshot.LocationID, snapshot.Branch, now, previous != snapshot.VersionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *Store) CurrentVersion(ownerID, diffID string) (review.RepositoryDiff, error) {
	var raw string
	err := store.db.QueryRow("SELECT v.manifest FROM current_versions c JOIN diff_versions v ON v.id=c.version_id JOIN diffs d ON d.id=c.diff_id WHERE d.owner_id=? AND d.id=?", ownerID, diffID).Scan(&raw)
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

func (store *Store) Preview(ownerID, diffID, fileID, fingerprint string) (review.FilePatch, error) {
	var raw string
	err := store.db.QueryRow("SELECT f.patch FROM diff_files f JOIN diff_versions v ON v.id=f.version_id JOIN diffs d ON d.id=v.diff_id WHERE d.owner_id=? AND d.id=? AND f.file_id=? AND f.fingerprint=? ORDER BY v.created_at DESC LIMIT 1", ownerID, diffID, fileID, fingerprint).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return review.FilePatch{}, ErrNotFound
	}
	if err != nil {
		return review.FilePatch{}, err
	}
	var preview review.FilePatch
	err = json.Unmarshal([]byte(raw), &preview)
	return preview, err
}

// Retain current/reviewed versions, bounding unreferenced history per live diff.
func (store *Store) PruneLiveVersions(diffID string) error {
	_, err := store.db.Exec(`DELETE FROM diff_versions WHERE diff_id=? AND id NOT IN (SELECT version_id FROM current_versions) AND id NOT IN (SELECT version_id FROM comments WHERE version_id IS NOT NULL) AND id NOT IN (SELECT version_id FROM marks WHERE version_id IS NOT NULL) AND id NOT IN (SELECT id FROM diff_versions WHERE diff_id=? ORDER BY created_at DESC LIMIT 5)`, diffID, diffID)
	return err
}
