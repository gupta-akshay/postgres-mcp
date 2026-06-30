package topqueries

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// ─── unit tests ───────────────────────────────────────────────────────────────

func TestTimeCol(t *testing.T) {
	cases := []struct {
		version int
		base    string
		want    string
	}{
		// PG13+ uses _exec_time suffix
		{130000, "total", "total_exec_time"},
		{130000, "mean", "mean_exec_time"},
		{130000, "stddev", "stddev_exec_time"},
		// PG12 uses _time suffix
		{120007, "total", "total_time"},
		{120007, "mean", "mean_time"},
		{120007, "stddev", "stddev_time"},
		// PG14 is also >= 13
		{140001, "total", "total_exec_time"},
	}

	for _, tc := range cases {
		got := timeCol(tc.version, tc.base)
		assert.Equal(t, tc.want, got, "version=%d base=%s", tc.version, tc.base)
	}
}

func TestMapToStats_Basic(t *testing.T) {
	rows := []map[string]any{
		{
			"query":               "SELECT * FROM users WHERE id = $1",
			"calls":               int64(42),
			"total_exec_time_ms":  float64(1234.56),
			"mean_exec_time_ms":   float64(29.39),
			"stddev_exec_time_ms": float64(5.12),
			"rows":                int64(42),
		},
	}

	stats := mapToStats(rows)

	require.Len(t, stats, 1)
	s := stats[0]
	assert.Equal(t, "SELECT * FROM users WHERE id = $1", s.Query)
	assert.Equal(t, int64(42), s.Calls)
	assert.InDelta(t, 1234.56, s.TotalExecTimeMS, 0.01)
	assert.InDelta(t, 29.39, s.MeanExecTimeMS, 0.01)
	assert.InDelta(t, 5.12, s.StddevExecTimeMS, 0.01)
	assert.Equal(t, int64(42), s.Rows)
}

func TestMapToStats_EmptyRows(t *testing.T) {
	stats := mapToStats(nil)
	assert.Empty(t, stats)
}

func TestMapToStats_MissingFields(t *testing.T) {
	// Fields that are absent should default to zero values
	rows := []map[string]any{
		{"query": "SELECT 1"},
	}
	stats := mapToStats(rows)
	require.Len(t, stats, 1)
	assert.Equal(t, "SELECT 1", stats[0].Query)
	assert.Equal(t, int64(0), stats[0].Calls)
	assert.Equal(t, float64(0), stats[0].TotalExecTimeMS)
}

func TestMapToStats_MultipleRows(t *testing.T) {
	rows := []map[string]any{
		{"query": "SELECT 1", "calls": int64(1)},
		{"query": "SELECT 2", "calls": int64(2)},
		{"query": "SELECT 3", "calls": int64(3)},
	}
	stats := mapToStats(rows)
	require.Len(t, stats, 3)
	assert.Equal(t, int64(1), stats[0].Calls)
	assert.Equal(t, int64(2), stats[1].Calls)
	assert.Equal(t, int64(3), stats[2].Calls)
}

// ─── integration tests ────────────────────────────────────────────────────────

func TestGetTopQueries_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	// pg_stat_statements may not be installed — the function should return a
	// clear error rather than panic.
	stats, err := GetTopQueries(ctx, d, MethodTotalTime, 5)
	if err != nil {
		// Acceptable: extension not installed
		assert.Contains(t, err.Error(), "pg_stat_statements")
		return
	}

	// If the extension is available, we expect valid results
	assert.LessOrEqual(t, len(stats), 5)
	for _, s := range stats {
		assert.NotEmpty(t, s.Query)
		assert.GreaterOrEqual(t, s.Calls, int64(0))
	}
}

func TestGetTopQueries_InvalidMethod_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	_, err := GetTopQueries(ctx, d, Method("nonsense"), 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown method")
}

// ─── helper ───────────────────────────────────────────────────────────────────

func integrationDB(t *testing.T) *db.Driver {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	d, err := db.New(context.Background(), dsn, false)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return d
}
