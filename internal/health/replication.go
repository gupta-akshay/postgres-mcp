package health

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

type replicationDetails struct {
	IsReplica bool              `json:"is_replica"`
	Standbys  []standbyInfo     `json:"standbys,omitempty"`
	Slots     []replicationSlot `json:"slots,omitempty"`
}

type standbyInfo struct {
	ClientAddr string `json:"client_addr"`
	State      string `json:"state"`
	SentLSN    string `json:"sent_lsn"`
	WriteLSN   string `json:"write_lsn"`
	FlushLSN   string `json:"flush_lsn"`
	ReplayLSN  string `json:"replay_lsn"`
	WriteLag   string `json:"write_lag,omitempty"`
	FlushLag   string `json:"flush_lag,omitempty"`
	ReplayLag  string `json:"replay_lag,omitempty"`
}

type replicationSlot struct {
	Name           string `json:"name"`
	Plugin         string `json:"plugin,omitempty"`
	SlotType       string `json:"slot_type"`
	Active         bool   `json:"active"`
	RestartLSN     string `json:"restart_lsn,omitempty"`
	ConfirmedFlush string `json:"confirmed_flush_lsn,omitempty"`
}

func runReplicationHealth(ctx context.Context, d db.Querier) Result {
	det := replicationDetails{}

	// Is this server a replica?
	recRows, err := d.InternalQuery(ctx, `SELECT pg_is_in_recovery() AS is_replica`)
	if err != nil {
		return Result{Check: CheckReplication, Status: StatusError,
			Message: fmt.Sprintf("query failed: %v", err)}
	}
	if len(recRows) > 0 {
		det.IsReplica, _ = recRows[0]["is_replica"].(bool)
	}

	// Active standbys (only meaningful on primary)
	standbyRows, err := d.InternalQuery(ctx, `
		SELECT
			COALESCE(client_addr::text, 'local') AS client_addr,
			state,
			sent_lsn::text,
			write_lsn::text,
			flush_lsn::text,
			replay_lsn::text,
			write_lag::text,
			flush_lag::text,
			replay_lag::text
		FROM pg_stat_replication
		ORDER BY client_addr
	`)
	if err == nil {
		for _, r := range standbyRows {
			det.Standbys = append(det.Standbys, standbyInfo{
				ClientAddr: db.ToString(r["client_addr"]),
				State:      db.ToString(r["state"]),
				SentLSN:    db.ToString(r["sent_lsn"]),
				WriteLSN:   db.ToString(r["write_lsn"]),
				FlushLSN:   db.ToString(r["flush_lsn"]),
				ReplayLSN:  db.ToString(r["replay_lsn"]),
				WriteLag:   db.ToString(r["write_lag"]),
				FlushLag:   db.ToString(r["flush_lag"]),
				ReplayLag:  db.ToString(r["replay_lag"]),
			})
		}
	}

	// Replication slots
	slotRows, err := d.InternalQuery(ctx, `
		SELECT
			slot_name,
			COALESCE(plugin, '') AS plugin,
			slot_type,
			active,
			restart_lsn::text,
			confirmed_flush_lsn::text
		FROM pg_replication_slots
		ORDER BY slot_name
	`)
	if err == nil {
		for _, r := range slotRows {
			active, _ := r["active"].(bool)
			det.Slots = append(det.Slots, replicationSlot{
				Name:           db.ToString(r["slot_name"]),
				Plugin:         db.ToString(r["plugin"]),
				SlotType:       db.ToString(r["slot_type"]),
				Active:         active,
				RestartLSN:     db.ToString(r["restart_lsn"]),
				ConfirmedFlush: db.ToString(r["confirmed_flush_lsn"]),
			})
		}
	}

	status := StatusOK
	msg := ""

	inactiveSlots := 0
	for _, s := range det.Slots {
		if !s.Active {
			inactiveSlots++
		}
	}

	laggingStandbys := 0
	for _, s := range det.Standbys {
		if s.State != "streaming" {
			laggingStandbys++
		}
	}

	if det.IsReplica {
		msg = "This server is a standby replica."
	} else if len(det.Standbys) == 0 && len(det.Slots) == 0 {
		msg = "No replication configured."
	} else {
		msg = fmt.Sprintf("Primary with %d standby(s) and %d replication slot(s).",
			len(det.Standbys), len(det.Slots))
	}

	if inactiveSlots > 0 {
		status = StatusWarning
		msg += fmt.Sprintf(" Warning: %d inactive slot(s) may cause WAL accumulation.", inactiveSlots)
	}
	if laggingStandbys > 0 {
		status = StatusWarning
		msg += fmt.Sprintf(" Warning: %d standby(s) not in streaming state.", laggingStandbys)
	}

	return Result{Check: CheckReplication, Status: status, Message: msg, Details: det}
}
