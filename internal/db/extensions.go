package db

import (
	"context"
	"fmt"
)

// ExtensionInfo holds the status of a PostgreSQL extension.
type ExtensionInfo struct {
	Name      string
	Installed bool
	Available bool
	Version   string
}

// CheckExtension queries pg_extension and pg_available_extensions for the given name.
func CheckExtension(ctx context.Context, d Querier, name string) (ExtensionInfo, error) {
	info := ExtensionInfo{Name: name}

	rows, err := d.InternalQuery(ctx, `
		SELECT
			e.extversion,
			TRUE AS installed,
			TRUE AS available
		FROM pg_extension e
		WHERE e.extname = $1
		UNION ALL
		SELECT
			ae.default_version,
			FALSE AS installed,
			TRUE AS available
		FROM pg_available_extensions ae
		LEFT JOIN pg_extension e ON e.extname = ae.name
		WHERE ae.name = $1 AND e.extname IS NULL
		LIMIT 1
	`, name)
	if err != nil {
		return info, fmt.Errorf("query extension %s: %w", name, err)
	}

	if len(rows) == 0 {
		return info, nil // not installed and not available
	}

	info.Available = true
	if v, ok := rows[0]["installed"].(bool); ok {
		info.Installed = v
	}
	info.Version = ToString(rows[0]["extversion"])
	return info, nil
}

// RequireExtension returns a formatted error when extension is not installed.
func RequireExtension(ctx context.Context, d Querier, name string) error {
	info, err := CheckExtension(ctx, d, name)
	if err != nil {
		return err
	}
	if info.Installed {
		return nil
	}
	if !info.Available {
		return fmt.Errorf("extension %q is not available on this server; install the postgresql package providing %q and try again", name, name)
	}
	return fmt.Errorf("extension %q is available but not installed; run CREATE EXTENSION IF NOT EXISTS %s", name, name)
}
