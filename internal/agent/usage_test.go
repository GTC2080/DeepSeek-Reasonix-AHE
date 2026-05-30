package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestPrintUsage(t *testing.T) {
	u := &provider.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		TotalTokens:      1200,
		CacheHitTokens:   900,
		CacheMissTokens:  100,
	}

	var b strings.Builder
	printUsage(&b, u, nil)
	if out := b.String(); !strings.Contains(out, "1200 tok") || !strings.Contains(out, "900 cached / 100 new") {
		t.Errorf("usage line = %q (want 1200 tok and 900 cached / 100 new)", out)
	}

	// With pricing: 900*0.02 + 100*1 + 200*2 = 518 per 1M = 0.000518 -> "¥0.0005".
	var c strings.Builder
	printUsage(&c, u, &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"})
	if out := c.String(); !strings.Contains(out, "¥0.0005") {
		t.Errorf("cost line = %q (want ¥0.0005...)", out)
	}

	// nil or zero usage prints nothing.
	var empty strings.Builder
	printUsage(&empty, nil, nil)
	printUsage(&empty, &provider.Usage{}, nil)
	if empty.Len() != 0 {
		t.Errorf("nil/zero usage should print nothing, got %q", empty.String())
	}
}

// TestPrintUsageDerivesMissFromHit covers the OpenAI/MiMo shape where only the
// cached count is reported; the displayed "new" value comes from
// PromptTokens - CacheHitTokens. Verifies the absolute split doesn't show 0.
func TestPrintUsageDerivesMissFromHit(t *testing.T) {
	u := &provider.Usage{
		PromptTokens:     3540,
		CompletionTokens: 378,
		TotalTokens:      3918,
		CacheHitTokens:   1133,
		// CacheMissTokens deliberately 0 — provider only reported the hit
	}
	var b strings.Builder
	printUsage(&b, u, nil)
	if out := b.String(); !strings.Contains(out, "1133 cached / 2407 new") {
		t.Errorf("usage line = %q (want 1133 cached / 2407 new)", out)
	}
}
