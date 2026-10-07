package reviewstore

import (
	"database/sql"
	"github.com/flexdinesh/servediff/internal/ingestion"
)

// HEAD and missing comparisons share legacy identity. Resolved commits belong
// to the snapshot, not the durable policy stream.
func comparisonPolicy(metadata ingestion.Metadata) string {
	if metadata.Comparison == nil || metadata.Comparison.Kind == "working-tree" {
		return ""
	}
	return metadata.Comparison.Kind + ":" + metadata.Comparison.BaseRef
}

func comparisonPolicySQL(metadata string) string {
	return `CASE WHEN COALESCE(json_extract(` + metadata + `,'$.comparison.kind'),'working-tree')='working-tree' THEN '' ELSE json_extract(` + metadata + `,'$.comparison.kind')||':'||json_extract(` + metadata + `,'$.comparison.baseRef') END`
}

func migrateObservationHeads(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(observation_stream_heads)`)
	if err != nil {
		return err
	}
	existing, migrated := false, false
	for rows.Next() {
		var index, required, primary int
		var name, kind string
		var defaultValue *string
		if err := rows.Scan(&index, &name, &kind, &required, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		existing = true
		migrated = migrated || name == "comparison_policy"
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !existing || migrated {
		return nil
	}
	for _, statement := range []string{
		`ALTER TABLE observation_stream_heads RENAME TO legacy_observation_stream_heads`,
		`CREATE TABLE observation_stream_heads (owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id TEXT NOT NULL, repository_key TEXT NOT NULL, checkout_key TEXT NOT NULL, branch TEXT NOT NULL, comparison_policy TEXT NOT NULL DEFAULT '', context_id TEXT NOT NULL, collected_at INTEGER NOT NULL, PRIMARY KEY(owner_id,source_id,repository_key,checkout_key,branch,comparison_policy))`,
		`INSERT INTO observation_stream_heads SELECT owner_id,source_id,repository_key,checkout_key,branch,'',context_id,collected_at FROM legacy_observation_stream_heads`,
		`DROP TABLE legacy_observation_stream_heads`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
