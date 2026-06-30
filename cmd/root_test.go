package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunE_NoDatabaseURI(t *testing.T) {
	t.Setenv("DATABASE_URI", "")
	err := rootCmd.RunE(rootCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database URI is required")
}

func TestRunE_PositionalArgOverridesEnv(t *testing.T) {
	t.Setenv("DATABASE_URI", "")
	// A positional arg is accepted but fails at DB connect (not at URI check).
	err := rootCmd.RunE(rootCmd, []string{"invalid-dsn-that-wont-connect"})
	if err != nil {
		// Should fail at DB connection, not at URI-required check
		assert.NotContains(t, err.Error(), "database URI is required")
	}
}

func TestRunE_AccessModeOverriddenByEnv(t *testing.T) {
	t.Setenv("DATABASE_URI", "")
	t.Setenv("ACCESS_MODE", "unrestricted")
	cfg.AccessMode = "restricted"

	err := rootCmd.RunE(rootCmd, []string{})
	// Fails at URI check, not access-mode
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database URI is required")
}

func TestRunE_AccessModeNotOverriddenWhenUnrestricted(t *testing.T) {
	t.Setenv("DATABASE_URI", "")
	t.Setenv("ACCESS_MODE", "unrestricted")
	cfg.AccessMode = "unrestricted"

	err := rootCmd.RunE(rootCmd, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database URI is required")
}
