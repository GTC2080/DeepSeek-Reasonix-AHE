package lab

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"reasonix/internal/trace"
)

// CacheGateOptions controls optional pass/fail evaluation for a cache report.
type CacheGateOptions struct {
	Enabled               bool
	MinHitRatio           float64
	MaxContractViolations int
}

// ReportTrace aggregates cache-relevant telemetry from one trace JSONL file.
func ReportTrace(path string) (CacheReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return CacheReport{}, fmt.Errorf("read trace: %w", err)
	}
	defer f.Close()

	var report CacheReport
	cacheStatsEvents := 0
	reader := bufio.NewReader(f)
	for lineNo := 1; ; lineNo++ {
		line, readErr := reader.ReadBytes('\n')
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			var ev trace.Event
			if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
				return report, fmt.Errorf("parse trace line %d: %w", lineNo, err)
			}
			applyTraceEvent(&report, ev, &cacheStatsEvents)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return report, fmt.Errorf("read trace line %d: %w", lineNo, readErr)
		}
	}

	total := report.PromptCacheHitTokens + report.PromptCacheMissTokens
	if total > 0 {
		report.CacheHitRatio = float64(report.PromptCacheHitTokens) / float64(total)
	}
	if cacheStatsEvents == 0 {
		report.Warnings = append(report.Warnings, "no cache_stats events found")
	}
	return report, nil
}

// AnalyzeTrace preserves the P4 eval-runner helper shape while delegating to
// the stricter P5 report API.
func AnalyzeTrace(path string) (CacheReport, string) {
	report, err := ReportTrace(path)
	if err != nil {
		return report, err.Error()
	}
	if len(report.Warnings) > 0 {
		return report, strings.Join(report.Warnings, "; ")
	}
	return report, ""
}

func applyTraceEvent(report *CacheReport, ev trace.Event, cacheStatsEvents *int) {
	switch ev.Type {
	case "session_start":
		if report.HarnessSnapshot == "" {
			report.HarnessSnapshot = stringData(ev.Data, "harness_snapshot")
		}
	case "model_response":
		report.ModelCalls++
	case "cache_stats":
		*cacheStatsEvents = *cacheStatsEvents + 1
		report.PromptCacheHitTokens += int64Data(ev.Data, "prompt_cache_hit_tokens")
		report.PromptCacheMissTokens += int64Data(ev.Data, "prompt_cache_miss_tokens")
		reasons := stringSliceData(ev.Data, "prefix_change_reasons")
		if boolData(ev.Data, "prefix_changed") || len(reasons) > 0 {
			report.StablePrefixHashDrift = true
			report.StablePrefixHashDriftReasons = appendUnique(report.StablePrefixHashDriftReasons, reasons...)
		}
	case "cache_contract_violation":
		report.ContractViolations++
		report.ContractViolationReasons = appendUnique(report.ContractViolationReasons, stringSliceData(ev.Data, "reasons")...)
		if report.HarnessSnapshot == "" {
			report.HarnessSnapshot = stringData(ev.Data, "harness_snapshot")
		}
	case "middleware_policy_decision":
		report.MiddlewarePolicyDecisions++
		report.MiddlewarePolicyIDs = appendUnique(report.MiddlewarePolicyIDs, stringData(ev.Data, "policy_id"))
		if report.HarnessSnapshot == "" {
			report.HarnessSnapshot = stringData(ev.Data, "harness_snapshot")
		}
	}
}

// FormatCacheReport returns the default human-readable cache report.
func FormatCacheReport(report CacheReport) string {
	var b strings.Builder
	b.WriteString("Reasonix-AHE Cache Report\n\n")
	reportLine(&b, "Model calls", strconv.Itoa(report.ModelCalls))
	reportLine(&b, "Harness snapshot", displayReportValue(report.HarnessSnapshot))
	reportLine(&b, "Prompt cache hit tokens", formatInt64(report.PromptCacheHitTokens))
	reportLine(&b, "Prompt cache miss tokens", formatInt64(report.PromptCacheMissTokens))
	reportLine(&b, "Cache hit ratio", fmt.Sprintf("%.2f%%", report.CacheHitRatio*100))
	reportLine(&b, "Stable prefix hash drift", yesNo(report.StablePrefixHashDrift))
	if len(report.StablePrefixHashDriftReasons) > 0 {
		reportLine(&b, "Prefix drift reasons", strings.Join(report.StablePrefixHashDriftReasons, ", "))
	}
	reportLine(&b, "Contract violations", strconv.Itoa(report.ContractViolations))
	if len(report.ContractViolationReasons) > 0 {
		reportLine(&b, "Violation reasons", strings.Join(report.ContractViolationReasons, ", "))
	}
	reportLine(&b, "Middleware policy decisions", strconv.Itoa(report.MiddlewarePolicyDecisions))
	if len(report.MiddlewarePolicyIDs) > 0 {
		reportLine(&b, "Middleware policy ids", strings.Join(report.MiddlewarePolicyIDs, ", "))
	}
	if len(report.Warnings) > 0 {
		reportLine(&b, "Warnings", strings.Join(report.Warnings, "; "))
	}
	return b.String()
}

func reportLine(b *strings.Builder, label, value string) {
	fmt.Fprintf(b, "%-27s %s\n", label+":", value)
}

func displayReportValue(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func formatInt64(v int64) string {
	raw := strconv.FormatInt(v, 10)
	if len(raw) <= 3 {
		return raw
	}
	var b strings.Builder
	pre := len(raw) % 3
	if pre == 0 {
		pre = 3
	}
	b.WriteString(raw[:pre])
	for i := pre; i < len(raw); i += 3 {
		b.WriteByte(',')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

// EvaluateCacheGate returns failure reasons for an explicitly enabled cache gate.
func EvaluateCacheGate(report CacheReport, opts CacheGateOptions) []string {
	if !opts.Enabled {
		return nil
	}
	var failures []string
	if report.ContractViolations > opts.MaxContractViolations {
		failures = append(failures, fmt.Sprintf("contract violations %d exceed %d", report.ContractViolations, opts.MaxContractViolations))
	}
	if opts.MinHitRatio > 0 && report.CacheHitRatio < opts.MinHitRatio {
		failures = append(failures, fmt.Sprintf("cache hit ratio %.4f below %.4f", report.CacheHitRatio, opts.MinHitRatio))
	}
	return failures
}

func boolData(data map[string]any, key string) bool {
	v, ok := data[key].(bool)
	return ok && v
}

func stringSliceData(data map[string]any, key string) []string {
	switch v := data[key].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, v := range values {
		seen[v] = true
	}
	for _, v := range additions {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		values = append(values, v)
	}
	return values
}
