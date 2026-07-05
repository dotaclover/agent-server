package security

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	bearerTokenPattern         = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	authorizationPattern       = regexp.MustCompile(`(?i)(authorization["'\s]*[:=]\s*["']?)(?:[A-Za-z]+\s+)?[A-Za-z0-9._~+/=-]+`)
	apiKeyPattern              = regexp.MustCompile(`(?i)(api[_-]?key["'\s:=]+)[A-Za-z0-9._~+/=-]+`)
	sensitiveAssignmentPattern = regexp.MustCompile(`(?i)((?:access[_-]?token|refresh[_-]?token|id[_-]?token|session[_-]?token|token|password|passwd|client[_-]?secret|secret)["'\s]*[:=]\s*["']?)[^&\s"',}]+`)
	skKeyPattern               = regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{8,}\b`)
)

func RedactSensitive(text string) string {
	text = authorizationPattern.ReplaceAllString(text, "${1}[redacted]")
	text = bearerTokenPattern.ReplaceAllString(text, "${1}[redacted]")
	text = apiKeyPattern.ReplaceAllString(text, "${1}[redacted]")
	text = sensitiveAssignmentPattern.ReplaceAllString(text, "${1}[redacted]")
	text = skKeyPattern.ReplaceAllString(text, "sk-[redacted]")
	return text
}

func SafeURLOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[invalid-url]"
	}
	return parsed.Scheme + "://" + parsed.Host
}
