package index

import (
	"fmt"
	"strings"
)

// IndexDefinition represents a candidate or recommended index.
type IndexDefinition struct {
	Schema  string
	Table   string
	Columns []string
	Type    string // btree (default), hash, gin, gist, etc.
}

// Name returns a deterministic index name.
func (d IndexDefinition) Name() string {
	cols := strings.Join(d.Columns, "_")
	// sanitise for use as identifier
	cols = strings.NewReplacer(" ", "_", "(", "", ")", "").Replace(cols)
	table := d.Table
	if idx := strings.LastIndex(table, "."); idx >= 0 {
		table = table[idx+1:]
	}
	name := fmt.Sprintf("pgmcp_%s_%s_idx", table, cols)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// CreateSQL returns the CREATE INDEX statement.
func (d IndexDefinition) CreateSQL() string {
	qualified := d.Table
	if d.Schema != "" && !strings.Contains(d.Table, ".") {
		qualified = d.Schema + "." + d.Table
	}
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s)",
		d.Name(), qualified, strings.Join(d.Columns, ", "))
}

// Recommendation holds an index recommendation with its impact metrics.
type Recommendation struct {
	Index             IndexDefinition `json:"index"`
	Definition        string          `json:"definition"`
	EstimatedSizeMB   float64         `json:"estimated_size_mb"`
	ImprovementFactor float64         `json:"improvement_factor"`
	CostReduction     float64         `json:"cost_reduction"`
	Warnings          []string        `json:"warnings,omitempty"`
}

// AnalysisResult is the top-level response for index analysis tools.
type AnalysisResult struct {
	Summary         Summary          `json:"summary"`
	Recommendations []Recommendation `json:"recommendations"`
	QueryImpact     []QueryImpact    `json:"query_impact"`
}

// Summary holds aggregate statistics about the analysis.
type Summary struct {
	RecommendationCount int     `json:"recommendation_count"`
	BaseCost            float64 `json:"base_cost"`
	NewCost             float64 `json:"new_cost"`
	ImprovementFactor   float64 `json:"improvement_factor"`
	TotalIndexSizeMB    float64 `json:"total_index_size_mb"`
}

// QueryImpact shows the per-query effect of the recommended indexes.
type QueryImpact struct {
	Query             string  `json:"query"`
	OriginalCost      float64 `json:"original_cost"`
	OptimizedCost     float64 `json:"optimized_cost"`
	ImprovementFactor float64 `json:"improvement_factor"`
}

// DTAConfig controls the behaviour of the greedy search algorithm.
type DTAConfig struct {
	// MaxIndexWidth is the maximum number of columns in a candidate index.
	MaxIndexWidth int
	// MinImprovementPct is the minimum cost reduction (%) to accept a candidate.
	MinImprovementPct float64
	// BudgetMB limits the total size of recommended indexes (-1 = unlimited).
	BudgetMB float64
	// TimeLimitSeconds caps the total analysis time.
	TimeLimitSeconds float64
	// ParetoAlpha controls the space-vs-performance trade-off in the objective
	// function: score = log(cost) + alpha * log(size_mb).
	ParetoAlpha float64
	// WorkloadLimit is the maximum number of queries taken from pg_stat_statements.
	WorkloadLimit int
}

// DefaultDTAConfig returns sensible defaults matching the Python reference.
func DefaultDTAConfig() DTAConfig {
	return DTAConfig{
		MaxIndexWidth:     4,
		MinImprovementPct: 5.0,
		BudgetMB:          -1,
		TimeLimitSeconds:  30,
		ParetoAlpha:       0.1,
		WorkloadLimit:     50,
	}
}
