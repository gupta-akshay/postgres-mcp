package health

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

type constraintDetails struct {
	Invalid []invalidConstraint `json:"invalid,omitempty"`
}

type invalidConstraint struct {
	Schema          string `json:"schema"`
	Table           string `json:"table"`
	Constraint      string `json:"constraint"`
	Type            string `json:"type"`
	ReferencedTable string `json:"referenced_table,omitempty"`
}

func runConstraintHealth(ctx context.Context, d db.Querier) Result {
	rows, err := d.InternalQuery(ctx, `
		SELECT
			n.nspname                     AS schema,
			c.relname                     AS table_name,
			con.conname                   AS constraint_name,
			CASE con.contype
				WHEN 'f' THEN 'FOREIGN KEY'
				WHEN 'c' THEN 'CHECK'
				WHEN 'u' THEN 'UNIQUE'
				WHEN 'p' THEN 'PRIMARY KEY'
				ELSE con.contype::text
			END                           AS constraint_type,
			CASE WHEN con.contype = 'f'
				THEN (
					SELECT nr.nspname || '.' || cr.relname
					FROM pg_class cr
					JOIN pg_namespace nr ON nr.oid = cr.relnamespace
					WHERE cr.oid = con.confrelid
				)
				ELSE NULL
			END                           AS referenced_table
		FROM pg_constraint con
		JOIN pg_class     c  ON c.oid  = con.conrelid
		JOIN pg_namespace n  ON n.oid  = c.relnamespace
		WHERE NOT con.convalidated
		  AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		ORDER BY n.nspname, c.relname, con.conname
	`)
	if err != nil {
		return Result{Check: CheckConstraint, Status: StatusError,
			Message: fmt.Sprintf("query failed: %v", err)}
	}

	invalids := make([]invalidConstraint, 0, len(rows))
	for _, r := range rows {
		invalids = append(invalids, invalidConstraint{
			Schema:          db.ToString(r["schema"]),
			Table:           db.ToString(r["table_name"]),
			Constraint:      db.ToString(r["constraint_name"]),
			Type:            db.ToString(r["constraint_type"]),
			ReferencedTable: db.ToString(r["referenced_table"]),
		})
	}

	det := constraintDetails{Invalid: invalids}
	status := StatusOK
	msg := "All constraints are validated."

	if len(invalids) > 0 {
		status = StatusWarning
		msg = fmt.Sprintf("%d unvalidated constraint(s) found. Run VALIDATE CONSTRAINT to verify data integrity.", len(invalids))
	}

	return Result{Check: CheckConstraint, Status: status, Message: msg, Details: det}
}
