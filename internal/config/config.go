package config

// Config holds the server configuration.
type Config struct {
	DatabaseURL string
	AccessMode  string // "unrestricted" or "restricted"
	Transport   string // "stdio" or "sse"
	SSEHost     string
	SSEPort     int
}

// IsRestricted returns true when the server operates in read-only mode.
func (c *Config) IsRestricted() bool {
	return c.AccessMode != "unrestricted"
}
