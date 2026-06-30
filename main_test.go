package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain_NoDSN covers main() by re-invoking the test binary in helper mode.
// The helper path calls main() with no args and no DATABASE_URI; cmd.Execute
// will call os.Exit(1). The parent asserts on the captured exit code + output.
func TestMain_NoDSN(t *testing.T) {
	if os.Getenv("PGMCP_MAIN_HELPER") == "1" {
		os.Args = []string{"postgres-mcp"}
		os.Unsetenv("DATABASE_URI")
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMain_NoDSN")
	cmd.Env = append(os.Environ(), "PGMCP_MAIN_HELPER=1")
	out, err := cmd.CombinedOutput()

	require.Error(t, err)
	ee, ok := err.(*exec.ExitError)
	require.True(t, ok)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Contains(t, string(out), "database URI is required")
}
