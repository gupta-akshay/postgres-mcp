package schema

import (
	"context"
	"fmt"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// SchemaInfo summarises one schema.
type SchemaInfo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

// ObjectInfo describes one object inside a schema.
type ObjectInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  string `json:"size,omitempty"`
	Owner string `json:"owner,omitempty"`
}

// ColumnInfo describes one column.
type ColumnInfo struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
}

// IndexInfo describes one index.
type IndexInfo struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Size       string `json:"size,omitempty"`
	IsUnique   bool   `json:"is_unique"`
	IsPrimary  bool   `json:"is_primary"`
}

// ConstraintInfo describes one constraint.
type ConstraintInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ObjectDetails holds full details for a table or view.
type ObjectDetails struct {
	Schema      string           `json:"schema"`
	Name        string           `json:"name"`
	Type        string           `json:"type"`
	Columns     []ColumnInfo     `json:"columns"`
	Indexes     []IndexInfo      `json:"indexes"`
	Constraints []ConstraintInfo `json:"constraints"`
	RowEstimate int64            `json:"row_estimate,omitempty"`
	TotalSize   string           `json:"total_size,omitempty"`
}

// ListSchemas returns all non-system schemas.
func ListSchemas(ctx context.Context, d db.Querier) ([]SchemaInfo, error) {
	rows, err := d.QueryRows(ctx, `
		SELECT schema_name, schema_owner
		FROM information_schema.schemata
		WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast')
		  AND schema_name NOT LIKE 'pg_temp_%'
		  AND schema_name NOT LIKE 'pg_toast_temp_%'
		ORDER BY schema_name
	`)
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}

	schemas := make([]SchemaInfo, 0, len(rows))
	for _, r := range rows {
		schemas = append(schemas, SchemaInfo{
			Name:  db.ToString(r["schema_name"]),
			Owner: db.ToString(r["schema_owner"]),
		})
	}
	return schemas, nil
}

// ListObjects returns tables, views, and sequences in the given schema.
func ListObjects(ctx context.Context, d db.Querier, schemaName string) ([]ObjectInfo, error) {
	rows, err := d.QueryRows(ctx, `
		SELECT
			table_name  AS name,
			table_type  AS type,
			pg_size_pretty(
				CASE table_type
					WHEN 'BASE TABLE' THEN
						pg_total_relation_size(
							(quote_ident(table_schema) || '.' || quote_ident(table_name))::regclass
						)
					ELSE NULL
				END
			) AS size,
			tableowner AS owner
		FROM information_schema.tables
		LEFT JOIN pg_tables
			ON pg_tables.schemaname = table_schema
			AND pg_tables.tablename  = table_name
		WHERE table_schema = $1
		UNION ALL
		SELECT
			s.sequence_name        AS name,
			'SEQUENCE'             AS type,
			NULL                   AS size,
			r.rolname              AS owner
		FROM information_schema.sequences s
		JOIN pg_class c
			ON  c.relname       = s.sequence_name
			AND c.relnamespace  = (SELECT oid FROM pg_namespace WHERE nspname = s.sequence_schema)
			AND c.relkind       = 'S'
		JOIN pg_roles r ON r.oid = c.relowner
		WHERE s.sequence_schema = $1
		ORDER BY type, name
	`, schemaName)
	if err != nil {
		return nil, fmt.Errorf("list objects in schema %q: %w", schemaName, err)
	}

	objs := make([]ObjectInfo, 0, len(rows))
	for _, r := range rows {
		objs = append(objs, ObjectInfo{
			Name:  db.ToString(r["name"]),
			Type:  db.ToString(r["type"]),
			Size:  db.ToString(r["size"]),
			Owner: db.ToString(r["owner"]),
		})
	}
	return objs, nil
}

// GetObjectDetails returns full column/index/constraint details for a table or view.
func GetObjectDetails(ctx context.Context, d db.Querier, schemaName, objectName string) (*ObjectDetails, error) {
	// Determine object type
	typeRows, err := d.QueryRows(ctx, `
		SELECT table_type
		FROM information_schema.tables
		WHERE table_schema = $1 AND table_name = $2
		UNION ALL
		SELECT 'SEQUENCE'
		FROM information_schema.sequences
		WHERE sequence_schema = $1 AND sequence_name = $2
		LIMIT 1
	`, schemaName, objectName)
	if err != nil {
		return nil, fmt.Errorf("check object type: %w", err)
	}
	if len(typeRows) == 0 {
		return nil, fmt.Errorf("object %q.%q not found", schemaName, objectName)
	}
	objType := db.ToString(typeRows[0]["table_type"])

	det := &ObjectDetails{
		Schema: schemaName,
		Name:   objectName,
		Type:   objType,
	}

	// Columns
	colRows, err := d.QueryRows(ctx, `
		SELECT
			column_name,
			data_type,
			is_nullable = 'YES' AS nullable,
			column_default
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position
	`, schemaName, objectName)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	for _, r := range colRows {
		nullable, _ := r["nullable"].(bool)
		det.Columns = append(det.Columns, ColumnInfo{
			Name:     db.ToString(r["column_name"]),
			DataType: db.ToString(r["data_type"]),
			Nullable: nullable,
			Default:  db.ToString(r["column_default"]),
		})
	}

	// Indexes (only for tables)
	if objType == "BASE TABLE" {
		idxRows, err := d.QueryRows(ctx, `
			SELECT
				i.relname          AS name,
				pg_get_indexdef(i.oid) AS definition,
				pg_size_pretty(pg_relation_size(i.oid)) AS size,
				ix.indisunique     AS is_unique,
				ix.indisprimary    AS is_primary
			FROM pg_index ix
			JOIN pg_class c  ON c.oid  = ix.indrelid
			JOIN pg_class i  ON i.oid  = ix.indexrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2
			ORDER BY i.relname
		`, schemaName, objectName)
		if err != nil {
			return nil, fmt.Errorf("list indexes: %w", err)
		}
		for _, r := range idxRows {
			isUnique, _ := r["is_unique"].(bool)
			isPrimary, _ := r["is_primary"].(bool)
			det.Indexes = append(det.Indexes, IndexInfo{
				Name:       db.ToString(r["name"]),
				Definition: db.ToString(r["definition"]),
				Size:       db.ToString(r["size"]),
				IsUnique:   isUnique,
				IsPrimary:  isPrimary,
			})
		}

		// Row estimate & size
		statsRows, err := d.QueryRows(ctx, `
			SELECT
				reltuples::bigint            AS row_estimate,
				pg_size_pretty(pg_total_relation_size(oid)) AS total_size
			FROM pg_class
			WHERE oid = ($1 || '.' || $2)::regclass
		`, quote(schemaName), quote(objectName))
		if err == nil && len(statsRows) > 0 {
			if v, err2 := db.ToFloat64(statsRows[0]["row_estimate"]); err2 == nil {
				det.RowEstimate = int64(v)
			}
			det.TotalSize = db.ToString(statsRows[0]["total_size"])
		}
	}

	// Constraints
	conRows, err := d.QueryRows(ctx, `
		SELECT
			constraint_name,
			constraint_type
		FROM information_schema.table_constraints
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY constraint_type, constraint_name
	`, schemaName, objectName)
	if err != nil {
		return nil, fmt.Errorf("list constraints: %w", err)
	}
	for _, r := range conRows {
		det.Constraints = append(det.Constraints, ConstraintInfo{
			Name: db.ToString(r["constraint_name"]),
			Type: db.ToString(r["constraint_type"]),
		})
	}

	return det, nil
}

func quote(s string) string {
	return `"` + s + `"`
}
