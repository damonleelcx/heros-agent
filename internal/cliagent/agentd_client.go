// Package cliagent drives a Codex-style terminal session against a running agentd.
package cliagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/harness"
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

func (c *AgentdClient) MemoryRetrieve(ctx context.Context, sessionID, query string, k int) ([]string, string, error) {
	if k <= 0 {
		k = 8
	}
	body := map[string]any{"query": query, "k": k}
	if strings.TrimSpace(sessionID) != "" {
		body["session_id"] = strings.TrimSpace(sessionID)
	}
	var r retrieveResp
	err := c.PostJSON(ctx, "/api/memory/retrieve", body, &r)
	if err != nil {
		return nil, "", err
	}
	return r.Chunks, r.Backend, nil
}

// ListPendingProposals returns pending proposals (CLI approval flow).
func (c *AgentdClient) ListPendingProposals(ctx context.Context) ([]map[string]any, error) {
	resp, err := c.req(ctx, http.MethodGet, "/api/proposals/pending", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("agentd /api/proposals/pending: %s: %s", resp.Status, string(b))
	}
	var list []map[string]any
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parse pending: %w", err)
	}
	return list, nil
}

// ApproveProposal applies a pending proposal (equivalent to the web UI approve button).
func (c *AgentdClient) ApproveProposal(ctx context.Context, id string) error {
	_, err := c.ApproveProposalJSON(ctx, id)
	return err
}

// ApproveProposalJSON applies a proposal and returns the updated proposal JSON (for tools / UI).
func (c *AgentdClient) ApproveProposalJSON(ctx context.Context, id string) (map[string]any, error) {
	path := "/api/proposals/" + url.PathEscape(id) + "/approve"
	resp, err := c.req(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("agentd %s: %s: %s", path, resp.Status, string(rb))
	}
	var out map[string]any
	if len(rb) > 0 && json.Unmarshal(rb, &out) == nil && out != nil {
		return out, nil
	}
	return map[string]any{"status": "approved", "id": id}, nil
}

// RejectProposal marks a proposal rejected.
func (c *AgentdClient) RejectProposal(ctx context.Context, id string) error {
	_, err := c.RejectProposalJSON(ctx, id)
	return err
}

// RejectProposalJSON rejects a proposal and returns the updated proposal JSON.
func (c *AgentdClient) RejectProposalJSON(ctx context.Context, id string) (map[string]any, error) {
	path := "/api/proposals/" + url.PathEscape(id) + "/reject"
	resp, err := c.req(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("agentd %s: %s: %s", path, resp.Status, string(rb))
	}
	var out map[string]any
	if len(rb) > 0 && json.Unmarshal(rb, &out) == nil && out != nil {
		return out, nil
	}
	return map[string]any{"status": "rejected", "id": id}, nil
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

// FetchTopology returns harness config from agentd (Layer 3); used for CLI doctrine text.
func (c *AgentdClient) FetchTopology(ctx context.Context) (harness.Topology, error) {
	var t harness.Topology
	err := c.GetJSON(ctx, "/api/config/topology", &t)
	return t, err
}

// HarnessRun executes Layer-3 leader → specialists → critic inside the same heros process (in-process HTTP handler).
func (c *AgentdClient) HarnessRun(ctx context.Context, goal string) (*harness.RunResult, error) {
	var res harness.RunResult
	err := c.PostJSON(ctx, "/api/harness/run", map[string]string{"goal": goal}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// HarnessRunWithProgress streams per-phase harness events while running and returns the final result.
func (c *AgentdClient) HarnessRunWithProgress(ctx context.Context, goal string, onProgress func(harness.ProgressEvent)) (*harness.RunResult, error) {
	resp, err := c.req(ctx, http.MethodPost, "/api/harness/run?stream=1", map[string]string{"goal": goal})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentd /api/harness/run?stream=1: %s: %s", resp.Status, string(body))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var final *harness.RunResult
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var frame struct {
			Type   string                `json:"type"`
			Event  harness.ProgressEvent `json:"event"`
			Result harness.RunResult     `json:"result"`
			Error  string                `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		switch frame.Type {
		case "event":
			if onProgress != nil {
				onProgress(frame.Event)
			}
		case "result":
			cp := frame.Result
			final = &cp
		case "error":
			return nil, fmt.Errorf("harness stream: %s", frame.Error)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if final == nil {
		return nil, fmt.Errorf("harness stream: missing final result")
	}
	return final, nil
}

// BuildContextBlock refreshes folder skills + tool registry view for the system prompt.
// dataDir is the agent home (skills/, tools/, …); rel_path values are relative to it, not the workspace.
func (c *AgentdClient) BuildContextBlock(ctx context.Context, dataDir string) (string, error) {
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
	b.WriteString("Each **`rel_path`** is under the agent **data_dir**, not the workspace. **`abs`** is the on-disk SKILL.md path when data_dir is known to this CLI.\n")
	if root := strings.TrimSpace(dataDir); root != "" {
		if absRoot, err := filepath.Abs(root); err == nil {
			b.WriteString(fmt.Sprintf("**data_dir (absolute):** `%s`\n", filepath.Clean(absRoot)))
		}
	}
	for _, e := range sk.Skills {
		line := fmt.Sprintf("- **%s** — %s (`tenant_scope=%s`, rel `%s`)", e.Name, e.Title, e.TenantScope, e.RelPath)
		if root := strings.TrimSpace(dataDir); root != "" {
			if absSkill, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(e.RelPath))); err == nil {
				line += fmt.Sprintf(", abs `%s`", filepath.Clean(absSkill))
			}
		}
		line += ")\n"
		b.WriteString(line)
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
	b.WriteString("\n## Operating doctrine (CLI)\n")
	b.WriteString("- Ground answers about **this workspace** with **heros_shell** (list/read files) before claiming ignorance.\n")
	b.WriteString("- Catalog skills live under **data_dir**, not the workspace; use **abs** paths above or **heros_read_skill** — never guess `workspace/skills/...`.\n")
	b.WriteString("- If a skill name above fits the task, call **heros_read_skill** before long improvised playbooks.\n")
	b.WriteString("- For work spanning many steps, call **heros_memory_search** early to recall prior decisions in this session/org memory.\n")
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
