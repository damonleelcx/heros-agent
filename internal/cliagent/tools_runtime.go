package cliagent

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

func pathUnderWorkspace(workDir, abs string) bool {
	wd, err1 := filepath.Abs(workDir)
	a, err2 := filepath.Abs(abs)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(wd, a)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func extAnsiStrip(args map[string]any) string {
	text := ArgString(args, "text")
	return ansiEscapeRe.ReplaceAllString(text, "")
}

func extStub(toolID string, args map[string]any) (string, error) {
	out, _ := json.Marshal(map[string]any{
		"tool_id": toolID,
		"status":  "unimplemented",
		"args":    args,
	})
	return string(out), nil
}
