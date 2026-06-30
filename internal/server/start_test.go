package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/config"
)

// TestStart_BadDSN covers the db.New error path in Start(). An unparseable DSN
// trips pgxpool.ParseConfig before any network IO, so no live DB is needed.
func TestStart_BadDSN(t *testing.T) {
	cfg := &config.Config{
		DatabaseURL: "not-a-valid-dsn",
		AccessMode:  "unrestricted",
		Transport:   "stdio",
	}

	err := Start(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect to database")
}
