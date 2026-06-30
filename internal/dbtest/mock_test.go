package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMock_Defaults(t *testing.T) {
	m := NewMock()
	v, err := m.Version(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 150000, v)
	assert.False(t, m.IsRestricted())
}

func TestMock_InternalQuery_Queue(t *testing.T) {
	m := NewMock().
		AddInternalQuery([]map[string]any{{"a": 1}}, nil).
		AddInternalQuery(nil, errors.New("second"))

	ctx := context.Background()
	rows, err := m.InternalQuery(ctx, "sql")
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	_, err = m.InternalQuery(ctx, "sql")
	assert.EqualError(t, err, "second")

	// Queue drained → returns (nil, nil)
	rows, err = m.InternalQuery(ctx, "sql")
	assert.NoError(t, err)
	assert.Nil(t, rows)
}

func TestMock_QueryRows_Queue(t *testing.T) {
	m := NewMock().
		AddQueryRows([]map[string]any{{"x": 42}}, nil).
		AddQueryRows(nil, errors.New("boom"))

	ctx := context.Background()
	rows, err := m.QueryRows(ctx, "sql")
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	_, err = m.QueryRows(ctx, "sql")
	assert.EqualError(t, err, "boom")

	// Drained
	rows, err = m.QueryRows(ctx, "sql")
	assert.NoError(t, err)
	assert.Nil(t, rows)
}

func TestMock_SetVersion(t *testing.T) {
	m := NewMock().SetVersion(120007)
	v, err := m.Version(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 120007, v)
}

func TestMock_SetVersionErr(t *testing.T) {
	wantErr := errors.New("ver boom")
	m := NewMock().SetVersionErr(wantErr)
	_, err := m.Version(context.Background())
	assert.Equal(t, wantErr, err)
}

func TestMock_SetRestricted(t *testing.T) {
	m := NewMock().SetRestricted(true)
	assert.True(t, m.IsRestricted())
}

func TestMock_Execute_Noop(t *testing.T) {
	m := NewMock()
	assert.NoError(t, m.Execute(context.Background(), "INSERT ..."))
}

func TestMock_Close_Noop(t *testing.T) {
	m := NewMock()
	m.Close()
}

// ─── helper builders ──────────────────────────────────────────────────────────

func TestExtensionInstalled(t *testing.T) {
	rows := ExtensionInstalled("1.10")
	require.Len(t, rows, 1)
	assert.Equal(t, "1.10", rows[0]["extversion"])
	assert.Equal(t, true, rows[0]["installed"])
	assert.Equal(t, true, rows[0]["available"])
}

func TestExtensionAvailableOnly(t *testing.T) {
	rows := ExtensionAvailableOnly("2.0")
	require.Len(t, rows, 1)
	assert.Equal(t, "2.0", rows[0]["extversion"])
	assert.Equal(t, false, rows[0]["installed"])
	assert.Equal(t, true, rows[0]["available"])
}

func TestExplainJSON(t *testing.T) {
	rows := ExplainJSON(12.5)
	require.Len(t, rows, 1)
	assert.Contains(t, rows[0]["QUERY PLAN"].(string), "12.5")
	assert.Contains(t, rows[0]["QUERY PLAN"].(string), "Result")
}

func TestExplainJSON_IntegerCost(t *testing.T) {
	rows := ExplainJSON(3.0)
	require.Len(t, rows, 1)
	assert.Contains(t, rows[0]["QUERY PLAN"].(string), "3.0")
}

func TestExplainJSONWithRelation(t *testing.T) {
	rows := ExplainJSONWithRelation("public", "orders", "status = 'active'", 42.0)
	require.Len(t, rows, 1)
	plan := rows[0]["QUERY PLAN"].(string)
	assert.Contains(t, plan, "public")
	assert.Contains(t, plan, "orders")
	assert.Contains(t, plan, "status")
	assert.Contains(t, plan, "42.0")
}

func TestFormatFloat_Integer(t *testing.T) {
	assert.Equal(t, "5.0", formatFloat(5.0))
}

func TestFormatFloat_Fractional(t *testing.T) {
	assert.Equal(t, "1.5", formatFloat(1.5))
}
