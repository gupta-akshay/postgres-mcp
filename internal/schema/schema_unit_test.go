package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/dbtest"
)

// ─── quote ────────────────────────────────────────────────────────────────────

func TestQuote(t *testing.T) {
	assert.Equal(t, `"hello"`, quote("hello"))
	assert.Equal(t, `"my schema"`, quote("my schema"))
	assert.Equal(t, `""`, quote(""))
}

// ─── ListSchemas ──────────────────────────────────────────────────────────────

func TestListSchemas_ReturnsParsedRows(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows([]map[string]any{
		{"schema_name": "public", "schema_owner": "postgres"},
		{"schema_name": "myapp", "schema_owner": "app_user"},
	}, nil)

	schemas, err := ListSchemas(context.Background(), mock)
	require.NoError(t, err)
	require.Len(t, schemas, 2)
	assert.Equal(t, "public", schemas[0].Name)
	assert.Equal(t, "postgres", schemas[0].Owner)
	assert.Equal(t, "myapp", schemas[1].Name)
}

func TestListSchemas_EmptyResult(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows(nil, nil)

	schemas, err := ListSchemas(context.Background(), mock)
	require.NoError(t, err)
	assert.Empty(t, schemas)
}

func TestListSchemas_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows(nil, errors.New("db error"))

	_, err := ListSchemas(context.Background(), mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list schemas")
}

// ─── ListObjects ──────────────────────────────────────────────────────────────

func TestListObjects_ReturnsParsedRows(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows([]map[string]any{
		{"name": "users", "type": "BASE TABLE", "size": "128 kB", "owner": "app"},
		{"name": "user_id_seq", "type": "SEQUENCE", "size": nil, "owner": "public"},
	}, nil)

	objs, err := ListObjects(context.Background(), mock, "public")
	require.NoError(t, err)
	require.Len(t, objs, 2)
	assert.Equal(t, "users", objs[0].Name)
	assert.Equal(t, "BASE TABLE", objs[0].Type)
}

func TestListObjects_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows(nil, errors.New("schema not found"))

	_, err := ListObjects(context.Background(), mock, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list objects")
}

// ─── GetObjectDetails ─────────────────────────────────────────────────────────

func TestGetObjectDetails_BaseTable(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock()

	// 1. type query
	mock.AddQueryRows([]map[string]any{
		{"table_type": "BASE TABLE"},
	}, nil)
	// 2. columns
	mock.AddQueryRows([]map[string]any{
		{"column_name": "id", "data_type": "integer", "nullable": false, "column_default": nil},
		{"column_name": "name", "data_type": "text", "nullable": true, "column_default": nil},
	}, nil)
	// 3. indexes
	mock.AddQueryRows([]map[string]any{
		{"name": "users_pkey", "definition": "CREATE UNIQUE INDEX ...", "size": "16 kB",
			"is_unique": true, "is_primary": true},
	}, nil)
	// 4. row stats
	mock.AddQueryRows([]map[string]any{
		{"row_estimate": int64(1000), "total_size": "256 kB"},
	}, nil)
	// 5. constraints
	mock.AddQueryRows([]map[string]any{
		{"constraint_name": "users_pkey", "constraint_type": "PRIMARY KEY"},
	}, nil)

	det, err := GetObjectDetails(ctx, mock, "public", "users")
	require.NoError(t, err)
	require.NotNil(t, det)
	assert.Equal(t, "public", det.Schema)
	assert.Equal(t, "users", det.Name)
	assert.Equal(t, "BASE TABLE", det.Type)
	assert.Len(t, det.Columns, 2)
	assert.Equal(t, "id", det.Columns[0].Name)
	assert.False(t, det.Columns[0].Nullable)
	assert.True(t, det.Columns[1].Nullable)
	assert.Len(t, det.Indexes, 1)
	assert.True(t, det.Indexes[0].IsPrimary)
	assert.Equal(t, int64(1000), det.RowEstimate)
	assert.Equal(t, "256 kB", det.TotalSize)
	assert.Len(t, det.Constraints, 1)
}

func TestGetObjectDetails_View(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock()

	// 1. type query
	mock.AddQueryRows([]map[string]any{
		{"table_type": "VIEW"},
	}, nil)
	// 2. columns
	mock.AddQueryRows([]map[string]any{
		{"column_name": "id", "data_type": "integer", "nullable": false, "column_default": nil},
	}, nil)
	// 3. constraints (no indexes for views)
	mock.AddQueryRows(nil, nil)

	det, err := GetObjectDetails(ctx, mock, "public", "v_users")
	require.NoError(t, err)
	assert.Equal(t, "VIEW", det.Type)
	assert.Len(t, det.Columns, 1)
	assert.Empty(t, det.Indexes) // no index query for views
}

func TestGetObjectDetails_NotFound(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock().AddQueryRows(nil, nil) // empty type result

	_, err := GetObjectDetails(ctx, mock, "public", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetObjectDetails_TypeQueryError(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock().AddQueryRows(nil, errors.New("db error"))

	_, err := GetObjectDetails(ctx, mock, "public", "users")
	require.Error(t, err)
}

func TestGetObjectDetails_ColumnQueryError(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock()
	mock.AddQueryRows([]map[string]any{{"table_type": "BASE TABLE"}}, nil)
	mock.AddQueryRows(nil, errors.New("columns error"))

	_, err := GetObjectDetails(ctx, mock, "public", "users")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list columns")
}

func TestGetObjectDetails_IndexQueryError(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock()
	mock.AddQueryRows([]map[string]any{{"table_type": "BASE TABLE"}}, nil)
	mock.AddQueryRows(nil, nil) // columns ok (empty)
	mock.AddQueryRows(nil, errors.New("index error"))

	_, err := GetObjectDetails(ctx, mock, "public", "users")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list indexes")
}

func TestGetObjectDetails_ConstraintQueryError(t *testing.T) {
	ctx := context.Background()
	mock := dbtest.NewMock()
	mock.AddQueryRows([]map[string]any{{"table_type": "BASE TABLE"}}, nil) // type
	mock.AddQueryRows(nil, nil)                                            // columns
	mock.AddQueryRows(nil, nil)                                            // indexes
	mock.AddQueryRows(nil, nil)                                            // row stats (ignored error)
	mock.AddQueryRows(nil, errors.New("constraints error"))

	_, err := GetObjectDetails(ctx, mock, "public", "users")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list constraints")
}
