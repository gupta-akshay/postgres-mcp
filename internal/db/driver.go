package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	restrictedStatementTimeout = 30 * time.Second
	// Tag added to internal queries so they can be filtered from top-query results.
	internalQueryTag = "/* postgres-mcp */"
)

// Querier is the database interface that sub-packages depend on.
// *Driver satisfies this interface, as does the test double in internal/dbtest.
type Querier interface {
	QueryRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error)
	InternalQuery(ctx context.Context, sql string, args ...any) ([]map[string]any, error)
	Execute(ctx context.Context, sql string, args ...any) error
	Version(ctx context.Context) (int, error)
	IsRestricted() bool
	Close()
	// WithConn acquires a single connection from the pool and calls fn with a
	// Querier that is pinned to that connection. This is required for operations
	// that depend on session-local state (e.g. HypoPG hypothetical indexes).
	WithConn(ctx context.Context, fn func(context.Context, Querier) error) error
}

// Driver wraps a pgxpool connection pool and enforces access-mode rules.
type Driver struct {
	pool       *pgxpool.Pool
	restricted bool
}

// Compile-time check: *Driver must satisfy Querier.
var _ Querier = (*Driver)(nil)

// New creates a Driver connected to dsn. When restricted is true, all user-facing
// queries run inside a READ ONLY transaction with a statement timeout.
func New(ctx context.Context, dsn string, restricted bool) (*Driver, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Driver{pool: pool, restricted: restricted}, nil
}

// Close releases all pool connections.
func (d *Driver) Close() {
	d.pool.Close()
}

// IsRestricted reports whether the driver is in restricted (read-only) mode.
func (d *Driver) IsRestricted() bool {
	return d.restricted
}

// QueryRows executes sql with args and returns all rows as a slice of maps.
// In restricted mode the query is wrapped in a READ ONLY transaction with a timeout.
func (d *Driver) QueryRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	if d.restricted {
		if err := validateNotWrite(sql); err != nil {
			return nil, err
		}
		return d.queryInReadOnlyTx(ctx, sql, args...)
	}
	return d.queryDirect(ctx, sql, args...)
}

// InternalQuery executes sql without access-mode checks. Used only for internal
// server operations (EXPLAIN, HypoPG calls, health queries).
func (d *Driver) InternalQuery(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	return d.queryDirect(ctx, internalQueryTag+" "+sql, args...)
}

// Execute runs a DML or DDL statement. Returns an error in restricted mode.
func (d *Driver) Execute(ctx context.Context, sql string, args ...any) error {
	if d.restricted {
		return fmt.Errorf("write operations are not permitted in restricted mode")
	}
	_, err := d.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// WithConn acquires a single connection from the pool and calls fn with a
// Querier that is pinned to that one connection.
func (d *Driver) WithConn(ctx context.Context, fn func(context.Context, Querier) error) error {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()
	return fn(ctx, &singleConnQuerier{conn: conn.Conn(), restricted: d.restricted})
}

// Version returns the PostgreSQL server version number (e.g. 150004).
func (d *Driver) Version(ctx context.Context) (int, error) {
	rows, err := d.InternalQuery(ctx, "SELECT current_setting('server_version_num')::int AS v")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("empty result for server_version_num")
	}
	v, err := toInt64(rows[0]["v"])
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

// ─── single-connection querier ───────────────────────────────────────────────

// singleConnQuerier wraps a single *pgx.Conn so all calls target the same
// backend session. Used by WithConn to pin session-local state (e.g. HypoPG).
type singleConnQuerier struct {
	conn       *pgx.Conn
	restricted bool
}

func (c *singleConnQuerier) InternalQuery(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := c.conn.Query(ctx, internalQueryTag+" "+sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRows(rows)
}

func (c *singleConnQuerier) QueryRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	if c.restricted {
		if err := validateNotWrite(sql); err != nil {
			return nil, err
		}
	}
	rows, err := c.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRows(rows)
}

func (c *singleConnQuerier) Execute(ctx context.Context, sql string, args ...any) error {
	if c.restricted {
		return fmt.Errorf("write operations are not permitted in restricted mode")
	}
	_, err := c.conn.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

func (c *singleConnQuerier) Version(ctx context.Context) (int, error) {
	rows, err := c.InternalQuery(ctx, "SELECT current_setting('server_version_num')::int AS v")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("empty result for server_version_num")
	}
	v, err := toInt64(rows[0]["v"])
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

func (c *singleConnQuerier) IsRestricted() bool { return c.restricted }
func (c *singleConnQuerier) Close()             {}

func (c *singleConnQuerier) WithConn(ctx context.Context, fn func(context.Context, Querier) error) error {
	return fn(ctx, c)
}

// ─── internal helpers ────────────────────────────────────────────────────────

func (d *Driver) queryDirect(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRows(rows)
}

func (d *Driver) queryInReadOnlyTx(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	timeoutMS := restrictedStatementTimeout.Milliseconds()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeoutMS)); err != nil {
		return nil, fmt.Errorf("set statement timeout: %w", err)
	}

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := collectRows(rows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return result, nil
}

// collectRows converts pgx rows into a slice of string-keyed maps.
// Duplicate column names (e.g. from SELECT * on a join) are disambiguated by
// appending _1, _2, … so no value is silently overwritten.
func collectRows(rows pgx.Rows) ([]map[string]any, error) {
	fds := rows.FieldDescriptions()
	var result []map[string]any

	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(fds))
		for i, fd := range fds {
			name := string(fd.Name)
			if _, exists := row[name]; exists {
				for n := 1; ; n++ {
					candidate := fmt.Sprintf("%s_%d", name, n)
					if _, dup := row[candidate]; !dup {
						name = candidate
						break
					}
				}
			}
			row[name] = jsonFriendly(vals[i])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// jsonFriendly converts pgx value types to JSON-serialisable Go values.
// json / jsonb columns are re-serialised as their raw JSON string so callers
// that expect a text payload (e.g. EXPLAIN FORMAT JSON consumers) still work.
func jsonFriendly(v any) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []byte:
		return string(t)
	case [16]byte: // UUID
		return fmt.Sprintf("%x-%x-%x-%x-%x", t[0:4], t[4:6], t[6:8], t[8:10], t[10:])
	case map[string]any, []any:
		// pgx v5 decodes json/jsonb values into native Go containers. Re-serialise
		// so downstream consumers (EXPLAIN JSON, jsonResult) get a stable string.
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
		return v
	case fmt.Stringer:
		return t.String()
	default:
		return v
	}
}

// validateNotWrite rejects obvious write-keyword statements as a secondary
// safeguard (primary protection is the READ ONLY transaction).
func validateNotWrite(sql string) error {
	upper := strings.TrimSpace(strings.ToUpper(sql))
	for _, kw := range []string{
		"INSERT", "UPDATE", "DELETE", "TRUNCATE",
		"DROP", "CREATE", "ALTER", "GRANT", "REVOKE",
		"COPY", "MERGE",
	} {
		if strings.HasPrefix(upper, kw) {
			return fmt.Errorf("statement type %s is not allowed in restricted mode", kw)
		}
	}
	return nil
}

// toInt64 converts various numeric types returned by pgx to int64.
func toInt64(v any) (int64, error) {
	switch t := v.(type) {
	case int16:
		return int64(t), nil
	case int32:
		return int64(t), nil
	case int64:
		return t, nil
	case float32:
		return int64(t), nil
	case float64:
		return int64(t), nil
	case string:
		var n int64
		_, err := fmt.Sscan(t, &n)
		return n, err
	}
	return 0, fmt.Errorf("cannot convert %T to int64", v)
}

// ToFloat64 converts various numeric types to float64.
func ToFloat64(v any) (float64, error) {
	switch t := v.(type) {
	case int16:
		return float64(t), nil
	case int32:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case float32:
		return float64(t), nil
	case float64:
		return t, nil
	case string:
		var f float64
		_, err := fmt.Sscan(t, &f)
		return f, err
	}
	return 0, fmt.Errorf("cannot convert %T to float64", v)
}

// ToString converts a value to its string representation.
func ToString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}
