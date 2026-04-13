package cliagent

import (
	"os"
	"regexp"
	"strings"
)

var (
	reProjectGrounding = regexp.MustCompile(`(?i)(` +
		`tell\s+me\s+about|what\s+('s|is|are)\s+this|about\s+this\s+project|this\s+project|the\s+project|our\s+project|` +
		`codebase|repo(sitory)?|what\s+do(es)?\s+it\s+do|` +
		`don'?t\s+you\s+know|what\s+do\s+you\s+need\s+to\s+start|` +
		`explain\s+(the\s+)?(app|code|repo|project)|overview\s+of|` +
		`how\s+does\s+(this|the)\s+(project|app|codebase)` +
		`)`)

	reMemoryGrounding = regexp.MustCompile(`(?i)(` +
		`any\s+memory|what\s+memory|what\s+do\s+you\s+remember|` +
		`do\s+you\s+have\s+memory|stored\s+in\s+memory|` +
		`recall\s+(from\s+)?(memory|session)|` +
		`what\s+('s|is)\s+in\s+(your\s+)?memory|` +
		`memory\s+you\s+have|episodic|semantic\s+memory` +
		`)`)
)

// WorkspaceGroundingRequired is true when the user is asking about the local workspace / product
// and the model should not answer from imagination — tools must run first.
func WorkspaceGroundingRequired(userLine, workDir string) bool {
	if os.Getenv("HEROS_NO_TOOL_FORCE") == "1" {
		return false
	}
	u := strings.TrimSpace(userLine)
	if u == "" || strings.HasPrefix(u, "/") {
		return false
	}
	wd := strings.TrimSpace(workDir)
	if wd == "" || wd == "." {
		return false
	}
	return reProjectGrounding.MatchString(u)
}

// MemoryGroundingRequired is true when the user asks what is in agent memory — answer via heros_memory_search, not chat fiction.
func MemoryGroundingRequired(userLine string) bool {
	if os.Getenv("HEROS_NO_TOOL_FORCE") == "1" {
		return false
	}
	u := strings.TrimSpace(userLine)
	if u == "" || strings.HasPrefix(u, "/") {
		return false
	}
	return reMemoryGrounding.MatchString(u)
}
