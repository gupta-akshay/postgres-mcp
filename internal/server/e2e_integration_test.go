package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/config"
	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// ─── E2E harness ──────────────────────────────────────────────────────────────

// setupE2E starts an in-process MCP server backed by a real postgres, returns
// an initialised client plus cleanup.
func setupE2E(t *testing.T, restricted bool) (*client.Client, *db.Driver) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping E2E test")
	}

	driver, err := db.New(context.Background(), dsn, restricted)
	require.NoError(t, err)
	t.Cleanup(driver.Close)

	mode := "unrestricted"
	if restricted {
		mode = "restricted"
	}
	cfg := &config.Config{DatabaseURL: dsn, AccessMode: mode}
	s := BuildServer(cfg, driver)

	c, err := client.NewInProcessClient(s)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	require.NoError(t, c.Start(ctx))
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)
	return c, driver
}

// callToolE2E invokes a tool and returns the decoded JSON payload.
func callToolE2E(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	require.NoError(t, err)
	return res
}

func decodeJSON(t *testing.T, res *mcp.CallToolResult, dst any) {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tb, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tb.Text), dst))
}

// seedE2E creates a fixture table + rows on the shared DB, drops on cleanup.
// Also runs a handful of filtered SELECTs so pg_stat_statements has content.
func seedE2E(t *testing.T, d *db.Driver) string {
	t.Helper()
	ctx := context.Background()

	table := fmt.Sprintf("e2e_orders_%d", time.Now().UnixNano())

	require.NoError(t, d.Execute(ctx, fmt.Sprintf(`
		CREATE TABLE %s (
			id        SERIAL PRIMARY KEY,
			status    TEXT,
			user_id   INT,
			created   TIMESTAMPTZ DEFAULT now()
		)`, table)))
	t.Cleanup(func() {
		_ = d.Execute(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	})

	// 100 rows
	require.NoError(t, d.Execute(ctx, fmt.Sprintf(`
		INSERT INTO %s (status, user_id)
		SELECT
			CASE WHEN s %% 3 = 0 THEN 'active' ELSE 'inactive' END,
			s %% 17
		FROM generate_series(1, 100) AS s
	`, table)))

	// Populate pg_stat_statements with a couple of user-facing queries.
	for i := 0; i < 3; i++ {
		_, err := d.QueryRows(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE status = 'active'", table))
		require.NoError(t, err)
	}
	return table
}

// ─── list_schemas ─────────────────────────────────────────────────────────────

func TestE2E_ListSchemas(t *testing.T) {
	c, _ := setupE2E(t, false)
	res := callToolE2E(t, c, "list_schemas", nil)
	assert.False(t, res.IsError)

	var schemas []map[string]any
	decodeJSON(t, res, &schemas)

	var found bool
	for _, s := range schemas {
		if s["name"] == "public" {
			found = true
			assert.NotEmpty(t, s["owner"])
		}
	}
	assert.True(t, found, "public schema should be listed")
}

// ─── list_objects ─────────────────────────────────────────────────────────────

func TestE2E_ListObjects(t *testing.T) {
	c, d := setupE2E(t, false)
	table := seedE2E(t, d)

	res := callToolE2E(t, c, "list_objects", map[string]any{"schema": "public"})
	assert.False(t, res.IsError)

	var objs []map[string]any
	decodeJSON(t, res, &objs)

	var found bool
	for _, o := range objs {
		if o["name"] == table {
			found = true
			assert.Equal(t, "BASE TABLE", o["type"])
		}
	}
	assert.True(t, found, "seeded table should be listed")
}

// ─── get_object_details ───────────────────────────────────────────────────────

func TestE2E_GetObjectDetails(t *testing.T) {
	c, d := setupE2E(t, false)
	table := seedE2E(t, d)

	res := callToolE2E(t, c, "get_object_details",
		map[string]any{"schema": "public", "object": table})
	assert.False(t, res.IsError)

	var det map[string]any
	decodeJSON(t, res, &det)

	assert.Equal(t, table, det["name"])
	cols, _ := det["columns"].([]any)
	assert.NotEmpty(t, cols)
	idxs, _ := det["indexes"].([]any)
	assert.NotEmpty(t, idxs, "at least the PK index")
}

// ─── explain_query ────────────────────────────────────────────────────────────

func TestE2E_ExplainQuery(t *testing.T) {
	c, _ := setupE2E(t, false)

	res := callToolE2E(t, c, "explain_query", map[string]any{"query": "SELECT 1 AS n"})
	assert.False(t, res.IsError)

	var out map[string]any
	decodeJSON(t, res, &out)

	base, _ := out["base_plan"].(map[string]any)
	require.NotNil(t, base)
	cost, _ := base["total_cost"].(float64)
	assert.GreaterOrEqual(t, cost, 0.0)
}

func TestE2E_ExplainQuery_WithHypoIndex(t *testing.T) {
	c, d := setupE2E(t, false)

	info, err := db.CheckExtension(context.Background(), d, "hypopg")
	require.NoError(t, err)
	if !info.Installed {
		t.Skip("hypopg not installed — skipping")
	}

	table := seedE2E(t, d)

	res := callToolE2E(t, c, "explain_query", map[string]any{
		"query":                fmt.Sprintf("SELECT * FROM %s WHERE status = 'active'", table),
		"hypothetical_indexes": []any{fmt.Sprintf("CREATE INDEX ON %s (status)", table)},
	})
	assert.False(t, res.IsError)

	var out map[string]any
	decodeJSON(t, res, &out)

	assert.NotNil(t, out["hypo_plan"], "hypo_plan should be present")
	// improvement_factor is optional — just check the key exists when cost > 0
	if b, ok := out["base_plan"].(map[string]any); ok {
		if cost, _ := b["total_cost"].(float64); cost > 0 {
			assert.Contains(t, out, "improvement_factor")
		}
	}
}

// ─── execute_sql ──────────────────────────────────────────────────────────────

func TestE2E_ExecuteSQL_Select(t *testing.T) {
	c, _ := setupE2E(t, false)

	res := callToolE2E(t, c, "execute_sql", map[string]any{"query": "SELECT 1 AS n"})
	assert.False(t, res.IsError)

	var out struct {
		Rows     []map[string]any `json:"rows"`
		RowCount int              `json:"row_count"`
	}
	decodeJSON(t, res, &out)
	assert.Equal(t, 1, out.RowCount)
	assert.EqualValues(t, 1, out.Rows[0]["n"])
}

func TestE2E_ExecuteSQL_RestrictedBlocksWrite(t *testing.T) {
	c, _ := setupE2E(t, true)

	res := callToolE2E(t, c, "execute_sql",
		map[string]any{"query": "INSERT INTO pg_class (relname) VALUES ('x')"})
	assert.True(t, res.IsError)
	tb, _ := res.Content[0].(mcp.TextContent)
	assert.Contains(t, tb.Text, "not allowed")
}

// ─── get_top_queries ──────────────────────────────────────────────────────────

func TestE2E_GetTopQueries_AllMethods(t *testing.T) {
	c, d := setupE2E(t, false)

	info, err := db.CheckExtension(context.Background(), d, "pg_stat_statements")
	require.NoError(t, err)
	if !info.Installed {
		t.Skip("pg_stat_statements not installed — skipping")
	}

	seedE2E(t, d)

	for _, method := range []string{"total_time", "mean_time", "resource"} {
		t.Run(method, func(t *testing.T) {
			res := callToolE2E(t, c, "get_top_queries",
				map[string]any{"method": method, "limit": float64(5)})
			assert.False(t, res.IsError)

			var stats []map[string]any
			decodeJSON(t, res, &stats)
			// Resource method may return empty if nothing is > 5% of total.
			// Only require non-empty query strings when rows came back.
			for _, s := range stats {
				assert.NotEmpty(t, s["query"])
			}
		})
	}
}

// ─── analyze_workload_indexes ─────────────────────────────────────────────────

func TestE2E_AnalyzeWorkloadIndexes(t *testing.T) {
	c, d := setupE2E(t, false)

	for _, ext := range []string{"pg_stat_statements", "hypopg"} {
		info, err := db.CheckExtension(context.Background(), d, ext)
		require.NoError(t, err)
		if !info.Installed {
			t.Skipf("%s not installed — skipping", ext)
		}
	}

	seedE2E(t, d)

	res := callToolE2E(t, c, "analyze_workload_indexes", map[string]any{
		"workload_limit":      float64(10),
		"time_limit_seconds":  float64(5),
		"min_improvement_pct": float64(5),
	})
	assert.False(t, res.IsError)

	var out map[string]any
	decodeJSON(t, res, &out)
	// Summary may be empty if workload was tiny; just ensure the key exists.
	assert.Contains(t, out, "summary")
}

// ─── analyze_query_indexes ────────────────────────────────────────────────────

func TestE2E_AnalyzeQueryIndexes(t *testing.T) {
	c, d := setupE2E(t, false)

	info, err := db.CheckExtension(context.Background(), d, "hypopg")
	require.NoError(t, err)
	if !info.Installed {
		t.Skip("hypopg not installed — skipping")
	}
	table := seedE2E(t, d)

	res := callToolE2E(t, c, "analyze_query_indexes", map[string]any{
		"queries":            []any{fmt.Sprintf("SELECT * FROM %s WHERE status = 'active'", table)},
		"time_limit_seconds": float64(5),
	})
	assert.False(t, res.IsError)

	var out map[string]any
	decodeJSON(t, res, &out)
	assert.Contains(t, out, "summary")
}

func TestE2E_AnalyzeQueryIndexes_EmptyQueries(t *testing.T) {
	c, _ := setupE2E(t, false)

	res := callToolE2E(t, c, "analyze_query_indexes",
		map[string]any{"queries": []any{}})
	assert.True(t, res.IsError)
}

// ─── analyze_db_health ────────────────────────────────────────────────────────

func TestE2E_AnalyzeDBHealth_All(t *testing.T) {
	c, _ := setupE2E(t, false)

	res := callToolE2E(t, c, "analyze_db_health",
		map[string]any{"checks": []any{"all"}})
	assert.False(t, res.IsError)

	var results []map[string]any
	decodeJSON(t, res, &results)
	assert.Len(t, results, 7, "expected 7 health check results")
	for _, r := range results {
		assert.NotEmpty(t, r["check"])
		assert.NotEmpty(t, r["status"])
	}
}

func TestE2E_AnalyzeDBHealth_Subset(t *testing.T) {
	c, _ := setupE2E(t, false)

	res := callToolE2E(t, c, "analyze_db_health",
		map[string]any{"checks": []any{"index", "vacuum"}})
	assert.False(t, res.IsError)

	var results []map[string]any
	decodeJSON(t, res, &results)
	assert.Len(t, results, 2)
}
