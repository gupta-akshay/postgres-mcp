# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o postgres-mcp .

# Run (binary)
./postgres-mcp "postgresql://user:pass@localhost:5432/mydb" --access-mode unrestricted

# Unit tests (no DB required)
go test ./...
make test-unit

# Full integration suite (spins up Docker Postgres on port 5433)
make test-integration

# Full suite with coverage (requires ≥ 95% total)
make coverage

# Disposable test DB lifecycle
make test-db-up
make test-db-down

# Run integration tests manually against any live Postgres
export TEST_DATABASE_URL="postgresql://postgres:test@localhost:5433/postgres_mcp_test?sslmode=disable"
go test -tags integration ./...

# Run a single package's tests
go test ./internal/health/...
go test -tags integration ./internal/db/...

# Lint / format
make lint
make fmt
go vet ./...
```

## Architecture

This is a PostgreSQL MCP server: it exposes nine tools over the Model Context Protocol so AI assistants can introspect and query a live Postgres database. It supports both **stdio** (default, one container per session) and **SSE** (long-running HTTP service) transports.

### Dependency direction

```
cmd/root.go → server.Start → { schema, explain, topqueries, health, index }
index → explain → db
schema, topqueries, health → db
```

Feature packages depend only on `db.Querier` — never on MCP types or the `server`/`cmd` packages. This keeps business logic testable without a live MCP client.

### `internal/db` — the Querier interface

All feature code calls `db.Querier`, not pgx directly:

- `QueryRows` — tool-surfaced SQL; subject to restricted-mode rules (keyword check + read-only transaction wrapper).
- `InternalQuery` — trusted server operations (EXPLAIN, health, DTA); bypasses restricted mode; prefixes SQL with `/* postgres-mcp */` so its own queries are filtered from workload stats.
- `Execute` — writes; blocked in restricted mode.
- `Version()` — used by `topqueries` and `index` to pick PG12 vs PG13+ column names.

Unit tests use `internal/dbtest.MockQuerier`, which queues FIFO responses via `AddQueryRows` / `AddInternalQuery`.

### `internal/server` — MCP wiring

`BuildServer` registers all tools against a `db.Querier`. It is transport-agnostic so E2E tests can drive it via an in-process MCP client. Each handler: reads args via `getString`/`getBool`/`getInt`/`getFloat`/`getStringArray` → calls the domain function → returns `jsonResult(value)` or `mcp.NewToolResultError`.

### `internal/health` — concurrent health checks

`AnalyzeHealth` runs each selected check in its own goroutine. Each check file (`index.go`, `connections.go`, `vacuum.go`, `sequences.go`, `replication.go`, `buffers.go`, `constraints.go`) issues SQL via `InternalQuery` and returns `[]Result` with `status` of `ok`/`warning`/`critical`/`error`.

### `internal/index` — DTA greedy algorithm

`dta.go` implements a greedy Database Tuning Advisor:
1. `filterExplainable` — drop queries where EXPLAIN fails.
2. `generateCandidates` — parse EXPLAIN JSON, walk `walkPlanNode`, extract columns from filter/join/hash/sort predicates via regex, build multi-column combinations up to `MaxIndexWidth`.
3. `filterExistingIndexes` — heuristic substring match against `pg_stat_user_indexes`.
4. Greedy loop — score candidates with HypoPG; objective is `log(cost) + ParetoAlpha * log(size_mb)`; always call `hypopg_reset()` after each hypothetical run.

`AnalyzeWorkload` requires both `pg_stat_statements` and HypoPG. `AnalyzeQueries` only requires HypoPG.

### `internal/explain` — HypoPG integration

`withHypotheticalIndexes` creates hypothetical indexes via `hypopg_create_index(...)`, runs a callback, then **always** calls `hypopg_reset()` (even on error) to avoid polluting the planner.

### Extension handling

`db.CheckExtension` / `db.RequireExtension` distinguish between "extension not installed in this DB" and "extension package not available on the server" — the error messages guide users to the right fix.

## Test conventions

- **Unit tests** live in `*_unit_test.go` files (no build tag needed — they're excluded from integration builds by naming, not tags).
- **Integration tests** live in `*_integration_test.go` files and must start with `//go:build integration`. They are excluded from `go test ./...` entirely (no skip noise).
- When adding a new SQL-issuing code path: use `QueryRows` if it's tool-surfaced (respects restricted mode), `InternalQuery` if it's a trusted server operation.
- CI enforces ≥ 95% total coverage; the disposable test DB (`docker-compose.test.yml`) bakes `pg_stat_statements` and `hypopg` into a `postgres:17` image on port 5433.
