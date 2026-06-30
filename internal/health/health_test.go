package health

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// ─── unit tests ───────────────────────────────────────────────────────────────

func TestResolveChecks_All(t *testing.T) {
	for _, input := range [][]string{nil, {}, {"all"}, {"ALL"}, {"All"}} {
		got, err := resolveChecks(input)
		require.NoError(t, err, "input=%v", input)
		assert.Len(t, got, len(allChecks), "input=%v should resolve to all checks", input)
	}
}

func TestResolveChecks_Specific(t *testing.T) {
	got, err := resolveChecks([]string{"index", "vacuum"})
	require.NoError(t, err)
	assert.Equal(t, []CheckName{CheckIndex, CheckVacuum}, got)
}

func TestResolveChecks_CaseInsensitive(t *testing.T) {
	got, err := resolveChecks([]string{"INDEX", "Buffer"})
	require.NoError(t, err)
	assert.Equal(t, []CheckName{CheckIndex, CheckBuffer}, got)
}

func TestResolveChecks_AllNames(t *testing.T) {
	names := []string{"index", "connection", "vacuum", "sequence", "replication", "buffer", "constraint"}
	got, err := resolveChecks(names)
	require.NoError(t, err)
	assert.Len(t, got, len(names))
}

func TestResolveChecks_InvalidName(t *testing.T) {
	_, err := resolveChecks([]string{"bogus"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

// ─── integration tests ────────────────────────────────────────────────────────

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

// ─── shared integration helper ────────────────────────────────────────────────

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
