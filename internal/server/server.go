package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/gupta-akshay/postgres-mcp/internal/config"
	"github.com/gupta-akshay/postgres-mcp/internal/db"
	"github.com/gupta-akshay/postgres-mcp/internal/explain"
	"github.com/gupta-akshay/postgres-mcp/internal/health"
	"github.com/gupta-akshay/postgres-mcp/internal/index"
	"github.com/gupta-akshay/postgres-mcp/internal/schema"
	"github.com/gupta-akshay/postgres-mcp/internal/topqueries"
)

// BuildServer constructs an MCP server with every tool registered against the
// supplied Querier. It is transport-agnostic so tests can drive it through an
// in-process client.
func BuildServer(cfg *config.Config, d db.Querier) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer(
		"postgres-mcp",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)

	registerSchemaTools(s, d)
	registerExplainTool(s, d)
	registerExecuteTool(s, d, cfg)
	registerTopQueriesTool(s, d)
	registerHealthTool(s, d)
	registerIndexTools(s, d)

	return s
}

// Start initialises the database connection, registers all MCP tools and
// starts serving on the configured transport.
func Start(cfg *config.Config) error {
	ctx := context.Background()

	driver, err := db.New(ctx, cfg.DatabaseURL, cfg.IsRestricted())
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer driver.Close()

	s := BuildServer(cfg, driver)

	switch cfg.Transport {
	case "sse":
		addr := fmt.Sprintf("%s:%d", cfg.SSEHost, cfg.SSEPort)
		baseURL := fmt.Sprintf("http://%s", addr)
		sseServer := mcpserver.NewSSEServer(s, mcpserver.WithBaseURL(baseURL))
		fmt.Printf("postgres-mcp listening on %s (SSE)\n", addr)
		return sseServer.Start(addr)
	default: // "stdio"
		return mcpserver.ServeStdio(s)
	}
}

// ─── schema tools ─────────────────────────────────────────────────────────────

func registerSchemaTools(s *mcpserver.MCPServer, d db.Querier) {
	// list_schemas
	s.AddTool(
		mcp.NewTool("list_schemas",
			mcp.WithDescription("List all non-system schemas in the database, including their owners."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			schemas, err := schema.ListSchemas(ctx, d)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(schemas)
		},
	)

	// list_objects
	s.AddTool(
		mcp.NewTool("list_objects",
			mcp.WithDescription("List tables, views, and sequences within a schema."),
			mcp.WithString("schema",
				mcp.Required(),
				mcp.Description("Schema name (e.g. 'public')"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			schemaName := getString(req, "schema")
			if schemaName == "" {
				return mcp.NewToolResultError("'schema' parameter is required"), nil
			}
			objs, err := schema.ListObjects(ctx, d, schemaName)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(objs)
		},
	)

	// get_object_details
	s.AddTool(
		mcp.NewTool("get_object_details",
			mcp.WithDescription("Get detailed information about a table or view: columns, indexes, constraints, and row estimates."),
			mcp.WithString("schema",
				mcp.Required(),
				mcp.Description("Schema name"),
			),
			mcp.WithString("object",
				mcp.Required(),
				mcp.Description("Table or view name"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			schemaName := getString(req, "schema")
			objectName := getString(req, "object")
			if schemaName == "" || objectName == "" {
				return mcp.NewToolResultError("'schema' and 'object' parameters are required"), nil
			}
			det, err := schema.GetObjectDetails(ctx, d, schemaName, objectName)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(det)
		},
	)
}

// ─── explain tool ─────────────────────────────────────────────────────────────

func registerExplainTool(s *mcpserver.MCPServer, d db.Querier) {
	s.AddTool(
		mcp.NewTool("explain_query",
			mcp.WithDescription(
				"Generate an EXPLAIN plan for a SQL query. "+
					"Optionally use ANALYZE for actual run-time stats, or supply hypothetical CREATE INDEX "+
					"statements (requires HypoPG) to simulate index impact without modifying the schema.",
			),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("SQL query to explain"),
			),
			mcp.WithBoolean("analyze",
				mcp.Description("Run EXPLAIN ANALYZE (actually executes the query). Default false."),
			),
			mcp.WithArray("hypothetical_indexes",
				mcp.Description("List of CREATE INDEX statements to simulate via HypoPG (e.g. 'CREATE INDEX ON orders (status)')"),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := getString(req, "query")
			if query == "" {
				return mcp.NewToolResultError("'query' parameter is required"), nil
			}
			analyze := getBool(req, "analyze")
			hypoStrs := getStringArray(req, "hypothetical_indexes")

			hypoIdxs := make([]explain.HypotheticalIndex, len(hypoStrs))
			for i, s := range hypoStrs {
				hypoIdxs[i] = explain.HypotheticalIndex{Definition: s}
			}

			result, err := explain.ExplainQuery(ctx, d, query, analyze, hypoIdxs)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(result)
		},
	)
}

// ─── execute_sql tool ─────────────────────────────────────────────────────────

func registerExecuteTool(s *mcpserver.MCPServer, d db.Querier, cfg *config.Config) {
	desc := "Execute a SQL statement and return results as JSON."
	if cfg.IsRestricted() {
		desc += " In restricted mode only read-only SELECT statements are permitted."
	} else {
		desc += " In unrestricted mode any SQL may be executed, including DDL and DML."
	}

	s.AddTool(
		mcp.NewTool("execute_sql",
			mcp.WithDescription(desc),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("SQL statement to execute"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := getString(req, "query")
			if query == "" {
				return mcp.NewToolResultError("'query' parameter is required"), nil
			}
			rows, err := d.QueryRows(ctx, query)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			type execResult struct {
				Rows     []map[string]any `json:"rows"`
				RowCount int              `json:"row_count"`
			}
			return jsonResult(execResult{Rows: rows, RowCount: len(rows)})
		},
	)
}

// ─── top_queries tool ─────────────────────────────────────────────────────────

func registerTopQueriesTool(s *mcpserver.MCPServer, d db.Querier) {
	s.AddTool(
		mcp.NewTool("get_top_queries",
			mcp.WithDescription(
				"Return the slowest or most resource-intensive queries from pg_stat_statements. "+
					"Requires the pg_stat_statements extension.",
			),
			mcp.WithString("method",
				mcp.Description("Ranking method: 'total_time' (default), 'mean_time', or 'resource' (blended I/O + CPU + WAL)"),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of queries to return (default 10)"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			methodStr := getString(req, "method")
			if methodStr == "" {
				methodStr = "total_time"
			}
			limit := getInt(req, "limit", 10)

			stats, err := topqueries.GetTopQueries(ctx, d, topqueries.Method(methodStr), limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(stats)
		},
	)
}

// ─── health tool ──────────────────────────────────────────────────────────────

func registerHealthTool(s *mcpserver.MCPServer, d db.Querier) {
	s.AddTool(
		mcp.NewTool("analyze_db_health",
			mcp.WithDescription(
				"Run database health checks across one or more dimensions. "+
					"Pass 'all' or omit 'checks' to run every check. "+
					"Available checks: index, connection, vacuum, sequence, replication, buffer, constraint.",
			),
			mcp.WithArray("checks",
				mcp.Description("List of checks to run, e.g. ['index','vacuum'] or ['all']"),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			checks := getStringArray(req, "checks")
			results, err := health.AnalyzeHealth(ctx, d, checks)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(results)
		},
	)
}

// ─── index tools ──────────────────────────────────────────────────────────────

func registerIndexTools(s *mcpserver.MCPServer, d db.Querier) {
	// analyze_workload_indexes
	s.AddTool(
		mcp.NewTool("analyze_workload_indexes",
			mcp.WithDescription(
				"Analyse the current query workload (from pg_stat_statements) and recommend indexes "+
					"using the Database Tuning Advisor (DTA) greedy algorithm. "+
					"Requires pg_stat_statements and HypoPG extensions.",
			),
			mcp.WithNumber("time_limit_seconds",
				mcp.Description("Maximum analysis time in seconds (default 30)"),
			),
			mcp.WithNumber("max_index_width",
				mcp.Description("Maximum number of columns per index (default 4)"),
			),
			mcp.WithNumber("min_improvement_pct",
				mcp.Description("Minimum query cost improvement % to accept an index (default 5)"),
			),
			mcp.WithNumber("budget_mb",
				mcp.Description("Total index storage budget in MB (-1 = unlimited, default)"),
			),
			mcp.WithNumber("workload_limit",
				mcp.Description("Maximum number of queries to pull from pg_stat_statements (default 50)"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cfg := dtaConfigFromRequest(req)
			result, err := index.AnalyzeWorkload(ctx, d, cfg)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(result)
		},
	)

	// analyze_query_indexes
	s.AddTool(
		mcp.NewTool("analyze_query_indexes",
			mcp.WithDescription(
				"Analyse a list of explicit SQL queries and recommend indexes using the DTA greedy algorithm. "+
					"Requires HypoPG. Up to 20 queries may be submitted.",
			),
			mcp.WithArray("queries",
				mcp.Required(),
				mcp.Description("List of SQL queries to analyse"),
				mcp.Items(map[string]any{"type": "string"}),
			),
			mcp.WithNumber("time_limit_seconds",
				mcp.Description("Maximum analysis time in seconds (default 30)"),
			),
			mcp.WithNumber("max_index_width",
				mcp.Description("Maximum columns per index (default 4)"),
			),
			mcp.WithNumber("min_improvement_pct",
				mcp.Description("Minimum improvement % to accept an index (default 5)"),
			),
			mcp.WithNumber("budget_mb",
				mcp.Description("Total storage budget in MB (-1 = unlimited)"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			queries := getStringArray(req, "queries")
			if len(queries) == 0 {
				return mcp.NewToolResultError("'queries' parameter must contain at least one SQL statement"), nil
			}
			if len(queries) > 20 {
				queries = queries[:20]
			}
			cfg := dtaConfigFromRequest(req)
			result, err := index.AnalyzeQueries(ctx, d, queries, cfg)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(result)
		},
	)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// args returns the arguments map from a CallToolRequest, handling both the
// map[string]any and any (JSON-decoded) variants that different mcp-go versions use.
func args(req mcp.CallToolRequest) map[string]any {
	switch v := req.Params.Arguments.(type) {
	case map[string]any:
		return v
	default:
		return nil
	}
}

func getString(req mcp.CallToolRequest, key string) string {
	a := args(req)
	if a == nil {
		return ""
	}
	if v, ok := a[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(req mcp.CallToolRequest, key string) bool {
	a := args(req)
	if a == nil {
		return false
	}
	if v, ok := a[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getInt(req mcp.CallToolRequest, key string, defaultVal int) int {
	a := args(req)
	if a == nil {
		return defaultVal
	}
	if v, ok := a[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return defaultVal
}

func getFloat(req mcp.CallToolRequest, key string, defaultVal float64) float64 {
	a := args(req)
	if a == nil {
		return defaultVal
	}
	if v, ok := a[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return defaultVal
}

func getStringArray(req mcp.CallToolRequest, key string) []string {
	a := args(req)
	if a == nil {
		return nil
	}
	v, ok := a[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func dtaConfigFromRequest(req mcp.CallToolRequest) index.DTAConfig {
	cfg := index.DefaultDTAConfig()
	if v := getFloat(req, "time_limit_seconds", 0); v > 0 {
		cfg.TimeLimitSeconds = v
	}
	if v := getInt(req, "max_index_width", 0); v > 0 {
		cfg.MaxIndexWidth = v
	}
	if v := getFloat(req, "min_improvement_pct", 0); v > 0 {
		cfg.MinImprovementPct = v
	}
	if v := getFloat(req, "budget_mb", 0); v != 0 {
		cfg.BudgetMB = v
	}
	if v := getInt(req, "workload_limit", 0); v > 0 {
		cfg.WorkloadLimit = v
	}
	return cfg
}
