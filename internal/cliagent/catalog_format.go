package cliagent

import (
	"fmt"
	"strings"
)

func FormatSkillsListMarkdown(sk catalogSkillsResp, dataDir string) string {
	var b strings.Builder
	b.WriteString("## Skills\n")
	root := strings.TrimSpace(dataDir)
	if root != "" {
		b.WriteString(fmt.Sprintf("data_dir: `%s`\n\n", root))
	}
	if len(sk.Skills) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, s := range sk.Skills {
		b.WriteString(fmt.Sprintf("%d. `%s` — %s (`tenant=%s`, `rel=%s`)\n", i+1, s.Name, s.Title, s.TenantScope, s.RelPath))
	}
	return b.String()
}

func FormatToolsListMarkdown(tl catalogToolsResp) string {
	var b strings.Builder
	b.WriteString("## Tools\n")
	if len(tl.Tools) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	for i, t := range tl.Tools {
		b.WriteString(fmt.Sprintf("%d. `%s` — %s (`risk=%s`, `tenant=%s`)\n", i+1, t.ToolID, trimDesc(t.Description, 160), t.RiskTier, t.TenantScope))
	}
	return b.String()
}
