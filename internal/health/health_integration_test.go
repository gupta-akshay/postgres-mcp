//go:build integration

package health

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

func TestAnalyzeHealth_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	results, err := AnalyzeHealth(ctx, d, []string{"all"})
	require.NoError(t, err)
	assert.Len(t, results, len(allChecks))

	for _, r := range results {
		assert.NotEmpty(t, string(r.Check), "check name should not be empty")
		assert.NotEmpty(t, r.Message, "message should not be empty")
		assert.Contains(t, []Status{StatusOK, StatusWarning, StatusCritical, StatusError}, r.Status,
			"check %s has unknown status %q", r.Check, r.Status)
	}
}

func TestAnalyzeHealth_Subset_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	results, err := AnalyzeHealth(ctx, d, []string{"connection", "buffer"})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	checkNames := []CheckName{results[0].Check, results[1].Check}
	assert.Contains(t, checkNames, CheckConnection)
	assert.Contains(t, checkNames, CheckBuffer)
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
