package reviewstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/servediff/internal/ingestion"
	"github.com/flexdinesh/servediff/internal/ingestionqueue"
)

func initializeQueue(tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS ingestion_records (sequence INTEGER PRIMARY KEY AUTOINCREMENT, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, submission_id TEXT NOT NULL, payload_hash TEXT NOT NULL, context_id TEXT NOT NULL DEFAULT '', UNIQUE(owner_id,source_id,submission_id))`,
		`INSERT INTO ingestion_records(owner_id,source_id,submission_id,payload_hash,context_id) SELECT owner_id,source_id,submission_id,payload_hash,context_id FROM observation_submissions WHERE true ORDER BY rowid ON CONFLICT DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS ingestion_jobs (id TEXT PRIMARY KEY, sequence INTEGER NOT NULL UNIQUE REFERENCES ingestion_records(sequence), payload TEXT NOT NULL, state TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, available_at INTEGER NOT NULL, lease_until INTEGER NOT NULL DEFAULT 0, lease_token TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '', context_id TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS ingestion_jobs_ready ON ingestion_jobs(state,available_at,lease_until)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	var columns int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('observation_stream_heads') WHERE name='arrival_sequence'").Scan(&columns); err != nil {
		return err
	}
	if columns == 0 {
		// Heads from the old synchronous path precede every newly accepted submission.
		if _, err := tx.Exec(`ALTER TABLE observation_stream_heads ADD COLUMN arrival_sequence INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func payloadBytes(request ingestion.Request) ([]byte, string, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

// recordSubmission is shared by direct commits and durable queue acceptance.
func recordSubmission(tx *sql.Tx, owner string, request ingestion.Request, hash string) (int64, string, error) {
	_, err := tx.Exec(`INSERT INTO ingestion_records(owner_id,source_id,submission_id,payload_hash) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, owner, request.Metadata.SourceID, request.SubmissionID, hash)
	if err != nil {
		return 0, "", err
	}
	var sequence int64
	var previousHash, id string
	err = tx.QueryRow(`SELECT sequence,payload_hash,context_id FROM ingestion_records WHERE owner_id=? AND source_id=? AND submission_id=?`, owner, request.Metadata.SourceID, request.SubmissionID).Scan(&sequence, &previousHash, &id)
	if err == nil && previousHash != hash {
		err = ErrSubmissionConflict
	}
	return sequence, id, err
}

// SQLiteQueue uses the same database owner as observations; it never opens a
// second connection pool or permits multiple processes to bypass store ownership.
type SQLiteQueue struct{ store *Store }

func (store *Store) Queue() *SQLiteQueue { return &SQLiteQueue{store: store} }

func (queue *SQLiteQueue) Accept(ctx context.Context, owner string, request ingestion.Request) (ingestionqueue.Job, error) {
	if err := ingestion.Validate(request); err != nil {
		return ingestionqueue.Job{}, err
	}
	raw, hash, err := payloadBytes(request)
	if err != nil {
		return ingestionqueue.Job{}, err
	}
	if len(raw) > ingestion.MaxRequestBytes {
		return ingestionqueue.Job{}, ingestionqueue.ErrFull
	}
	tx, err := queue.store.db.BeginTx(ctx, nil)
	if err != nil {
		return ingestionqueue.Job{}, err
	}
	defer tx.Rollback()
	sequence, contextID, err := recordSubmission(tx, owner, request, hash)
	if err != nil {
		return ingestionqueue.Job{}, err
	}
	job, err := scanJob(tx.QueryRow(jobSelect+` WHERE j.sequence=?`, sequence))
	if err == nil {
		return job, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	var count, bytes int64
	if err := tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(length(CAST(payload AS BLOB))),0) FROM ingestion_jobs WHERE state IN ('queued','running','retrying')`).Scan(&count, &bytes); err != nil {
		return job, err
	}
	if count >= 1024 || bytes+int64(len(raw)) > 256<<20 {
		return job, ingestionqueue.ErrFull
	}
	state := "queued"
	payload := string(raw)
	if contextID != "" {
		state, payload = "succeeded", ""
	}
	id := rand.Text()
	_, err = tx.Exec(`INSERT INTO ingestion_jobs(id,sequence,payload,state,available_at,context_id) VALUES(?,?,?,?,?,?)`, id, sequence, payload, state, time.Now().UnixMilli(), contextID)
	if err != nil {
		return job, err
	}
	job = ingestionqueue.Job{ID: id, SubmissionID: request.SubmissionID, State: state, ContextID: contextID}
	return job, tx.Commit()
}

const jobSelect = `SELECT j.id,r.submission_id,j.state,j.attempt,j.context_id,j.detail FROM ingestion_jobs j JOIN ingestion_records r ON r.sequence=j.sequence`

func scanJob(row *sql.Row) (ingestionqueue.Job, error) {
	var job ingestionqueue.Job
	err := row.Scan(&job.ID, &job.SubmissionID, &job.State, &job.Attempt, &job.ContextID, &job.Detail)
	return job, err
}

func (queue *SQLiteQueue) Get(ctx context.Context, owner, id string) (ingestionqueue.Job, error) {
	job, err := scanJob(queue.store.db.QueryRowContext(ctx, jobSelect+` WHERE r.owner_id=? AND j.id=?`, owner, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return job, err
}

func (queue *SQLiteQueue) Claim(ctx context.Context, duration time.Duration) (ingestionqueue.Delivery, error) {
	var delivery ingestionqueue.Delivery
	tx, err := queue.store.db.BeginTx(ctx, nil)
	if err != nil {
		return delivery, err
	}
	defer tx.Rollback()
	now := time.Now()
	var payload string
	err = tx.QueryRow(`SELECT j.id,r.owner_id,r.submission_id,j.payload,j.attempt FROM ingestion_jobs j JOIN ingestion_records r ON r.sequence=j.sequence WHERE (j.state IN ('queued','retrying') AND j.available_at<=?) OR (j.state='running' AND j.lease_until<=?) ORDER BY j.sequence LIMIT 1`, now.UnixMilli(), now.UnixMilli()).Scan(&delivery.ID, &delivery.OwnerID, &delivery.SubmissionID, &payload, &delivery.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery, ingestionqueue.ErrEmpty
	}
	if err != nil {
		return delivery, err
	}
	if err := json.Unmarshal([]byte(payload), &delivery.Request); err != nil {
		return delivery, err
	}
	delivery.Token, delivery.State, delivery.Attempt = rand.Text(), "running", delivery.Attempt+1
	_, err = tx.Exec(`UPDATE ingestion_jobs SET state='running',attempt=?,lease_token=?,lease_until=? WHERE id=?`, delivery.Attempt, delivery.Token, now.Add(duration).UnixMilli(), delivery.ID)
	if err != nil {
		return delivery, err
	}
	return delivery, tx.Commit()
}

func (queue *SQLiteQueue) update(ctx context.Context, delivery ingestionqueue.Delivery, statement string, args ...interface{}) error {
	args = append(args, delivery.ID, delivery.Token, time.Now().UnixMilli())
	result, err := queue.store.db.ExecContext(ctx, statement+` WHERE id=? AND lease_token=? AND state='running' AND lease_until>?`, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ingestionqueue.ErrLeaseLost
	}
	return err
}
func (queue *SQLiteQueue) Renew(ctx context.Context, delivery ingestionqueue.Delivery, duration time.Duration) error {
	return queue.update(ctx, delivery, `UPDATE ingestion_jobs SET lease_until=?`, time.Now().Add(duration).UnixMilli())
}
func (queue *SQLiteQueue) Complete(ctx context.Context, delivery ingestionqueue.Delivery, id string) error {
	err := queue.update(ctx, delivery, `UPDATE ingestion_jobs SET state='succeeded',context_id=?,payload='',detail='',lease_token=''`, id)
	if errors.Is(err, ingestionqueue.ErrLeaseLost) {
		job, readErr := queue.Get(ctx, delivery.OwnerID, delivery.ID)
		if readErr == nil && job.State == "succeeded" && job.ContextID == id {
			return nil
		}
	}
	return err
}
func (queue *SQLiteQueue) Retry(ctx context.Context, delivery ingestionqueue.Delivery, next time.Time, detail string) error {
	return queue.update(ctx, delivery, `UPDATE ingestion_jobs SET state='retrying',available_at=?,detail=?,lease_token=''`, next.UnixMilli(), detail)
}
func (queue *SQLiteQueue) Fail(ctx context.Context, delivery ingestionqueue.Delivery, detail string) error {
	return queue.update(ctx, delivery, `UPDATE ingestion_jobs SET state='failed',detail=?,payload='',lease_token=''`, detail)
}

var _ ingestionqueue.Queue = (*SQLiteQueue)(nil)
