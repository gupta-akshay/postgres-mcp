package health

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

const (
	maxTotalConnectionsDefault = 500
	maxIdleInTxDefault         = 100
)

type connectionDetails struct {
	TotalConnections  int64   `json:"total_connections"`
	ActiveConnections int64   `json:"active_connections"`
	IdleInTransaction int64   `json:"idle_in_transaction"`
	MaxConnections    int64   `json:"max_connections"`
	UsagePct          float64 `json:"usage_pct"`
}

func runConnectionHealth(ctx context.Context, d db.Querier) Result {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			count(*)                                            AS total,
			count(*) FILTER (WHERE state = 'active')           AS active,
			count(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
			(SELECT setting::bigint FROM pg_settings WHERE name = 'max_connections') AS max_conn
		FROM pg_stat_activity
		WHERE pid != pg_backend_pid()
	`)
	if err != nil {
		return Result{Check: CheckConnection, Status: StatusError,
			Message: fmt.Sprintf("query failed: %v", err)}
	}
	if len(rows) == 0 {
		return Result{Check: CheckConnection, Status: StatusOK,
			Message: "No connection data available."}
	}

	r := rows[0]
	totalF, _ := db.ToFloat64(r["total"])
	activeF, _ := db.ToFloat64(r["active"])
	idleF, _ := db.ToFloat64(r["idle_in_tx"])
	maxF, _ := db.ToFloat64(r["max_conn"])

	det := connectionDetails{
		TotalConnections:  int64(totalF),
		ActiveConnections: int64(activeF),
		IdleInTransaction: int64(idleF),
		MaxConnections:    int64(maxF),
	}
	if maxF > 0 {
		det.UsagePct = totalF / maxF * 100
	}

	status := StatusOK
	msgs := []string{}

	if det.TotalConnections > maxTotalConnectionsDefault {
		status = StatusWarning
		msgs = append(msgs, fmt.Sprintf("high total connections (%d)", det.TotalConnections))
	}
	if det.IdleInTransaction > maxIdleInTxDefault {
		status = StatusWarning
		msgs = append(msgs, fmt.Sprintf("many idle-in-transaction connections (%d)", det.IdleInTransaction))
	}
	if det.UsagePct > 90 {
		status = StatusCritical
		msgs = append(msgs, fmt.Sprintf("connection usage at %.0f%% of max", det.UsagePct))
	}

	msg := "Connection usage is healthy."
	if len(msgs) > 0 {
		msg = "Connection concerns: " + joinEnglish(msgs) + "."
	}

	return Result{Check: CheckConnection, Status: status, Message: msg, Details: det}
}
