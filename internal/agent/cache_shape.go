package agent

import (
	"encoding/json"

	"reasonix/internal/cachecontract"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// PrefixShape hashes the portions of the request prefix that influence
// provider-side prompt-cache reuse. Comparing snapshots across turns
// lets us explain *why* a cache miss happened.
type PrefixShape struct {
	SystemHash        string
	ToolsHash         string
	PrefixHash        string
	LogRewriteVersion int
	ToolSchemaTokens  int
}

// CacheDiagnostics is a type alias for event.CacheDiagnostics so the agent
// can construct and compare diagnostics without importing event itself in
// every call site, while still assigning to event.Event.CacheDiagnostics.
type CacheDiagnostics = event.CacheDiagnostics

// CaptureShape takes a snapshot of the current prefix state.
func CaptureShape(systemPrompt string, schemas []provider.ToolSchema, rewriteVersion int) PrefixShape {
	shape := cachecontract.Capture(systemPrompt, schemas, rewriteVersion)
	return PrefixShape{
		SystemHash:        shape.SystemPromptHash,
		ToolsHash:         shape.ToolSchemaHash,
		PrefixHash:        shape.StablePrefixHash,
		LogRewriteVersion: shape.LogRewriteVersion,
		ToolSchemaTokens:  shape.ToolSchemaTokens,
	}
}

// CompareShape returns diagnostics describing what changed between two shapes.
func CompareShape(prev, cur PrefixShape, usage *provider.Usage) CacheDiagnostics {
	reasons := []string{}
	if prev.SystemHash != "" && prev.SystemHash != cur.SystemHash {
		reasons = append(reasons, "system")
	}
	if prev.ToolsHash != "" && prev.ToolsHash != cur.ToolsHash {
		reasons = append(reasons, "tools")
	}
	if prev.LogRewriteVersion != cur.LogRewriteVersion {
		reasons = append(reasons, "log_rewrite")
	}
	var miss, hit int
	if usage != nil {
		miss = usage.CacheMissTokens
		hit = usage.CacheHitTokens
	}
	return CacheDiagnostics{
		PrefixHash:          cur.PrefixHash,
		PrefixChanged:       len(reasons) > 0,
		PrefixChangeReasons: reasons,
		SystemHash:          cur.SystemHash,
		ToolsHash:           cur.ToolsHash,
		LogRewriteVersion:   cur.LogRewriteVersion,
		ToolSchemaTokens:    cur.ToolSchemaTokens,
		CacheMissTokens:     miss,
		CacheHitTokens:      hit,
	}
}

// estimateTokens gives a rough token count from byte length.
// A proper tokenizer would be more accurate, but for diagnostic
// purposes a byte-based estimate is sufficient and zero-alloc.
func estimateTokens(s string) int {
	return cachecontract.EstimateTokens(s)
}

// SchemaTokenCosts returns per-tool token cost estimates for display.
func SchemaTokenCosts(schemas []provider.ToolSchema) []ToolSchemaCost {
	out := make([]ToolSchemaCost, 0, len(schemas))
	for _, s := range schemas {
		b, _ := json.Marshal(s)
		out = append(out, ToolSchemaCost{Name: s.Name, Tokens: estimateTokens(string(b))})
	}
	return out
}

// ToolSchemaCost is a per-tool token cost estimate for diagnostic display.
type ToolSchemaCost struct {
	Name   string
	Tokens int
}
