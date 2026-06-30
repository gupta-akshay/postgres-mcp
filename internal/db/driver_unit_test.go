package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── New error paths ──────────────────────────────────────────────────────────

func TestNew_ParseDSNError(t *testing.T) {
	_, err := New(context.Background(), "://invalid", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse database URL")
}

func TestNew_PoolCreateFail(t *testing.T) {
	orig := poolFactory
	t.Cleanup(func() { poolFactory = orig })
	poolFactory = func(_ context.Context, _ *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, errors.New("pool creation failed")
	}
	_, err := New(context.Background(), "postgresql://localhost/testdb", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create connection pool")
}

func TestNew_PingFail(t *testing.T) {
	// Bind a port and close it immediately so the next connect is refused instantly.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dsn := fmt.Sprintf(
		"postgresql://postgres:x@127.0.0.1:%d/testdb?sslmode=disable&connect_timeout=2",
		port,
	)
	_, err = New(ctx, dsn, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping database")
}

// ─── jsonFriendly edge cases ─────────────────────────────────────────────────

func TestJsonFriendly_UUID(t *testing.T) {
	var uuid [16]byte
	copy(uuid[:], []byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4,
		0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00})
	result := jsonFriendly(uuid)
	s, ok := result.(string)
	require.True(t, ok, "UUID [16]byte should become a string")
	assert.Len(t, s, 36, "UUID string should be 36 chars")
	assert.Contains(t, s, "-")
}

func TestJsonFriendly_Stringer(t *testing.T) {
	// time.Time implements fmt.Stringer and is not caught by earlier cases.
	ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	result := jsonFriendly(ts)
	s, ok := result.(string)
	require.True(t, ok, "Stringer should become a string")
	assert.Contains(t, s, "2024")
}

func TestJsonFriendly_SliceAny(t *testing.T) {
	input := []any{1, "two"}
	result := jsonFriendly(input)
	s, ok := result.(string)
	require.True(t, ok, "[]any should be JSON-marshalled to string")
	assert.Contains(t, s, "two")
}

func TestJsonFriendly_JsonMarshalFail(t *testing.T) {
	// Channels are not JSON-serialisable → falls back to returning v unchanged.
	ch := make(chan int)
	defer close(ch)
	input := []any{ch}
	result := jsonFriendly(input)
	assert.Equal(t, input, result, "on marshal failure the original value is returned")
}
