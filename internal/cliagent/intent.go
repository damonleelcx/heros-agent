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

	reFileActionGrounding = regexp.MustCompile(`(?i)(` +
		`(create|add|write|update|edit|modify|delete|remove|read|open|show)\s+.*(file|folder|directory|test|spec)|` +
		`(file|folder|directory|test|spec)\s+.*(create|add|write|update|edit|modify|delete|remove|read|open|show)|` +
		`help\s+me\s+(add|create|write|update|edit|delete|remove)|` +
		`make\s+(a|an)\s+.*(file|test)|` +
		`point\s+out\s+the\s+file\s+path` +
		`)`)

	reLongHorizonHarness = regexp.MustCompile(`(?i)(` +
		`i\s+want\s+to\s+(build|create|design|implement)|` +
		`let'?s\s+(build|design|implement)|` +
		`add\s+more\s+backend\s+functionalit(y|ies)|` +
		`connect\s+(the\s+)?front\s*end\s+to\s+(the\s+)?back\s*end|` +
		`connect\s+(the\s+)?backend\s+to\s+(the\s+)?frontend|` +
		`frontend\s+.*backend\s+integration|` +
		`backend\s+.*frontend\s+integration|` +
		`full[\s-]?stack|` +
		`crud\s+api|` +
		`production-?ready|` +
		`architecture|orchestrat(e|ion)|` +
		`distributed\s+system|` +
		`multi-?step|long-?running|long-?horizon|` +
		`agent\s+harness|` +
		`template` +
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

// FileActionGroundingRequired is true when the user asks to create/update/delete/read files.
// Force at least one tool call so the agent acts instead of giving only instructions.
func FileActionGroundingRequired(userLine string) bool {
	if os.Getenv("HEROS_NO_TOOL_FORCE") == "1" {
		return false
	}
	u := strings.TrimSpace(userLine)
	if u == "" || strings.HasPrefix(u, "/") {
		return false
	}
	return reFileActionGrounding.MatchString(u)
}

// LongHorizonHarnessRequired is true when the request is a likely multi-step build/design task.
// In that case, the first action should be heros_run_harness instead of a one-shot assistant reply.
func LongHorizonHarnessRequired(userLine string) bool {
	if os.Getenv("HEROS_NO_TOOL_FORCE") == "1" {
		return false
	}
	u := strings.TrimSpace(userLine)
	if u == "" || strings.HasPrefix(u, "/") {
		return false
	}
	return reLongHorizonHarness.MatchString(u)
}
