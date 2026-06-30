package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/config"
	"github.com/gupta-akshay/postgres-mcp/internal/dbtest"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// newServer builds a fresh mcp server with every tool registered against mock.
func newServer(t *testing.T, mock *dbtest.MockQuerier, cfg *config.Config) *mcpserver.MCPServer {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{AccessMode: "unrestricted"}
	}
	return BuildServer(cfg, mock)
}

// callTool starts an in-process client against s, invokes tool, returns the result.
func callTool(t *testing.T, s *mcpserver.MCPServer, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	c, err := client.NewInProcessClient(s)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	require.NoError(t, c.Start(ctx))
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)

	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args

	res, err := c.CallTool(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

// resultText returns the first text content of a tool result.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, r.Content)
	tb, ok := r.Content[0].(mcp.TextContent)
	require.True(t, ok, "content[0] must be TextContent")
	return tb.Text
}

// ─── BuildServer ──────────────────────────────────────────────────────────────

func TestBuildServer_RegistersAllTools(t *testing.T) {
	s := newServer(t, dbtest.NewMock(), nil)
	require.NotNil(t, s)

	c, err := client.NewInProcessClient(s)
	require.NoError(t, err)
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, c.Start(ctx))
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)

	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	expected := []string{
		"list_schemas", "list_objects", "get_object_details",
		"explain_query", "execute_sql", "get_top_queries",
		"analyze_workload_indexes", "analyze_query_indexes", "analyze_db_health",
	}
	for _, name := range expected {
		assert.True(t, names[name], "tool %s should be registered", name)
	}
}

// ─── list_schemas ─────────────────────────────────────────────────────────────

func TestHandler_ListSchemas_Success(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows([]map[string]any{
		{"schema_name": "public", "schema_owner": "postgres"},
	}, nil)

	res := callTool(t, newServer(t, mock, nil), "list_schemas", nil)
	assert.False(t, res.IsError)
	assert.Contains(t, resultText(t, res), "public")
}

func TestHandler_ListSchemas_Error(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows(nil, errors.New("boom"))

	res := callTool(t, newServer(t, mock, nil), "list_schemas", nil)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "boom")
}

// ─── list_objects ─────────────────────────────────────────────────────────────

func TestHandler_ListObjects_Success(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows([]map[string]any{
		{"name": "users", "type": "BASE TABLE", "size": "128 kB", "owner": "app"},
	}, nil)

	res := callTool(t, newServer(t, mock, nil), "list_objects",
		map[string]any{"schema": "public"})
	assert.False(t, res.IsError)
	assert.Contains(t, resultText(t, res), "users")
}

func TestHandler_ListObjects_MissingSchema(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "list_objects", nil)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "'schema' parameter is required")
}

func TestHandler_ListObjects_DBError(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows(nil, errors.New("schema boom"))

	res := callTool(t, newServer(t, mock, nil), "list_objects",
		map[string]any{"schema": "public"})
	assert.True(t, res.IsError)
}

// ─── get_object_details ───────────────────────────────────────────────────────

func TestHandler_GetObjectDetails_Success(t *testing.T) {
	mock := dbtest.NewMock().
		// type lookup
		AddQueryRows([]map[string]any{{"table_type": "BASE TABLE"}}, nil).
		// columns
		AddQueryRows([]map[string]any{
			{"column_name": "id", "data_type": "integer", "nullable": false, "column_default": "nextval(...)"},
		}, nil).
		// indexes
		AddQueryRows([]map[string]any{
			{"name": "users_pkey", "definition": "CREATE UNIQUE INDEX ...",
				"size": "16 kB", "is_unique": true, "is_primary": true},
		}, nil).
		// stats row_estimate/total_size
		AddQueryRows([]map[string]any{
			{"row_estimate": float64(100), "total_size": "32 kB"},
		}, nil).
		// constraints
		AddQueryRows([]map[string]any{
			{"constraint_name": "users_pkey", "constraint_type": "PRIMARY KEY"},
		}, nil)

	res := callTool(t, newServer(t, mock, nil), "get_object_details",
		map[string]any{"schema": "public", "object": "users"})
	assert.False(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "users")
	assert.Contains(t, text, "users_pkey")
}

func TestHandler_GetObjectDetails_MissingParams(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "get_object_details",
		map[string]any{"schema": "public"}) // missing object
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "required")
}

func TestHandler_GetObjectDetails_NotFound(t *testing.T) {
	// empty type lookup → sub-pkg returns "object not found"
	mock := dbtest.NewMock().AddQueryRows(nil, nil)

	res := callTool(t, newServer(t, mock, nil), "get_object_details",
		map[string]any{"schema": "public", "object": "ghost"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "not found")
}

// ─── explain_query ────────────────────────────────────────────────────────────

func TestHandler_ExplainQuery_Success(t *testing.T) {
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExplainJSON(0.5), nil)

	res := callTool(t, newServer(t, mock, nil), "explain_query",
		map[string]any{"query": "SELECT 1"})
	assert.False(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "total_cost")
}

func TestHandler_ExplainQuery_MissingQuery(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "explain_query", nil)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "'query'")
}

func TestHandler_ExplainQuery_WithHypotheticalIndexes(t *testing.T) {
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExplainJSON(10.0), nil).                      // base EXPLAIN
		AddInternalQuery(dbtest.ExtensionInstalled("1.4.0"), nil).            // hypopg check
		AddInternalQuery([]map[string]any{{"indexrelid": float64(42)}}, nil). // hypopg_create_index
		AddInternalQuery(dbtest.ExplainJSON(1.0), nil).                       // hypo EXPLAIN
		AddInternalQuery(nil, nil)                                            // hypopg_reset

	res := callTool(t, newServer(t, mock, nil), "explain_query", map[string]any{
		"query":                "SELECT 1",
		"hypothetical_indexes": []any{"CREATE INDEX ON t (col)"},
	})
	assert.False(t, res.IsError)
	assert.Contains(t, resultText(t, res), "improvement")
}

func TestHandler_ExplainQuery_DBError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("syntax error"))

	res := callTool(t, newServer(t, mock, nil), "explain_query",
		map[string]any{"query": "INVALID"})
	assert.True(t, res.IsError)
}

func TestHandler_ExplainQuery_RestrictedForcesAnalyzeFalse(t *testing.T) {
	// In restricted mode, analyze=true is silently forced to false.
	// The handler should still succeed (analyze=false runs without executing).
	cfg := &config.Config{AccessMode: "restricted"}
	mock := dbtest.NewMock().
		SetRestricted(true).
		AddInternalQuery(dbtest.ExplainJSON(0.5), nil)

	res := callTool(t, newServer(t, mock, cfg), "explain_query",
		map[string]any{"query": "SELECT 1", "analyze": true})
	assert.False(t, res.IsError)
	assert.Contains(t, resultText(t, res), "total_cost")
}

// ─── execute_sql ──────────────────────────────────────────────────────────────

func TestHandler_ExecuteSQL_Success(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows([]map[string]any{
		{"n": int32(1)},
	}, nil)

	res := callTool(t, newServer(t, mock, nil), "execute_sql",
		map[string]any{"query": "SELECT 1 AS n"})
	assert.False(t, res.IsError)
	text := resultText(t, res)

	var out struct {
		Rows     []map[string]any `json:"rows"`
		RowCount int              `json:"row_count"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	assert.Equal(t, 1, out.RowCount)
}

func TestHandler_ExecuteSQL_MissingQuery(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "execute_sql", nil)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "'query'")
}

func TestHandler_ExecuteSQL_DBError(t *testing.T) {
	mock := dbtest.NewMock().AddQueryRows(nil, errors.New("db down"))

	res := callTool(t, newServer(t, mock, nil), "execute_sql",
		map[string]any{"query": "SELECT 1"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "db down")
}

func TestHandler_ExecuteSQL_RestrictedDescription(t *testing.T) {
	// Covers the cfg.IsRestricted() = true branch in registerExecuteTool.
	cfg := &config.Config{AccessMode: "restricted"}
	mock := dbtest.NewMock().SetRestricted(true).
		AddQueryRows([]map[string]any{{"n": int32(1)}}, nil)
	s := newServer(t, mock, cfg)

	// Sanity: the tool still works with a SELECT because we pre-queued rows.
	res := callTool(t, s, "execute_sql", map[string]any{"query": "SELECT 1"})
	assert.False(t, res.IsError)

	// And the tool's description mentions "read-only"
	c, err := client.NewInProcessClient(s)
	require.NoError(t, err)
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, c.Start(ctx))
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)
	var desc string
	for _, tool := range tools.Tools {
		if tool.Name == "execute_sql" {
			desc = tool.Description
		}
	}
	assert.Contains(t, strings.ToLower(desc), "read-only")
}

// ─── get_top_queries ──────────────────────────────────────────────────────────

func TestHandler_GetTopQueries_Success(t *testing.T) {
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil). // pg_stat_statements
		AddQueryRows([]map[string]any{
			{"query": "SELECT 1", "calls": int64(100),
				"total_exec_time_ms": 123.4, "mean_exec_time_ms": 1.2,
				"stddev_exec_time_ms": 0.1, "rows": int64(100)},
		}, nil)

	res := callTool(t, newServer(t, mock, nil), "get_top_queries",
		map[string]any{"method": "total_time", "limit": float64(5)})
	assert.False(t, res.IsError)
	assert.Contains(t, resultText(t, res), "SELECT 1")
}

func TestHandler_GetTopQueries_DefaultMethod(t *testing.T) {
	// Empty method arg should default to total_time.
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil).
		AddQueryRows(nil, nil)

	res := callTool(t, newServer(t, mock, nil), "get_top_queries", nil)
	assert.False(t, res.IsError)
}

func TestHandler_GetTopQueries_ExtensionMissing(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil) // pg_stat_statements not available

	res := callTool(t, newServer(t, mock, nil), "get_top_queries", nil)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "pg_stat_statements")
}

// ─── analyze_workload_indexes ─────────────────────────────────────────────────

func TestHandler_AnalyzeWorkloadIndexes_Success(t *testing.T) {
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil).  // pg_stat_statements
		AddInternalQuery(dbtest.ExtensionInstalled("1.4.0"), nil). // hypopg
		AddInternalQuery(nil, nil)                                 // workload load → empty

	res := callTool(t, newServer(t, mock, nil), "analyze_workload_indexes",
		map[string]any{"workload_limit": float64(10)})
	assert.False(t, res.IsError)
}

func TestHandler_AnalyzeWorkloadIndexes_ExtensionMissing(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil) // pg_stat_statements missing

	res := callTool(t, newServer(t, mock, nil), "analyze_workload_indexes", nil)
	assert.True(t, res.IsError)
}

// ─── analyze_query_indexes ────────────────────────────────────────────────────

func TestHandler_AnalyzeQueryIndexes_Success(t *testing.T) {
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExtensionInstalled("1.4.0"), nil) // hypopg

	res := callTool(t, newServer(t, mock, nil), "analyze_query_indexes",
		map[string]any{"queries": []any{"SELECT 1"}})
	assert.False(t, res.IsError)
}

func TestHandler_AnalyzeQueryIndexes_EmptyQueries(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "analyze_query_indexes",
		map[string]any{"queries": []any{}})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "at least one")
}

func TestHandler_AnalyzeQueryIndexes_MissingQueries(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "analyze_query_indexes", nil)
	assert.True(t, res.IsError)
}

func TestHandler_AnalyzeQueryIndexes_TooManyQueries(t *testing.T) {
	// Pass 21 queries → handler truncates to 20. Mock only the hypopg check;
	// explainable filtering will exhaust remaining calls but sub-pkg tolerates that.
	mock := dbtest.NewMock().
		AddInternalQuery(dbtest.ExtensionInstalled("1.4.0"), nil)

	qs := make([]any, 21)
	for i := range qs {
		qs[i] = "SELECT 1"
	}
	res := callTool(t, newServer(t, mock, nil), "analyze_query_indexes",
		map[string]any{"queries": qs})
	assert.False(t, res.IsError)
}

func TestHandler_AnalyzeQueryIndexes_HypoPGMissing(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil) // hypopg not available

	res := callTool(t, newServer(t, mock, nil), "analyze_query_indexes",
		map[string]any{"queries": []any{"SELECT 1"}})
	assert.True(t, res.IsError)
}

// ─── analyze_db_health ────────────────────────────────────────────────────────

func TestHandler_AnalyzeDBHealth_Success(t *testing.T) {
	// Health checks run concurrently; each will make InternalQuery calls.
	// We just queue enough nil responses for them to all complete (returning error statuses).
	mock := dbtest.NewMock()
	for i := 0; i < 50; i++ {
		mock.AddInternalQuery(nil, nil)
	}

	res := callTool(t, newServer(t, mock, nil), "analyze_db_health",
		map[string]any{"checks": []any{"all"}})
	assert.False(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "check")
}

func TestHandler_AnalyzeDBHealth_Subset(t *testing.T) {
	mock := dbtest.NewMock()
	for i := 0; i < 20; i++ {
		mock.AddInternalQuery(nil, nil)
	}

	res := callTool(t, newServer(t, mock, nil), "analyze_db_health",
		map[string]any{"checks": []any{"index", "vacuum"}})
	assert.False(t, res.IsError)
}

func TestHandler_AnalyzeDBHealth_UnknownCheck(t *testing.T) {
	res := callTool(t, newServer(t, dbtest.NewMock(), nil), "analyze_db_health",
		map[string]any{"checks": []any{"bogus"}})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "bogus")
}
