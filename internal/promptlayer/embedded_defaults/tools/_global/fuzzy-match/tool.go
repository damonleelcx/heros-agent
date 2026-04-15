package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "compare"
	}
	switch action {
	case "status":
		return toolcontract.Ok("fuzzy-match", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "compare":
		a := asString(args["a"])
		b := asString(args["b"])
		if a == "" || b == "" {
			return toolcontract.Error("fuzzy-match", toolcontract.ErrorCodeValidationError, "missing strings a or b", action, args), nil
		}
		score := similarity(a, b)
		return toolcontract.Ok("fuzzy-match", action, args, map[string]any{
			"a":         a,
			"b":         b,
			"score":     score,
			"match":     score >= 0.7,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("fuzzy-match", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func similarity(a, b string) float64 {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0
	}
	dist := levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	return 1.0 - float64(dist)/float64(maxLen)
}

func levenshtein(a, b string) int {
	da := []rune(a)
	db := []rune(b)
	dp := make([][]int, len(da)+1)
	for i := range dp {
		dp[i] = make([]int, len(db)+1)
	}
	for i := 0; i <= len(da); i++ {
		dp[i][0] = i
	}
	for j := 0; j <= len(db); j++ {
		dp[0][j] = j
	}
	for i := 1; i <= len(da); i++ {
		for j := 1; j <= len(db); j++ {
			cost := 0
			if da[i-1] != db[j-1] {
				cost = 1
			}
			dp[i][j] = min3(
				dp[i-1][j]+1,
				dp[i][j-1]+1,
				dp[i-1][j-1]+cost,
			)
		}
	}
	return dp[len(da)][len(db)]
}

func min3(a, b, c int) int {
	if a < b && a < c {
		return a
	}
	if b < c {
		return b
	}
	return c
}
