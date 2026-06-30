package index

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexDefinitionName(t *testing.T) {
	cases := []struct {
		def          IndexDefinition
		wantContains []string // fragments the name must contain
		maxLen       int
	}{
		{
			def:          IndexDefinition{Schema: "public", Table: "orders", Columns: []string{"status"}},
			wantContains: []string{"orders", "status"},
			maxLen:       63,
		},
		{
			def:          IndexDefinition{Schema: "public", Table: "orders", Columns: []string{"user_id", "status"}},
			wantContains: []string{"orders", "user_id"},
			maxLen:       63,
		},
	}

	for _, tc := range cases {
		name := tc.def.Name()
		assert.NotEmpty(t, name)
		assert.LessOrEqual(t, len(name), tc.maxLen)
		for _, frag := range tc.wantContains {
			assert.Contains(t, name, frag, "name=%q should contain %q", name, frag)
		}
	}
}

func TestIndexDefinitionName_TruncatesLongNames(t *testing.T) {
	// Very long column list should still produce a name ≤ 63 chars
	cols := []string{"column_one", "column_two", "column_three", "column_four"}
	def := IndexDefinition{Schema: "public", Table: "very_long_table_name_here", Columns: cols}
	name := def.Name()
	assert.LessOrEqual(t, len(name), 63, "name must not exceed PostgreSQL identifier limit")
}

func TestIndexDefinitionCreateSQL(t *testing.T) {
	def := IndexDefinition{
		Schema:  "public",
		Table:   "orders",
		Columns: []string{"status", "created_at"},
		Type:    "btree",
	}

	sql := def.CreateSQL()

	assert.True(t, strings.HasPrefix(sql, "CREATE INDEX "), "must start with CREATE INDEX")
	assert.Contains(t, sql, "orders")
	assert.Contains(t, sql, "status")
	assert.Contains(t, sql, "created_at")
	// Columns must appear in order
	statusIdx := strings.Index(sql, "status")
	createdIdx := strings.Index(sql, "created_at")
	assert.Less(t, statusIdx, createdIdx, "columns must appear in definition order")
}

func TestIndexDefinitionCreateSQL_SchemaQualified(t *testing.T) {
	def := IndexDefinition{
		Schema:  "myschema",
		Table:   "mytable",
		Columns: []string{"id"},
	}
	sql := def.CreateSQL()
	assert.Contains(t, sql, "myschema.mytable", "should qualify table with schema")
}

func TestIndexDefinitionCreateSQL_AlreadyQualified(t *testing.T) {
	def := IndexDefinition{
		Schema:  "public",
		Table:   "public.orders", // already qualified
		Columns: []string{"id"},
	}
	sql := def.CreateSQL()
	// Should NOT double-qualify
	assert.NotContains(t, sql, "public.public.orders")
}

func TestDefaultDTAConfig(t *testing.T) {
	cfg := DefaultDTAConfig()

	require.Greater(t, cfg.MaxIndexWidth, 0)
	require.Greater(t, cfg.MinImprovementPct, 0.0)
	require.Greater(t, cfg.TimeLimitSeconds, 0.0)
	require.Greater(t, cfg.WorkloadLimit, 0)
}
