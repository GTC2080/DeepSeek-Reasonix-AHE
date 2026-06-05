// Package cachecontract captures and compares the cache-stable request prefix
// used to preserve provider-side prompt cache reuse across a session.
package cachecontract

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"reasonix/internal/provider"
)

const Version = "cache_contract.v0.1"

// Shape is a point-in-time snapshot of the cache-stable prefix. The rewrite
// version and token estimate are diagnostics; Compare intentionally ignores
// rewrite-only changes because compaction does not alter the stable prefix.
type Shape struct {
	SystemPromptHash  string `json:"system_prompt_hash"`
	ToolSchemaHash    string `json:"tool_schema_hash"`
	StablePrefixHash  string `json:"stable_prefix_hash"`
	LogRewriteVersion int    `json:"log_rewrite_version,omitempty"`
	ToolSchemaTokens  int    `json:"tool_schema_tokens,omitempty"`
}

// Contract is the session baseline the agent checks before each model call.
type Contract struct {
	Version                 string    `json:"version"`
	SessionID               string    `json:"session_id"`
	SystemPromptHash        string    `json:"system_prompt_hash"`
	ToolSchemaHash          string    `json:"tool_schema_hash"`
	StablePrefixHash        string    `json:"stable_prefix_hash"`
	HarnessSnapshot         string    `json:"harness_snapshot,omitempty"`
	HarnessStablePrefixHash string    `json:"harness_stable_prefix_hash,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
}

// Violation describes a current shape that no longer matches the baseline.
type Violation struct {
	SessionID string   `json:"session_id"`
	Expected  Shape    `json:"expected"`
	Actual    Shape    `json:"actual"`
	Reasons   []string `json:"reasons"`
}

// Capture takes a deterministic snapshot of the provider request prefix.
func Capture(systemPrompt string, schemas []provider.ToolSchema, rewriteVersion int) Shape {
	normalizedSchemas := normalizeToolSchemas(schemas)
	toolsJSON, _ := json.Marshal(normalizedSchemas)
	return Shape{
		SystemPromptHash: hashValue(systemPrompt),
		ToolSchemaHash:   hashValue(normalizedSchemas),
		StablePrefixHash: hashValue(prefixMaterial{
			SystemPrompt: systemPrompt,
			ToolSchemas:  normalizedSchemas,
		}),
		LogRewriteVersion: rewriteVersion,
		ToolSchemaTokens:  EstimateTokens(string(toolsJSON)),
	}
}

// NewContract creates the session baseline from the first captured shape.
func NewContract(sessionID string, shape Shape, createdAt time.Time) Contract {
	return NewContractWithHarness(sessionID, shape, createdAt, "", "")
}

// NewContractWithHarness creates a session baseline and records the active
// harness snapshot identity, when one was loaded for this session.
func NewContractWithHarness(sessionID string, shape Shape, createdAt time.Time, harnessSnapshot, harnessStablePrefixHash string) Contract {
	return Contract{
		Version:                 Version,
		SessionID:               sessionID,
		SystemPromptHash:        shape.SystemPromptHash,
		ToolSchemaHash:          shape.ToolSchemaHash,
		StablePrefixHash:        shape.StablePrefixHash,
		HarnessSnapshot:         harnessSnapshot,
		HarnessStablePrefixHash: harnessStablePrefixHash,
		CreatedAt:               createdAt,
	}
}

// Compare returns a violation when the current shape no longer matches the
// contract's stable prefix fields.
func Compare(contract Contract, actual Shape) (Violation, bool) {
	expected := contract.Shape()
	reasons := []string{}
	if contract.SystemPromptHash != "" && contract.SystemPromptHash != actual.SystemPromptHash {
		reasons = append(reasons, "system_prompt_hash")
	}
	if contract.ToolSchemaHash != "" && contract.ToolSchemaHash != actual.ToolSchemaHash {
		reasons = append(reasons, "tool_schema_hash")
	}
	if len(reasons) == 0 && contract.StablePrefixHash != "" && contract.StablePrefixHash != actual.StablePrefixHash {
		reasons = append(reasons, "stable_prefix_hash")
	}
	if len(reasons) == 0 {
		return Violation{}, false
	}
	return Violation{
		SessionID: contract.SessionID,
		Expected:  expected,
		Actual:    actual,
		Reasons:   reasons,
	}, true
}

// Shape returns the comparable shape encoded in the contract.
func (c Contract) Shape() Shape {
	return Shape{
		SystemPromptHash: c.SystemPromptHash,
		ToolSchemaHash:   c.ToolSchemaHash,
		StablePrefixHash: c.StablePrefixHash,
	}
}

// EstimateTokens gives a rough token count from byte length.
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return len(s) / 4
}

// NewSessionID returns an opaque local session identifier for cache contracts.
func NewSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "cache-session-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("cache-session-%d", time.Now().UnixNano())
}

type prefixMaterial struct {
	SystemPrompt string                `json:"system_prompt"`
	ToolSchemas  []provider.ToolSchema `json:"tool_schemas"`
}

func normalizeToolSchemas(schemas []provider.ToolSchema) []provider.ToolSchema {
	out := make([]provider.ToolSchema, len(schemas))
	for i, schema := range schemas {
		out[i] = provider.ToolSchema{
			Name:        schema.Name,
			Description: schema.Description,
			Parameters:  provider.CanonicalizeSchema(schema.Parameters),
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Description != out[j].Description {
			return out[i].Description < out[j].Description
		}
		return string(out[i].Parameters) < string(out[j].Parameters)
	})
	return out
}

func hashValue(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
