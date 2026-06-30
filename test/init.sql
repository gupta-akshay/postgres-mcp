-- Extensions required by postgres-mcp integration tests.
-- Mounted into /docker-entrypoint-initdb.d/ so they run once on first boot
-- of a fresh postgres_mcp_test database.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE EXTENSION IF NOT EXISTS hypopg;
