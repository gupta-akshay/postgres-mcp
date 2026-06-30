# postgres-mcp — code guide

This document describes how the **postgres-mcp** repository is structured, how control flows from the CLI to PostgreSQL, and where to look when debugging. It complements the user-facing [README](../README.md).

## Stack

| Layer | Technology |
|--------|------------|
| Language | Go 1.26+ (`go.mod`) |
| CLI | [spf13/cobra](https://github.com/spf13/cobra) |
| MCP protocol | [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| PostgreSQL driver | [jackc/pgx/v5](https://github.com/jackc/pgx) connection pool |

## Repository layout

```
main.go                 # Delegates to cmd.Execute()
cmd/root.go             # Cobra root command, flags, DATABASE_URI handling
internal/config/        # Struct + IsRestricted()
internal/server/        # MCP server bootstrap, tool registration, JSON helpers
internal/db/            # Connection pool, access modes, Querier interface, row mapping
internal/dbtest/        # MockQuerier for unit tests
internal/schema/        # Catalog introspection (schemas, objects, details)
internal/explain/       # EXPLAIN (JSON), HypoPG hypothetical indexes
internal/topqueries/    # pg_stat_statements rankings (version-aware columns)
internal/health/        # Orchestrator + one file per check (index, connection, …)
internal/index/         # DTA-style index advisor (types.go + dta.go)
```

Feature code lives under `internal/` and depends on `db.Querier`, not on MCP types, which keeps business logic testable without a live MCP client.

## Boot sequence

1. **`main.go`** calls `cmd.Execute()`.
2. **`cmd/root.go`** parses flags and the optional positional database URI. The URI may also come from `DATABASE_URI`. `ACCESS_MODE` can override the default restricted mode when the flag is still the default.
3. **`server.Start`** in `internal/server/server.go`:
   - Builds a `db.Driver` with `db.New(ctx, url, restricted)`.
   - Constructs an `MCPServer` (name `postgres-mcp`, version `1.0.0`, tools-only capabilities).
   - Registers tools (schema, explain, execute, top queries, health, index tools).
   - Serves over **stdio** (default) or **SSE** (`NewSSEServer` + `Start` on `sse-host`:`sse-port`).

If the database URL is missing, the root command returns an error before `server.Start` runs.

## Configuration (`internal/config`)

`Config` holds:

- `DatabaseURL` — connection string.
- `AccessMode` — anything other than `"unrestricted"` is treated as **restricted** (`IsRestricted()`).

There is no separate config file; everything is flags and environment variables wired in `cmd/root.go`.

## Database access (`internal/db`)

### `Querier` interface

Sub-packages use `db.Querier`:

- `QueryRows` — user-facing / tool SQL; subject to restricted-mode rules.
- `InternalQuery` — server internals (EXPLAIN, HypoPG, health queries); **no** restricted keyword check; always runs as a direct pool query.
- `Execute` — writes; **blocked** in restricted mode.
- `Version` — reads `server_version_num` via `InternalQuery`.
- `IsRestricted` / `Close`.

Tests use `internal/dbtest.MockQuerier`, which implements the same interface with FIFO-queued responses.

### Restricted mode

When `restricted` is true:

1. `QueryRows` first runs `validateNotWrite` (prefix check for DML/DDL keywords). This is a **secondary** guard.
2. SQL runs inside a **read-only transaction** with `SET LOCAL statement_timeout` (30s). Primary enforcement is PostgreSQL's read-only transaction, not the keyword list.

When unrestricted, `QueryRows` uses the pool directly with no transaction wrapper.

### Internal query tag

`InternalQuery` prefixes SQL with `/* postgres-mcp */`. **`topqueries`** and **`index.AnalyzeWorkload`** filter out statements containing that substring so the server's own queries do not pollute workload statistics.

### Row handling

- `collectRows` maps `pgx` rows to `[]map[string]any` using column names as keys.
- `jsonFriendly` normalises `[]byte`, UUIDs, and `Stringer` values for JSON output.

### Extensions (`internal/db/extensions.go`)

`CheckExtension` unions `pg_extension` and `pg_available_extensions`. `RequireExtension` returns actionable errors (install package vs `CREATE EXTENSION`). Used by explain (HypoPG), top queries / DTA (`pg_stat_statements`, HypoPG).

## MCP server (`internal/server/server.go`)

### Tool registration pattern

Each tool is `mcp.NewTool(...)` plus a handler that:

1. Reads arguments via `getString`, `getBool`, `getInt`, `getFloat`, `getStringArray`.
2. Calls a package-level function in `schema`, `explain`, `topqueries`, `health`, or `index`.
3. Returns `jsonResult(value)` → `json.MarshalIndent` → `mcp.NewToolResultText`, or `mcp.NewToolResultError` for client-visible errors.

Errors from the domain layer are returned as tool errors (not panics), so the MCP client always gets a structured failure message.

### Request argument parsing

`args()` normalises `req.Params.Arguments` to `map[string]any` for compatibility across mcp-go versions. Numeric tool parameters arrive as `float64` from JSON; `getInt` truncates accordingly.

### Registered tools (mental map)

| Tool | Package | Notes |
|------|---------|--------|
| `list_schemas` | `schema` | `information_schema.schemata` |
| `list_objects` | `schema` | Tables/views + sequences in one schema |
| `get_object_details` | `schema` | Columns, indexes (tables), constraints, stats |
| `explain_query` | `explain` | Optional `analyze`, optional HypoPG indexes |
| `execute_sql` | `db.QueryRows` | Description reflects restricted vs unrestricted |
| `get_top_queries` | `topqueries` | Needs `pg_stat_statements` |
| `analyze_db_health` | `health` | Concurrent checks |
| `analyze_workload_indexes` | `index` | DTA + workload from stats |
| `analyze_query_indexes` | `index` | DTA on up to 20 explicit queries |

## Schema introspection (`internal/schema`)

- **ListSchemas** — non-system schemas from `information_schema.schemata`.
- **ListObjects** — `information_schema.tables` joined with `pg_tables` for size; union with `information_schema.sequences`.
- **GetObjectDetails** — resolves type (table/view/sequence), loads columns from `information_schema.columns`, indexes from `pg_index` / `pg_class` / `pg_namespace` for `BASE TABLE`, row estimate and total size from `pg_class`, constraints from `information_schema.table_constraints`.

All of these use `QueryRows`, so they respect restricted mode.

## EXPLAIN and HypoPG (`internal/explain`)

- **`ExplainQuery`** runs a baseline `EXPLAIN (FORMAT JSON, COSTS true)` via `InternalQuery` (and optionally `ANALYZE`, `BUFFERS`). If hypothetical index definitions are provided, it checks HypoPG, then **`withHypotheticalIndexes`**: for each definition, `SELECT hypopg_create_index('...')` (with escaped quotes), runs the callback, then **`hypopg_reset()`** in a `defer`-like pattern (always reset, even on error).
- Parsed JSON is the standard PostgreSQL array form; the code reads `Plan`, `Planning Time`, `Execution Time`, and top-level **`Total Cost`** from the plan node.
- **`GetQueryCost` / `GetQueryCostWithIndexes`** are used by the index advisor to score queries.

Debugging tips: if EXPLAIN fails for a query, DTA will drop it in `filterExplainable`. Wrong column name for the plan row (`QUERY PLAN` vs `query plan`) is handled with a fallback.

## Top queries (`internal/topqueries`)

- Requires **`pg_stat_statements`** installed.
- PostgreSQL **12 vs 13+** renames timing columns (`total_time` → `total_exec_time`, etc.); `timeCol` selects the correct names using `d.Version()`.
- **`resource`** method builds a CTE of totals, ranks by share of time/blocks/WAL, and filters rows where any share exceeds 5%. WAL fields are gated on version ≥ 13.
- Queries exclude the internal comment pattern used by `InternalQuery`.

## Health checks (`internal/health`)

**`health.go`** — `AnalyzeHealth` resolves check names (`all`, empty → full set), then runs each selected check in its own goroutine and collects `[]Result`.

Each result has `check`, `status` (`ok` / `warning` / `critical` / `error`), `message`, and optional `details`.

| File | Focus |
|------|--------|
| `index.go` | Invalid, duplicate, bloated (over 100MB heuristic), unused (under 50 scans) indexes |
| `connections.go` | `pg_stat_activity` vs `max_connections`, idle-in-transaction |
| `vacuum.go` | Top tables by `age(relfrozenxid)`, dead tuples, vacuum timestamps |
| `sequences.go` | `pg_sequences` usage near max |
| `replication.go` | `pg_is_in_recovery`, `pg_stat_replication`, replication slots |
| `buffers.go` | Aggregate cache hit % from `pg_statio_user_tables` / `pg_statio_user_indexes` |
| `constraints.go` | `NOT convalidated` constraints in user schemas |

All health SQL goes through **`InternalQuery`** so it is not subject to read-only transaction wrapping or the client keyword filter (and does not need to be "SELECT-only" for the execute path).

## Index advisor / DTA (`internal/index`)

- **`types.go`** — `IndexDefinition`, `CreateSQL()` (qualified table, `pgmcp_*` index names), `DTAConfig` / `DefaultDTAConfig`, JSON DTOs for recommendations and summary.
- **`dta.go`** — main algorithm:
  1. **`filterExplainable`** — keep queries where `explain.GetQueryCost` succeeds.
  2. **`computeCosts`** — map query → planner total cost; with indexes, uses `explain.GetQueryCostWithIndexes`.
  3. **`generateCandidates`** — for each query, **`extractPlanInfo`** runs `EXPLAIN (FORMAT JSON)` and walks the plan JSON (`walkPlanNode`) collecting relation names and columns from filter/join/hash/sort fields via regex; builds multi-column combinations up to `MaxIndexWidth`.
  4. **`filterExistingIndexes`** — compares candidates to `pg_stat_user_indexes` definitions with **`hasSimilarIndex`** (heuristic substring match).
  5. **Greedy loop** — until time budget, each remaining candidate is evaluated with current selection via HypoPG costs; objective is `log(cost) + ParetoAlpha * log(size_mb)`; **`estimateIndexSizeMB`** uses `hypopg_create_index`, `hypopg_relation_size`, then **`hypopg_reset()`**.
  6. **`buildResult`** — summary, per-index recommendations, per-query impact.

**`AnalyzeWorkload`** requires both **`pg_stat_statements`** and **HypoPG**; **`AnalyzeQueries`** only requires HypoPG.

Workload query text is loaded with **`InternalQuery`** (and excludes the postgres-mcp comment). Server tool **`analyze_query_indexes`** caps the list at 20 queries.

## Testing

- **Unit tests** — packages use `dbtest.NewMock()` and enqueue `AddInternalQuery` / `AddQueryRows` responses in order. Helpers like `ExplainJSON`, `ExtensionInstalled` build row shapes the production code expects.
- **Integration tests** — `internal/db/integration_test.go` (and similar) may require a real `DATABASE_URL`; skip when unset.

When adding a new code path that issues SQL, decide whether it must be **`QueryRows`** (respects restricted mode for tool-surfaced SQL) or **`InternalQuery`** (trusted server operations).

## Debugging checklist

1. **Connection** — Parse/ping failures originate in `db.New`; check URL, TLS, and network.
2. **Restricted vs unrestricted** — `execute_sql` and keyword validation only affect `QueryRows` / `Execute`; EXPLAIN and health use `InternalQuery`.
3. **Missing extension** — Errors often come from `db.RequireExtension`; message distinguishes "not available" vs "available but not installed".
4. **Version-specific SQL** — Top queries and DTA workload ordering use `Version()` for column names; odd behaviour on PG12 vs PG15 may be column renames.
5. **MCP arguments** — If a tool sees empty parameters, verify the client sends the schema expected by `args()` (map vs wrapped types).
6. **HypoPG state** — `hypopg_reset()` is called after hypothetical runs; if HypoPG errors mid-flight, reset is still attempted; persistent odd planner behaviour on the server is unlikely from this process but worth knowing for manual DB experiments.

## Dependency direction

```
cmd → server → { db, schema, explain, topqueries, health, index }
index → explain → db
schema, topqueries, health → db
```

Avoid importing `server` or `cmd` from `internal` packages; keep new features behind `db.Querier` for testability.
