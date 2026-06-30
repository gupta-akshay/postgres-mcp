package topqueries

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// Method controls how queries are ranked.
type Method string

const (
	MethodTotalTime Method = "total_time"
	MethodMeanTime  Method = "mean_time"
	MethodResource  Method = "resource"
)

// QueryStat holds statistics for one query from pg_stat_statements.
type QueryStat struct {
	Query            string  `json:"query"`
	Calls            int64   `json:"calls"`
	TotalExecTimeMS  float64 `json:"total_exec_time_ms"`
	MeanExecTimeMS   float64 `json:"mean_exec_time_ms"`
	StddevExecTimeMS float64 `json:"stddev_exec_time_ms,omitempty"`
	Rows             int64   `json:"rows"`
	// Resource metrics (filled for MethodResource)
	SharedBlksHit  int64   `json:"shared_blks_hit,omitempty"`
	SharedBlksRead int64   `json:"shared_blks_read,omitempty"`
	WalBytes       int64   `json:"wal_bytes,omitempty"`
	PctTime        float64 `json:"pct_time,omitempty"`
	PctBlocks      float64 `json:"pct_blocks,omitempty"`
	PctWAL         float64 `json:"pct_wal,omitempty"`
}

// GetTopQueries returns the slowest or most resource-intensive queries.
// method is one of "total_time", "mean_time", or "resource".
func GetTopQueries(ctx context.Context, d db.Querier, method Method, limit int) ([]QueryStat, error) {
	// Verify pg_stat_statements is installed
	if err := db.RequireExtension(ctx, d, "pg_stat_statements"); err != nil {
		return nil, err
	}

	version, err := d.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("get server version: %w", err)
	}

	switch method {
	case MethodTotalTime:
		return queryByTime(ctx, d, version, "total", limit)
	case MethodMeanTime:
		return queryByTime(ctx, d, version, "mean", limit)
	case MethodResource:
		return queryByResource(ctx, d, version, limit)
	default:
		return nil, fmt.Errorf("unknown method %q; use total_time, mean_time, or resource", method)
	}
}

// ─── internal helpers ────────────────────────────────────────────────────────

// column name differs between PG12 and PG13+
func timeCol(version int, base string) string {
	if version >= 130000 {
		return base + "_exec_time"
	}
	return base + "_time"
}

func queryByTime(ctx context.Context, d db.Querier, version int, base string, limit int) ([]QueryStat, error) {
	totalCol := timeCol(version, "total")
	meanCol := timeCol(version, "mean")
	stddevCol := timeCol(version, "stddev")

	orderCol := totalCol
	if base == "mean" {
		orderCol = meanCol
	}

	sql := fmt.Sprintf(`
		SELECT
			query,
			calls,
			round(%s::numeric, 2)    AS total_exec_time_ms,
			round(%s::numeric, 2)    AS mean_exec_time_ms,
			round(%s::numeric, 2)    AS stddev_exec_time_ms,
			rows
		FROM pg_stat_statements
		WHERE query NOT LIKE $1
		ORDER BY %s DESC
		LIMIT $2
	`, totalCol, meanCol, stddevCol, orderCol)

	rows, err := d.QueryRows(ctx, sql, "%/* postgres-mcp */%", limit)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	return mapToStats(rows), nil
}

func queryByResource(ctx context.Context, d db.Querier, version int, limit int) ([]QueryStat, error) {
	totalCol := timeCol(version, "total")
	meanCol := timeCol(version, "mean")

	// WAL bytes column was added in PG13. On PG12, use literal 0 expressions
	// instead of column references — "s.0" is not valid SQL.
	var walBytesExpr, pctWalExpr, totalWalExpr string
	if version >= 130000 {
		walBytesExpr = "COALESCE(s.wal_bytes, 0)"
		pctWalExpr = "round((COALESCE(s.wal_bytes, 0) / NULLIF(t.total_wal, 0) * 100)::numeric, 2)"
		totalWalExpr = "sum(wal_bytes)"
	} else {
		walBytesExpr = "0"
		pctWalExpr = "0"
		totalWalExpr = "0"
	}

	sql := fmt.Sprintf(`
		WITH totals AS (
			SELECT
				sum(%s)                      AS total_time,
				sum(shared_blks_hit + shared_blks_read) AS total_blocks,
				%s                           AS total_wal
			FROM pg_stat_statements
		),
		ranked AS (
			SELECT
				s.query,
				s.calls,
				round(s.%s::numeric, 2)  AS total_exec_time_ms,
				round(s.%s::numeric, 2)  AS mean_exec_time_ms,
				s.rows,
				s.shared_blks_hit,
				s.shared_blks_read,
				%s                       AS wal_bytes,
				round((s.%s / NULLIF(t.total_time,   0) * 100)::numeric, 2) AS pct_time,
				round((100.0 * (s.shared_blks_hit + s.shared_blks_read) / NULLIF(t.total_blocks, 0))::numeric, 2) AS pct_blocks,
				%s                       AS pct_wal
			FROM pg_stat_statements s, totals t
			WHERE s.query NOT LIKE $1
		)
		SELECT * FROM ranked
		WHERE pct_time > 5 OR pct_blocks > 5 OR pct_wal > 5
		ORDER BY (pct_time + pct_blocks + COALESCE(pct_wal, 0)) DESC
		LIMIT $2
	`, totalCol, totalWalExpr, totalCol, meanCol, walBytesExpr, totalCol, pctWalExpr)

	rows, err := d.QueryRows(ctx, sql, "%/* postgres-mcp */%", limit)
	if err != nil {
		return nil, fmt.Errorf("query pg_stat_statements (resource): %w", err)
	}

	stats := mapToStats(rows)
	// fill resource-specific fields
	for i, r := range rows {
		if v, ok := r["shared_blks_hit"]; ok {
			f, _ := db.ToFloat64(v)
			stats[i].SharedBlksHit = int64(f)
		}
		if v, ok := r["shared_blks_read"]; ok {
			f, _ := db.ToFloat64(v)
			stats[i].SharedBlksRead = int64(f)
		}
		if v, ok := r["wal_bytes"]; ok {
			f, _ := db.ToFloat64(v)
			stats[i].WalBytes = int64(f)
		}
		if v, ok := r["pct_time"]; ok {
			f, _ := db.ToFloat64(v)
			stats[i].PctTime = f
		}
		if v, ok := r["pct_blocks"]; ok {
			f, _ := db.ToFloat64(v)
			stats[i].PctBlocks = f
		}
		if v, ok := r["pct_wal"]; ok {
			f, _ := db.ToFloat64(v)
			stats[i].PctWAL = f
		}
	}
	return stats, nil
}

func mapToStats(rows []map[string]any) []QueryStat {
	stats := make([]QueryStat, 0, len(rows))
	for _, r := range rows {
		s := QueryStat{
			Query: db.ToString(r["query"]),
		}
		if v, ok := r["calls"]; ok {
			f, _ := db.ToFloat64(v)
			s.Calls = int64(f)
		}
		if v, ok := r["total_exec_time_ms"]; ok {
			s.TotalExecTimeMS, _ = db.ToFloat64(v)
		}
		if v, ok := r["mean_exec_time_ms"]; ok {
			s.MeanExecTimeMS, _ = db.ToFloat64(v)
		}
		if v, ok := r["stddev_exec_time_ms"]; ok {
			s.StddevExecTimeMS, _ = db.ToFloat64(v)
		}
		if v, ok := r["rows"]; ok {
			f, _ := db.ToFloat64(v)
			s.Rows = int64(f)
		}
		stats = append(stats, s)
	}
	return stats
}
