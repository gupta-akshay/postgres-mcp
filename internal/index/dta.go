package index

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
	"github.com/gupta-akshay/postgres-mcp/internal/explain"
)

// ─── public entry points ─────────────────────────────────────────────────────

// AnalyzeWorkload fetches the top queries from pg_stat_statements and recommends
// indexes using the DTA greedy algorithm.
func AnalyzeWorkload(ctx context.Context, d db.Querier, cfg DTAConfig) (*AnalysisResult, error) {
	if err := db.RequireExtension(ctx, d, "pg_stat_statements"); err != nil {
		return nil, err
	}
	if err := db.RequireExtension(ctx, d, "hypopg"); err != nil {
		return nil, err
	}

	version, err := d.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("get version: %w", err)
	}

	timeCol := "total_exec_time"
	if version < 130000 {
		timeCol = "total_time"
	}

	rows, err := d.InternalQuery(ctx, fmt.Sprintf(`
		SELECT query
		FROM pg_stat_statements
		WHERE %s > 0
		  AND query NOT LIKE $1
		ORDER BY %s DESC
		LIMIT $2
	`, timeCol, timeCol), "%/* postgres-mcp */%", cfg.WorkloadLimit)
	if err != nil {
		return nil, fmt.Errorf("load workload: %w", err)
	}

	queries := make([]string, 0, len(rows))
	for _, r := range rows {
		q := db.ToString(r["query"])
		if q != "" {
			queries = append(queries, q)
		}
	}

	return runDTA(ctx, d, queries, cfg)
}

// AnalyzeQueries recommends indexes for an explicit list of SQL queries.
func AnalyzeQueries(ctx context.Context, d db.Querier, queries []string, cfg DTAConfig) (*AnalysisResult, error) {
	if err := db.RequireExtension(ctx, d, "hypopg"); err != nil {
		return nil, err
	}
	return runDTA(ctx, d, queries, cfg)
}

// ─── core DTA algorithm ───────────────────────────────────────────────────────

type candidate struct {
	def       IndexDefinition
	createSQL string
}

func runDTA(ctx context.Context, d db.Querier, queries []string, cfg DTAConfig) (*AnalysisResult, error) {
	if len(queries) == 0 {
		return &AnalysisResult{}, nil
	}

	// 1. Filter to queries we can EXPLAIN without errors
	explainable := filterExplainable(ctx, d, queries)
	if len(explainable) == 0 {
		return &AnalysisResult{
			Summary: Summary{},
		}, nil
	}

	// 2. Baseline costs
	baseCosts := computeCosts(ctx, d, explainable, nil)
	totalBase := sumCosts(baseCosts)

	// 3. Generate candidate indexes from query plans
	candidates := generateCandidates(ctx, d, explainable, cfg.MaxIndexWidth)
	// Remove candidates whose columns are already covered by existing indexes
	candidates = filterExistingIndexes(ctx, d, candidates)

	// 4. Greedy selection loop
	deadline := time.Now().Add(time.Duration(cfg.TimeLimitSeconds * float64(time.Second)))
	var selected []candidate
	var totalSizeMB float64

	for len(candidates) > 0 && time.Now().Before(deadline) {
		best, bestCost, bestSizeMB := findBest(ctx, d, explainable, selected, candidates, baseCosts, cfg)

		// Check minimum improvement
		currentCost := totalCostWith(ctx, d, explainable, selected)
		improvement := (currentCost - bestCost) / max64(currentCost, 1)
		if improvement < cfg.MinImprovementPct/100 {
			break
		}

		// Check budget
		if cfg.BudgetMB > 0 && totalSizeMB+bestSizeMB > cfg.BudgetMB {
			break
		}

		selected = append(selected, *best)
		totalSizeMB += bestSizeMB

		// Remove the selected candidate and any that are subsets of it
		candidates = removeSelected(candidates, *best)
	}

	// 5. Build result
	return buildResult(ctx, d, explainable, selected, baseCosts, totalBase)
}

// ─── candidate generation ────────────────────────────────────────────────────

func generateCandidates(ctx context.Context, d db.Querier, queries []string, maxWidth int) []candidate {
	// Map from "schema.table" to set of column names referenced in conditions
	tableColumns := make(map[string][]string)

	for _, q := range queries {
		planInfo := extractPlanInfo(ctx, d, q)
		for tableKey, cols := range planInfo {
			existing := tableColumns[tableKey]
			tableColumns[tableKey] = unionStrings(existing, cols)
		}
	}

	// Generate single- and multi-column index candidates
	var cands []candidate
	seen := make(map[string]bool)

	for tableKey, cols := range tableColumns {
		parts := strings.SplitN(tableKey, ".", 2)
		schema, table := "", tableKey
		if len(parts) == 2 {
			schema, table = parts[0], parts[1]
		}

		// Deduplicate columns
		cols = dedup(cols)

		// Generate combinations of width 1..maxWidth
		for width := 1; width <= min(maxWidth, len(cols)); width++ {
			for _, combo := range combinations(cols, width) {
				def := IndexDefinition{
					Schema:  schema,
					Table:   table,
					Columns: combo,
					Type:    "btree",
				}
				key := def.CreateSQL()
				if !seen[key] {
					seen[key] = true
					cands = append(cands, candidate{def: def, createSQL: key})
				}
			}
		}
	}
	return cands
}

// extractPlanInfo runs EXPLAIN JSON and walks the plan tree to find
// table names and columns referenced in filter/join/sort conditions.
func extractPlanInfo(ctx context.Context, d db.Querier, query string) map[string][]string {
	result := make(map[string][]string)

	rows, err := d.InternalQuery(ctx, "EXPLAIN (FORMAT JSON, COSTS true) "+query)
	if err != nil {
		return result
	}
	if len(rows) == 0 {
		return result
	}

	raw := db.ToString(rows[0]["QUERY PLAN"])
	if raw == "" {
		raw = db.ToString(rows[0]["query plan"])
	}

	var plans []map[string]any
	if err := json.Unmarshal([]byte(raw), &plans); err != nil || len(plans) == 0 {
		return result
	}

	if planNode, ok := plans[0]["Plan"].(map[string]any); ok {
		walkPlanNode(planNode, result, "")
	}
	return result
}

// planConditionFields are the EXPLAIN JSON fields that contain condition text.
var planConditionFields = []string{
	"Filter", "Index Cond", "Join Filter", "Hash Cond",
	"Recheck Cond", "One-Time Filter",
}

// columnPattern matches bare column names immediately before comparison operators.
var columnPattern = regexp.MustCompile(
	`\b([a-zA-Z_]\w*)` +
		`\s*(?:=|<>|!=|<=|>=|<|>|~~\*?|!~~\*?|IS\s|IS\s+NOT\s|IN\s*\(|LIKE\s|ILIKE\s|BETWEEN\s|@>|<@|@@)`)

// walkPlanNode is the public entry point; joinCols starts empty.
func walkPlanNode(node map[string]any, out map[string][]string, currentTable string) {
	walkPlanNodeInternal(node, out, currentTable, nil)
}

// walkPlanNodeInternal recurses through EXPLAIN JSON.  joinCols carries columns
// from ancestor join nodes that could not be attributed yet (no Relation Name at
// that level); they are attributed to the first child that resolves a relation.
func walkPlanNodeInternal(node map[string]any, out map[string][]string, currentTable string, joinCols []string) {
	hasRelation := false
	if rel, ok := node["Relation Name"].(string); ok {
		hasRelation = true
		schema, _ := node["Schema"].(string)
		if schema != "" {
			currentTable = schema + "." + rel
		} else {
			currentTable = rel
		}
		// Attribute join-condition columns collected at ancestor join nodes
		if len(joinCols) > 0 {
			out[currentTable] = append(out[currentTable], joinCols...)
		}
	}

	// Extract columns from condition strings
	var pendingCols []string
	for _, field := range planConditionFields {
		if cond, ok := node[field].(string); ok {
			cols := extractColumnsFromCondition(cond)
			if len(cols) > 0 {
				if currentTable != "" {
					out[currentTable] = append(out[currentTable], cols...)
				} else {
					// Join/intermediate node with no relation: save for child scans
					pendingCols = append(pendingCols, cols...)
				}
			}
		}
	}

	// Sort / Group keys
	for _, field := range []string{"Sort Key", "Group Key", "Presorted Key"} {
		if keys, ok := node[field].([]any); ok {
			for _, k := range keys {
				if s, ok := k.(string); ok {
					col := strings.Fields(s)[0] // strip DESC/ASC/NULLS
					col = strings.Trim(col, "()")
					if isValidColumn(col) && currentTable != "" {
						out[currentTable] = append(out[currentTable], col)
					}
				}
			}
		}
	}

	// Determine which columns to pass to child nodes.
	// Once a relation is found (hasRelation || currentTable != ""), pending
	// columns were already attributed; children inherit currentTable with no
	// extra joinCols.  For join nodes with no relation, pass the accumulated
	// pending set so the first child scan can attribute them.
	var childJoinCols []string
	if !hasRelation && currentTable == "" {
		childJoinCols = append(joinCols, pendingCols...)
	}

	// Recurse into sub-plans
	if plans, ok := node["Plans"].([]any); ok {
		for _, p := range plans {
			if pNode, ok := p.(map[string]any); ok {
				walkPlanNodeInternal(pNode, out, currentTable, childJoinCols)
			}
		}
	}
}

func extractColumnsFromCondition(cond string) []string {
	// Remove table-qualifier prefix (e.g. "t1.col" -> keep "col")
	// and the "(table.col = ...)" wrapper
	matches := columnPattern.FindAllStringSubmatch(cond, -1)
	var cols []string
	for _, m := range matches {
		if len(m) >= 2 {
			col := strings.ToLower(m[1])
			if isValidColumn(col) {
				cols = append(cols, col)
			}
		}
	}
	return cols
}

// isValidColumn rejects SQL keywords and very short tokens.
var sqlKeywords = map[string]bool{
	"and": true, "or": true, "not": true, "null": true, "true": true,
	"false": true, "is": true, "in": true, "any": true, "all": true,
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"select": true, "from": true, "where": true, "join": true, "on": true,
	"between": true, "like": true, "ilike": true, "exists": true,
}

func isValidColumn(s string) bool {
	if len(s) < 2 {
		return false
	}
	return !sqlKeywords[strings.ToLower(s)]
}

// ─── cost evaluation ─────────────────────────────────────────────────────────

func computeCosts(ctx context.Context, d db.Querier, queries []string, indexDefs []string) map[string]float64 {
	costs := make(map[string]float64, len(queries))
	if len(indexDefs) == 0 {
		for _, q := range queries {
			c, err := explain.GetQueryCost(ctx, d, q)
			if err == nil {
				costs[q] = c
			}
		}
		return costs
	}
	// with hypothetical indexes
	for _, q := range queries {
		c, err := explain.GetQueryCostWithIndexes(ctx, d, q, indexDefs)
		if err == nil {
			costs[q] = c
		}
	}
	return costs
}

func sumCosts(costs map[string]float64) float64 {
	var total float64
	for _, c := range costs {
		total += c
	}
	return total
}

func totalCostWith(ctx context.Context, d db.Querier, queries []string, sel []candidate) float64 {
	defs := collectDefs(sel)
	costs := computeCosts(ctx, d, queries, defs)
	return sumCosts(costs)
}

func collectDefs(sel []candidate) []string {
	defs := make([]string, len(sel))
	for i, c := range sel {
		defs[i] = c.createSQL
	}
	return defs
}

// findBest tests each remaining candidate (combined with already-selected indexes)
// and returns the one with the best objective score.
func findBest(
	ctx context.Context,
	d db.Querier,
	queries []string,
	selected []candidate,
	remaining []candidate,
	baseCosts map[string]float64,
	cfg DTAConfig,
) (*candidate, float64, float64) {
	selectedDefs := collectDefs(selected)

	bestScore := math.Inf(1)
	var bestCand *candidate
	bestCost := math.Inf(1)
	bestSizeMB := 0.0

	for i := range remaining {
		cand := &remaining[i]
		testDefs := append(selectedDefs, cand.createSQL)

		// Cost with this candidate added
		costs := computeCosts(ctx, d, queries, testDefs)
		totalCost := sumCosts(costs)

		// Estimate index size
		sizeMB := estimateIndexSizeMB(ctx, d, cand.def)

		// Objective: log(cost) + alpha * log(size)
		score := math.Log(math.Max(totalCost, 1)) +
			cfg.ParetoAlpha*math.Log(math.Max(sizeMB, 0.001))

		if score < bestScore {
			bestScore = score
			bestCand = cand
			bestCost = totalCost
			bestSizeMB = sizeMB
		}
	}
	return bestCand, bestCost, bestSizeMB
}

// estimateIndexSizeMB estimates the size of an index via HypoPG.  All three
// calls (create, size, reset) are pinned to one connection so the session-local
// hypothetical index is visible between calls.
func estimateIndexSizeMB(ctx context.Context, d db.Querier, def IndexDefinition) float64 {
	createSQL := def.CreateSQL()
	var sizeMB float64

	_ = d.WithConn(ctx, func(ctx context.Context, q db.Querier) error {
		// SELECT * expands the set-returning function so indexrelid is a named column.
		rows, err := q.InternalQuery(ctx,
			fmt.Sprintf("SELECT * FROM hypopg_create_index('%s')", escapeSQ(createSQL)))
		if err != nil || len(rows) == 0 {
			return nil // use fallback
		}

		indexoidF, _ := db.ToFloat64(rows[0]["indexrelid"])
		if indexoidF == 0 {
			// try any other positive numeric column as a fallback
			for _, v := range rows[0] {
				if f, err := db.ToFloat64(v); err == nil && f > 0 {
					indexoidF = f
					break
				}
			}
		}

		if indexoidF > 0 {
			sizeRows, err := q.InternalQuery(ctx,
				fmt.Sprintf("SELECT hypopg_relation_size(%d) AS sz", int64(indexoidF)))
			if err == nil && len(sizeRows) > 0 {
				sz, _ := db.ToFloat64(sizeRows[0]["sz"])
				sizeMB = sz / (1024 * 1024)
			}
		}

		q.InternalQuery(ctx, "SELECT hypopg_reset()") //nolint:errcheck
		return nil
	})

	if sizeMB == 0 {
		sizeMB = 1.0
	}
	return sizeMB
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func filterExplainable(ctx context.Context, d db.Querier, queries []string) []string {
	var out []string
	for _, q := range queries {
		_, err := explain.GetQueryCost(ctx, d, q)
		if err == nil {
			out = append(out, q)
		}
	}
	return out
}

func filterExistingIndexes(ctx context.Context, d db.Querier, cands []candidate) []candidate {
	// Get all existing index definitions
	rows, err := d.InternalQuery(ctx, `
		SELECT pg_get_indexdef(indexrelid) AS def
		FROM pg_stat_user_indexes
	`)
	if err != nil {
		return cands
	}
	existing := make(map[string]bool, len(rows))
	for _, r := range rows {
		existing[strings.ToLower(db.ToString(r["def"]))] = true
	}

	var out []candidate
	for _, c := range cands {
		// Quick check: see if a similar index already exists
		if !hasSimilarIndex(existing, c.def) {
			out = append(out, c)
		}
	}
	return out
}

func hasSimilarIndex(existing map[string]bool, def IndexDefinition) bool {
	table := strings.ToLower(def.Table)
	proposedCols := make([]string, len(def.Columns))
	for i, c := range def.Columns {
		proposedCols[i] = strings.ToLower(strings.TrimSpace(c))
	}

	for existDef := range existing {
		existLower := strings.ToLower(existDef)
		if !strings.Contains(existLower, table) {
			continue
		}
		// Extract the column list from "... ON table (col1, col2, ...)"
		existCols := extractIndexColumns(existLower)
		if len(existCols) == 0 {
			continue
		}
		// The proposed index is redundant only when its columns are a leading
		// prefix of the existing index (or an exact match). A single-column
		// proposal for (status) is NOT covered by (tenant_id, status) because
		// the existing index cannot satisfy standalone queries on status alone.
		if len(proposedCols) > len(existCols) {
			continue
		}
		isPrefix := true
		for i, c := range proposedCols {
			if strings.TrimSpace(existCols[i]) != c {
				isPrefix = false
				break
			}
		}
		if isPrefix {
			return true
		}
	}
	return false
}

// extractIndexColumns parses the column list from a lowercased index definition
// string of the form "... on table (col1, col2, ...)".
func extractIndexColumns(def string) []string {
	start := strings.LastIndex(def, "(")
	end := strings.LastIndex(def, ")")
	if start < 0 || end <= start {
		return nil
	}
	colStr := def[start+1 : end]
	parts := strings.Split(colStr, ",")
	cols := make([]string, len(parts))
	for i, p := range parts {
		cols[i] = strings.TrimSpace(p)
	}
	return cols
}

func removeSelected(cands []candidate, sel candidate) []candidate {
	var out []candidate
	for _, c := range cands {
		if c.createSQL != sel.createSQL {
			out = append(out, c)
		}
	}
	return out
}

func buildResult(
	ctx context.Context,
	d db.Querier,
	queries []string,
	selected []candidate,
	baseCosts map[string]float64,
	totalBase float64,
) (*AnalysisResult, error) {
	if len(selected) == 0 {
		return &AnalysisResult{
			Summary: Summary{
				BaseCost:          totalBase,
				NewCost:           totalBase,
				ImprovementFactor: 1.0,
			},
		}, nil
	}

	allDefs := collectDefs(selected)
	finalCosts := computeCosts(ctx, d, queries, allDefs)
	totalFinal := sumCosts(finalCosts)

	recs := make([]Recommendation, 0, len(selected))
	var sizeMB float64
	for _, sel := range selected {
		sz := estimateIndexSizeMB(ctx, d, sel.def)
		sizeMB += sz
		imp := 1.0
		if totalFinal > 0 {
			imp = totalBase / math.Max(totalFinal, 0.001)
		}
		recs = append(recs, Recommendation{
			Index:             sel.def,
			Definition:        sel.createSQL,
			EstimatedSizeMB:   sz,
			ImprovementFactor: imp,
			CostReduction:     totalBase - totalFinal,
		})
	}

	impacts := make([]QueryImpact, 0, len(queries))
	for _, q := range queries {
		base := baseCosts[q]
		final := finalCosts[q]
		imp := 1.0
		if final > 0 {
			imp = base / math.Max(final, 0.001)
		}
		if base != final {
			impacts = append(impacts, QueryImpact{
				Query:             q,
				OriginalCost:      base,
				OptimizedCost:     final,
				ImprovementFactor: imp,
			})
		}
	}

	impFactor := 1.0
	if totalFinal > 0 {
		impFactor = totalBase / math.Max(totalFinal, 0.001)
	}

	return &AnalysisResult{
		Summary: Summary{
			RecommendationCount: len(recs),
			BaseCost:            totalBase,
			NewCost:             totalFinal,
			ImprovementFactor:   impFactor,
			TotalIndexSizeMB:    sizeMB,
		},
		Recommendations: recs,
		QueryImpact:     impacts,
	}, nil
}

// ─── combinatorics helpers ────────────────────────────────────────────────────

// combinations returns all ordered subsets of cols of exactly length n.
func combinations(cols []string, n int) [][]string {
	if n == 0 || n > len(cols) {
		return nil
	}
	if n == 1 {
		result := make([][]string, len(cols))
		for i, c := range cols {
			result[i] = []string{c}
		}
		return result
	}
	var result [][]string
	for i, c := range cols {
		for _, rest := range combinations(cols[i+1:], n-1) {
			combo := make([]string, 0, n)
			combo = append(combo, c)
			combo = append(combo, rest...)
			result = append(result, combo)
		}
	}
	return result
}

func dedup(ss []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range ss {
		l := strings.ToLower(s)
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool)
	for _, s := range a {
		seen[strings.ToLower(s)] = true
	}
	out := append([]string{}, a...)
	for _, s := range b {
		l := strings.ToLower(s)
		if !seen[l] {
			seen[l] = true
			out = append(out, s)
		}
	}
	return out
}

func escapeSQ(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
