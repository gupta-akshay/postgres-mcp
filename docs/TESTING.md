# postgres-mcp — Pre-Share Smoke-Test Checklist

Run every box in this checklist before announcing the server to other teams.
A peer who has never used the tool should be able to follow it end-to-end.

## 0. Prereqs

- [ ] `docker --version` works
- [ ] `go version` reports ≥ 1.26.4
- [ ] A PostgreSQL 15+ reachable locally (Postgres.app, Homebrew, or `docker-compose up`) with:
  - [ ] `pg_stat_statements` in `shared_preload_libraries`
  - [ ] `CREATE EXTENSION hypopg;` run against the database

---

## 1. Build

- [ ] `go build -o postgres-mcp .` succeeds with no warnings
- [ ] `./postgres-mcp --help` prints usage including `--access-mode`, `--transport`, `--sse-host`, `--sse-port`
- [ ] `docker build -t postgres-mcp:smoke .` succeeds
- [ ] `docker run --rm postgres-mcp:smoke --help` prints usage

---

## 2. Automated tests

- [ ] `make test-unit` — all green, no failures
- [ ] `make test-db-up && make test-db-wait` — disposable Postgres is ready
- [ ] `make test-integration` — all green against the disposable DB
- [ ] `make coverage` — overall coverage ≥ 95 %
- [ ] `make test-db-down` — stack torn down cleanly

---

## 3. stdio transport via Claude Code — unrestricted mode

Configure `~/.claude/settings.json` to point at the built binary:

```json
{
  "mcpServers": {
    "postgres": {
      "command": "/absolute/path/to/postgres-mcp",
      "args": ["postgresql://postgres@localhost:5432/your_db"],
      "env": { "ACCESS_MODE": "unrestricted" }
    }
  }
}
```

Restart Claude Code, then in a chat:

- [ ] "List all schemas in the database" → returns `public` plus any user schemas
- [ ] "Show me the details of the <table-name> table" → returns columns + indexes
- [ ] "Run SELECT 1" → returns `{"rows":[{"n":1}],"row_count":1}`
- [ ] "EXPLAIN SELECT * FROM <table-name> WHERE <column> = '<value>'" → returns a plan with `total_cost`
- [ ] "What are the top 5 slowest queries?" → returns a ranked list (or a clean "extension not installed" message)
- [ ] "Run a full database health check" → returns seven check results, each with a status
- [ ] "Recommend indexes for: SELECT * FROM <table-name> WHERE <column> = 'x'" → returns a recommendation or an empty list
- [ ] "Analyse the live workload and suggest indexes" → completes within 30 s

---

## 4. stdio transport — restricted mode

Change the `env` block to `{"ACCESS_MODE": "restricted"}` and restart Claude Code.

- [ ] "Run INSERT INTO <table-name> VALUES (...)" → server returns "not allowed" / "not permitted"
- [ ] "Run SELECT 1" → still returns results

---

## 5. SSE transport via docker-compose

- [ ] `cp .env.example .env 2>/dev/null || true`
- [ ] Set `DATABASE_URI` and `ACCESS_MODE=unrestricted` in `.env`
- [ ] `docker compose up -d`
- [ ] `docker compose ps` → service is healthy
- [ ] `curl -sN http://localhost:8000/sse` opens an SSE stream and emits an `endpoint` event
- [ ] In a second terminal, POST a `tools/list` request to the session endpoint — 9 tools returned
- [ ] POST a `tools/call` request invoking `list_schemas` — returns JSON with schemas
- [ ] `docker compose down` — clean shutdown

---

## 6. SSE transport via Claude Code

Point Claude Code at `http://localhost:8000/sse`, restart.

- [ ] Repeat every prompt from section 3 — same answers, same structure

---

## 7. Error-path sanity

- [ ] Start the binary with an invalid DSN (`./postgres-mcp foo`) → prints a clear connection error, exits non-zero
- [ ] Start the binary with no DSN and no env var → prints "database URI is required", exits 1
- [ ] In Claude Code, call `explain_query` with an empty `query` → "'query' parameter is required"
- [ ] In Claude Code, call `analyze_query_indexes` with an empty array → validation error

---

## 8. Cleanup

- [ ] Stop any running containers
- [ ] Revert MCP config if you changed it for testing
- [ ] Confirm no stray `postgres-mcp` processes (`ps aux | grep postgres-mcp`)

When every box above is ticked, the server is ready to share.
