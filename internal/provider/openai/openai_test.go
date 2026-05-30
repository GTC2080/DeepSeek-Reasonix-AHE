package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// TestIsRetryableStatus covers the boundary: 408/429/5xx retry, other 4xx don't.
func TestIsRetryableStatus(t *testing.T) {
	retry := []int{408, 429, 500, 502, 503, 504, 599}
	noRetry := []int{200, 400, 401, 403, 404, 422}
	for _, s := range retry {
		if !isRetryableStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	for _, s := range noRetry {
		if isRetryableStatus(s) {
			t.Errorf("status %d should not be retryable", s)
		}
	}
}

// TestIsTransientErr keeps user-intent errors (ctx cancel / deadline) out of
// the retry path while letting network-level failures through.
func TestIsTransientErr(t *testing.T) {
	if isTransientErr(nil) {
		t.Error("nil error should not be transient")
	}
	if isTransientErr(context.Canceled) {
		t.Error("ctx canceled should not be transient")
	}
	if isTransientErr(context.DeadlineExceeded) {
		t.Error("ctx deadline should not be transient")
	}
	if !isTransientErr(errors.New("connection reset")) {
		t.Error("generic network-ish error should be transient")
	}
}

// TestStreamAuthError verifies a 401 surfaces as an actionable *provider.AuthError
// (naming the provider and its key env var) rather than a raw status body.
func TestStreamAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Authentication Fails, Your api key: ****ae54 is invalid"}}`))
	}))
	defer srv.Close()

	p, err := New(provider.Config{
		Name:    "deepseek",
		BaseURL: srv.URL,
		Model:   "deepseek-v4",
		APIKey:  "bad",
		Extra:   map[string]any{"api_key_env": "DEEPSEEK_API_KEY"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = p.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	var authErr *provider.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("want *provider.AuthError, got %T: %v", err, err)
	}
	if authErr.Provider != "deepseek" || authErr.KeyEnv != "DEEPSEEK_API_KEY" || authErr.Status != 401 {
		t.Errorf("AuthError fields wrong: %+v", authErr)
	}
	if msg := authErr.Error(); !strings.Contains(msg, "DEEPSEEK_API_KEY") || strings.Contains(msg, "ae54") {
		t.Errorf("message should name the env var and not dump the raw body: %q", msg)
	}
}

// TestNormaliseUsageDeepSeekShape covers DeepSeek's top-level cache fields.
func TestNormaliseUsageDeepSeekShape(t *testing.T) {
	u := normaliseUsage(&wireUsage{
		PromptTokens:          1000,
		CompletionTokens:      200,
		TotalTokens:           1200,
		PromptCacheHitTokens:  900,
		PromptCacheMissTokens: 100,
	})
	if u.CacheHitTokens != 900 || u.CacheMissTokens != 100 {
		t.Errorf("DeepSeek-shape cache fields lost: hit=%d miss=%d", u.CacheHitTokens, u.CacheMissTokens)
	}
}

// TestNormaliseUsageMiMoShape covers the nested prompt_tokens_details /
// completion_tokens_details path used by OpenAI and MiMo. Miss is derived
// from prompt - hit when only hit is provided.
func TestNormaliseUsageMiMoShape(t *testing.T) {
	u := normaliseUsage(&wireUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
		PromptTokensDetails: &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: 600},
		CompletionTokensDetails: &struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		}{ReasoningTokens: 180},
	})
	if u.CacheHitTokens != 600 || u.CacheMissTokens != 400 {
		t.Errorf("nested cache normalisation wrong: hit=%d miss=%d (want 600 / 400)", u.CacheHitTokens, u.CacheMissTokens)
	}
	if u.ReasoningTokens != 180 {
		t.Errorf("reasoning tokens lost: %d", u.ReasoningTokens)
	}
}
