package health

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

type indexHealthDetails struct {
	Invalid   []invalidIndex   `json:"invalid,omitempty"`
	Duplicate []duplicateIndex `json:"duplicate,omitempty"`
	Bloated   []bloatedIndex   `json:"bloated,omitempty"`
	Unused    []unusedIndex    `json:"unused,omitempty"`
}

type invalidIndex struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Index    string `json:"index"`
	IndexDef string `json:"index_def"`
}

type duplicateIndex struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Index1 string `json:"index1"`
	Index2 string `json:"index2"`
	Def    string `json:"definition"`
}

type bloatedIndex struct {
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	Index     string `json:"index"`
	IndexSize string `json:"index_size"`
}

type unusedIndex struct {
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	Index     string `json:"index"`
	Scans     int64  `json:"scans"`
	IndexSize string `json:"index_size"`
}

const (
	bloatThresholdBytes int64 = 100 * 1024 * 1024 // 100 MB
	unusedScanThreshold int64 = 50
)

func runIndexHealth(ctx context.Context, d db.Querier) Result {
	det := indexHealthDetails{}

	det.Invalid, _ = invalidIndexes(ctx, d)
	det.Duplicate, _ = duplicateIndexes(ctx, d)
	det.Bloated, _ = bloatedIndexes(ctx, d)
	det.Unused, _ = unusedIndexes(ctx, d)

	problems := len(det.Invalid) + len(det.Duplicate) + len(det.Bloated) + len(det.Unused)

	status := StatusOK
	msg := "No index health issues found."

	if problems > 0 {
		status = StatusWarning
		parts := []string{}
		if n := len(det.Invalid); n > 0 {
			parts = append(parts, fmt.Sprintf("%d invalid", n))
		}
		if n := len(det.Duplicate); n > 0 {
			parts = append(parts, fmt.Sprintf("%d duplicate", n))
		}
		if n := len(det.Bloated); n > 0 {
			parts = append(parts, fmt.Sprintf("%d bloated (>100MB)", n))
		}
		if n := len(det.Unused); n > 0 {
			parts = append(parts, fmt.Sprintf("%d unused (<50 scans)", n))
		}
		msg = fmt.Sprintf("Index issues found: %s.", joinEnglish(parts))
		if len(det.Invalid) > 0 {
			status = StatusCritical
		}
	}

	return Result{Check: CheckIndex, Status: status, Message: msg, Details: det}
}

func invalidIndexes(ctx context.Context, d db.Querier) ([]invalidIndex, error) {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			n.nspname                    AS schema,
			c.relname                    AS table_name,
			i.relname                    AS index_name,
			pg_get_indexdef(i.oid)       AS index_def
		FROM pg_index x
		JOIN pg_class     c ON c.oid = x.indrelid
		JOIN pg_class     i ON i.oid = x.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE NOT x.indisvalid
		  AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		ORDER BY n.nspname, c.relname
	`)
	if err != nil {
		return nil, err
	}
	out := make([]invalidIndex, 0, len(rows))
	for _, r := range rows {
		out = append(out, invalidIndex{
			Schema:   db.ToString(r["schema"]),
			Table:    db.ToString(r["table_name"]),
			Index:    db.ToString(r["index_name"]),
			IndexDef: db.ToString(r["index_def"]),
		})
	}
	return out, nil
}

func duplicateIndexes(ctx context.Context, d db.Querier) ([]duplicateIndex, error) {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			s1.schemaname   AS schema,
			s1.relname      AS table_name,
			s1.indexrelname AS index1,
			s2.indexrelname AS index2,
			pg_get_indexdef(s1.indexrelid) AS definition
		FROM pg_stat_user_indexes s1
		JOIN pg_stat_user_indexes s2
			ON  s1.relid = s2.relid
			AND s1.indexrelid < s2.indexrelid
		JOIN pg_index i1 ON i1.indexrelid = s1.indexrelid
		JOIN pg_index i2 ON i2.indexrelid = s2.indexrelid
		WHERE i1.indkey::text        = i2.indkey::text
		  AND i1.indpred             IS NOT DISTINCT FROM i2.indpred
		  AND i1.indexprs            IS NOT DISTINCT FROM i2.indexprs
		  AND i1.indclass::text      = i2.indclass::text
		  AND i1.indoption::text     = i2.indoption::text
		  AND i1.indcollation::text  = i2.indcollation::text
		ORDER BY s1.schemaname, s1.relname
	`)
	if err != nil {
		return nil, err
	}
	out := make([]duplicateIndex, 0, len(rows))
	for _, r := range rows {
		out = append(out, duplicateIndex{
			Schema: db.ToString(r["schema"]),
			Table:  db.ToString(r["table_name"]),
			Index1: db.ToString(r["index1"]),
			Index2: db.ToString(r["index2"]),
			Def:    db.ToString(r["definition"]),
		})
	}
	return out, nil
}

func bloatedIndexes(ctx context.Context, d db.Querier) ([]bloatedIndex, error) {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			schemaname                        AS schema,
			tablename                         AS table_name,
			indexname                         AS index_name,
			pg_size_pretty(pg_relation_size(indexrelid)) AS index_size,
			pg_relation_size(indexrelid)      AS size_bytes
		FROM pg_stat_user_indexes
		WHERE pg_relation_size(indexrelid) > $1
		ORDER BY pg_relation_size(indexrelid) DESC
	`, bloatThresholdBytes)
	if err != nil {
		return nil, err
	}
	out := make([]bloatedIndex, 0, len(rows))
	for _, r := range rows {
		out = append(out, bloatedIndex{
			Schema:    db.ToString(r["schema"]),
			Table:     db.ToString(r["table_name"]),
			Index:     db.ToString(r["index_name"]),
			IndexSize: db.ToString(r["index_size"]),
		})
	}
	return out, nil
}

func unusedIndexes(ctx context.Context, d db.Querier) ([]unusedIndex, error) {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			s.schemaname                             AS schema,
			s.relname                                AS table_name,
			s.indexrelname                           AS index_name,
			s.idx_scan                               AS scans,
			pg_size_pretty(pg_relation_size(s.indexrelid)) AS index_size
		FROM pg_stat_user_indexes s
		JOIN pg_index i ON i.indexrelid = s.indexrelid
		WHERE s.idx_scan < $1
		  AND NOT i.indisprimary
		  AND NOT i.indisunique
		  AND NOT EXISTS (
			  SELECT 1 FROM pg_constraint c
			  WHERE c.conindid = s.indexrelid
		  )
		ORDER BY pg_relation_size(s.indexrelid) DESC
	`, unusedScanThreshold)
	if err != nil {
		return nil, err
	}
	out := make([]unusedIndex, 0, len(rows))
	for _, r := range rows {
		scansF, _ := db.ToFloat64(r["scans"])
		out = append(out, unusedIndex{
			Schema:    db.ToString(r["schema"]),
			Table:     db.ToString(r["table_name"]),
			Index:     db.ToString(r["index_name"]),
			Scans:     int64(scansF),
			IndexSize: db.ToString(r["index_size"]),
		})
	}
	return out, nil
}
