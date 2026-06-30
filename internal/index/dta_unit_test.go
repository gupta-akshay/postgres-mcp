package index

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/dbtest"
)

var ctx = context.Background()

// explainRow builds a mock EXPLAIN (FORMAT JSON) response row.
func explainRow(totalCost float64) map[string]any {
	return map[string]any{
		"QUERY PLAN": `[{"Plan": {"Node Type": "Result", "Total Cost": ` +
			formatFloatStr(totalCost) + `, "Startup Cost": 0.0, "Plan Rows": 1}}]`,
	}
}

func formatFloatStr(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%d.0", int(f))
	}
	return fmt.Sprintf("%g", f)
}

func explainRowWithRelation(schema, table, filter string, cost float64) map[string]any {
	return map[string]any{
		"QUERY PLAN": `[{"Plan": {"Node Type": "Seq Scan", "Schema": "` + schema +
			`", "Relation Name": "` + table +
			`", "Filter": "` + filter +
			`", "Total Cost": ` + formatFloatStr(cost) + `, "Plan Rows": 100}}]`,
	}
}

// ─── AnalyzeQueries ──────────────────────────────────────────────────────────

func TestAnalyzeQueries_HypoPGNotInstalled(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionAvailableOnly("1.0"), nil) // hypopg available but not installed

	_, err := AnalyzeQueries(ctx, mock, []string{"SELECT 1"}, DefaultDTAConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

func TestAnalyzeQueries_EmptyQueries(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil) // hypopg installed

	result, err := AnalyzeQueries(ctx, mock, []string{}, DefaultDTAConfig())
	require.NoError(t, err)
	require.NotNil(t, result)
	// Empty queries → empty result
}

func TestAnalyzeQueries_NoExplainableQueries(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil) // hypopg
	// filterExplainable: GetQueryCost → InternalQuery fails for the one query
	mock.AddInternalQuery(nil, errors.New("syntax error"))

	result, err := AnalyzeQueries(ctx, mock, []string{"INVALID SQL"}, DefaultDTAConfig())
	require.NoError(t, err)
	require.NotNil(t, result)
}

// ─── AnalyzeWorkload ─────────────────────────────────────────────────────────

func TestAnalyzeWorkload_PgStatStatementsNotInstalled(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionAvailableOnly("1.10"), nil)

	_, err := AnalyzeWorkload(ctx, mock, DefaultDTAConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_stat_statements")
}

func TestAnalyzeWorkload_HypoPGNotInstalled(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)    // pg_stat_statements ok
	mock.AddInternalQuery(dbtest.ExtensionAvailableOnly("1.0"), nil) // hypopg not installed

	_, err := AnalyzeWorkload(ctx, mock, DefaultDTAConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hypopg")
}

func TestAnalyzeWorkload_VersionError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)
	mock.SetVersionErr(errors.New("version error"))

	_, err := AnalyzeWorkload(ctx, mock, DefaultDTAConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get version")
}

func TestAnalyzeWorkload_WorkloadQueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)
	mock.AddInternalQuery(nil, errors.New("workload query failed"))

	_, err := AnalyzeWorkload(ctx, mock, DefaultDTAConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load workload")
}

func TestAnalyzeWorkload_EmptyWorkload(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil)
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)
	mock.AddInternalQuery(nil, nil) // empty workload

	result, err := AnalyzeWorkload(ctx, mock, DefaultDTAConfig())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestAnalyzeWorkload_WithWorkload(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil) // pg_stat_statements
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)  // hypopg
	// workload query: one query row (empty string filtered out)
	mock.AddInternalQuery([]map[string]any{
		{"query": "SELECT * FROM orders WHERE status = $1"},
		{"query": ""}, // empty string — should be skipped
	}, nil)

	// filterExplainable: GetQueryCost for "SELECT * FROM orders..."
	mock.AddInternalQuery([]map[string]any{explainRow(100.0)}, nil)
	// baseline costs
	mock.AddInternalQuery([]map[string]any{explainRow(100.0)}, nil)
	// generateCandidates: extractPlanInfo
	mock.AddInternalQuery([]map[string]any{explainRowWithRelation("public", "orders", "(status = $1)", 100.0)}, nil)
	// filterExistingIndexes: no existing
	mock.AddInternalQuery(nil, nil)
	// findBest: computeCosts with candidate (same cost → no improvement)
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil) // hypopg_create
	mock.AddInternalQuery([]map[string]any{explainRow(100.0)}, nil)          // EXPLAIN (same)
	mock.AddInternalQuery(nil, nil)                                          // hypopg_reset
	// estimateIndexSizeMB
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"sz": float64(1024 * 1024)}}, nil)
	mock.AddInternalQuery(nil, nil) // reset
	// totalCostWith (check improvement threshold)
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil) // hypopg_create
	mock.AddInternalQuery([]map[string]any{explainRow(100.0)}, nil)          // explain
	mock.AddInternalQuery(nil, nil)                                          // reset

	cfg := DefaultDTAConfig()
	cfg.MinImprovementPct = 50.0 // high threshold: no improvement → no recs
	result, err := AnalyzeWorkload(ctx, mock, cfg)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestAnalyzeWorkload_PG12Version(t *testing.T) {
	// PG 12 uses "total_time" column instead of "total_exec_time"
	mock := dbtest.NewMock().SetVersion(120000)                   // PG 12
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.10"), nil) // pg_stat_statements
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)  // hypopg
	// workload query returns empty → no queries → empty result
	mock.AddInternalQuery(nil, nil)

	result, err := AnalyzeWorkload(ctx, mock, DefaultDTAConfig())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Recommendations)
}

// ─── runDTA ───────────────────────────────────────────────────────────────────

func TestRunDTA_EmptyQueries(t *testing.T) {
	mock := dbtest.NewMock()
	result, err := runDTA(ctx, mock, []string{}, DefaultDTAConfig())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestRunDTA_AllQueriesUnexplainable(t *testing.T) {
	mock := dbtest.NewMock()
	// filterExplainable: each query → explain fails
	mock.AddInternalQuery(nil, errors.New("bad SQL"))

	result, err := runDTA(ctx, mock, []string{"BAD SQL"}, DefaultDTAConfig())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestRunDTA_NoImprovingIndex(t *testing.T) {
	// Scenario: one query, one candidate, but candidate provides no improvement
	// above MinImprovementPct threshold.
	mock := dbtest.NewMock()

	cost := 100.0

	// filterExplainable: GetQueryCost → InternalQuery
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)

	// computeCosts (baseline): GetQueryCost → InternalQuery
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)

	// generateCandidates: extractPlanInfo → InternalQuery (with relation)
	mock.AddInternalQuery([]map[string]any{
		explainRowWithRelation("public", "users", "(status = $1)", cost),
	}, nil)

	// filterExistingIndexes: InternalQuery (pg_stat_user_indexes) → no existing
	mock.AddInternalQuery(nil, nil)

	// findBest: computeCosts with candidate → same cost (no improvement)
	// hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(123)}}, nil)
	// EXPLAIN with hypo index → same cost
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	// estimateIndexSizeMB: hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(123)}}, nil)
	// hypopg_relation_size
	mock.AddInternalQuery([]map[string]any{{"sz": float64(1024 * 1024)}}, nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	// totalCostWith (after best): computeCosts → InternalQuery
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)

	cfg := DefaultDTAConfig()
	cfg.MinImprovementPct = 5.0 // require 5% improvement

	result, err := runDTA(ctx, mock, []string{"SELECT * FROM users WHERE status = $1"}, cfg)
	require.NoError(t, err)
	require.NotNil(t, result)
	// No improvement means no recommendations
	assert.Empty(t, result.Recommendations)
}

// ─── generateCandidates ───────────────────────────────────────────────────────

func TestGenerateCandidates_WithExplainOutput(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{
		explainRowWithRelation("public", "orders", "(status = $1)", 50.0),
	}, nil)

	cands := generateCandidates(ctx, mock, []string{"SELECT * FROM orders WHERE status = $1"}, 2)
	assert.NotEmpty(t, cands)
	// Should have at least one candidate for the "status" column
	var found bool
	for _, c := range cands {
		if c.def.Table == "orders" {
			found = true
		}
	}
	assert.True(t, found, "expected candidate for orders table")
}

func TestGenerateCandidates_ExplainError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("cannot explain"))

	cands := generateCandidates(ctx, mock, []string{"INVALID"}, 2)
	assert.Empty(t, cands)
}

func TestExtractPlanInfo_EmptyRows(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, nil) // empty rows

	result := extractPlanInfo(ctx, mock, "SELECT 1")
	assert.Empty(t, result)
}

func TestExtractPlanInfo_InvalidJSON(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"QUERY PLAN": "not-json"}}, nil)

	result := extractPlanInfo(ctx, mock, "SELECT 1")
	assert.Empty(t, result)
}

func TestExtractPlanInfo_NoPlanKey(t *testing.T) {
	// Valid JSON array but no "Plan" key → walkPlanNode not called
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"QUERY PLAN": `[{"NoPlan": {}}]`}}, nil)

	result := extractPlanInfo(ctx, mock, "SELECT 1")
	assert.Empty(t, result)
}

func TestExtractPlanInfo_LowercaseKey(t *testing.T) {
	// pgx may return lowercase column name "query plan"
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{
		{"query plan": `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "orders", "Filter": "(status = $1)"}}]`},
	}, nil)

	result := extractPlanInfo(ctx, mock, "SELECT 1")
	// Should parse successfully and find the "orders" table
	assert.NotEmpty(t, result)
}

func TestRunDTA_BudgetExceeded(t *testing.T) {
	// Scenario: one query, one candidate with huge size exceeding budget.
	// Flow: filterExplainable → baseline → generateCandidates → filterExisting →
	//       findBest (improvement is big) → totalCostWith → budget check → BREAK
	mock := dbtest.NewMock()
	cost := 100.0

	// 1. filterExplainable: GetQueryCost → InternalQuery (EXPLAIN)
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)
	// 2. baseline: computeCosts → GetQueryCost → InternalQuery (EXPLAIN)
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)
	// 3. generateCandidates: extractPlanInfo → InternalQuery (EXPLAIN with relation)
	mock.AddInternalQuery([]map[string]any{
		explainRowWithRelation("public", "t", "(col = $1)", cost),
	}, nil)
	// 4. filterExistingIndexes: InternalQuery (pg_stat_user_indexes)
	mock.AddInternalQuery(nil, nil)
	// 5a. findBest: computeCosts(with candidate) → withHypotheticalIndexes:
	//     hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil)
	//     EXPLAIN with hypo (huge improvement: 1.0 vs 100.0)
	mock.AddInternalQuery([]map[string]any{explainRow(1.0)}, nil)
	//     hypopg_reset
	mock.AddInternalQuery(nil, nil)
	// 5b. findBest: estimateIndexSizeMB → hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(2)}}, nil)
	//     hypopg_relation_size: returns 100 MB
	mock.AddInternalQuery([]map[string]any{{"sz": float64(100 * 1024 * 1024)}}, nil)
	//     hypopg_reset
	mock.AddInternalQuery(nil, nil)
	// 6. totalCostWith(selected=[]) → computeCosts(no defs) → GetQueryCost → EXPLAIN
	mock.AddInternalQuery([]map[string]any{explainRow(cost)}, nil)
	// → improvement ≈ 99%, passes threshold → reaches budget check → BREAK

	cfg := DefaultDTAConfig()
	cfg.MinImprovementPct = 0.1 // very low threshold so we reach budget check
	cfg.BudgetMB = 0.001        // 1 KB budget — 100 MB index exceeds it

	result, err := runDTA(ctx, mock, []string{"SELECT * FROM t WHERE col = $1"}, cfg)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Budget exceeded before selecting any index → no recommendations
	assert.Empty(t, result.Recommendations)
}

func TestRunDTA_SelectsIndex(t *testing.T) {
	// Scenario: one query, one candidate with good improvement and budget not exceeded.
	// After selection, second iteration has no more candidates → loop exits.
	mock := dbtest.NewMock()
	baseCost := 100.0
	afterCost := 10.0 // 90% improvement

	// 1. filterExplainable
	mock.AddInternalQuery([]map[string]any{explainRow(baseCost)}, nil)
	// 2. baseline computeCosts
	mock.AddInternalQuery([]map[string]any{explainRow(baseCost)}, nil)
	// 3. generateCandidates
	mock.AddInternalQuery([]map[string]any{
		explainRowWithRelation("public", "orders", "(status = $1)", baseCost),
	}, nil)
	// 4. filterExistingIndexes
	mock.AddInternalQuery(nil, nil)

	// Iteration 1:
	// 5a. findBest: computeCosts(with candidate)
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil) // create
	mock.AddInternalQuery([]map[string]any{explainRow(afterCost)}, nil)      // EXPLAIN
	mock.AddInternalQuery(nil, nil)                                          // reset
	// 5b. findBest: estimateIndexSizeMB (small: 1MB)
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(2)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"sz": float64(1024 * 1024)}}, nil)
	mock.AddInternalQuery(nil, nil)
	// 6. totalCostWith(selected=[])
	mock.AddInternalQuery([]map[string]any{explainRow(baseCost)}, nil)
	// → improvement = (100 - 10) / 100 = 90% → passes
	// → budget: -1 (unlimited) → no check
	// → selects candidate → candidates becomes empty → loop exits

	// 7. buildResult: computeCosts(with selected) → withHypotheticalIndexes
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(3)}}, nil) // create
	mock.AddInternalQuery([]map[string]any{explainRow(afterCost)}, nil)      // EXPLAIN
	mock.AddInternalQuery(nil, nil)                                          // reset
	// 8. buildResult: estimateIndexSizeMB for recommendation
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(4)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"sz": float64(1024 * 1024)}}, nil)
	mock.AddInternalQuery(nil, nil)

	cfg := DefaultDTAConfig()
	cfg.MinImprovementPct = 5.0 // 90% improvement >> 5% threshold
	cfg.BudgetMB = -1           // unlimited

	result, err := runDTA(ctx, mock, []string{"SELECT * FROM orders WHERE status = $1"}, cfg)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Recommendations, 1)
	assert.Greater(t, result.Summary.ImprovementFactor, 1.0)
}

// ─── filterExistingIndexes ────────────────────────────────────────────────────

func TestFilterExistingIndexes_QueryError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("query error"))

	cands := []candidate{
		{def: IndexDefinition{Schema: "public", Table: "users", Columns: []string{"email"}, Type: "btree"}},
	}
	// On error, returns all candidates unchanged
	result := filterExistingIndexes(ctx, mock, cands)
	assert.Equal(t, cands, result)
}

func TestFilterExistingIndexes_FiltersExisting(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{
		{"def": "CREATE INDEX users_email_idx ON users (email)"},
	}, nil)

	cands := []candidate{
		{def: IndexDefinition{Table: "users", Columns: []string{"email"}, Type: "btree"}},
	}
	result := filterExistingIndexes(ctx, mock, cands)
	assert.Empty(t, result, "email index already exists, should be filtered out")
}

// ─── estimateIndexSizeMB ─────────────────────────────────────────────────────

func TestEstimateIndexSizeMB_FallbackOIDColumn(t *testing.T) {
	// hypopg_create_index returns a row without "indexrelid" key but with
	// another column that has a positive OID value → triggers the fallback loop
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{{"oid": float64(999)}}, nil)            // no "indexrelid" key
	mock.AddInternalQuery([]map[string]any{{"sz": float64(2 * 1024 * 1024)}}, nil) // relation size: 2MB
	mock.AddInternalQuery(nil, nil)                                                // hypopg_reset

	def := IndexDefinition{Schema: "public", Table: "t", Columns: []string{"col"}, Type: "btree"}
	size := estimateIndexSizeMB(ctx, mock, def)
	assert.InDelta(t, 2.0, size, 0.01)
}

func TestEstimateIndexSizeMB_HypopgError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("hypopg not available"))

	def := IndexDefinition{Schema: "public", Table: "t", Columns: []string{"col"}, Type: "btree"}
	size := estimateIndexSizeMB(ctx, mock, def)
	assert.Equal(t, 1.0, size, "should return fallback 1.0 MB on error")
}

func TestEstimateIndexSizeMB_ZeroOID(t *testing.T) {
	mock := dbtest.NewMock()
	// hypopg_create_index returns oid=0
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(0)}}, nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	def := IndexDefinition{Schema: "public", Table: "t", Columns: []string{"col"}, Type: "btree"}
	size := estimateIndexSizeMB(ctx, mock, def)
	assert.Equal(t, 1.0, size, "should return fallback 1.0 MB when oid=0")
}

func TestEstimateIndexSizeMB_Success(t *testing.T) {
	mock := dbtest.NewMock()
	// hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(999)}}, nil)
	// hypopg_relation_size: 2 MB
	mock.AddInternalQuery([]map[string]any{{"sz": float64(2 * 1024 * 1024)}}, nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	def := IndexDefinition{Schema: "public", Table: "t", Columns: []string{"col"}, Type: "btree"}
	size := estimateIndexSizeMB(ctx, mock, def)
	assert.InDelta(t, 2.0, size, 0.01)
}

// ─── buildResult ──────────────────────────────────────────────────────────────

func TestBuildResult_NoSelected(t *testing.T) {
	mock := dbtest.NewMock()
	baseCosts := map[string]float64{"SELECT 1": 10.0}

	result, err := buildResult(ctx, mock, []string{"SELECT 1"}, nil, baseCosts, 10.0)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Recommendations)
	assert.InDelta(t, 1.0, result.Summary.ImprovementFactor, 0.001)
}

func TestBuildResult_WithSelected(t *testing.T) {
	mock := dbtest.NewMock()

	query := "SELECT * FROM orders WHERE status = $1"
	sel := []candidate{
		{
			def:       IndexDefinition{Schema: "public", Table: "orders", Columns: []string{"status"}, Type: "btree"},
			createSQL: "CREATE INDEX pgmcp_orders_status_idx ON public.orders (status)",
		},
	}
	baseCosts := map[string]float64{query: 100.0}

	// computeCosts (final costs with selected indexes)
	// → GetQueryCostWithIndexes → withHypotheticalIndexes:
	//   hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil)
	//   EXPLAIN
	mock.AddInternalQuery([]map[string]any{explainRow(50.0)}, nil)
	//   hypopg_reset
	mock.AddInternalQuery(nil, nil)

	// estimateIndexSizeMB for the selected candidate:
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(2)}}, nil)
	mock.AddInternalQuery([]map[string]any{{"sz": float64(1024 * 1024)}}, nil)
	mock.AddInternalQuery(nil, nil) // hypopg_reset

	result, err := buildResult(ctx, mock, []string{query}, sel, baseCosts, 100.0)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Recommendations, 1)
	assert.Greater(t, result.Summary.ImprovementFactor, 1.0)
}

// ─── computeCosts ─────────────────────────────────────────────────────────────

func TestComputeCosts_NoDefs(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery([]map[string]any{explainRow(42.0)}, nil)

	costs := computeCosts(ctx, mock, []string{"SELECT 1"}, nil)
	assert.InDelta(t, 42.0, costs["SELECT 1"], 0.001)
}

func TestComputeCosts_WithDefs(t *testing.T) {
	mock := dbtest.NewMock()
	// hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil)
	// EXPLAIN
	mock.AddInternalQuery([]map[string]any{explainRow(10.0)}, nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	costs := computeCosts(ctx, mock, []string{"SELECT 1"}, []string{"CREATE INDEX x ON t (a)"})
	assert.InDelta(t, 10.0, costs["SELECT 1"], 0.001)
}

func TestComputeCosts_ExplainError(t *testing.T) {
	mock := dbtest.NewMock()
	mock.AddInternalQuery(nil, errors.New("explain error"))

	costs := computeCosts(ctx, mock, []string{"INVALID"}, nil)
	assert.Empty(t, costs)
}

// ─── hasSimilarIndex ──────────────────────────────────────────────────────────

func TestHasSimilarIndex_ExactMatch(t *testing.T) {
	existing := map[string]bool{
		"CREATE INDEX users_email_idx ON users (email)": true,
	}
	def := IndexDefinition{Table: "users", Columns: []string{"email"}}
	assert.True(t, hasSimilarIndex(existing, def), "exact column match should be filtered")
}

func TestHasSimilarIndex_LeadingPrefix(t *testing.T) {
	// Existing multi-column index: (tenant_id, status)
	// Proposed single-column index: (tenant_id) — it IS a leading prefix.
	existing := map[string]bool{
		"CREATE INDEX idx ON orders (tenant_id, status)": true,
	}
	def := IndexDefinition{Table: "orders", Columns: []string{"tenant_id"}}
	assert.True(t, hasSimilarIndex(existing, def), "(tenant_id) is a leading prefix of (tenant_id, status)")
}

func TestHasSimilarIndex_NotLeadingPrefix(t *testing.T) {
	// Existing: (tenant_id, status). Proposed: (status) alone.
	// (status) is NOT a leading prefix, so the recommendation should NOT be suppressed.
	existing := map[string]bool{
		"CREATE INDEX idx ON orders (tenant_id, status)": true,
	}
	def := IndexDefinition{Table: "orders", Columns: []string{"status"}}
	assert.False(t, hasSimilarIndex(existing, def), "(status) is not a leading prefix of (tenant_id, status)")
}

func TestHasSimilarIndex_DifferentTable(t *testing.T) {
	existing := map[string]bool{
		"CREATE INDEX users_email_idx ON users (email)": true,
	}
	def := IndexDefinition{Table: "orders", Columns: []string{"email"}}
	assert.False(t, hasSimilarIndex(existing, def), "different table should not match")
}

func TestHasSimilarIndex_NoParensInExisting(t *testing.T) {
	// Existing def contains the table name but has no column list (no parens).
	// extractIndexColumns returns nil → len(existCols) == 0 → continue.
	existing := map[string]bool{
		"create index users_idx on users using btree": true,
	}
	def := IndexDefinition{Table: "users", Columns: []string{"email"}}
	assert.False(t, hasSimilarIndex(existing, def))
}

func TestHasSimilarIndex_ProposedLongerThanExisting(t *testing.T) {
	// Proposed index has more columns than the existing one → can't be a leading prefix.
	existing := map[string]bool{
		"CREATE INDEX orders_status_idx ON orders (status)": true,
	}
	def := IndexDefinition{Table: "orders", Columns: []string{"status", "created_at"}}
	assert.False(t, hasSimilarIndex(existing, def))
}

// ─── extractIndexColumns ──────────────────────────────────────────────────────

func TestExtractIndexColumns_NoParens(t *testing.T) {
	result := extractIndexColumns("create index no_parens_def on t using btree")
	assert.Nil(t, result, "def without parens should return nil")
}

// ─── walkPlanNode ─────────────────────────────────────────────────────────────

func TestWalkPlanNode_JoinConditionPropagated(t *testing.T) {
	// Simulate a Hash Join node with a Hash Cond, and two child Seq Scan nodes.
	// The join condition columns should be attributed to both child relation tables.
	planJSON := map[string]any{
		"Node Type": "Hash Join",
		"Hash Cond": "(o.customer_id = c.id)",
		"Plans": []any{
			map[string]any{
				"Node Type":     "Seq Scan",
				"Relation Name": "orders",
				"Schema":        "public",
			},
			map[string]any{
				"Node Type": "Hash",
				"Plans": []any{
					map[string]any{
						"Node Type":     "Seq Scan",
						"Relation Name": "customers",
						"Schema":        "public",
					},
				},
			},
		},
	}

	out := make(map[string][]string)
	walkPlanNode(planJSON, out, "")

	// Both tables should receive the join-condition columns
	ordersFound := false
	customersFound := false
	for _, col := range out["public.orders"] {
		if col == "customer_id" || col == "id" {
			ordersFound = true
		}
	}
	for _, col := range out["public.customers"] {
		if col == "customer_id" || col == "id" {
			customersFound = true
		}
	}
	assert.True(t, ordersFound, "expected join condition columns attributed to public.orders")
	assert.True(t, customersFound, "expected join condition columns attributed to public.customers")
}
