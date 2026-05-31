package bridge

import (
	"regexp"
)

func stripANSI(value string) string {
	return ansiControlPattern.ReplaceAllString(value, "")
}

func redactSensitiveText(value string) string {
	out := bearerTokenPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	out = bareEnrollTokenPattern.ReplaceAllString(out, "enr_[REDACTED]")
	out = bareOpenAIKeyPattern.ReplaceAllString(out, "sk-[REDACTED]")
	for _, pattern := range sensitiveValuePatterns {
		out = pattern.ReplaceAllString(out, "$1[REDACTED]")
	}
	return out
}

var ansiControlPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\a]*(?:\a|\x1b\\)`)

var bearerTokenPattern = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`)

var bareEnrollTokenPattern = regexp.MustCompile(`\benr_[A-Za-z0-9_-]+\b`)

var bareOpenAIKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)

var sensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b((?:api[_-]?key|token|secret|password|session|authorization)\s*[:=]\s*)["']?[^"'\s]+`),
	regexp.MustCompile(`(?i)\b((?:OPENAI_API_KEY|ANTHROPIC_API_KEY|CLAUDE_API_KEY|GEMINI_API_KEY|AWS_SECRET_ACCESS_KEY|AWS_SESSION_TOKEN)=)["']?[^"'\s]+`),
}
