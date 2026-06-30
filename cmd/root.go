package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/gupta-akshay/postgres-mcp/internal/config"
	"github.com/gupta-akshay/postgres-mcp/internal/server"
)

var cfg config.Config

var rootCmd = &cobra.Command{
	Use:   "postgres-mcp [DATABASE_URI]",
	Short: "PostgreSQL MCP Server",
	Long: `A Model Context Protocol (MCP) server for PostgreSQL database analysis,
query optimization, and health monitoring.

The database URI can be passed as a positional argument or via the DATABASE_URI
environment variable (useful for Docker / docker-compose deployments).

Access modes:
  restricted   - Read-only access enforced via read-only transactions (default)
  unrestricted - Full read/write access

Transports:
  stdio - Standard input/output (default, for use with MCP clients)
  sse   - HTTP Server-Sent Events

Examples:
  postgres-mcp "postgresql://user:pass@localhost:5432/mydb"
  postgres-mcp "postgresql://user:pass@localhost:5432/mydb" --access-mode unrestricted
  postgres-mcp "postgresql://user:pass@localhost:5432/mydb" --transport sse --sse-port 8080
  DATABASE_URI="postgresql://user:pass@localhost:5432/mydb" postgres-mcp`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Accept URI from positional arg or environment variable
		if len(args) > 0 {
			cfg.DatabaseURL = args[0]
		} else {
			cfg.DatabaseURL = os.Getenv("DATABASE_URI")
		}
		if cfg.DatabaseURL == "" {
			return fmt.Errorf("database URI is required — pass it as an argument or set the DATABASE_URI environment variable")
		}

		// Allow access-mode to be overridden via env var as well
		if cfg.AccessMode == "restricted" {
			if envMode := os.Getenv("ACCESS_MODE"); envMode != "" {
				cfg.AccessMode = envMode
			}
		}

		return server.Start(&cfg)
	},
}

func init() {
	rootCmd.Flags().StringVar(&cfg.AccessMode, "access-mode", "restricted",
		"Access mode: 'unrestricted' (full access) or 'restricted' (read-only). Also read from ACCESS_MODE env var.")
	rootCmd.Flags().StringVar(&cfg.Transport, "transport", "stdio",
		"Transport type: 'stdio' or 'sse'")
	rootCmd.Flags().StringVar(&cfg.SSEHost, "sse-host", "0.0.0.0",
		"SSE server bind host")
	rootCmd.Flags().IntVar(&cfg.SSEPort, "sse-port", 8000,
		"SSE server port")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
