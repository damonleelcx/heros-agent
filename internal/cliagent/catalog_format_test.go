package cliagent

import "testing"

func TestFormatSkillsListMarkdown(t *testing.T) {
	sk := catalogSkillsResp{
		Skills: []struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			TenantScope string `json:"tenant_scope"`
			RelPath     string `json:"rel_path"`
		}{
			{Name: "skill-a", Title: "Skill A", TenantScope: "global", RelPath: "skills/_global/skill-a/SKILL.md"},
		},
	}
	got := FormatSkillsListMarkdown(sk, "C:/data")
	if got == "" || got[0:2] != "##" {
		t.Fatalf("unexpected formatted skills output: %q", got)
	}
}

func TestFormatToolsListMarkdown(t *testing.T) {
	tl := catalogToolsResp{
		Tools: []struct {
			ToolID      string `json:"tool_id"`
			Description string `json:"description"`
			RiskTier    string `json:"risk_tier"`
			TenantScope string `json:"tenant_scope"`
		}{
			{ToolID: "tool-a", Description: "A tool", RiskTier: "low", TenantScope: "global"},
		},
	}
	got := FormatToolsListMarkdown(tl)
	if got == "" || got[0:2] != "##" {
		t.Fatalf("unexpected formatted tools output: %q", got)
	}
}
