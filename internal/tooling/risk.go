package tooling

import (
	"regexp"
	"strings"
)

type RiskTier string

const (
	RiskLow    RiskTier = "low"
	RiskMedium RiskTier = "medium"
	RiskHigh   RiskTier = "high"
)

var (
	reHigh = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(rm\s+-rf|mkfs|dd\s+if=|:(){ :|:& };:|format\s+c:|del\s+/s|shutdown|reboot|chmod\s+777\s+/)\b`),
		regexp.MustCompile(`(?i)\b(curl|wget)\s+.*\|\s*(bash|sh)\b`),
	}
	reMedium = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(git\s+push|npm\s+publish|docker\s+run|kubectl\s+(apply|delete)|terraform\s+apply)\b`),
		regexp.MustCompile(`(?i)\b(curl|wget)\s+https?://`),
		regexp.MustCompile(`(?i)\b(echo|printf)\s+.*>\s*[/~]`),
	}
	reReadOnly = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(ls|dir|cat|type|head|tail|find|grep|git\s+status|git\s+log|git\s+diff|pwd|whoami|env|go\s+version)\b`),
	}
)

// ClassifyCLI applies a three-tier risk model (Layer 4).
func ClassifyCLI(cmd string) RiskTier {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return RiskLow
	}
	for _, r := range reHigh {
		if r.MatchString(c) {
			return RiskHigh
		}
	}
	for _, r := range reMedium {
		if r.MatchString(c) {
			return RiskMedium
		}
	}
	for _, r := range reReadOnly {
		if r.MatchString(c) {
			return RiskLow
		}
	}
	// writes without explicit read-only pattern → medium
	if strings.ContainsAny(c, ">") || strings.Contains(c, ">>") {
		return RiskMedium
	}
	return RiskMedium
}
