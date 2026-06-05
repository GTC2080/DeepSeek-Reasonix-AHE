package trace

import (
	"regexp"
	"unicode/utf8"
)

const PreviewBytes = 2048

var (
	jsonSecretValueRE = regexp.MustCompile(`(?i)(\"(?:api[_-]?key|apikey|token|authorization|password|secret)\"\s*:\s*)(\"(?:\\.|[^\"\\])*\"|[^,\}\s]+)`)
	bareSecretValueRE = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API_KEY|TOKEN|PASSWORD|SECRET|AUTHORIZATION)\b\s*=\s*)[^\s]+`)
	bearerRE          = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/\-=]+`)
	skRE              = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
)

// RedactString removes common secret values from trace text. Redaction is always
// applied, including full mode.
func RedactString(s string) string {
	s = jsonSecretValueRE.ReplaceAllString(s, `${1}"[REDACTED]"`)
	s = bareSecretValueRE.ReplaceAllString(s, `${1}[REDACTED]`)
	s = bearerRE.ReplaceAllString(s, `Bearer [REDACTED]`)
	s = skRE.ReplaceAllString(s, `sk-[REDACTED]`)
	return s
}

func modeText(mode Mode, key, value string, data map[string]any) {
	if value == "" || mode == ModeMetadata {
		return
	}
	redacted := RedactString(value)
	if mode == ModeFull {
		data[key] = redacted
		return
	}
	data[key+"_preview"] = truncatePreview(redacted, PreviewBytes)
}

func truncatePreview(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	n := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if n+size > maxBytes {
			if i == 0 {
				return ""
			}
			return s[:i] + "...[truncated]"
		}
		n += size
	}
	return s
}
