package health

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

const cacheHitWarningPct = 95.0

type bufferDetails struct {
	TableCacheHitPct float64 `json:"table_cache_hit_pct"`
	IndexCacheHitPct float64 `json:"index_cache_hit_pct"`
}

func runBufferHealth(ctx context.Context, d db.Querier) Result {
	tableRows, err := d.InternalQuery(ctx, `
		SELECT
			COALESCE(
				sum(heap_blks_hit)::float / NULLIF(sum(heap_blks_hit) + sum(heap_blks_read), 0) * 100,
			100) AS hit_pct
		FROM pg_statio_user_tables
	`)
	if err != nil {
		return Result{Check: CheckBuffer, Status: StatusError,
			Message: fmt.Sprintf("query pg_statio_user_tables failed: %v", err)}
	}

	idxRows, err := d.InternalQuery(ctx, `
		SELECT
			COALESCE(
				sum(idx_blks_hit)::float / NULLIF(sum(idx_blks_hit) + sum(idx_blks_read), 0) * 100,
			100) AS hit_pct
		FROM pg_statio_user_indexes
	`)
	if err != nil {
		return Result{Check: CheckBuffer, Status: StatusError,
			Message: fmt.Sprintf("query pg_statio_user_indexes failed: %v", err)}
	}

	det := bufferDetails{}
	if len(tableRows) > 0 {
		det.TableCacheHitPct, _ = db.ToFloat64(tableRows[0]["hit_pct"])
	}
	if len(idxRows) > 0 {
		det.IndexCacheHitPct, _ = db.ToFloat64(idxRows[0]["hit_pct"])
	}

	status := StatusOK
	msg := fmt.Sprintf("Buffer cache hit rates: tables=%.1f%%, indexes=%.1f%%.",
		det.TableCacheHitPct, det.IndexCacheHitPct)

	if det.TableCacheHitPct < cacheHitWarningPct || det.IndexCacheHitPct < cacheHitWarningPct {
		status = StatusWarning
		msg = fmt.Sprintf("Buffer cache hit rates are below %.0f%%: tables=%.1f%%, indexes=%.1f%%. "+
			"Consider increasing shared_buffers.", cacheHitWarningPct,
			det.TableCacheHitPct, det.IndexCacheHitPct)
	}

	return Result{Check: CheckBuffer, Status: status, Message: msg, Details: det}
}
