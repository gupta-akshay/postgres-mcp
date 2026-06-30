// Package dbtest provides a controllable in-memory implementation of db.Querier
// for use in unit tests across sub-packages.
package dbtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/gupta-akshay/postgres-mcp/internal/db"
)

// Compile-time check: *MockQuerier must satisfy db.Querier.
var _ db.Querier = (*MockQuerier)(nil)

type qCall struct {
	rows []map[string]any
	err  error
}

// MockQuerier is a controllable implementation of db.Querier.
// Responses are queued: each Add* call enqueues one response which is
// consumed (FIFO) by the corresponding method.  When the queue is empty,
// the method returns (nil, nil) / 0 / false as appropriate.
// All methods are goroutine-safe.
type MockQuerier struct {
	mu            sync.Mutex
	internalCalls []qCall
	queryCalls    []qCall
	versionVal    int
	versionErr    error
	restricted    bool
}

// NewMock returns a MockQuerier with default settings (PG 15, unrestricted).
func NewMock() *MockQuerier {
	return &MockQuerier{versionVal: 150000}
}

// AddInternalQuery enqueues one response for the next InternalQuery call.
func (m *MockQuerier) AddInternalQuery(rows []map[string]any, err error) *MockQuerier {
	m.internalCalls = append(m.internalCalls, qCall{rows, err})
	return m
}

// AddQueryRows enqueues one response for the next QueryRows call.
func (m *MockQuerier) AddQueryRows(rows []map[string]any, err error) *MockQuerier {
	m.queryCalls = append(m.queryCalls, qCall{rows, err})
	return m
}

// SetVersion configures the value returned by Version.
func (m *MockQuerier) SetVersion(v int) *MockQuerier {
	m.versionVal = v
	return m
}

// SetVersionErr configures an error to be returned by Version.
func (m *MockQuerier) SetVersionErr(err error) *MockQuerier {
	m.versionErr = err
	return m
}

// SetRestricted configures the value returned by IsRestricted.
func (m *MockQuerier) SetRestricted(r bool) *MockQuerier {
	m.restricted = r
	return m
}

// ExtensionInstalled returns a pre-built rows slice that simulates an installed extension.
func ExtensionInstalled(version string) []map[string]any {
	return []map[string]any{
		{"extversion": version, "installed": true, "available": true},
	}
}

// ExtensionAvailableOnly returns rows that simulate an available-but-not-installed extension.
func ExtensionAvailableOnly(version string) []map[string]any {
	return []map[string]any{
		{"extversion": version, "installed": false, "available": true},
	}
}

// ExplainJSON returns a rows slice that simulates EXPLAIN (FORMAT JSON) output.
func ExplainJSON(totalCost float64) []map[string]any {
	return []map[string]any{
		{"QUERY PLAN": `[{"Plan": {"Node Type": "Result", "Total Cost": ` +
			formatFloat(totalCost) + `, "Startup Cost": 0.0, "Plan Rows": 1, "Plan Width": 0}}]`},
	}
}

// ExplainJSONWithRelation returns EXPLAIN JSON that includes a relation (for DTA candidate extraction).
func ExplainJSONWithRelation(schema, table, filterCond string, totalCost float64) []map[string]any {
	plan := `[{"Plan": {"Node Type": "Seq Scan", "Schema": "` + schema +
		`", "Relation Name": "` + table +
		`", "Filter": "` + filterCond +
		`", "Total Cost": ` + formatFloat(totalCost) + `, "Startup Cost": 0.0, "Plan Rows": 100, "Plan Width": 8}}]`
	return []map[string]any{
		{"QUERY PLAN": plan},
	}
}

func formatFloat(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%d.0", int(f))
	}
	return fmt.Sprintf("%g", f)
}

// ── db.Querier implementation ─────────────────────────────────────────────────

func (m *MockQuerier) InternalQuery(_ context.Context, _ string, _ ...any) ([]map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.internalCalls) == 0 {
		return nil, nil
	}
	call := m.internalCalls[0]
	m.internalCalls = m.internalCalls[1:]
	return call.rows, call.err
}

func (m *MockQuerier) QueryRows(_ context.Context, _ string, _ ...any) ([]map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queryCalls) == 0 {
		return nil, nil
	}
	call := m.queryCalls[0]
	m.queryCalls = m.queryCalls[1:]
	return call.rows, call.err
}

func (m *MockQuerier) Execute(_ context.Context, _ string, _ ...any) error { return nil }

func (m *MockQuerier) WithConn(ctx context.Context, fn func(context.Context, db.Querier) error) error {
	return fn(ctx, m)
}

func (m *MockQuerier) Version(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versionVal, m.versionErr
}

func (m *MockQuerier) IsRestricted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restricted
}

func (m *MockQuerier) Close() {}
