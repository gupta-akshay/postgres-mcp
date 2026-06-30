package health

import (
	"context"
	"fmt"
	"time"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

const xidWarningThreshold = 200_000_000 // 200M remaining XIDs before wraparound

type vacuumDetails struct {
	Tables []tableVacuumInfo `json:"tables"`
}

type tableVacuumInfo struct {
	Schema         string `json:"schema"`
	Table          string `json:"table"`
	XIDAge         int64  `json:"xid_age"`
	RemainingXIDs  int64  `json:"remaining_xids"`
	LastVacuum     string `json:"last_vacuum,omitempty"`
	LastAutoVacuum string `json:"last_autovacuum,omitempty"`
	DeadTuples     int64  `json:"dead_tuples"`
	LiveTuples     int64  `json:"live_tuples"`
}

func runVacuumHealth(ctx context.Context, d db.Querier) Result {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			n.nspname                        AS schema,
			c.relname                        AS table_name,
			age(c.relfrozenxid)              AS xid_age,
			2147483648 - age(c.relfrozenxid) AS remaining_xids,
			s.last_vacuum,
			s.last_autovacuum,
			COALESCE(s.n_dead_tup, 0)        AS dead_tuples,
			COALESCE(s.n_live_tup, 0)        AS live_tuples
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		WHERE c.relkind = 'r'
		  AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		ORDER BY age(c.relfrozenxid) DESC
		LIMIT 20
	`)
	if err != nil {
		return Result{Check: CheckVacuum, Status: StatusError,
			Message: fmt.Sprintf("query failed: %v", err)}
	}

	infos := make([]tableVacuumInfo, 0, len(rows))
	criticalCount := 0
	warningCount := 0

	for _, r := range rows {
		xidAgeF, _ := db.ToFloat64(r["xid_age"])
		remainF, _ := db.ToFloat64(r["remaining_xids"])
		deadF, _ := db.ToFloat64(r["dead_tuples"])
		liveF, _ := db.ToFloat64(r["live_tuples"])

		info := tableVacuumInfo{
			Schema:        db.ToString(r["schema"]),
			Table:         db.ToString(r["table_name"]),
			XIDAge:        int64(xidAgeF),
			RemainingXIDs: int64(remainF),
			DeadTuples:    int64(deadF),
			LiveTuples:    int64(liveF),
		}

		if t, ok := r["last_vacuum"]; ok && t != nil {
			if tv, ok := t.(time.Time); ok {
				info.LastVacuum = tv.Format(time.RFC3339)
			}
		}
		if t, ok := r["last_autovacuum"]; ok && t != nil {
			if tv, ok := t.(time.Time); ok {
				info.LastAutoVacuum = tv.Format(time.RFC3339)
			}
		}

		if info.RemainingXIDs < xidWarningThreshold/10 {
			criticalCount++
		} else if info.RemainingXIDs < xidWarningThreshold {
			warningCount++
		}
		infos = append(infos, info)
	}

	det := vacuumDetails{Tables: infos}
	status := StatusOK
	msg := "Vacuum health is normal. No XID wraparound risk detected."

	if criticalCount > 0 {
		status = StatusCritical
		msg = fmt.Sprintf("CRITICAL: %d table(s) approaching XID wraparound (<20M remaining XIDs). Run VACUUM FREEZE immediately.", criticalCount)
	} else if warningCount > 0 {
		status = StatusWarning
		msg = fmt.Sprintf("Warning: %d table(s) have fewer than %dM remaining XIDs before wraparound.", warningCount, xidWarningThreshold/1_000_000)
	}

	return Result{Check: CheckVacuum, Status: status, Message: msg, Details: det}
}
