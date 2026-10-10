package reviewstore

import (
	"github.com/flexdinesh/diffx/internal/ingestion"
)

// HEAD and missing comparisons share legacy identity. Resolved commits belong
// to the snapshot, not the durable policy stream.
func comparisonPolicy(metadata ingestion.Metadata) string {
	return ingestion.ComparisonPolicy(metadata)
}

func comparisonPolicySQL(metadata string) string {
	return `CASE WHEN COALESCE(json_extract(` + metadata + `,'$.comparison.kind'),'working-tree')='working-tree' THEN '' ELSE json_extract(` + metadata + `,'$.comparison.kind')||':'||json_extract(` + metadata + `,'$.comparison.baseRef') END`
}
