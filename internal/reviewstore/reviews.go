package reviewstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewdata"
)

func (store *Store) reviewFor(transaction *sql.Tx, contextID string, mode review.DiffMode) (string, string, error) {
	var diffID, reviewID string
	err := transaction.QueryRow(`SELECT d.id, r.id FROM diffs d JOIN reviews r ON r.diff_id=d.id WHERE (d.id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)) AND d.mode=? AND (d.expires_at IS NULL OR d.expires_at>?)`, contextID, mode, time.Now().UnixMilli()).Scan(&diffID, &reviewID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return diffID, reviewID, err
}

func versionFor(transaction *sql.Tx, diffID, versionID string) error {
	if versionID == "" {
		return nil
	}
	var exists int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM diff_versions WHERE id=? AND diff_id=?`, versionID, diffID).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return errors.New("version belongs to a different diff")
	}
	return nil
}

func (store *Store) Comments(contextID string) ([]review.ReviewComment, error) {
	rows, err := store.db.Query(`SELECT c.data FROM comments c JOIN reviews r ON r.id=c.review_id JOIN diffs d ON d.id=r.diff_id WHERE (d.id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)) AND (d.expires_at IS NULL OR d.expires_at>?) ORDER BY c.created_at, c.id`, contextID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := make([]review.ReviewComment, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var comment review.ReviewComment
		if err := json.Unmarshal([]byte(raw), &comment); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (store *Store) PutComment(contextID string, comment review.ReviewComment) error {
	transaction, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	diffID, reviewID, err := store.reviewFor(transaction, contextID, comment.Scope)
	if err != nil {
		return err
	}
	if comment.DiffID != "" && comment.DiffID != diffID {
		return errors.New("comment belongs to a different diff")
	}
	if err := versionFor(transaction, diffID, comment.VersionID); err != nil {
		return err
	}
	comment.DiffID = diffID
	raw, err := json.Marshal(comment)
	if err != nil {
		return err
	}
	var versionID any
	if comment.VersionID != "" {
		versionID = comment.VersionID
	}
	result, err := transaction.Exec(`INSERT INTO comments(id, review_id, version_id, file_id, data, created_at) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET version_id=excluded.version_id, file_id=excluded.file_id, data=excluded.data WHERE comments.review_id=excluded.review_id`, comment.ID, reviewID, versionID, comment.Path, string(raw), int64(comment.CreatedAt))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("comment ID belongs to another review")
	}
	return transaction.Commit()
}

func (store *Store) ImportComments(contextID string, comments []review.ReviewComment) ([]review.ReviewComment, error) {
	for _, comment := range comments {
		if !comment.Valid() {
			return nil, errors.New("invalid comment")
		}
	}
	existing, err := store.Comments(contextID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(existing)+len(comments))
	for _, comment := range existing {
		seen[comment.ID] = true
	}
	for _, comment := range comments {
		// Imports preserve existing IDs and never overwrite local edits.
		if !seen[comment.ID] {
			if err := store.PutComment(contextID, comment); err != nil {
				return nil, err
			}
			seen[comment.ID] = true
		}
	}
	return store.Comments(contextID)
}

func (store *Store) DeleteComments(contextID string, selected map[string]bool) (int, error) {
	transaction, err := store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback()
	deleted := 0
	for id, include := range selected {
		if !include {
			continue
		}
		result, err := transaction.Exec(`DELETE FROM comments WHERE id=? AND review_id IN (SELECT r.id FROM reviews r JOIN diffs d ON d.id=r.diff_id WHERE d.id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?))`, id, contextID)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += int(count)
	}
	return deleted, transaction.Commit()
}

func (store *Store) DeleteComment(contextID, commentID string) (bool, error) {
	deleted, err := store.DeleteComments(contextID, map[string]bool{commentID: true})
	return deleted == 1, err
}

func (store *Store) Marks(contextID string, scope review.DiffMode) ([]review.ReviewMark, error) {
	rows, err := store.db.Query(`SELECT m.data FROM marks m JOIN reviews r ON r.id=m.review_id JOIN diffs d ON d.id=r.diff_id WHERE (d.id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)) AND d.mode=? AND (d.expires_at IS NULL OR d.expires_at>?) ORDER BY m.file_id`, contextID, scope, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marks := make([]review.ReviewMark, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var mark review.ReviewMark
		if err := json.Unmarshal([]byte(raw), &mark); err != nil {
			return nil, err
		}
		marks = append(marks, mark)
	}
	return marks, rows.Err()
}

func (store *Store) PutMark(contextID string, mark review.ReviewMark) error {
	transaction, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	diffID, reviewID, err := store.reviewFor(transaction, contextID, mark.Scope)
	if err != nil {
		return err
	}
	if mark.DiffID != "" && mark.DiffID != diffID {
		return errors.New("mark belongs to a different diff")
	}
	if err := versionFor(transaction, diffID, mark.VersionID); err != nil {
		return err
	}
	mark.DiffID = diffID
	raw, err := json.Marshal(mark)
	if err != nil {
		return err
	}
	var versionID any
	if mark.VersionID != "" {
		versionID = mark.VersionID
	}
	_, err = transaction.Exec(`INSERT INTO marks(review_id, file_id, version_id, data) VALUES(?, ?, ?, ?) ON CONFLICT(review_id, file_id) DO UPDATE SET version_id=excluded.version_id, data=excluded.data`, reviewID, mark.FileID, versionID, string(raw))
	if err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *Store) DeleteMark(contextID string, scope review.DiffMode, fileID string) error {
	_, err := store.db.Exec(`DELETE FROM marks WHERE file_id=? AND review_id IN (SELECT r.id FROM reviews r JOIN diffs d ON d.id=r.diff_id WHERE (d.id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)) AND d.mode=?)`, fileID, contextID, scope)
	return err
}

func (store *Store) ClearMarks(contextID string, scope review.DiffMode) error {
	_, err := store.db.Exec(`DELETE FROM marks WHERE review_id IN (SELECT r.id FROM reviews r JOIN diffs d ON d.id=r.diff_id WHERE (d.id IN (SELECT diff_id FROM observation_scopes WHERE context_id=?)) AND d.mode=?)`, contextID, scope)
	return err
}

func (store *Store) StoredVersion(ownerID, diffID, versionID string, now time.Time) (review.RepositoryDiff, error) {
	var raw string
	err := store.db.QueryRow(`SELECT v.manifest FROM diff_versions v JOIN diffs d ON d.id=v.diff_id WHERE d.owner_id=? AND d.id=? AND v.id=? AND (d.expires_at IS NULL OR d.expires_at>?)`, ownerID, diffID, versionID, now.UnixMilli()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return review.RepositoryDiff{}, ErrNotFound
	}
	if err != nil {
		return review.RepositoryDiff{}, err
	}
	var snapshot review.RepositoryDiff
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return review.RepositoryDiff{}, fmt.Errorf("invalid stored diff version: %w", err)
	}
	return snapshot, nil
}

func VersionID(diffID, revision string) string {
	return reviewdata.VersionID(diffID, revision)
}
