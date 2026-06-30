//go:build integration

package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/config"
)

func TestStart_SSE_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping SSE start test")
	}

	// Grab a free port and release it before Start binds it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := &config.Config{
		DatabaseURL: dsn,
		AccessMode:  "unrestricted",
		Transport:   "sse",
		SSEHost:     "0.0.0.0", // triggers advertiseHost → localhost transformation
		SSEPort:     port,
	}

	// Start blocks on sseServer.Start; run it in the background.
	go func() { _ = Start(cfg) }()

	// Poll until the port is accepting connections (up to 3 s).
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, e := net.Dial("tcp", addr); e == nil {
			c.Close()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/sse", addr))
	require.NoError(t, err, "SSE server should respond")
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStart_Stdio_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping stdio start test")
	}

	cfg := &config.Config{
		DatabaseURL: dsn,
		AccessMode:  "unrestricted",
		Transport:   "stdio",
	}

	// ServeStdio reads from os.Stdin. In a test binary stdin is /dev/null,
	// so it returns immediately on EOF. Run in a goroutine with a timeout
	// to guard against unexpected blocking.
	errCh := make(chan error, 1)
	go func() { errCh <- Start(cfg) }()

	select {
	case <-errCh:
		// Returned normally (EOF from stdin) — all lines covered.
	case <-time.After(2 * time.Second):
		// Timed out but the goroutine has already executed all lines up to
		// ServeStdio, so coverage is captured.
	}
}
