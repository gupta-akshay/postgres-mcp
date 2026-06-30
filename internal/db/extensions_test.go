package db

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── stub Querier for extensions tests ───────────────────────────────────────

type stubQuerier struct {
	rows []map[string]any
	err  error
}

func (s *stubQuerier) InternalQuery(_ context.Context, _ string, _ ...any) ([]map[string]any, error) {
	return s.rows, s.err
}
func (s *stubQuerier) QueryRows(_ context.Context, _ string, _ ...any) ([]map[string]any, error) {
	return nil, nil
}
func (s *stubQuerier) Execute(_ context.Context, _ string, _ ...any) error { return nil }
func (s *stubQuerier) Version(_ context.Context) (int, error)              { return 150000, nil }
func (s *stubQuerier) IsRestricted() bool                                  { return false }
func (s *stubQuerier) Close()                                              {}
func (s *stubQuerier) WithConn(_ context.Context, fn func(context.Context, Querier) error) error {
	return fn(context.Background(), s)
}

// ─── CheckExtension ───────────────────────────────────────────────────────────

func TestCheckExtension_Installed(t *testing.T) {
	q := &stubQuerier{rows: []map[string]any{
		{"extversion": "1.10", "installed": true, "available": true},
	}}
	ctx := context.Background()

	info, err := CheckExtension(ctx, q, "pg_stat_statements")
	require.NoError(t, err)
	assert.Equal(t, "pg_stat_statements", info.Name)
	assert.True(t, info.Installed)
	assert.True(t, info.Available)
	assert.Equal(t, "1.10", info.Version)
}

func TestCheckExtension_AvailableNotInstalled(t *testing.T) {
	q := &stubQuerier{rows: []map[string]any{
		{"extversion": "1.0", "installed": false, "available": true},
	}}
	ctx := context.Background()

	info, err := CheckExtension(ctx, q, "hypopg")
	require.NoError(t, err)
	assert.Equal(t, "hypopg", info.Name)
	assert.False(t, info.Installed)
	assert.True(t, info.Available)
}

func TestCheckExtension_NotAvailable(t *testing.T) {
	q := &stubQuerier{rows: nil} // empty result
	ctx := context.Background()

	info, err := CheckExtension(ctx, q, "nonexistent_ext")
	require.NoError(t, err)
	assert.False(t, info.Installed)
	assert.False(t, info.Available)
}

func TestCheckExtension_QueryError(t *testing.T) {
	q := &stubQuerier{err: errors.New("connection lost")}
	ctx := context.Background()

	_, err := CheckExtension(ctx, q, "pg_stat_statements")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_stat_statements")
}

// ─── RequireExtension ─────────────────────────────────────────────────────────

func TestRequireExtension_Installed(t *testing.T) {
	q := &stubQuerier{rows: []map[string]any{
		{"extversion": "1.0", "installed": true, "available": true},
	}}
	ctx := context.Background()

	err := RequireExtension(ctx, q, "pg_stat_statements")
	require.NoError(t, err)
}

func TestRequireExtension_AvailableButNotInstalled(t *testing.T) {
	q := &stubQuerier{rows: []map[string]any{
		{"extversion": "1.0", "installed": false, "available": true},
	}}
	ctx := context.Background()

	err := RequireExtension(ctx, q, "hypopg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
	assert.Contains(t, err.Error(), "CREATE EXTENSION")
}

func TestRequireExtension_NotAvailable(t *testing.T) {
	q := &stubQuerier{rows: nil}
	ctx := context.Background()

	err := RequireExtension(ctx, q, "custom_ext")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestRequireExtension_QueryError(t *testing.T) {
	q := &stubQuerier{err: errors.New("timeout")}
	ctx := context.Background()

	err := RequireExtension(ctx, q, "hypopg")
	require.Error(t, err)
}
