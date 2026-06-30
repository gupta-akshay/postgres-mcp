package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── New (unit) ───────────────────────────────────────────────────────────────

// TestNew_BadDSN covers the pgxpool.ParseConfig failure path without any
// network IO. An unparseable DSN fails before any connection is attempted.
func TestNew_BadDSN(t *testing.T) {
	_, err := New(context.Background(), "not-a-valid-dsn", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse database URL")
}

// ─── validateNotWrite ─────────────────────────────────────────────────────────

func TestValidateNotWrite_AllowsReads(t *testing.T) {
	allowed := []string{
		"SELECT * FROM users",
		"select id FROM orders WHERE status = 'active'",
		"  SELECT 1", // leading whitespace
		"EXPLAIN SELECT * FROM users",
		"WITH cte AS (SELECT 1) SELECT * FROM cte",
		"SHOW max_connections",
	}
	for _, sql := range allowed {
		require.NoError(t, validateNotWrite(sql), "should allow: %s", sql)
	}
}

func TestValidateNotWrite_BlocksWrites(t *testing.T) {
	blocked := []string{
		"INSERT INTO users VALUES (1, 'a')",
		"UPDATE users SET name = 'x' WHERE id = 1",
		"DELETE FROM users WHERE id = 1",
		"TRUNCATE TABLE users",
		"DROP TABLE users",
		"CREATE TABLE t (id int)",
		"ALTER TABLE users ADD COLUMN foo text",
		"GRANT SELECT ON users TO app",
		"REVOKE SELECT ON users FROM app",
		"COPY users FROM '/tmp/data.csv'",
		"MERGE INTO users USING src ON ...",
	}
	for _, sql := range blocked {
		assert.Error(t, validateNotWrite(sql), "should block: %s", sql)
	}
}

// ─── jsonFriendly ─────────────────────────────────────────────────────────────

func TestJSONFriendly_Nil(t *testing.T) {
	assert.Nil(t, jsonFriendly(nil))
}

func TestJSONFriendly_String(t *testing.T) {
	assert.Equal(t, "hello", jsonFriendly("hello"))
}

func TestJSONFriendly_Bytes(t *testing.T) {
	assert.Equal(t, "hello", jsonFriendly([]byte("hello")))
}

func TestJSONFriendly_UUID(t *testing.T) {
	uuid := [16]byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4,
		0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00}
	result := jsonFriendly(uuid)
	s, ok := result.(string)
	require.True(t, ok, "UUID should become a string")
	assert.NotEmpty(t, s)
}

func TestJSONFriendly_PassThrough(t *testing.T) {
	assert.Equal(t, 42, jsonFriendly(42))
	assert.Equal(t, 3.14, jsonFriendly(3.14))
	assert.Equal(t, true, jsonFriendly(true))
}

type myStringer struct{ val string }

func (m myStringer) String() string { return m.val }

func TestJSONFriendly_Stringer(t *testing.T) {
	s := myStringer{"world"}
	result := jsonFriendly(s)
	assert.Equal(t, "world", result)
}

// ─── ToString ─────────────────────────────────────────────────────────────────

func TestToString(t *testing.T) {
	assert.Equal(t, "", ToString(nil))
	assert.Equal(t, "hello", ToString("hello"))
	assert.Equal(t, "hello", ToString([]byte("hello")))
	assert.Equal(t, "42", ToString(42))
}

// ─── ToFloat64 ────────────────────────────────────────────────────────────────

func TestToFloat64(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{int16(10), 10},
		{int32(20), 20},
		{int64(30), 30},
		{float32(1.5), 1.5},
		{float64(2.5), 2.5},
		{"3.14", 3.14},
	}
	for _, tc := range cases {
		got, err := ToFloat64(tc.in)
		require.NoError(t, err, "input: %v (%T)", tc.in, tc.in)
		assert.InDelta(t, tc.want, got, 0.001)
	}
}

func TestToFloat64_Error(t *testing.T) {
	_, err := ToFloat64([]byte("not a number"))
	assert.Error(t, err)
}

// ─── toInt64 ──────────────────────────────────────────────────────────────────

func TestToInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int16(1), 1},
		{int32(2), 2},
		{int64(3), 3},
		{float32(4), 4},
		{float64(5), 5},
		{"99", 99},
	}
	for _, tc := range cases {
		got, err := toInt64(tc.in)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

func TestToInt64_Error(t *testing.T) {
	_, err := toInt64([]byte("not a number"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot convert")
}
