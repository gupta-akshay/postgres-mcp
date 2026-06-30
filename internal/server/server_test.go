package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gupta-akshay/postgres-mcp/internal/index"
)

// makeRequest builds a CallToolRequest with the given arguments map.
func makeRequest(arguments map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = arguments
	return req
}

// ─── args ─────────────────────────────────────────────────────────────────────

func TestArgs_MapType(t *testing.T) {
	m := map[string]any{"key": "value"}
	req := makeRequest(m)
	got := args(req)
	assert.Equal(t, m, got)
}

func TestArgs_NilArguments(t *testing.T) {
	var req mcp.CallToolRequest
	// Params.Arguments is nil (zero value of any)
	assert.Nil(t, args(req))
}

func TestArgs_WrongType(t *testing.T) {
	var req mcp.CallToolRequest
	req.Params.Arguments = "this is not a map"
	assert.Nil(t, args(req))
}

// ─── getString ────────────────────────────────────────────────────────────────

func TestGetString(t *testing.T) {
	req := makeRequest(map[string]any{"schema": "public", "num": 42})
	assert.Equal(t, "public", getString(req, "schema"))
	assert.Equal(t, "", getString(req, "missing"))
	assert.Equal(t, "", getString(req, "num")) // wrong type → empty string
}

func TestGetString_NilArgs(t *testing.T) {
	var req mcp.CallToolRequest
	assert.Equal(t, "", getString(req, "key"))
}

// ─── getBool ─────────────────────────────────────────────────────────────────

func TestGetBool(t *testing.T) {
	req := makeRequest(map[string]any{"flag": true, "other": false})
	assert.True(t, getBool(req, "flag"))
	assert.False(t, getBool(req, "other"))
	assert.False(t, getBool(req, "missing"))
}

func TestGetBool_NilArgs(t *testing.T) {
	var req mcp.CallToolRequest
	req.Params.Arguments = "not a map"
	assert.False(t, getBool(req, "key"))
}

// ─── getInt ───────────────────────────────────────────────────────────────────

func TestGetInt(t *testing.T) {
	req := makeRequest(map[string]any{"limit": float64(10), "zero": float64(0)})
	assert.Equal(t, 10, getInt(req, "limit", 5))
	assert.Equal(t, 0, getInt(req, "zero", 5))
	assert.Equal(t, 99, getInt(req, "missing", 99)) // uses default
}

func TestGetInt_NilArgs(t *testing.T) {
	var req mcp.CallToolRequest
	req.Params.Arguments = "not a map"
	assert.Equal(t, 42, getInt(req, "key", 42))
}

// ─── getFloat ─────────────────────────────────────────────────────────────────

func TestGetFloat(t *testing.T) {
	req := makeRequest(map[string]any{"alpha": float64(0.5)})
	assert.InDelta(t, 0.5, getFloat(req, "alpha", 0.1), 0.001)
	assert.InDelta(t, 99.9, getFloat(req, "missing", 99.9), 0.001)
}

func TestGetFloat_NilArgs(t *testing.T) {
	var req mcp.CallToolRequest
	req.Params.Arguments = "not a map"
	assert.InDelta(t, 3.14, getFloat(req, "key", 3.14), 0.001)
}

// ─── getStringArray ───────────────────────────────────────────────────────────

func TestGetStringArray(t *testing.T) {
	req := makeRequest(map[string]any{
		"checks": []any{"index", "vacuum", "buffer"},
	})
	got := getStringArray(req, "checks")
	assert.Equal(t, []string{"index", "vacuum", "buffer"}, got)
}

func TestGetStringArray_MissingKey(t *testing.T) {
	req := makeRequest(map[string]any{})
	assert.Nil(t, getStringArray(req, "checks"))
}

func TestGetStringArray_WrongType(t *testing.T) {
	req := makeRequest(map[string]any{"checks": "not an array"})
	assert.Nil(t, getStringArray(req, "checks"))
}

func TestGetStringArray_Empty(t *testing.T) {
	req := makeRequest(map[string]any{"checks": []any{}})
	got := getStringArray(req, "checks")
	assert.Empty(t, got)
}

func TestGetStringArray_NonStringItems(t *testing.T) {
	// Mixed types: only string items should be included
	req := makeRequest(map[string]any{
		"items": []any{"valid", 42, true, "also-valid"},
	})
	got := getStringArray(req, "items")
	assert.Equal(t, []string{"valid", "also-valid"}, got)
}

func TestGetStringArray_NilArgs(t *testing.T) {
	var req mcp.CallToolRequest
	req.Params.Arguments = "not a map" // args() returns nil
	assert.Nil(t, getStringArray(req, "checks"))
}

// ─── dtaConfigFromRequest ─────────────────────────────────────────────────────

func TestDTAConfigFromRequest_Defaults(t *testing.T) {
	req := makeRequest(map[string]any{})
	cfg := dtaConfigFromRequest(req)
	defaults := index.DefaultDTAConfig()

	assert.Equal(t, defaults.MaxIndexWidth, cfg.MaxIndexWidth)
	assert.Equal(t, defaults.MinImprovementPct, cfg.MinImprovementPct)
	assert.Equal(t, defaults.TimeLimitSeconds, cfg.TimeLimitSeconds)
	assert.Equal(t, defaults.WorkloadLimit, cfg.WorkloadLimit)
}

func TestDTAConfigFromRequest_Overrides(t *testing.T) {
	req := makeRequest(map[string]any{
		"time_limit_seconds":  float64(60),
		"max_index_width":     float64(2),
		"min_improvement_pct": float64(10),
		"budget_mb":           float64(500),
		"workload_limit":      float64(20),
	})
	cfg := dtaConfigFromRequest(req)

	assert.Equal(t, 60.0, cfg.TimeLimitSeconds)
	assert.Equal(t, 2, cfg.MaxIndexWidth)
	assert.Equal(t, 10.0, cfg.MinImprovementPct)
	assert.Equal(t, 500.0, cfg.BudgetMB)
	assert.Equal(t, 20, cfg.WorkloadLimit)
}

// ─── jsonResult ───────────────────────────────────────────────────────────────

func TestJSONResult_ValidData(t *testing.T) {
	data := map[string]any{"key": "value", "num": 42}
	result, err := jsonResult(data)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Result should contain exactly one text content block with valid JSON
	require.Len(t, result.Content, 1)
	textBlock, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "content should be TextContent")

	var parsed map[string]any
	err = json.Unmarshal([]byte(textBlock.Text), &parsed)
	require.NoError(t, err, "result text must be valid JSON")
	assert.Equal(t, "value", parsed["key"])
}

func TestJSONResult_Slice(t *testing.T) {
	data := []string{"a", "b", "c"}
	result, err := jsonResult(data)
	require.NoError(t, err)
	require.NotNil(t, result)

	textBlock, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var parsed []string
	require.NoError(t, json.Unmarshal([]byte(textBlock.Text), &parsed))
	assert.Equal(t, data, parsed)
}

func TestJSONResult_Nil(t *testing.T) {
	result, err := jsonResult(nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestJSONResult_MarshalError(t *testing.T) {
	// Channels cannot be marshaled to JSON — triggers the error path.
	result, err := jsonResult(make(chan int))
	require.NoError(t, err) // jsonResult swallows the error and returns a tool error
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	textBlock, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textBlock.Text, "marshal result")
}
