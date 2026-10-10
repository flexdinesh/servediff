package reviewstore

import (
	"database/sql"

	"github.com/flexdinesh/diffx/internal/ingestion"
	"github.com/flexdinesh/diffx/internal/reviewdata"
)

// SessionAssociation records observation, never ownership or authorship.
type SessionAssociation = reviewdata.SessionAssociation

func initializeSessions(tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS agent_sessions (owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, harness TEXT NOT NULL, session_id TEXT NOT NULL, name TEXT NOT NULL, named_at INTEGER NOT NULL, PRIMARY KEY(owner_id,source_id,harness,session_id))`,
		`CREATE INDEX IF NOT EXISTS agent_sessions_name ON agent_sessions(owner_id,source_id,name)`,
		`CREATE TABLE IF NOT EXISTS observation_sessions (context_id TEXT NOT NULL REFERENCES observations(context_id) ON DELETE CASCADE, owner_id TEXT NOT NULL, source_id TEXT NOT NULL, harness TEXT NOT NULL, session_id TEXT NOT NULL, first_observed_at INTEGER NOT NULL, last_observed_at INTEGER NOT NULL, PRIMARY KEY(context_id,source_id,harness,session_id), FOREIGN KEY(owner_id,source_id,harness,session_id) REFERENCES agent_sessions(owner_id,source_id,harness,session_id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS observation_sessions_identity ON observation_sessions(owner_id,source_id,harness,session_id,context_id)`,
		`CREATE INDEX IF NOT EXISTS observation_submissions_context ON observation_submissions(context_id)`,
		`CREATE INDEX IF NOT EXISTS observation_submissions_run ON observation_submissions(owner_id,json_extract(metadata,'$.runId'),context_id)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func putSessionAssociation(tx *sql.Tx, ownerID, contextID string, metadata ingestion.Metadata) error {
	session := ingestion.AgentSession{Harness: metadata.Agent, ID: metadata.RunID}
	if metadata.AgentSession != nil {
		session = *metadata.AgentSession
	}
	if session.ID == "" || session.Harness == "" {
		return nil
	}
	namedAt := int64(0)
	if session.Name != "" {
		namedAt = metadata.CollectedAt
	}
	if _, err := tx.Exec(`INSERT INTO agent_sessions(owner_id,source_id,harness,session_id,name,named_at) VALUES(?,?,?,?,?,?) ON CONFLICT(owner_id,source_id,harness,session_id) DO UPDATE SET name=excluded.name,named_at=excluded.named_at WHERE excluded.name<>'' AND excluded.named_at>=agent_sessions.named_at`, ownerID, metadata.SourceID, session.Harness, session.ID, session.Name, namedAt); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO observation_sessions(context_id,owner_id,source_id,harness,session_id,first_observed_at,last_observed_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(context_id,source_id,harness,session_id) DO UPDATE SET first_observed_at=MIN(first_observed_at,excluded.first_observed_at),last_observed_at=MAX(last_observed_at,excluded.last_observed_at)`, contextID, ownerID, metadata.SourceID, session.Harness, session.ID, metadata.CollectedAt, metadata.CollectedAt)
	return err
}
