package explain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// HypotheticalIndex is a CREATE INDEX statement to simulate via HypoPG.
type HypotheticalIndex struct {
	Definition string `json:"definition"` // e.g. "CREATE INDEX ON orders (status)"
}

// PlanResult holds the output of an EXPLAIN (ANALYZE) call.
type PlanResult struct {
	Plan         any     `json:"plan"`
	PlanningTime float64 `json:"planning_time_ms,omitempty"`
	ExecTime     float64 `json:"execution_time_ms,omitempty"`
	TotalCost    float64 `json:"total_cost"`
}

// ExplainResult is the full response for the explain_query tool.
type ExplainResult struct {
	Query       string      `json:"query"`
	BasePlan    *PlanResult `json:"base_plan"`
	HypoPlan    *PlanResult `json:"hypo_plan,omitempty"`
	Improvement float64     `json:"improvement_factor,omitempty"`
	Indexes     []string    `json:"simulated_indexes,omitempty"`
}

// ExplainQuery runs EXPLAIN [ANALYZE] on query. When hypotheticalIndexes is
// provided it also runs EXPLAIN with those indexes simulated via HypoPG and
// returns a before/after comparison.
func ExplainQuery(
	ctx context.Context,
	d db.Querier,
	query string,
	analyze bool,
	hypotheticalIndexes []HypotheticalIndex,
) (*ExplainResult, error) {
	basePlan, err := runExplain(ctx, d, query, analyze, false)
	if err != nil {
		return nil, fmt.Errorf("explain query: %w", err)
	}

	result := &ExplainResult{
		Query:    query,
		BasePlan: basePlan,
	}

	if len(hypotheticalIndexes) == 0 {
		return result, nil
	}

	// Verify HypoPG is available
	if err := db.RequireExtension(ctx, d, "hypopg"); err != nil {
		return nil, err
	}

	defs := make([]string, len(hypotheticalIndexes))
	for i, h := range hypotheticalIndexes {
		defs[i] = h.Definition
	}

	hypoPlan, err := withHypotheticalIndexes(ctx, d, defs, func() (*PlanResult, error) {
		return runExplain(ctx, d, query, false, false)
	})
	if err != nil {
		return nil, fmt.Errorf("explain with hypothetical indexes: %w", err)
	}

	result.HypoPlan = hypoPlan
	result.Indexes = defs
	if basePlan.TotalCost > 0 {
		result.Improvement = basePlan.TotalCost / max(hypoPlan.TotalCost, 0.001)
	}
	return result, nil
}

// GetQueryCost returns the planner's estimated total cost for query (using
// hypothetical indexes if provided). Used internally by the DTA algorithm.
func GetQueryCost(ctx context.Context, d db.Querier, query string) (float64, error) {
	plan, err := runExplain(ctx, d, query, false, false)
	if err != nil {
		return 0, err
	}
	return plan.TotalCost, nil
}

// GetQueryCostWithIndexes returns the estimated cost after simulating indexes.
func GetQueryCostWithIndexes(ctx context.Context, d db.Querier, query string, indexDefs []string) (float64, error) {
	var cost float64
	var planErr error

	_, err := withHypotheticalIndexes(ctx, d, indexDefs, func() (*PlanResult, error) {
		plan, err := runExplain(ctx, d, query, false, false)
		if plan != nil {
			cost = plan.TotalCost
		}
		planErr = err
		return plan, err
	})
	if err != nil {
		return 0, err
	}
	return cost, planErr
}

// ─── internal helpers ────────────────────────────────────────────────────────

func runExplain(ctx context.Context, d db.Querier, query string, analyze, generic bool) (*PlanResult, error) {
	opts := []string{"FORMAT JSON", "COSTS true"}
	if analyze {
		opts = append(opts, "ANALYZE true", "BUFFERS true")
	}
	if generic {
		opts = append(opts, "GENERIC_PLAN true")
	}

	explainSQL := fmt.Sprintf("EXPLAIN (%s) %s", strings.Join(opts, ", "), query)
	rows, err := d.InternalQuery(ctx, explainSQL)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("EXPLAIN returned no output")
	}

	// pgx returns the JSON column as a string (text type from PostgreSQL)
	raw := db.ToString(rows[0]["QUERY PLAN"])
	if raw == "" {
		// try lowercase key
		raw = db.ToString(rows[0]["query plan"])
	}

	// EXPLAIN (FORMAT JSON) returns a JSON array: [{Plan:{...}, Planning Time:..., ...}]
	var plans []map[string]any
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		return nil, fmt.Errorf("parse explain JSON: %w", err)
	}
	if len(plans) == 0 {
		return nil, fmt.Errorf("empty EXPLAIN output")
	}

	top := plans[0]
	planNode := top["Plan"]

	result := &PlanResult{Plan: planNode}

	if pt, ok := top["Planning Time"].(float64); ok {
		result.PlanningTime = pt
	}
	if et, ok := top["Execution Time"].(float64); ok {
		result.ExecTime = et
	}

	// Extract total cost from top-level plan node
	if planMap, ok := planNode.(map[string]any); ok {
		if tc, ok := planMap["Total Cost"].(float64); ok {
			result.TotalCost = tc
		}
	}

	return result, nil
}

// withHypotheticalIndexes creates hypothetical indexes via HypoPG, runs fn,
// then resets all hypothetical indexes regardless of fn's outcome.
func withHypotheticalIndexes(ctx context.Context, d db.Querier, defs []string, fn func() (*PlanResult, error)) (*PlanResult, error) {
	// Create each hypothetical index
	for _, def := range defs {
		_, err := d.InternalQuery(ctx, fmt.Sprintf("SELECT hypopg_create_index('%s')", escapeSingleQuotes(def)))
		if err != nil {
			d.InternalQuery(ctx, "SELECT hypopg_reset()") //nolint:errcheck
			return nil, fmt.Errorf("create hypothetical index %q: %w", def, err)
		}
	}

	result, fnErr := fn()

	// Always reset, even on error
	d.InternalQuery(ctx, "SELECT hypopg_reset()") //nolint:errcheck

	return result, fnErr
}

func escapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
