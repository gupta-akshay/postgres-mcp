# postgres-mcp — PostgreSQL MCP Server

A [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server written in Go that connects AI coding assistants (Claude Code, Cursor) directly to a PostgreSQL database for analysis, query optimization, and health monitoring.

> A Go reimplementation of the open-source [crystaldba/postgres-mcp](https://github.com/crystaldba/postgres-mcp). Pure Go, no Python, no CGo. Single static binary.

---

## Documentation

- **[Codebase guide](docs/CODEBASE.md)** — how the Go code is organised, MCP tool wiring, package boundaries, and debugging tips for contributors.
- **[Pre-share smoke-test checklist](docs/TESTING.md)** — checkbox-driven verification every feature works in both transports + both access modes before announcing the server.

---

## Why It's Useful

Instead of copy-pasting queries into a separate SQL client, you can interact with the database directly inside your AI assistant conversation:

- **SQL execution** — run queries against your local DB without leaving the editor
- **EXPLAIN plans** — paste a slow query and get an annotated plan
- **Hypothetical index simulation** — test `CREATE INDEX` impact on the query planner *before* touching the schema (via `hypopg`)
- **Schema intelligence** — the assistant understands your actual table/column structure, leading to more accurate SQL generation
- **Automated index recommendations** — the DTA (Database Tuning Advisor) greedy algorithm analyses your workload and recommends indexes
- **Health checks** — connection utilisation, vacuum health, buffer cache hit rates, sequence limits, replication lag

---

## Available MCP Tools


| Tool                       | Description                                                                                               |
| -------------------------- | --------------------------------------------------------------------------------------------------------- |
| `list_schemas`             | List all non-system schemas and their owners                                                              |
| `list_objects`             | List tables, views, and sequences in a schema                                                             |
| `get_object_details`       | Columns, indexes, constraints, and row estimates for a table                                              |
| `explain_query`            | EXPLAIN (ANALYZE) a query; optionally simulate hypothetical indexes via HypoPG                            |
| `execute_sql`              | Run arbitrary SQL (read-only in restricted mode)                                                          |
| `get_top_queries`          | Slowest/most resource-intensive queries from `pg_stat_statements`                                         |
| `analyze_workload_indexes` | Recommend indexes based on the real query workload in `pg_stat_statements`                                |
| `analyze_query_indexes`    | Recommend indexes for an explicit list of SQL queries                                                     |
| `analyze_db_health`        | Health checks: index validity, connections, vacuum/XID, sequences, replication, buffer cache, constraints |


---

## Prerequisites

- **Docker** (recommended) — or Go 1.26.4+ to build from source
- **PostgreSQL** running locally (Postgres.app, Homebrew, or Docker)
- **Claude Code** and/or **Cursor**

Optional but strongly recommended PostgreSQL extensions:


| Extension            | What it enables                                                                     |
| -------------------- | ----------------------------------------------------------------------------------- |
| `pg_stat_statements` | `get_top_queries` and `analyze_workload_indexes`                                    |
| `hypopg`             | Hypothetical index simulation in `explain_query` and both `analyze_*_indexes` tools |


---

## Quick Start

### 1. Build the Docker image

```bash
git clone https://github.com/gupta-akshay/postgres-mcp.git
cd postgres-mcp
docker build -t postgres-mcp:latest .
```

That's it — the image is a ~15 MB static binary on Alpine. No Python, no system dependencies.

---

### 2. Configure Claude Code (stdio — recommended)

Register with the `claude` CLI (writes to `~/.claude.json` — the correct location for MCP servers; `~/.claude/settings.json` is **not** read for MCP config):

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

Verify:

```bash
claude mcp list          # postgres: ... ✓ Connected
claude mcp get postgres
```

> **Restart Claude Code** after adding — MCP servers are only loaded on startup.

> **macOS note:** Docker Desktop maps `host.docker.internal` to the host automatically. You don't need `--add-host` on Mac; keep it for Linux compatibility.

> **Project-scoped alternative:** commit a `.mcp.json` at the repo root with the same `{ "mcpServers": { "postgres": { ... } } }` shape so teammates auto-pick it up.

---

### 3. Configure Cursor

Edit `~/.cursor/mcp.json` (global) **or** `.cursor/mcp.json` at the project root, **or** use the Command Palette → **Cursor Settings → MCP tab**.

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

## Docker Compose (SSE mode — optional)

If you prefer to keep the server running as a background service rather than spawning a fresh container per session, use the SSE transport:

```bash
# 1. Copy and fill in credentials
cp .env.example .env
# Edit .env — set DATABASE_URI and ACCESS_MODE

# 2. Start the server
docker-compose up -d

# 3. Check it's healthy
docker-compose ps
```

The server is now reachable at `http://localhost:8000/sse`.

**Claude Code — SSE config** (`~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "postgres": {
      "url": "http://localhost:8000/sse"
    }
  }
}
```

**Cursor — SSE config** (`~/.cursor/mcp.json`):

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


| Mode           | Use case                      | What it allows                                          |
| -------------- | ----------------------------- | ------------------------------------------------------- |
| `unrestricted` | Local development             | Full read/write, DDL, DML                               |
| `restricted`   | Shared / production databases | Read-only; enforced at the PostgreSQL transaction level |


Set via `--access-mode` flag or `ACCESS_MODE` environment variable.

---

## PostgreSQL Extensions Setup

### Install on Postgres.app (macOS)

`pg_stat_statements` is bundled with Postgres.app. `hypopg` must be built from source.

**Build hypopg:**

```bash
# Add Postgres.app binaries to PATH (adjust version as needed)
export PATH="/Applications/Postgres.app/Contents/Versions/17/bin:$PATH"

# Requires Xcode command line tools
xcode-select --install

git clone https://github.com/HypoPG/hypopg.git
cd hypopg
make && make install
```

**Enable pg_stat_statements** (requires a server restart):

1. Find your `postgresql.conf`:
  ```sql
   SHOW config_file;
  ```
2. Add (or append) to `shared_preload_libraries`:
  ```
   shared_preload_libraries = 'pg_stat_statements'
  ```
3. Restart Postgres.app (quit and reopen from the menu bar, or via CLI):
  ```bash
   pg_ctl restart -D "/Users/$USER/Library/Application Support/Postgres/var-17"
  ```
4. Create the extensions:
  ```sql
   CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
   CREATE EXTENSION IF NOT EXISTS hypopg;
  ```

---

### Install on Homebrew Postgres (macOS)

```bash
brew install hypopg
```

**Enable pg_stat_statements** (requires a server restart):

1. Find your `postgresql.conf`:
  ```sql
   SHOW config_file;
  ```
2. Add to `shared_preload_libraries`:
  ```
   shared_preload_libraries = 'pg_stat_statements'
  ```
   If there are already entries, comma-separate:
3. Restart:
  ```bash
   brew services restart postgresql@17   # adjust version
  ```
4. Create the extensions:
  ```sql
   CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
   CREATE EXTENSION IF NOT EXISTS hypopg;
  ```

---

### Install on cloud-managed Postgres (RDS, Cloud SQL, Supabase)

Both extensions are pre-installed. Just enable them:

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE EXTENSION IF NOT EXISTS hypopg;
```

---

## What You Can Ask After Setup

Once connected, use your AI assistant directly in conversation:

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

---

## Build From Source

```bash
go build -o postgres-mcp .

# Run
./postgres-mcp "postgresql://user:pass@localhost:5432/mydb" --access-mode unrestricted

# Or with env var
DATABASE_URI="postgresql://user:pass@localhost:5432/mydb" \
ACCESS_MODE=unrestricted \
./postgres-mcp
```

Requires Go 1.26.4+. No CGo, no system libraries — the binary is fully self-contained.

---

## Testing

The test suite is split into **unit tests** (no database required) and **integration / end-to-end tests** (require a live PostgreSQL with `pg_stat_statements` and `hypopg`).

### Quick commands (via Makefile)

```bash
make test-unit         # unit tests only (integration tests skip)
make test-integration  # spins up docker-compose Postgres, runs the full suite
make coverage          # full suite with coverage profile (expects ≥ 95% total)
make test-db-up        # start disposable Postgres (with pg_stat_statements + hypopg)
make test-db-down      # stop and remove the disposable DB
```

### Manual

```bash
# unit only
go test ./...

# full suite against any Postgres you choose
export TEST_DATABASE_URL="postgresql://postgres:test@localhost:5433/postgres_mcp_test?sslmode=disable"
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

Coverage is currently ~97.5 % overall. CI (`.github/workflows/ci.yml`) runs the full suite on every PR and blocks merges that drop below 95 %.

### What the tests cover

- **Unit**: all sub-package business logic against `dbtest.MockQuerier`; every MCP handler wired via an in-process MCP client + mock.
- **Integration**: the `db.Driver` against a live Postgres — connection modes, restricted-mode write blocking, JSON column decoding.
- **End-to-end**: every one of the nine MCP tools invoked over the real MCP protocol (`internal/server/e2e_integration_test.go`) against a live Postgres with `pg_stat_statements` + `hypopg`.

### Disposable test database

`docker-compose.test.yml` + `Dockerfile.test-postgres` bake both required extensions into a `postgres:17` image. `make test-db-up` starts it on port 5433 so it won't collide with a host-local Postgres on 5432.

---

## Project Structure

```
postgres-mcp/
├── main.go
├── cmd/root.go                  — CLI (cobra)
├── Dockerfile                   — runtime image (~15 MB static binary)
├── Dockerfile.test-postgres     — PG17 + pg_stat_statements + hypopg for tests
├── docker-compose.yml           — production SSE deployment
├── docker-compose.test.yml      — disposable test database
├── Makefile                     — test / coverage / lint targets
├── .github/workflows/ci.yml     — full suite in CI, ≥ 95 % coverage gate
├── test/init.sql                — extension bootstrap for the test DB
└── internal/
    ├── config/                  — Config struct
    ├── db/                      — pgxpool driver + Querier interface + extension checks
    ├── dbtest/                  — shared MockQuerier test double
    ├── schema/                  — list_schemas, list_objects, get_object_details
    ├── explain/                 — EXPLAIN plans + HypoPG simulation
    ├── topqueries/              — pg_stat_statements analysis
    ├── health/                  — 7 health check modules (parallel)
    ├── index/                   — DTA greedy algorithm + types
    └── server/                  — MCP server, tool registration, E2E tests
```

---

## Troubleshooting

**"pg_stat_statements extension is not installed"**
Follow the extension setup above. The extension also requires `shared_preload_libraries` — a simple `CREATE EXTENSION` is not enough.

**"hypopg extension is not installed"**
Run `CREATE EXTENSION IF NOT EXISTS hypopg;` in psql. If unavailable, install the package first (see above).

**"dial tcp: connect: connection refused" from Docker**
Use `host.docker.internal` instead of `localhost` in your `DATABASE_URI` when running via Docker. On Linux, also pass `--add-host=host.docker.internal:host-gateway` to `docker run`.

**Claude Code doesn't see the server**
MCP servers are only loaded at startup. Restart Claude Code after editing `~/.claude/settings.json`.

**"write operations are not permitted in restricted mode"**
Set `ACCESS_MODE=unrestricted` in your MCP config env (or `--access-mode unrestricted` flag) for local development.
