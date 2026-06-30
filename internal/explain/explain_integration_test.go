//go:build integration

package explain

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

func TestExplainQuery_Basic_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	result, err := ExplainQuery(ctx, d, "SELECT 1 AS n", false, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.BasePlan)

	assert.Equal(t, "SELECT 1 AS n", result.Query)
	assert.GreaterOrEqual(t, result.BasePlan.TotalCost, 0.0)
	assert.Nil(t, result.HypoPlan, "no hypothetical plan expected")
}

func TestExplainQuery_PlanHasExpectedFields_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	result, err := ExplainQuery(ctx, d, "SELECT pg_sleep(0)", false, nil)
	require.NoError(t, err)

	plan, ok := result.BasePlan.Plan.(map[string]any)
	require.True(t, ok, "plan should be a JSON object")
	assert.Contains(t, plan, "Node Type")
	assert.Contains(t, plan, "Total Cost")
}

func TestGetQueryCost_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	cost, err := GetQueryCost(ctx, d, "SELECT 1")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cost, 0.0)
}

func TestExplainQuery_InvalidSQL_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	_, err := ExplainQuery(ctx, d, "THIS IS NOT SQL", false, nil)
	assert.Error(t, err, "invalid SQL should return an error")
}

func TestExplainQuery_WithHypotheticalIndexes_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	info, err := db.CheckExtension(ctx, d, "hypopg")
	require.NoError(t, err)
	if !info.Installed {
		t.Skip("hypopg not installed — skipping hypothetical index test")
	}

	// Use a real (non-temp) table so the definition is visible on every
	// connection that pgxpool hands out. Temp tables are connection-scoped.
	_, err = d.InternalQuery(ctx, `
		CREATE TABLE IF NOT EXISTS expl_test (
			id     SERIAL PRIMARY KEY,
			status TEXT
		)
	`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = d.InternalQuery(context.Background(), "DROP TABLE IF EXISTS expl_test")
	})

	result, err := ExplainQuery(ctx, d, "SELECT * FROM expl_test WHERE status = 'active'", false,
		[]HypotheticalIndex{{Definition: "CREATE INDEX ON expl_test (status)"}})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.HypoPlan, "hypothetical plan should be present")
	assert.GreaterOrEqual(t, result.Improvement, 0.0)
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
