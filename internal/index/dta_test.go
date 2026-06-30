package index

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── combinations ─────────────────────────────────────────────────────────────

func TestCombinations_Width1(t *testing.T) {
	cols := []string{"a", "b", "c"}
	got := combinations(cols, 1)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"a"}, got[0])
	assert.Equal(t, []string{"b"}, got[1])
	assert.Equal(t, []string{"c"}, got[2])
}

func TestCombinations_Width2(t *testing.T) {
	cols := []string{"a", "b", "c"}
	got := combinations(cols, 2)
	require.Len(t, got, 3) // C(3,2) = 3
	assert.Equal(t, []string{"a", "b"}, got[0])
	assert.Equal(t, []string{"a", "c"}, got[1])
	assert.Equal(t, []string{"b", "c"}, got[2])
}

func TestCombinations_Width3(t *testing.T) {
	cols := []string{"a", "b", "c"}
	got := combinations(cols, 3)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"a", "b", "c"}, got[0])
}

func TestCombinations_WidthExceedsLen(t *testing.T) {
	assert.Nil(t, combinations([]string{"a", "b"}, 3))
}

func TestCombinations_WidthZero(t *testing.T) {
	assert.Nil(t, combinations([]string{"a", "b"}, 0))
}

func TestCombinations_EmptySlice(t *testing.T) {
	assert.Nil(t, combinations([]string{}, 1))
}

func TestCombinations_FourColumns(t *testing.T) {
	cols := []string{"a", "b", "c", "d"}
	// C(4,2) = 6
	assert.Len(t, combinations(cols, 2), 6)
	// C(4,3) = 4
	assert.Len(t, combinations(cols, 3), 4)
	// C(4,4) = 1
	assert.Len(t, combinations(cols, 4), 1)
}

// ─── dedup ────────────────────────────────────────────────────────────────────

func TestDedup_RemovesDuplicates(t *testing.T) {
	got := dedup([]string{"status", "STATUS", "created_at", "status"})
	assert.Len(t, got, 2)
}

func TestDedup_PreservesOrder(t *testing.T) {
	got := dedup([]string{"b", "a", "c"})
	assert.Equal(t, []string{"b", "a", "c"}, got)
}

func TestDedup_Empty(t *testing.T) {
	assert.Nil(t, dedup(nil))
	assert.Nil(t, dedup([]string{}))
}

func TestDedup_Single(t *testing.T) {
	assert.Equal(t, []string{"x"}, dedup([]string{"x"}))
}

// ─── unionStrings ─────────────────────────────────────────────────────────────

func TestUnionStrings(t *testing.T) {
	got := unionStrings([]string{"a", "b"}, []string{"b", "c"})
	assert.Len(t, got, 3)
	assert.Contains(t, got, "a")
	assert.Contains(t, got, "b")
	assert.Contains(t, got, "c")
}

func TestUnionStrings_EmptyA(t *testing.T) {
	got := unionStrings(nil, []string{"a"})
	assert.Equal(t, []string{"a"}, got)
}

func TestUnionStrings_EmptyB(t *testing.T) {
	got := unionStrings([]string{"a"}, nil)
	assert.Equal(t, []string{"a"}, got)
}

func TestUnionStrings_CaseInsensitiveDedupe(t *testing.T) {
	got := unionStrings([]string{"Status"}, []string{"status"})
	assert.Len(t, got, 1)
}

// ─── isValidColumn ────────────────────────────────────────────────────────────

func TestIsValidColumn_Valid(t *testing.T) {
	valid := []string{"status", "user_id", "created_at", "email", "order_total", "id"}
	for _, col := range valid {
		assert.True(t, isValidColumn(col), "expected %q to be valid", col)
	}
}

func TestIsValidColumn_TooShort(t *testing.T) {
	assert.False(t, isValidColumn(""))
	assert.False(t, isValidColumn("a"))
}

func TestIsValidColumn_SQLKeywords(t *testing.T) {
	keywords := []string{"and", "or", "not", "null", "true", "false", "is", "in",
		"any", "all", "case", "when", "then", "else", "end",
		"select", "from", "where", "join", "on", "between", "like", "ilike"}
	for _, kw := range keywords {
		assert.False(t, isValidColumn(kw), "SQL keyword %q should be invalid", kw)
	}
}

// ─── extractColumnsFromCondition ─────────────────────────────────────────────

func TestExtractColumnsFromCondition_Equality(t *testing.T) {
	cols := extractColumnsFromCondition("(status = $1)")
	assert.Contains(t, cols, "status")
}

func TestExtractColumnsFromCondition_Comparison(t *testing.T) {
	cols := extractColumnsFromCondition("(created_at > '2023-01-01'::timestamp)")
	assert.Contains(t, cols, "created_at")
}

func TestExtractColumnsFromCondition_ILIKE(t *testing.T) {
	cols := extractColumnsFromCondition("(email ~~* $1)")
	assert.Contains(t, cols, "email")
}

func TestExtractColumnsFromCondition_IsNull(t *testing.T) {
	cols := extractColumnsFromCondition("(deleted_at IS NULL)")
	assert.Contains(t, cols, "deleted_at")
}

func TestExtractColumnsFromCondition_LessThanEqual(t *testing.T) {
	cols := extractColumnsFromCondition("(price <= $1)")
	assert.Contains(t, cols, "price")
}

func TestExtractColumnsFromCondition_InClause(t *testing.T) {
	cols := extractColumnsFromCondition("(status IN ($1, $2))")
	assert.Contains(t, cols, "status")
}

func TestExtractColumnsFromCondition_Between(t *testing.T) {
	cols := extractColumnsFromCondition("(age BETWEEN $1 AND $2)")
	assert.Contains(t, cols, "age")
}

func TestExtractColumnsFromCondition_TableQualified(t *testing.T) {
	// "t.status = $1" — the regex should extract "status" (not "t")
	cols := extractColumnsFromCondition("(t.status = $1)")
	assert.Contains(t, cols, "status")
	// "t" is too short (1 char), should not be included
	for _, c := range cols {
		assert.NotEqual(t, "t", c)
	}
}

func TestExtractColumnsFromCondition_NoKeywords(t *testing.T) {
	// SQL keywords like AND, OR must not appear in results
	cols := extractColumnsFromCondition("(status = $1 AND name = $2)")
	assert.NotContains(t, cols, "and")
	assert.Contains(t, cols, "status")
	assert.Contains(t, cols, "name")
}

func TestExtractColumnsFromCondition_Empty(t *testing.T) {
	assert.Empty(t, extractColumnsFromCondition(""))
	assert.Empty(t, extractColumnsFromCondition("()"))
}

// ─── walkPlanNode ─────────────────────────────────────────────────────────────

func TestWalkPlanNode_SimpleSeqScan(t *testing.T) {
	out := make(map[string][]string)
	plan := map[string]any{
		"Node Type":     "Seq Scan",
		"Relation Name": "orders",
		"Filter":        "(status = $1)",
		"Plans":         []any{},
	}
	walkPlanNode(plan, out, "")

	assert.Contains(t, out, "orders")
	assert.Contains(t, out["orders"], "status")
}

func TestWalkPlanNode_IndexScan(t *testing.T) {
	out := make(map[string][]string)
	plan := map[string]any{
		"Node Type":     "Index Scan",
		"Relation Name": "users",
		"Index Cond":    "(email = $1)",
		"Filter":        "(is_active IS NOT NULL)",
	}
	walkPlanNode(plan, out, "")

	require.Contains(t, out, "users")
	assert.Contains(t, out["users"], "email")
	assert.Contains(t, out["users"], "is_active")
}

func TestWalkPlanNode_NestedPlans(t *testing.T) {
	out := make(map[string][]string)
	plan := map[string]any{
		"Node Type": "Hash Join",
		"Hash Cond": "(o.user_id = u.id)",
		"Plans": []any{
			map[string]any{
				"Node Type":     "Seq Scan",
				"Relation Name": "orders",
				"Filter":        "(status = $1)",
				"Plans":         []any{},
			},
			map[string]any{
				"Node Type": "Hash",
				"Plans": []any{
					map[string]any{
						"Node Type":     "Seq Scan",
						"Relation Name": "users",
						"Filter":        "(created_at > $2)",
						"Plans":         []any{},
					},
				},
			},
		},
	}
	walkPlanNode(plan, out, "")

	assert.Contains(t, out, "orders")
	assert.Contains(t, out["orders"], "status")
	assert.Contains(t, out, "users")
	assert.Contains(t, out["users"], "created_at")
}

func TestWalkPlanNode_SortKey(t *testing.T) {
	out := make(map[string][]string)
	plan := map[string]any{
		"Node Type":     "Index Scan",
		"Relation Name": "orders",
		"Sort Key":      []any{"created_at DESC NULLS LAST", "id"},
		"Filter":        "(status = $1)",
	}
	walkPlanNode(plan, out, "")

	require.Contains(t, out, "orders")
	assert.Contains(t, out["orders"], "created_at")
	assert.Contains(t, out["orders"], "id")
}

func TestWalkPlanNode_SchemaQualified(t *testing.T) {
	out := make(map[string][]string)
	plan := map[string]any{
		"Node Type":     "Seq Scan",
		"Schema":        "myschema",
		"Relation Name": "orders",
		"Filter":        "(status = $1)",
	}
	walkPlanNode(plan, out, "")

	assert.Contains(t, out, "myschema.orders")
	assert.Contains(t, out["myschema.orders"], "status")
}

// ─── pure helpers ─────────────────────────────────────────────────────────────

func TestEscapeSQ(t *testing.T) {
	assert.Equal(t, "it''s fine", escapeSQ("it's fine"))
	assert.Equal(t, "no quotes", escapeSQ("no quotes"))
	assert.Equal(t, "''quoted''", escapeSQ("'quoted'"))
}

func TestMax64(t *testing.T) {
	assert.Equal(t, 10.0, max64(10, 5))
	assert.Equal(t, 10.0, max64(5, 10))
	assert.Equal(t, 0.0, max64(0, 0))
}

func TestMin(t *testing.T) {
	assert.Equal(t, 3, min(3, 5))
	assert.Equal(t, 3, min(5, 3))
	assert.Equal(t, 0, min(0, 0))
}

func TestSumCosts(t *testing.T) {
	costs := map[string]float64{"a": 10, "b": 20, "c": 5}
	assert.Equal(t, 35.0, sumCosts(costs))
	assert.Equal(t, 0.0, sumCosts(nil))
}

func TestCollectDefs(t *testing.T) {
	cands := []candidate{
		{createSQL: "CREATE INDEX a ON t (x)"},
		{createSQL: "CREATE INDEX b ON t (y)"},
	}
	got := collectDefs(cands)
	assert.Equal(t, []string{"CREATE INDEX a ON t (x)", "CREATE INDEX b ON t (y)"}, got)
}

func TestCollectDefs_Empty(t *testing.T) {
	assert.Empty(t, collectDefs(nil))
}

func TestRemoveSelected(t *testing.T) {
	all := []candidate{
		{createSQL: "sql1"},
		{createSQL: "sql2"},
		{createSQL: "sql3"},
	}
	remaining := removeSelected(all, candidate{createSQL: "sql2"})
	require.Len(t, remaining, 2)
	for _, c := range remaining {
		assert.NotEqual(t, "sql2", c.createSQL)
	}
}

func TestHasSimilarIndex(t *testing.T) {
	existing := map[string]bool{
		"create index idx on orders (status)": true,
	}

	def := IndexDefinition{Table: "orders", Columns: []string{"status"}}
	assert.True(t, hasSimilarIndex(existing, def))

	def2 := IndexDefinition{Table: "orders", Columns: []string{"user_id"}}
	assert.False(t, hasSimilarIndex(existing, def2))

	def3 := IndexDefinition{Table: "users", Columns: []string{"status"}}
	assert.False(t, hasSimilarIndex(existing, def3))
}
