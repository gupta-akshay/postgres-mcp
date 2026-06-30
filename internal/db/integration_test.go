//go:build integration

package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── integration tests ────────────────────────────────────────────────────────

func TestNew_Integration(t *testing.T) {
	d := integrationDB(t, false)
	assert.NotNil(t, d)
	assert.False(t, d.IsRestricted())
}

func TestNew_Restricted_Integration(t *testing.T) {
	d := integrationDB(t, true)
	assert.True(t, d.IsRestricted())
}

func TestQueryRows_Select_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	rows, err := d.QueryRows(ctx, "SELECT 1 AS n, 'hello' AS s")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int32(1), rows[0]["n"])
	assert.Equal(t, "hello", rows[0]["s"])
}

func TestQueryRows_MultipleRows_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	rows, err := d.QueryRows(ctx, "SELECT generate_series(1, 5) AS n")
	require.NoError(t, err)
	assert.Len(t, rows, 5)
}

func TestQueryRows_Restricted_BlocksWrites_Integration(t *testing.T) {
	d := integrationDB(t, true)
	ctx := context.Background()

	_, err := d.QueryRows(ctx, "INSERT INTO pg_class (relname) VALUES ('x')")
	assert.Error(t, err, "restricted mode should block INSERT")
}

func TestQueryRows_Restricted_AllowsSelect_Integration(t *testing.T) {
	d := integrationDB(t, true)
	ctx := context.Background()

	rows, err := d.QueryRows(ctx, "SELECT current_database() AS db")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.NotEmpty(t, rows[0]["db"])
}

func TestInternalQuery_Integration(t *testing.T) {
	d := integrationDB(t, true) // even restricted driver can run internal queries
	ctx := context.Background()

	rows, err := d.InternalQuery(ctx, "SELECT version() AS v")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.NotEmpty(t, rows[0]["v"])
}

func TestVersion_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	ver, err := d.Version(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ver, 120000, "should be PG12 or newer")
}

func TestExecute_Unrestricted_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	// Create + drop a temp table; round-trip through Execute for both.
	err := d.Execute(ctx, "CREATE TEMP TABLE pgmcp_exec_test (id int)")
	require.NoError(t, err)

	err = d.Execute(ctx, "INSERT INTO pgmcp_exec_test VALUES (1), (2)")
	require.NoError(t, err)

	rows, err := d.QueryRows(ctx, "SELECT count(*) AS n FROM pgmcp_exec_test")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	n, err := ToFloat64(rows[0]["n"])
	require.NoError(t, err)
	assert.Equal(t, 2.0, n)
}

func TestExecute_Restricted_Integration(t *testing.T) {
	d := integrationDB(t, true)
	ctx := context.Background()

	err := d.Execute(ctx, "CREATE TEMP TABLE should_not_create (id int)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not permitted")
}

func TestQueryRows_SyntaxError_Unrestricted_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	_, err := d.QueryRows(ctx, "SELECT FROM WHERE")
	require.Error(t, err)
}

func TestQueryRows_SyntaxError_Restricted_Integration(t *testing.T) {
	d := integrationDB(t, true)
	ctx := context.Background()

	_, err := d.QueryRows(ctx, "SELECT FROM WHERE")
	require.Error(t, err)
}

func TestCollectRows_DuplicateColumns_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	// Two columns with the same alias: one stays "n", the other becomes "n_1".
	rows, err := d.InternalQuery(ctx, "SELECT 1 AS n, 2 AS n")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, hasN := rows[0]["n"]
	_, hasN1 := rows[0]["n_1"]
	assert.True(t, hasN && hasN1, "duplicate column names should be disambiguated")
}

func TestJsonFriendly_UUID_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	rows, err := d.InternalQuery(ctx, "SELECT gen_random_uuid() AS id")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	id, ok := rows[0]["id"].(string)
	require.True(t, ok, "UUID column should be returned as string, got %T", rows[0]["id"])
	assert.Len(t, id, 36, "UUID string should be 36 chars")
}

func TestWithConn_CancelledContext_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so pool.Acquire fails

	err := d.WithConn(ctx, func(_ context.Context, _ Querier) error {
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquire connection")
}

func TestWithConn_AllMethods_Integration(t *testing.T) {
	d := integrationDB(t, false)
	ctx := context.Background()

	err := d.WithConn(ctx, func(ctx context.Context, q Querier) error {
		rows, err := q.InternalQuery(ctx, "SELECT 1 AS n")
		require.NoError(t, err)
		require.Len(t, rows, 1)

		rows, err = q.QueryRows(ctx, "SELECT 2 AS m")
		require.NoError(t, err)
		require.Len(t, rows, 1)

		err = q.Execute(ctx, "CREATE TEMP TABLE _pgmcp_conn_test (id int)")
		require.NoError(t, err)

		ver, err := q.Version(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, ver, 120000)

		assert.False(t, q.IsRestricted())
		q.Close()

		return q.WithConn(ctx, func(ctx context.Context, inner Querier) error {
			rows, err := inner.InternalQuery(ctx, "SELECT 3 AS k")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			return nil
		})
	})
	require.NoError(t, err)
}

func TestWithConn_Restricted_BlocksExecute_Integration(t *testing.T) {
	d := integrationDB(t, true)
	ctx := context.Background()

	err := d.WithConn(ctx, func(ctx context.Context, q Querier) error {
		return q.Execute(ctx, "CREATE TEMP TABLE should_not_exist (id int)")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not permitted")
}

func TestWithConn_Restricted_BlocksQueryRows_Integration(t *testing.T) {
	d := integrationDB(t, true)
	ctx := context.Background()

	err := d.WithConn(ctx, func(ctx context.Context, q Querier) error {
		_, err := q.QueryRows(ctx, "DELETE FROM pg_class WHERE false")
		return err
	})
	require.Error(t, err)
}

// ─── helper ───────────────────────────────────────────────────────────────────

func integrationDB(t *testing.T, restricted bool) *Driver {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	d, err := New(context.Background(), dsn, restricted)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return d
}
