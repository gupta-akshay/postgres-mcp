# postgres-mcp — Talk to Your PostgreSQL Database from Your AI Assistant

[![CI](https://github.com/gupta-akshay/postgres-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/gupta-akshay/postgres-mcp/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26.4+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Coverage](https://img.shields.io/badge/Coverage-97.5%25-brightgreen)](https://github.com/gupta-akshay/postgres-mcp/actions/workflows/ci.yml)
[![GitHub Stars](https://img.shields.io/github/stars/gupta-akshay/postgres-mcp?style=flat)](https://github.com/gupta-akshay/postgres-mcp/stargazers)
[![Contributors](https://img.shields.io/github/contributors/gupta-akshay/postgres-mcp)](https://github.com/gupta-akshay/postgres-mcp/graphs/contributors)

Stop context-switching between your editor and a SQL client. **postgres-mcp** is a [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server that gives Claude Code and Cursor direct, live access to your PostgreSQL database — so you can analyse slow queries, explore your schema, simulate indexes, and check database health without ever leaving the conversation.

> A Go reimplementation of [crystaldba/postgres-mcp](https://github.com/crystaldba/postgres-mcp). Pure Go, no Python, no CGo. Ships as a ~15 MB static binary on Alpine.

---

## What You Can Do

Once connected, your AI assistant becomes a PostgreSQL power user. Just ask:

```
"Show me the 10 slowest queries right now"
"Run EXPLAIN ANALYZE on this query and tell me why it's slow"
"What indexes exist on the orders table?"
"Simulate adding an index on orders(status, user_id) — will it help?"
"Recommend indexes for this set of queries based on the actual query planner cost"
"Analyse the full database health — any vacuum, sequence, or connection issues?"
"List all tables in the public schema and their sizes"
"Check if there are any invalid or duplicate indexes"
```

No copy-pasting. No switching tabs. No stale schema guesses — the assistant reads your actual live schema.

---

## How It Works

postgres-mcp exposes nine tools over the MCP protocol. Your AI assistant calls them like function calls; you get structured JSON results back in the conversation.

| Tool | What it does |
| ---- | ------------ |
| `list_schemas` | List all non-system schemas and their owners |
| `list_objects` | List tables, views, and sequences in a schema |
| `get_object_details` | Columns, indexes, constraints, and row estimates for a table |
| `explain_query` | EXPLAIN (ANALYZE) a query; optionally simulate hypothetical indexes via HypoPG |
| `execute_sql` | Run arbitrary SQL (read-only in restricted mode) |
| `get_top_queries` | Slowest/most resource-intensive queries from `pg_stat_statements` |
| `analyze_workload_indexes` | Recommend indexes based on the real query workload in `pg_stat_statements` |
| `analyze_query_indexes` | Recommend indexes for an explicit list of SQL queries |
| `analyze_db_health` | Health checks: index validity, connections, vacuum/XID, sequences, replication, buffer cache, constraints |

The standout features are **hypothetical index simulation** (via `hypopg`) and the **DTA greedy index advisor**: you can test whether an index would actually help the query planner *before* creating it, or let the advisor analyse your real workload and tell you which indexes to add.

---

## Prerequisites

- **Docker** (recommended) — or Go 1.26.4+ to build from source
- **PostgreSQL** running locally (Postgres.app, Homebrew, or Docker)
- **Claude Code** and/or **Cursor**

Two PostgreSQL extensions are optional but unlock the most useful features:

| Extension | What it unlocks |
| --------- | --------------- |
| `pg_stat_statements` | `get_top_queries` and `analyze_workload_indexes` |
| `hypopg` | Hypothetical index simulation in `explain_query` and both `analyze_*_indexes` tools |

[Jump to extension setup →](#postgresql-extensions-setup)

---

## Quick Start

### 1. Build the Docker image

```bash
git clone https://github.com/gupta-akshay/postgres-mcp.git
cd postgres-mcp
docker build -t postgres-mcp:latest .
```

That's the whole install. No Python environment, no system libraries, no version conflicts.

---

### 2. Wire it into Claude Code (stdio — recommended)

Register with the `claude` CLI. This writes to `~/.claude.json` — the correct location for MCP servers (`~/.claude/settings.json` is **not** read for MCP config):

```bash
claude mcp add-json postgres '{
  "command": "docker",
  "args": [
    "run", "-i", "--rm",
    "-e", "DATABASE_URI",
    "-e", "ACCESS_MODE",
    "--add-host=host.docker.internal:host-gateway",
    "postgres-mcp:latest"
  ],
  "env": {
    "DATABASE_URI": "postgresql://username:password@host.docker.internal:5432/dbname",
    "ACCESS_MODE": "unrestricted"
  }
}' --scope user
```

Verify it connected:

```bash
claude mcp list          # postgres: ... ✓ Connected
claude mcp get postgres
```

> **Restart Claude Code** after adding — MCP servers are only loaded on startup.

> **macOS note:** Docker Desktop maps `host.docker.internal` automatically. You don't need `--add-host` on Mac; keep it for Linux compatibility.

> **Team setup:** Commit a `.mcp.json` at the repo root with the same `{ "mcpServers": { "postgres": { ... } } }` shape and teammates get it automatically.

---

### 3. Wire it into Cursor

Edit `~/.cursor/mcp.json` (global), `.cursor/mcp.json` (project), or go to Command Palette → **Cursor Settings → MCP tab**:

```json
{
  "mcpServers": {
    "postgres": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "DATABASE_URI",
        "-e", "ACCESS_MODE",
        "--add-host=host.docker.internal:host-gateway",
        "postgres-mcp:latest"
      ],
      "env": {
        "DATABASE_URI": "postgresql://username:password@host.docker.internal:5432/dbname",
        "ACCESS_MODE": "unrestricted"
      }
    }
  }
}
```

---

## Keep It Running with Docker Compose (SSE mode)

The default stdio transport spins up a fresh container per session. If you'd rather keep a persistent server running in the background, use SSE mode:

```bash
# Copy and fill in credentials
cp .env.example .env     # set DATABASE_URI and ACCESS_MODE

# Start the server
docker-compose up -d

# Confirm it's healthy
docker-compose ps
```

The server listens at `http://localhost:8000/sse`.

**Claude Code** (`~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "postgres": {
      "url": "http://localhost:8000/sse"
    }
  }
}
```

**Cursor** (`~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "postgres": {
      "url": "http://localhost:8000/sse"
    }
  }
}
```

---

## Access Modes

postgres-mcp supports two access modes so you can safely connect it to databases you don't fully own:

| Mode | Use case | What it allows |
| ---- | -------- | -------------- |
| `unrestricted` | Local development | Full read/write, DDL, DML |
| `restricted` | Shared / production databases | Read-only; enforced at the PostgreSQL transaction level |

Set via the `--access-mode` flag or the `ACCESS_MODE` environment variable. In restricted mode, `EXPLAIN ANALYZE` is also silently downgraded to `EXPLAIN` (since ANALYZE actually executes the query).

---

## PostgreSQL Extensions Setup

### Postgres.app (macOS)

`pg_stat_statements` is bundled. `hypopg` must be built from source:

```bash
export PATH="/Applications/Postgres.app/Contents/Versions/17/bin:$PATH"
xcode-select --install   # if not already installed

git clone https://github.com/HypoPG/hypopg.git
cd hypopg && make && make install
```

Enable `pg_stat_statements` (requires a server restart):

1. Find `postgresql.conf`:
   ```sql
   SHOW config_file;
   ```
2. Add to `shared_preload_libraries`:
   ```
   shared_preload_libraries = 'pg_stat_statements'
   ```
3. Restart Postgres.app (quit and reopen, or via CLI):
   ```bash
   pg_ctl restart -D "/Users/$USER/Library/Application Support/Postgres/var-17"
   ```
4. Create both extensions:
   ```sql
   CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
   CREATE EXTENSION IF NOT EXISTS hypopg;
   ```

---

### Homebrew Postgres (macOS)

```bash
brew install hypopg
```

Enable `pg_stat_statements` (requires a server restart):

1. Find `postgresql.conf`:
   ```sql
   SHOW config_file;
   ```
2. Add to `shared_preload_libraries` (comma-separate if other entries exist):
   ```
   shared_preload_libraries = 'pg_stat_statements'
   ```
3. Restart:
   ```bash
   brew services restart postgresql@17   # adjust version
   ```
4. Create both extensions:
   ```sql
   CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
   CREATE EXTENSION IF NOT EXISTS hypopg;
   ```

---

### Cloud-managed Postgres (RDS, Cloud SQL, Supabase)

Both extensions ship pre-installed. Just enable them:

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE EXTENSION IF NOT EXISTS hypopg;
```

---

## Build From Source

No Docker required — the binary has no CGo and no system dependencies:

```bash
go build -o postgres-mcp .

# Run with positional URI
./postgres-mcp "postgresql://user:pass@localhost:5432/mydb" --access-mode unrestricted

# Or with environment variables
DATABASE_URI="postgresql://user:pass@localhost:5432/mydb" \
ACCESS_MODE=unrestricted \
./postgres-mcp
```

Requires Go 1.26.4+.

---

## Testing

The test suite has three layers — you can run just the fast layer or the full stack:

### Fast: unit tests (no database needed)

```bash
go test ./...
# or
make test-unit
```

Unit tests cover all sub-package business logic against a `MockQuerier` and every MCP handler via an in-process MCP client.

### Full: integration + end-to-end tests

```bash
make test-integration   # spins up a Docker Postgres, runs everything
make coverage           # same, plus a coverage report (CI gate: ≥ 95%)
```

Or point at any live Postgres you already have:

```bash
export TEST_DATABASE_URL="postgresql://postgres:test@localhost:5433/postgres_mcp_test?sslmode=disable"
go test -tags integration ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

Integration tests hit a real `db.Driver` — connection modes, restricted-mode write blocking, JSON column decoding. End-to-end tests (`internal/server/e2e_integration_test.go`) invoke all nine MCP tools over the real protocol against a live Postgres with both extensions installed.

### Disposable test database

`make test-db-up` starts a pre-configured Postgres 17 image (with `pg_stat_statements` + `hypopg` baked in) on port **5433** — it won't collide with a host-local Postgres on 5432.

```bash
make test-db-up    # start
make test-db-down  # stop and remove volume
```

Coverage is currently **~97.5%**. CI blocks merges that drop below 95%.

---

## Project Structure

```
postgres-mcp/
├── main.go
├── cmd/root.go                  — CLI entry point (cobra), flag + env wiring
├── Dockerfile                   — runtime image (~15 MB static binary on Alpine)
├── Dockerfile.test-postgres     — PG17 + pg_stat_statements + hypopg for tests
├── docker-compose.yml           — production SSE deployment
├── docker-compose.test.yml      — disposable test database
├── Makefile                     — test / coverage / lint targets
├── .github/workflows/ci.yml     — full suite in CI, ≥ 95% coverage gate
├── test/init.sql                — extension bootstrap for the test DB
└── internal/
    ├── config/                  — Config struct + IsRestricted()
    ├── db/                      — pgxpool driver, Querier interface, extension checks
    ├── dbtest/                  — MockQuerier test double (shared across packages)
    ├── schema/                  — list_schemas, list_objects, get_object_details
    ├── explain/                 — EXPLAIN plans + HypoPG hypothetical index simulation
    ├── topqueries/              — pg_stat_statements rankings (PG12/13+ version-aware)
    ├── health/                  — orchestrator + 7 parallel health check modules
    ├── index/                   — DTA greedy algorithm + types
    └── server/                  — MCP server bootstrap, tool registration, E2E tests
```

For a deeper dive into the architecture and debugging tips, see the **[codebase guide](docs/CODEBASE.md)**.

---

## Troubleshooting

**`"pg_stat_statements extension is not installed"`**
`pg_stat_statements` requires `shared_preload_libraries` — `CREATE EXTENSION` alone is not enough. Follow the [extension setup](#postgresql-extensions-setup) above, restart Postgres, then run `CREATE EXTENSION`.

**`"hypopg extension is not installed"`**
Run `CREATE EXTENSION IF NOT EXISTS hypopg;` in psql. If the extension is unavailable on the server, install the package first ([setup instructions above](#postgresql-extensions-setup)).

**`"dial tcp: connect: connection refused"` from Docker**
Use `host.docker.internal` instead of `localhost` in your `DATABASE_URI`. On Linux, also pass `--add-host=host.docker.internal:host-gateway` to `docker run`.

**Claude Code doesn't see the server**
MCP servers only load at startup. Restart Claude Code after any changes to `~/.claude.json`.

**`"write operations are not permitted in restricted mode"`**
Set `ACCESS_MODE=unrestricted` (or `--access-mode unrestricted`) for local development.
