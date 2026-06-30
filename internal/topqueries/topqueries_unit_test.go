package topqueries

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/dbtest"
)

// statRow builds a pg_stat_statements-like row.
func statRow(query string, calls int64, total, mean, stddev float64, rows int64) map[string]any {
	return map[string]any{
		"query":               query,
		"calls":               calls,
		"total_exec_time_ms":  total,
		"mean_exec_time_ms":   mean,
		"stddev_exec_time_ms": stddev,
		"rows":                rows,
	}
}

// ─── GetTopQueries ────────────────────────────────────────────────────────────

func TestGetTopQueries_TotalTime(t *testing.T) {
	mock := dbtest.NewMock()
	// RequireExtension → CheckExtension → InternalQuery
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	// queryByTime → QueryRows
	mock.AddQueryRows([]map[string]any{
		statRow("SELECT * FROM users", 100, 5000, 50, 10, 100),
	}, nil)

	stats, err := GetTopQueries(context.Background(), mock, MethodTotalTime, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, "SELECT * FROM users", stats[0].Query)
	assert.Equal(t, int64(100), stats[0].Calls)
}

func TestGetTopQueries_MeanTime(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.AddQueryRows([]map[string]any{
		statRow("SELECT 1", 1, 100, 100, 0, 1),
	}, nil)

	stats, err := GetTopQueries(context.Background(), mock, MethodMeanTime, 5)
	require.NoError(t, err)
	require.Len(t, stats, 1)
}

func TestGetTopQueries_Resource(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	// Resource method uses QueryRows with additional fields
	mock.AddQueryRows([]map[string]any{
		{
			"query":              "SELECT big FROM t",
			"calls":              int64(50),
			"total_exec_time_ms": float64(10000),
			"mean_exec_time_ms":  float64(200),
			"rows":               int64(50),
			"shared_blks_hit":    int64(1000),
			"shared_blks_read":   int64(500),
			"wal_bytes":          int64(2048),
			"pct_time":           float64(15.5),
			"pct_blocks":         float64(8.2),
			"pct_wal":            float64(3.1),
		},
	}, nil)

	stats, err := GetTopQueries(context.Background(), mock, MethodResource, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, int64(1000), stats[0].SharedBlksHit)
	assert.Equal(t, int64(500), stats[0].SharedBlksRead)
	assert.Equal(t, int64(2048), stats[0].WalBytes)
	assert.InDelta(t, 15.5, stats[0].PctTime, 0.01)
	assert.InDelta(t, 8.2, stats[0].PctBlocks, 0.01)
	assert.InDelta(t, 3.1, stats[0].PctWAL, 0.01)
}

func TestGetTopQueries_UnknownMethod(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)

	_, err := GetTopQueries(context.Background(), mock, Method("bogus"), 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown method")
}

func TestGetTopQueries_ExtensionNotInstalled(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionAvailableOnly("1.10"), nil)

	_, err := GetTopQueries(context.Background(), mock, MethodTotalTime, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

func TestGetTopQueries_VersionError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.SetVersionErr(errors.New("version query failed"))

	_, err := GetTopQueries(context.Background(), mock, MethodTotalTime, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get server version")
}

func TestGetTopQueries_QueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.AddQueryRows(nil, errors.New("pg_stat_statements error"))

	_, err := GetTopQueries(context.Background(), mock, MethodTotalTime, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_stat_statements")
}

func TestGetTopQueries_ResourceQueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.AddQueryRows(nil, errors.New("resource query error"))

	_, err := GetTopQueries(context.Background(), mock, MethodResource, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_stat_statements")
}

func TestGetTopQueries_PG12Version(t *testing.T) {
	mock := dbtest.NewMock().SetVersion(120007)
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.8"), nil)
	mock.AddQueryRows([]map[string]any{
		statRow("SELECT 1", 1, 10, 10, 0, 1),
	}, nil)

	stats, err := GetTopQueries(context.Background(), mock, MethodTotalTime, 10)
	require.NoError(t, err)
	require.Len(t, stats, 1)
}

// Covers the PG12 branch of queryByResource (walCol/totalWalExpr = "0").
func TestGetTopQueries_Resource_PG12(t *testing.T) {
	mock := dbtest.NewMock().SetVersion(120007)
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.8"), nil)
	mock.AddQueryRows([]map[string]any{
		{
			"query":              "SELECT 1",
			"calls":              int64(1),
			"total_exec_time_ms": float64(100),
			"mean_exec_time_ms":  float64(100),
			"rows":               int64(1),
			"shared_blks_hit":    int64(10),
			"shared_blks_read":   int64(5),
			"wal_bytes":          int64(0),
			"pct_time":           float64(10),
			"pct_blocks":         float64(5),
			"pct_wal":            float64(0),
		},
	}, nil)

	stats, err := GetTopQueries(context.Background(), mock, MethodResource, 5)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, int64(0), stats[0].WalBytes)
}
