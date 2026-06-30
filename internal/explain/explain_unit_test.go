package explain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/dbtest"
)

const simpleExplainJSON = `[{"Plan": {"Node Type": "Result", "Total Cost": 0.01, "Startup Cost": 0.0, "Plan Rows": 1, "Plan Width": 0}}]`
const analyzeExplainJSON = `[{"Plan": {"Node Type": "Result", "Total Cost": 0.01, "Startup Cost": 0.0, "Plan Rows": 1, "Plan Width": 0}, "Planning Time": 0.05, "Execution Time": 0.02}]`

func explainRow(json string) []map[string]any {
	return []map[string]any{{"QUERY PLAN": json}}
}

// ─── GetQueryCost ────────────────────────────────────────────────────────────

func TestGetQueryCost_Success(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(explainRow(simpleExplainJSON), nil)

	cost, err := GetQueryCost(context.Background(), mock, "SELECT 1")
	require.NoError(t, err)
	assert.InDelta(t, 0.01, cost, 0.001)
}

func TestGetQueryCost_QueryError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("syntax error"))

	_, err := GetQueryCost(context.Background(), mock, "INVALID")
	require.Error(t, err)
}

func TestGetQueryCost_EmptyResult(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, nil) // empty rows

	_, err := GetQueryCost(context.Background(), mock, "SELECT 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no output")
}

func TestGetQueryCost_EmptyRaw(t *testing.T) {
	// Row with empty QUERY PLAN (no key at all)
	mock := dbtest.NewMock().AddInternalQuery([]map[string]any{{}}, nil)

	_, err := GetQueryCost(context.Background(), mock, "SELECT 1")
	require.Error(t, err)
}

func TestGetQueryCost_LowercaseKey(t *testing.T) {
	// pgx may lowercase column names
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{{"query plan": simpleExplainJSON}}, nil)

	cost, err := GetQueryCost(context.Background(), mock, "SELECT 1")
	require.NoError(t, err)
	assert.InDelta(t, 0.01, cost, 0.001)
}

// ─── ExplainQuery ────────────────────────────────────────────────────────────

func TestExplainQuery_NoIndexes(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(explainRow(simpleExplainJSON), nil)

	res, err := ExplainQuery(context.Background(), mock, "SELECT 1", false, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "SELECT 1", res.Query)
	require.NotNil(t, res.BasePlan)
	assert.InDelta(t, 0.01, res.BasePlan.TotalCost, 0.001)
	assert.Nil(t, res.HypoPlan)
}

func TestExplainQuery_Analyze(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(explainRow(analyzeExplainJSON), nil)

	res, err := ExplainQuery(context.Background(), mock, "SELECT 1", true, nil)
	require.NoError(t, err)
	require.NotNil(t, res.BasePlan)
	assert.InDelta(t, 0.05, res.BasePlan.PlanningTime, 0.001)
	assert.InDelta(t, 0.02, res.BasePlan.ExecTime, 0.001)
}

func TestExplainQuery_BasePlanError(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(nil, errors.New("bad SQL"))

	_, err := ExplainQuery(context.Background(), mock, "BAD", false, nil)
	require.Error(t, err)
}

func TestExplainQuery_WithHypotheticalIndexes(t *testing.T) {
	mock := dbtest.NewMock()
	// 1. Base EXPLAIN
	mock.AddInternalQuery(explainRow(simpleExplainJSON), nil)
	// 2. RequireExtension check (hypopg installed)
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)
	// 3. hypopg_create_index call
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1234)}}, nil)
	// 4. EXPLAIN with hypo indexes (lower cost)
	mock.AddInternalQuery(explainRow(`[{"Plan": {"Node Type": "Index Scan", "Total Cost": 0.001}}]`), nil)
	// 5. hypopg_reset call
	mock.AddInternalQuery(nil, nil)

	hypoIdxs := []HypotheticalIndex{{Definition: "CREATE INDEX ON t (col)"}}
	res, err := ExplainQuery(context.Background(), mock, "SELECT * FROM t", false, hypoIdxs)
	require.NoError(t, err)
	require.NotNil(t, res.HypoPlan)
	assert.GreaterOrEqual(t, res.Improvement, 0.0)
}

func TestExplainQuery_HypoPGNotInstalled(t *testing.T) {
	mock := dbtest.NewMock()
	// 1. Base EXPLAIN
	mock.AddInternalQuery(explainRow(simpleExplainJSON), nil)
	// 2. RequireExtension → extension available but not installed
	mock.AddInternalQuery(dbtest.ExtensionAvailableOnly("1.0"), nil)

	hypoIdxs := []HypotheticalIndex{{Definition: "CREATE INDEX ON t (col)"}}
	_, err := ExplainQuery(context.Background(), mock, "SELECT 1", false, hypoIdxs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

// ─── GetQueryCostWithIndexes ──────────────────────────────────────────────────

func TestGetQueryCostWithIndexes_Success(t *testing.T) {
	mock := dbtest.NewMock()
	// hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(999)}}, nil)
	// EXPLAIN
	mock.AddInternalQuery(explainRow(`[{"Plan": {"Node Type": "Index Scan", "Total Cost": 5.0}}]`), nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	cost, err := GetQueryCostWithIndexes(context.Background(), mock, "SELECT 1", []string{"CREATE INDEX x ON t (a)"})
	require.NoError(t, err)
	assert.InDelta(t, 5.0, cost, 0.001)
}

func TestGetQueryCostWithIndexes_HypopgCreateError(t *testing.T) {
	mock := dbtest.NewMock()
	// hypopg_create_index fails
	mock.AddInternalQuery(nil, errors.New("hypopg error"))
	// hypopg_reset (called after failure)
	mock.AddInternalQuery(nil, nil)

	_, err := GetQueryCostWithIndexes(context.Background(), mock, "SELECT 1", []string{"CREATE INDEX x ON t (a)"})
	require.Error(t, err)
}

// ─── runExplain ───────────────────────────────────────────────────────────────

func TestRunExplain_AnalyzeBlockedInRestrictedMode(t *testing.T) {
	mock := dbtest.NewMock().SetRestricted(true)

	_, err := runExplain(context.Background(), mock, "DELETE FROM t WHERE id = 1", true, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restricted")
}

func TestRunExplain_AnalyzeAllowedWhenUnrestricted(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(explainRow(analyzeExplainJSON), nil)

	res, err := runExplain(context.Background(), mock, "SELECT 1", true, false)
	require.NoError(t, err)
	assert.InDelta(t, 0.01, res.TotalCost, 0.001)
}

func TestRunExplain_InvalidJSON(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{{"QUERY PLAN": "not json"}}, nil)

	_, err := runExplain(context.Background(), mock, "SELECT 1", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse explain JSON")
}

func TestRunExplain_EmptyPlanArray(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{{"QUERY PLAN": "[]"}}, nil)

	_, err := runExplain(context.Background(), mock, "SELECT 1", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty EXPLAIN output")
}

func TestRunExplain_NoPlanNodeCostField(t *testing.T) {
	// Plan node without Total Cost → TotalCost stays 0
	json := `[{"Plan": {"Node Type": "Result"}}]`
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{{"QUERY PLAN": json}}, nil)

	res, err := runExplain(context.Background(), mock, "SELECT 1", false, false)
	require.NoError(t, err)
	assert.Equal(t, 0.0, res.TotalCost)
}

// TestExplainQuery_HypoIndexError covers the withHypotheticalIndexes error path (line 71-73).
func TestExplainQuery_HypoIndexError(t *testing.T) {
	mock := dbtest.NewMock()
	// 1. Base EXPLAIN succeeds
	mock.AddInternalQuery(explainRow(simpleExplainJSON), nil)
	// 2. RequireExtension: hypopg installed
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)
	// 3. hypopg_create_index fails
	mock.AddInternalQuery(nil, errors.New("hypopg_create_index failed"))
	// 4. hypopg_reset (called even on error)
	mock.AddInternalQuery(nil, nil)

	hypoIdxs := []HypotheticalIndex{{Definition: "CREATE INDEX ON t (col)"}}
	_, err := ExplainQuery(context.Background(), mock, "SELECT 1", false, hypoIdxs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "explain with hypothetical indexes")
}

// TestRunExplain_GenericPlan covers the generic=true branch (GENERIC_PLAN option).
func TestRunExplain_GenericPlan(t *testing.T) {
	mock := dbtest.NewMock().AddInternalQuery(
		[]map[string]any{{"QUERY PLAN": simpleExplainJSON}}, nil)

	res, err := runExplain(context.Background(), mock, "SELECT $1", false, true)
	require.NoError(t, err)
	assert.InDelta(t, 0.01, res.TotalCost, 0.001)
}

// TestExplainQuery_ImprovementZeroBaseCost covers the basePlan.TotalCost == 0 branch.
func TestExplainQuery_ImprovementZeroBaseCost(t *testing.T) {
	mock := dbtest.NewMock()
	// Base plan with 0 cost
	mock.AddInternalQuery(explainRow(`[{"Plan": {"Node Type": "Result", "Total Cost": 0.0}}]`), nil)
	// RequireExtension
	mock.AddInternalQuery(dbtest.ExtensionInstalled("1.0"), nil)
	// hypopg_create_index
	mock.AddInternalQuery([]map[string]any{{"indexrelid": float64(1)}}, nil)
	// Hypo EXPLAIN
	mock.AddInternalQuery(explainRow(`[{"Plan": {"Node Type": "Result", "Total Cost": 0.0}}]`), nil)
	// hypopg_reset
	mock.AddInternalQuery(nil, nil)

	hypoIdxs := []HypotheticalIndex{{Definition: "CREATE INDEX ON t (col)"}}
	res, err := ExplainQuery(context.Background(), mock, "SELECT 1", false, hypoIdxs)
	require.NoError(t, err)
	// Improvement should be 0 when base cost is 0
	assert.Equal(t, 0.0, res.Improvement)
}

// ─── helpers (pure unit, no DB) ───────────────────────────────────────────────

func TestEscapeSingleQuotes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"no quotes", "no quotes"},
		{"it's", "it''s"},
		{"'quoted'", "''quoted''"},
		{"a'b'c", "a''b''c"},
		{"", ""},
	}
	for _, tc := range cases {
		got := escapeSingleQuotes(tc.in)
		assert.Equal(t, tc.want, got, "input: %q", tc.in)
	}
}

func TestMax(t *testing.T) {
	assert.Equal(t, 10.0, max(10, 5))
	assert.Equal(t, 10.0, max(5, 10))
	assert.Equal(t, 5.0, max(5, 5))
	assert.Equal(t, 0.001, max(0.001, 0.0))
}
