//go:build integration

package topqueries

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

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
