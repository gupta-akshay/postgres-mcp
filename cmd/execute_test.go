package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecute_NoDSN_ExitsNonZero covers cmd.Execute() by re-invoking the test
// binary in helper mode. The helper path calls Execute() with no args and no
// env var, which makes RunE return an error → Execute() prints to stderr and
// calls os.Exit(1). os/exec captures the exit code and stderr.
func TestExecute_NoDSN_ExitsNonZero(t *testing.T) {
	if os.Getenv("PGMCP_EXECUTE_HELPER") == "1" {
		// Helper path: runs inside the sub-process spawned below.
		os.Args = []string{"postgres-mcp"}
		os.Unsetenv("DATABASE_URI")
		Execute() // expected to call os.Exit(1)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestExecute_NoDSN_ExitsNonZero")
	cmd.Env = append(os.Environ(), "PGMCP_EXECUTE_HELPER=1")
	out, err := cmd.CombinedOutput()

	require.Error(t, err, "expected non-zero exit")
	ee, ok := err.(*exec.ExitError)
	require.True(t, ok, "expected *exec.ExitError, got %T", err)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Contains(t, string(out), "database URI is required")
}

// TestExecute_HelpFlag covers the success path of Execute() (no error returned).
// --help causes cobra to print usage and exit zero without invoking RunE.
func TestExecute_HelpFlag(t *testing.T) {
	if os.Getenv("PGMCP_EXECUTE_HELP_HELPER") == "1" {
		os.Args = []string{"postgres-mcp", "--help"}
		Execute()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestExecute_HelpFlag")
	cmd.Env = append(os.Environ(), "PGMCP_EXECUTE_HELP_HELPER=1")
	out, err := cmd.CombinedOutput()

	require.NoError(t, err, "expected zero exit, output: %s", out)
	assert.True(t,
		strings.Contains(string(out), "PostgreSQL MCP Server") ||
			strings.Contains(string(out), "postgres-mcp"),
		"usage text should appear: %s", out)
}
