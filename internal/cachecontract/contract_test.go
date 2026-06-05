package cachecontract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func TestCaptureStableAcrossEquivalentToolSchemas(t *testing.T) {
	firstSchemas := []provider.ToolSchema{
		{
			Name:        "write_file",
			Description: "write a file",
			Parameters:  json.RawMessage(`{"type":"object","required":["path","content"],"properties":{"content":{"type":"string"},"path":{"type":"string"}}}`),
		},
		{
			Name:        "read_file",
			Description: "read a file",
			Parameters:  json.RawMessage(`{"properties":{"path":{"type":"string"}},"type":"object"}`),
		},
	}
	secondSchemas := []provider.ToolSchema{
		{
			Name:        "read_file",
			Description: "read a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		},
		{
			Name:        "write_file",
			Description: "write a file",
			Parameters:  json.RawMessage(`{"properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["content","path"],"type":"object"}`),
		},
	}

	first := Capture("system", firstSchemas, 1)
	second := Capture("system", secondSchemas, 99)

	for _, hash := range []string{first.SystemPromptHash, first.ToolSchemaHash, first.StablePrefixHash} {
		if !strings.HasPrefix(hash, "sha256:") {
			t.Fatalf("hash %q should use sha256:<hex>", hash)
		}
	}
	if first.ToolSchemaHash != second.ToolSchemaHash {
		t.Fatalf("tool schema hash should be stable: %q != %q", first.ToolSchemaHash, second.ToolSchemaHash)
	}
	if first.StablePrefixHash != second.StablePrefixHash {
		t.Fatalf("stable prefix hash should be stable: %q != %q", first.StablePrefixHash, second.StablePrefixHash)
	}
	if first.ToolSchemaTokens <= 0 {
		t.Fatalf("tool schema token estimate should be populated: %+v", first)
	}
	if firstSchemas[0].Name != "write_file" || firstSchemas[1].Name != "read_file" {
		t.Fatalf("Capture mutated caller schema order: got [%s %s]", firstSchemas[0].Name, firstSchemas[1].Name)
	}
}

func TestCompareDetectsSystemPromptDrift(t *testing.T) {
	base := Capture("stable system", nil, 1)
	contract := NewContract("sess-1", base, time.Unix(123, 0).UTC())
	actual := Capture("changed system", nil, 1)

	violation, ok := Compare(contract, actual)
	if !ok {
		t.Fatalf("expected system prompt drift")
	}
	if !reflect.DeepEqual(violation.Reasons, []string{"system_prompt_hash"}) {
		t.Fatalf("reasons = %v, want [system_prompt_hash]", violation.Reasons)
	}
	if violation.Expected.SystemPromptHash != base.SystemPromptHash {
		t.Fatalf("expected shape did not come from contract: %+v", violation.Expected)
	}
	if violation.Actual.SystemPromptHash != actual.SystemPromptHash {
		t.Fatalf("actual shape did not come from current capture: %+v", violation.Actual)
	}
}

func TestCompareDetectsToolSchemaDrift(t *testing.T) {
	base := Capture("system", []provider.ToolSchema{{
		Name:        "read_file",
		Description: "read",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}, 1)
	contract := NewContract("sess-1", base, time.Unix(123, 0).UTC())
	actual := Capture("system", []provider.ToolSchema{{
		Name:        "read_file",
		Description: "read",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, {
		Name:        "write_file",
		Description: "write",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}, 1)

	violation, ok := Compare(contract, actual)
	if !ok {
		t.Fatalf("expected tool schema drift")
	}
	if !reflect.DeepEqual(violation.Reasons, []string{"tool_schema_hash"}) {
		t.Fatalf("reasons = %v, want [tool_schema_hash]", violation.Reasons)
	}
}

func TestCompareIgnoresNonContractRewriteVersion(t *testing.T) {
	base := Capture("system", nil, 1)
	contract := NewContract("sess-1", base, time.Unix(123, 0).UTC())
	actual := base
	actual.LogRewriteVersion = 2

	if violation, ok := Compare(contract, actual); ok {
		t.Fatalf("rewrite-only change should not violate the contract: %+v", violation)
	}
}
