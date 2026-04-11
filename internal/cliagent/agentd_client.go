// Package cliagent drives a Codex-style terminal session against a running agentd.
package cliagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AgentdClient calls agentd HTTP APIs (folder skills, tools, memory, graph, CLI policy).
type AgentdClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func (c *AgentdClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *AgentdClient) req(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	return c.client().Do(req)
}

func (c *AgentdClient) GetJSON(ctx context.Context, path string, out any) error {
	resp, err := c.req(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("agentd %s: %s: %s", path, resp.Status, string(b))
	}
	return json.Unmarshal(b, out)
}

func (c *AgentdClient) PostJSON(ctx context.Context, path string, body any, out any) error {
	resp, err := c.req(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("agentd %s: %s: %s", path, resp.Status, string(b))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// --- Responses matching agentd JSON shapes ---

type catalogSkillsResp struct {
	Skills []struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		TenantScope string `json:"tenant_scope"`
		RelPath     string `json:"rel_path"`
	} `json:"skills"`
}

type catalogToolsResp struct {
	Tools []struct {
		ToolID      string `json:"tool_id"`
		Description string `json:"description"`
		RiskTier    string `json:"risk_tier"`
		TenantScope string `json:"tenant_scope"`
	} `json:"tools"`
}

type systemPromptResp struct {
	Body string `json:"body"`
}

type skillBodyResp struct {
	Body string `json:"body"`
}

type retrieveResp struct {
	Chunks  []string `json:"chunks"`
	Backend string   `json:"backend"`
}

type cliExecResp struct {
	RiskTier string `json:"risk_tier"`
	Outcome  string `json:"outcome"`
	Output   string `json:"output"`
	Error    string `json:"error"`
}

type graphNeighborsResp struct {
	Source    string `json:"source"`
	Neighbors []any  `json:"neighbors"`
}

func (c *AgentdClient) SystemPrompt(ctx context.Context) (string, error) {
	var r systemPromptResp
	if err := c.GetJSON(ctx, "/api/prompt/system", &r); err != nil {
		return "", err
	}
	return r.Body, nil
}

func (c *AgentdClient) CatalogSkills(ctx context.Context) (catalogSkillsResp, error) {
	var r catalogSkillsResp
	err := c.GetJSON(ctx, "/api/catalog/skills", &r)
	return r, err
}

func (c *AgentdClient) CatalogTools(ctx context.Context) (catalogToolsResp, error) {
	var r catalogToolsResp
	err := c.GetJSON(ctx, "/api/catalog/tools", &r)
	return r, err
}

func (c *AgentdClient) SkillBody(ctx context.Context, name string) (string, error) {
	path := "/api/catalog/skills/body?name=" + url.QueryEscape(name)
	var r skillBodyResp
	if err := c.GetJSON(ctx, path, &r); err != nil {
		return "", err
	}
	return r.Body, nil
}

func (c *AgentdClient) MemoryRetrieve(ctx context.Context, query string, k int) ([]string, string, error) {
	if k <= 0 {
		k = 8
	}
	var r retrieveResp
	err := c.PostJSON(ctx, "/api/memory/retrieve", map[string]any{"query": query, "k": k}, &r)
	if err != nil {
		return nil, "", err
	}
	return r.Chunks, r.Backend, nil
}

func (c *AgentdClient) MemoryEpisodic(ctx context.Context, sessionID, role, content string, importance float64) error {
	return c.PostJSON(ctx, "/api/memory/episodic", map[string]any{
		"session_id": sessionID,
		"role":       role,
		"content":    content,
		"importance": importance,
	}, nil)
}

func (c *AgentdClient) CLIExec(ctx context.Context, command string) (*cliExecResp, error) {
	var r cliExecResp
	err := c.PostJSON(ctx, "/api/cli/exec", map[string]string{"command": command}, &r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SubmitProposal queues a self-evolution proposal (same shape as POST /api/proposals).
func (c *AgentdClient) SubmitProposal(ctx context.Context, layer, title, rationale, diff, targetTenant string) (map[string]any, error) {
	body := map[string]any{
		"layer": layer, "title": title, "rationale": rationale, "diff": diff,
	}
	if strings.TrimSpace(targetTenant) != "" {
		body["target_tenant"] = strings.TrimSpace(targetTenant)
	}
	var out map[string]any
	err := c.PostJSON(ctx, "/api/proposals", body, &out)
	return out, err
}

func (c *AgentdClient) GraphNeighbors(ctx context.Context, entityID string) (*graphNeighborsResp, error) {
	path := "/api/graph/neighbors?id=" + url.QueryEscape(entityID)
	var r graphNeighborsResp
	err := c.GetJSON(ctx, path, &r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// BuildContextBlock refreshes folder skills + tool registry view for the system prompt.
func (c *AgentdClient) BuildContextBlock(ctx context.Context) (string, error) {
	sk, err := c.CatalogSkills(ctx)
	if err != nil {
		return "", fmt.Errorf("catalog skills: %w", err)
	}
	tl, err := c.CatalogTools(ctx)
	if err != nil {
		return "", fmt.Errorf("catalog tools: %w", err)
	}
	var b strings.Builder
	b.WriteString("## Folder skills (indexed on agentd from disk)\n")
	for _, e := range sk.Skills {
		b.WriteString(fmt.Sprintf("- **%s** — %s (`tenant_scope=%s`, path `%s`)\n", e.Name, e.Title, e.TenantScope, e.RelPath))
	}
	if len(sk.Skills) == 0 {
		b.WriteString("(none)\n")
	}
	b.WriteString("\n## Registered tools (from tools/*/tool.yaml + registry)\n")
	for _, t := range tl.Tools {
		b.WriteString(fmt.Sprintf("- **%s** — %s [risk=%s, tenant=%s]\n", t.ToolID, trimDesc(t.Description, 120), t.RiskTier, t.TenantScope))
	}
	if len(tl.Tools) == 0 {
		b.WriteString("(none)\n")
	}
	return b.String(), nil
}

func trimDesc(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// DefaultHTTPClient with sane timeout.
func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}
