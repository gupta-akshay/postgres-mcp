//go:build integration

package schema_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
	"github.com/gupta-akshay/postgres-mcp/internal/schema"
)

// ─── integration tests ────────────────────────────────────────────────────────

func TestListSchemas_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	schemas, err := schema.ListSchemas(ctx, d)
	require.NoError(t, err)
	assert.NotEmpty(t, schemas, "at least the public schema should exist")

	// public schema should always be present
	var found bool
	for _, s := range schemas {
		if s.Name == "public" {
			found = true
			assert.NotEmpty(t, s.Owner)
		}
	}
	assert.True(t, found, "public schema should be listed")
}

func TestListSchemas_ExcludesSystemSchemas_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	schemas, err := schema.ListSchemas(ctx, d)
	require.NoError(t, err)

	for _, s := range schemas {
		assert.NotEqual(t, "pg_catalog", s.Name)
		assert.NotEqual(t, "information_schema", s.Name)
		assert.NotEqual(t, "pg_toast", s.Name)
	}
}

func TestListObjects_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()
	tableName, cleanup := createTestTable(t, d, "public")
	defer cleanup()

	objs, err := schema.ListObjects(ctx, d, "public")
	require.NoError(t, err)
	assert.NotEmpty(t, objs)

	var found bool
	for _, o := range objs {
		if o.Name == tableName {
			found = true
			assert.Equal(t, "BASE TABLE", o.Type)
		}
	}
	assert.True(t, found, "test table %q should appear in object list", tableName)
}

func TestListObjects_UnknownSchema_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	objs, err := schema.ListObjects(ctx, d, "schema_that_does_not_exist_xyz")
	require.NoError(t, err)
	assert.Empty(t, objs)
}

func TestGetObjectDetails_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()
	tableName, cleanup := createTestTable(t, d, "public")
	defer cleanup()

	det, err := schema.GetObjectDetails(ctx, d, "public", tableName)
	require.NoError(t, err)
	require.NotNil(t, det)

	assert.Equal(t, "public", det.Schema)
	assert.Equal(t, tableName, det.Name)
	assert.Equal(t, "BASE TABLE", det.Type)

	// Should have columns
	assert.NotEmpty(t, det.Columns)
	colNames := make([]string, len(det.Columns))
	for i, c := range det.Columns {
		colNames[i] = c.Name
	}
	assert.Contains(t, colNames, "id")
	assert.Contains(t, colNames, "name")
	assert.Contains(t, colNames, "status")

	// id is NOT nullable (PRIMARY KEY)
	for _, c := range det.Columns {
		if c.Name == "id" {
			assert.False(t, c.Nullable)
		}
	}

	// Should have the PRIMARY KEY index
	assert.NotEmpty(t, det.Indexes)
	var hasPK bool
	for _, idx := range det.Indexes {
		if idx.IsPrimary {
			hasPK = true
		}
	}
	assert.True(t, hasPK, "table should have a primary key index")

	// Should have the PRIMARY KEY constraint
	assert.NotEmpty(t, det.Constraints)
}

func TestGetObjectDetails_NotFound_Integration(t *testing.T) {
	d := integrationDB(t)
	ctx := context.Background()

	_, err := schema.GetObjectDetails(ctx, d, "public", "table_that_does_not_exist_xyz")
	assert.Error(t, err)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

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

// createTestTable creates a temporary table with a known schema and returns
// its name plus a cleanup function that drops it.
func createTestTable(t *testing.T, d *db.Driver, schemaName string) (string, func()) {
	t.Helper()
	tableName := fmt.Sprintf("pgmcp_test_%d", time.Now().UnixNano()%1_000_000)
	ctx := context.Background()

	_, err := d.InternalQuery(ctx, fmt.Sprintf(`
		CREATE TABLE %s.%s (
			id      SERIAL PRIMARY KEY,
			name    TEXT    NOT NULL,
			status  TEXT    DEFAULT 'active',
			created TIMESTAMPTZ DEFAULT NOW()
		)
	`, schemaName, tableName))
	require.NoError(t, err, "create test table")

	cleanup := func() {
		d.InternalQuery(context.Background(), //nolint:errcheck
			fmt.Sprintf("DROP TABLE IF EXISTS %s.%s", schemaName, tableName))
	}
	return tableName, cleanup
}
