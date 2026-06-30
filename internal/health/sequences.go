package health

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

const sequenceWarningPct = 90.0 // warn when sequence is > 90% used

type sequenceDetails struct {
	Sequences []sequenceInfo `json:"sequences"`
}

type sequenceInfo struct {
	Schema              string  `json:"schema"`
	Name                string  `json:"name"`
	DataType            string  `json:"data_type"`
	MaxValue            int64   `json:"max_value"`
	LastValue           int64   `json:"last_value,omitempty"`
	UsagePct            float64 `json:"usage_pct"`
	PrivilegeRestricted bool    `json:"privilege_restricted,omitempty"`
}

func runSequenceHealth(ctx context.Context, d db.Querier) Result {
	// pg_sequences is available from PG10+; last_value is NULL when the sequence
	// has never been used OR when the caller lacks SELECT/USAGE privilege.
	// has_sequence_privilege() lets us distinguish the two cases.
	rows, err := d.InternalQuery(ctx, `
		SELECT
			schemaname   AS schema,
			sequencename AS name,
			data_type,
			max_value::bigint,
			last_value,
			has_sequence_privilege(
				quote_ident(schemaname) || '.' || quote_ident(sequencename),
				'SELECT,USAGE'
			) AS can_read,
			CASE
				WHEN last_value IS NULL THEN 0
				ELSE round(last_value::numeric / max_value::numeric * 100, 2)
			END AS usage_pct
		FROM pg_sequences
		WHERE schemaname NOT IN ('pg_catalog','information_schema','pg_toast')
		ORDER BY usage_pct DESC NULLS LAST
	`)
	if err != nil {
		return Result{Check: CheckSequence, Status: StatusError,
			Message: fmt.Sprintf("query failed: %v", err)}
	}

	infos := make([]sequenceInfo, 0, len(rows))
	warnCount := 0
	restrictedCount := 0

	for _, r := range rows {
		maxF, _ := db.ToFloat64(r["max_value"])
		lastF, _ := db.ToFloat64(r["last_value"])
		pctF, _ := db.ToFloat64(r["usage_pct"])
		canRead, _ := r["can_read"].(bool)

		info := sequenceInfo{
			Schema:   db.ToString(r["schema"]),
			Name:     db.ToString(r["name"]),
			DataType: db.ToString(r["data_type"]),
			MaxValue: int64(maxF),
			UsagePct: pctF,
		}
		if r["last_value"] != nil {
			info.LastValue = int64(lastF)
		}
		// last_value is NULL because of missing privilege, not because unused
		if r["last_value"] == nil && !canRead {
			info.PrivilegeRestricted = true
			restrictedCount++
		} else if pctF >= sequenceWarningPct {
			warnCount++
		}
		infos = append(infos, info)
	}

	det := sequenceDetails{Sequences: infos}
	status := StatusOK
	msg := "All sequences have sufficient remaining values."

	if warnCount > 0 {
		status = StatusWarning
		msg = fmt.Sprintf("%d sequence(s) are over %.0f%% used and may exhaust soon.", warnCount, sequenceWarningPct)
	}
	if restrictedCount > 0 {
		if status == StatusOK {
			status = StatusWarning
		}
		msg += fmt.Sprintf(" %d sequence(s) could not be read due to insufficient privileges — usage unknown.", restrictedCount)
	}

	return Result{Check: CheckSequence, Status: status, Message: msg, Details: det}
}
