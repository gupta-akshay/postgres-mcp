package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/dbtest"
)

var ctx = context.Background()

// ─── runCheck default branch ──────────────────────────────────────────────────

func TestRunCheck_UnknownCheck(t *testing.T) {
	mock := dbtest.NewMock()
	result := runCheck(ctx, mock, CheckName("totally_unknown"))
	assert.Equal(t, StatusError, result.Status)
	assert.Equal(t, "unknown check", result.Message)
}

// ─── runConnectionHealth ─────────────────────────────────────────────────────

func connRow(total, active, idleInTx, maxConn int64) map[string]any {
	return map[string]any{
		"total":      total,
		"active":     active,
		"idle_in_tx": idleInTx,
		"max_conn":   maxConn,
	}
}

func TestRunConnectionHealth_OK(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{connRow(10, 5, 0, 100)}, nil)

	r := runConnectionHealth(ctx, mock)
	assert.Equal(t, CheckConnection, r.Check)
	assert.Equal(t, StatusOK, r.Status)
}

func TestRunConnectionHealth_HighTotal(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{connRow(600, 100, 0, 1000)}, nil)

	r := runConnectionHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "high total connections")
}

func TestRunConnectionHealth_HighIdleInTx(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{connRow(50, 10, 150, 200)}, nil)

	r := runConnectionHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "idle-in-transaction")
}

func TestRunConnectionHealth_CriticalUsage(t *testing.T) {
	// 95 out of 100 = 95% usage → critical
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{connRow(95, 90, 0, 100)}, nil)

	r := runConnectionHealth(ctx, mock)
	assert.Equal(t, StatusCritical, r.Status)
	assert.Contains(t, r.Message, "connection usage")
}

func TestRunConnectionHealth_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("db error"))

	r := runConnectionHealth(ctx, mock)
	assert.Equal(t, CheckConnection, r.Check)
	assert.Equal(t, StatusError, r.Status)
}

func TestRunConnectionHealth_EmptyRows(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil)

	r := runConnectionHealth(ctx, mock)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "No connection data")
}

// ─── runVacuumHealth ─────────────────────────────────────────────────────────

func vacuumRow(xidAge, remaining int64) map[string]any {
	return map[string]any{
		"schema":          "public",
		"table_name":      "some_table",
		"xid_age":         xidAge,
		"remaining_xids":  remaining,
		"last_vacuum":     nil,
		"last_autovacuum": nil,
		"dead_tuples":     int64(0),
		"live_tuples":     int64(1000),
	}
}

func TestRunVacuumHealth_OK(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{vacuumRow(10_000_000, 2_137_483_648)}, nil)

	r := runVacuumHealth(ctx, mock)
	assert.Equal(t, CheckVacuum, r.Check)
	assert.Equal(t, StatusOK, r.Status)
}

func TestRunVacuumHealth_Warning(t *testing.T) {
	// remaining < 200M → warning
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{vacuumRow(2_000_000_000, 150_000_000)}, nil)

	r := runVacuumHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
}

func TestRunVacuumHealth_Critical(t *testing.T) {
	// remaining < 20M → critical
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{vacuumRow(2_127_483_648, 10_000_000)}, nil)

	r := runVacuumHealth(ctx, mock)
	assert.Equal(t, StatusCritical, r.Status)
	assert.Contains(t, r.Message, "CRITICAL")
}

func TestRunVacuumHealth_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("db error"))

	r := runVacuumHealth(ctx, mock)
	assert.Equal(t, StatusError, r.Status)
}

func TestRunVacuumHealth_Empty(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil)

	r := runVacuumHealth(ctx, mock)
	assert.Equal(t, StatusOK, r.Status)
}

func TestRunVacuumHealth_WithTimestamps(t *testing.T) {
	now := time.Now().UTC()
	row := map[string]any{
		"schema":          "public",
		"table_name":      "tbl",
		"xid_age":         int64(1_000_000),
		"remaining_xids":  int64(2_000_000_000),
		"last_vacuum":     now,
		"last_autovacuum": now,
		"dead_tuples":     int64(0),
		"live_tuples":     int64(100),
	}
	mock := dbtest.NewMock().AddInternalQuery([]map[string]any{row}, nil)

	r := runVacuumHealth(ctx, mock)
	assert.Equal(t, StatusOK, r.Status)
	// Details should contain tables
	det, ok := r.Details.(vacuumDetails)
	require.True(t, ok)
	require.Len(t, det.Tables, 1)
	assert.NotEmpty(t, det.Tables[0].LastVacuum)
	assert.NotEmpty(t, det.Tables[0].LastAutoVacuum)
}

// ─── runSequenceHealth ───────────────────────────────────────────────────────

func seqRow(schema, name string, maxVal, lastVal int64, pct float64) map[string]any {
	return map[string]any{
		"schema":     schema,
		"name":       name,
		"data_type":  "bigint",
		"max_value":  maxVal,
		"last_value": lastVal,
		"usage_pct":  pct,
	}
}

func seqRowNullLast(schema, name string, maxVal int64) map[string]any {
	return map[string]any{
		"schema":     schema,
		"name":       name,
		"data_type":  "bigint",
		"max_value":  maxVal,
		"last_value": nil,
		"usage_pct":  float64(0),
	}
}

func TestRunSequenceHealth_OK(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{seqRow("public", "id_seq", 9223372036854775807, 1000, 0.0)}, nil)

	r := runSequenceHealth(ctx, mock)
	assert.Equal(t, CheckSequence, r.Check)
	assert.Equal(t, StatusOK, r.Status)
}

func TestRunSequenceHealth_Warning(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{seqRow("public", "id_seq", 10000, 9500, 95.0)}, nil)

	r := runSequenceHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "90%")
}

func TestRunSequenceHealth_NullLastValue(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{seqRowNullLast("public", "id_seq", 10000)}, nil)

	r := runSequenceHealth(ctx, mock)
	assert.Equal(t, StatusOK, r.Status)
}

func TestRunSequenceHealth_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("error"))

	r := runSequenceHealth(ctx, mock)
	assert.Equal(t, StatusError, r.Status)
}

// ─── runReplicationHealth ────────────────────────────────────────────────────

func TestRunReplicationHealth_NoReplication(t *testing.T) {
	mock := dbtest.NewMock()
	// is_replica query
	mock.AddInternalQuery([]map[string]any{{"is_replica": false}}, nil)
	// standbys (empty)
	mock.AddInternalQuery(nil, nil)
	// slots (empty)
	mock.AddInternalQuery(nil, nil)

	r := runReplicationHealth(ctx, mock)
	assert.Equal(t, CheckReplication, r.Check)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "No replication")
}

func TestRunReplicationHealth_IsReplica(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"is_replica": true}}, nil)
	mock.AddInternalQuery(nil, nil)
	mock.AddInternalQuery(nil, nil)

	r := runReplicationHealth(ctx, mock)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "standby replica")
}

func TestRunReplicationHealth_WithStandbys(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"is_replica": false}}, nil)
	mock.AddInternalQuery([]map[string]any{
		{
			"client_addr": "10.0.0.2",
			"state":       "streaming",
			"sent_lsn":    "0/3000000",
			"write_lsn":   "0/3000000",
			"flush_lsn":   "0/3000000",
			"replay_lsn":  "0/3000000",
			"write_lag":   nil,
			"flush_lag":   nil,
			"replay_lag":  nil,
		},
	}, nil)
	// slots (empty)
	mock.AddInternalQuery(nil, nil)

	r := runReplicationHealth(ctx, mock)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "1 standby(s)")
}

func TestRunReplicationHealth_InactiveSlot(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"is_replica": false}}, nil)
	mock.AddInternalQuery(nil, nil) // no standbys
	mock.AddInternalQuery([]map[string]any{
		{
			"slot_name":           "slot1",
			"plugin":              "pgoutput",
			"slot_type":           "logical",
			"active":              false,
			"restart_lsn":         "0/1000000",
			"confirmed_flush_lsn": "0/1000000",
		},
	}, nil)

	r := runReplicationHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "inactive slot")
}

func TestRunReplicationHealth_LaggingStandby(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"is_replica": false}}, nil)
	mock.AddInternalQuery([]map[string]any{
		{
			"client_addr": "10.0.0.2",
			"state":       "catchup", // not streaming
			"sent_lsn":    "0/3000000",
			"write_lsn":   "0/2000000",
			"flush_lsn":   "0/2000000",
			"replay_lsn":  "0/1000000",
			"write_lag":   nil,
			"flush_lag":   nil,
			"replay_lag":  nil,
		},
	}, nil)
	mock.AddInternalQuery(nil, nil) // no slots

	r := runReplicationHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "not in streaming state")
}

func TestRunReplicationHealth_QueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("is_replica failed"))

	r := runReplicationHealth(ctx, mock)
	assert.Equal(t, StatusError, r.Status)
}

// ─── runBufferHealth ─────────────────────────────────────────────────────────

func TestRunBufferHealth_OK(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(99.5)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(98.0)}}, nil)

	r := runBufferHealth(ctx, mock)
	assert.Equal(t, CheckBuffer, r.Check)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "99.5%")
}

func TestRunBufferHealth_Warning(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(90.0)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(85.0)}}, nil)

	r := runBufferHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "below 95%")
}

func TestRunBufferHealth_FirstQueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("table stats error"))

	r := runBufferHealth(ctx, mock)
	assert.Equal(t, StatusError, r.Status)
	assert.Contains(t, r.Message, "pg_statio_user_tables")
}

func TestRunBufferHealth_SecondQueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(99.0)}}, nil)
	mock.AddInternalQuery(nil, errors.New("idx stats error"))

	r := runBufferHealth(ctx, mock)
	assert.Equal(t, StatusError, r.Status)
	assert.Contains(t, r.Message, "pg_statio_user_indexes")
}

// ─── runConstraintHealth ──────────────────────────────────────────────────────

func TestRunConstraintHealth_OK(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil) // no invalid constraints

	r := runConstraintHealth(ctx, mock)
	assert.Equal(t, CheckConstraint, r.Check)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "validated")
}

func TestRunConstraintHealth_HasInvalid(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery([]map[string]any{
		{
			"schema":           "public",
			"table_name":       "orders",
			"constraint_name":  "orders_user_id_fkey",
			"constraint_type":  "FOREIGN KEY",
			"referenced_table": "public.users",
		},
	}, nil)

	r := runConstraintHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "1 unvalidated")
}

func TestRunConstraintHealth_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("query error"))

	r := runConstraintHealth(ctx, mock)
	assert.Equal(t, StatusError, r.Status)
}

// ─── runIndexHealth ───────────────────────────────────────────────────────────

func TestRunIndexHealth_OK(t *testing.T) {
	mock := dbtest.NewMock()
	// 4 InternalQuery calls: invalid, duplicate, bloated, unused
	mock.AddInternalQuery(nil, nil) // no invalid
	mock.AddInternalQuery(nil, nil) // no duplicate
	mock.AddInternalQuery(nil, nil) // no bloated
	mock.AddInternalQuery(nil, nil) // no unused

	r := runIndexHealth(ctx, mock)
	assert.Equal(t, CheckIndex, r.Check)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Message, "No index health issues")
}

func TestRunIndexHealth_InvalidIndex(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{
		{"schema": "public", "table_name": "orders", "index_name": "bad_idx", "index_def": "CREATE INDEX ..."},
	}, nil)
	mock.AddInternalQuery(nil, nil)
	mock.AddInternalQuery(nil, nil)
	mock.AddInternalQuery(nil, nil)

	r := runIndexHealth(ctx, mock)
	assert.Equal(t, StatusCritical, r.Status)
	assert.Contains(t, r.Message, "invalid")
}

func TestRunIndexHealth_Warnings(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, nil) // no invalid
	mock.AddInternalQuery([]map[string]any{
		{"schema": "public", "table_name": "t", "index1": "idx1", "index2": "idx2", "definition": "CREATE INDEX ..."},
	}, nil) // duplicate
	mock.AddInternalQuery([]map[string]any{
		{"schema": "public", "table_name": "t", "index_name": "big_idx", "index_size": "200 MB"},
	}, nil) // bloated
	mock.AddInternalQuery([]map[string]any{
		{"schema": "public", "table_name": "t", "index_name": "old_idx", "scans": int64(5), "index_size": "50 MB"},
	}, nil) // unused

	r := runIndexHealth(ctx, mock)
	assert.Equal(t, StatusWarning, r.Status)
	assert.Contains(t, r.Message, "duplicate")
	assert.Contains(t, r.Message, "bloated")
	assert.Contains(t, r.Message, "unused")
}

func TestRunIndexHealth_QueryErrors(t *testing.T) {
	// When sub-queries fail, the check still returns a result (no panic)
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("error1"))
	mock.AddInternalQuery(nil, errors.New("error2"))
	mock.AddInternalQuery(nil, errors.New("error3"))
	mock.AddInternalQuery(nil, errors.New("error4"))

	r := runIndexHealth(ctx, mock)
	assert.Equal(t, CheckIndex, r.Check)
	assert.Equal(t, StatusOK, r.Status) // no items means OK
}

// ─── AnalyzeHealth and runCheck ──────────────────────────────────────────────

func TestAnalyzeHealth_InvalidCheck(t *testing.T) {
	mock := dbtest.NewMock()
	_, err := AnalyzeHealth(ctx, mock, []string{"bogus_check"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_check")
}

// TestAnalyzeHealth_SingleCheck exercises the goroutine path in AnalyzeHealth.
func TestAnalyzeHealth_SingleCheck_Constraint(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, nil) // no invalid constraints

	results, err := AnalyzeHealth(ctx, mock, []string{"constraint"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckConstraint, results[0].Check)
}

func TestAnalyzeHealth_SingleCheck_Connection(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{connRow(5, 3, 0, 100)}, nil)

	results, err := AnalyzeHealth(ctx, mock, []string{"connection"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckConnection, results[0].Check)
}

func TestAnalyzeHealth_SingleCheck_Vacuum(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, nil)

	results, err := AnalyzeHealth(ctx, mock, []string{"vacuum"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckVacuum, results[0].Check)
}

func TestAnalyzeHealth_SingleCheck_Sequence(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, nil)

	results, err := AnalyzeHealth(ctx, mock, []string{"sequence"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckSequence, results[0].Check)
}

func TestAnalyzeHealth_SingleCheck_Replication(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"is_replica": false}}, nil) // is_replica
	mock.AddInternalQuery(nil, nil)                                     // standbys
	mock.AddInternalQuery(nil, nil)                                     // slots

	results, err := AnalyzeHealth(ctx, mock, []string{"replication"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckReplication, results[0].Check)
}

func TestAnalyzeHealth_SingleCheck_Buffer(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(99.0)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"hit_pct": float64(99.0)}}, nil)

	results, err := AnalyzeHealth(ctx, mock, []string{"buffer"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckBuffer, results[0].Check)
}

func TestAnalyzeHealth_SingleCheck_Index(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, nil) // invalid
	mock.AddInternalQuery(nil, nil) // duplicate
	mock.AddInternalQuery(nil, nil) // bloated
	mock.AddInternalQuery(nil, nil) // unused

	results, err := AnalyzeHealth(ctx, mock, []string{"index"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, CheckIndex, results[0].Check)
}
