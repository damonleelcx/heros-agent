package harness

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/promptlayer"
)

// Topology describes Leader-Follower + Team+Critic (Layer 3).
type Topology struct {
	Specialists       []string `json:"specialists"`
	CriticThreshold   float64  `json:"critic_threshold"`
	MaxCriticRetries  int      `json:"max_critic_retries"`
	LeaderModel       string   `json:"leader_model"`
}

func DefaultTopology() Topology {
	return Topology{
		Specialists:      []string{"researcher", "coder", "writer", "analyst"},
		CriticThreshold: 0.55,
		MaxCriticRetries: 2,
	}
}

func LoadTopology(db *sql.DB) (Topology, error) {
	var js string
	err := db.QueryRow(`SELECT topology_json FROM harness_config WHERE id = 1`).Scan(&js)
	if err == sql.ErrNoRows {
		return DefaultTopology(), nil
	}
	if err != nil {
		return Topology{}, err
	}
	var t Topology
	if err := json.Unmarshal([]byte(js), &t); err != nil {
		return DefaultTopology(), nil
	}
	if t.CriticThreshold <= 0 {
		t.CriticThreshold = 0.55
	}
	if t.MaxCriticRetries <= 0 {
		t.MaxCriticRetries = 2
	}
	if len(t.Specialists) == 0 {
		t.Specialists = DefaultTopology().Specialists
	}
	return t, nil
}

func SaveTopology(db *sql.DB, t Topology) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO harness_config (id, topology_json, updated_at) VALUES (1, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET topology_json = excluded.topology_json, updated_at = datetime('now')`, string(b))
	return err
}

// SeedHarness inserts default topology if missing.
func SeedHarness(db *sql.DB) error {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM harness_config WHERE id = 1`).Scan(&n)
	if n > 0 {
		return nil
	}
	return SaveTopology(db, DefaultTopology())
}

type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

type Orchestrator struct {
	DB     *sql.DB
	DataDir string
	LLM    LLMConfig
}

type RunResult struct {
	Goal           string            `json:"goal"`
	Plan           []string          `json:"plan"`
	SubResults     map[string]string `json:"sub_results"`
	CriticScores   map[string]float64 `json:"critic_scores"`
	Final          string            `json:"final"`
	Retries        int               `json:"retries"`
}

// Run executes leader decomposition, parallel specialist stubs, critic loop.
func (o *Orchestrator) Run(ctx context.Context, goal string) (*RunResult, error) {
	topo, err := LoadTopology(o.DB)
	if err != nil {
		return nil, err
	}
	sys, _, err := promptlayer.LoadActiveSystemPrompt(o.DB, o.DataDir)
	if err != nil {
		sys = ""
	}

	// Leader: break into sub-tasks (LLM or heuristic)
	plan, err := o.leaderPlan(ctx, sys, topo, goal)
	if err != nil {
		plan = []string{goal}
	}

	res := &RunResult{Goal: goal, Plan: plan, SubResults: map[string]string{}, CriticScores: map[string]float64{}}

	// Followers: one pass per plan step with specialist rotation
	for i, step := range plan {
		spec := topo.Specialists[i%len(topo.Specialists)]
		out, err := o.specialist(ctx, sys, spec, step)
		if err != nil {
			out = fmt.Sprintf("(specialist %s error: %v)", spec, err)
		}
		key := fmt.Sprintf("%s:%d", spec, i)
		res.SubResults[key] = out
	}

	merged := mergeSubResults(res.SubResults)
	retries := 0
	for {
		score, rationale, err := o.critic(ctx, sys, topo, goal, merged)
		if err != nil {
			score = 0.6
			rationale = err.Error()
		}
		res.CriticScores[fmt.Sprintf("attempt_%d", retries)] = score
		if score >= topo.CriticThreshold || retries >= topo.MaxCriticRetries {
			res.Final = merged + "\n\n[critic score=" + fmt.Sprintf("%.2f", score) + "] " + rationale
			res.Retries = retries
			return res, nil
		}
		merged, err = o.refine(ctx, sys, goal, merged, rationale)
		if err != nil {
			merged = merged + "\n" + rationale
		}
		retries++
	}
}

func mergeSubResults(m map[string]string) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString("### ")
		b.WriteString(k)
		b.WriteString("\n")
		b.WriteString(v)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func (o *Orchestrator) leaderPlan(ctx context.Context, sys string, topo Topology, goal string) ([]string, error) {
	model := topo.LeaderModel
	if model == "" {
		model = o.LLM.Model
	}
	prompt := `Decompose the user goal into 2-5 concrete sub-tasks, one per line, no numbering. Goal: ` + goal
	raw, err := o.chat(ctx, model, sys, prompt)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return []string{goal}, nil
	}
	return lines, nil
}

func (o *Orchestrator) specialist(ctx context.Context, sys, role, step string) (string, error) {
	prompt := fmt.Sprintf("You are the %s specialist. Execute this sub-task briefly and practically:\n%s", role, step)
	return o.chat(ctx, o.LLM.Model, sys, prompt)
}

func (o *Orchestrator) critic(ctx context.Context, sys string, topo Topology, goal, draft string) (float64, string, error) {
	prompt := fmt.Sprintf(`Score 0.0-1.0 how well the draft satisfies the goal. Reply JSON only: {"score":0.0,"rationale":"..."}
Goal: %s
Draft:
%s`, goal, draft)
	raw, err := o.chat(ctx, o.LLM.Model, sys, prompt)
	if err != nil {
		return 0, "", err
	}
	var out struct {
		Score     float64 `json:"score"`
		Rationale string  `json:"rationale"`
	}
	_ = json.Unmarshal([]byte(extractJSON(raw)), &out)
	if out.Score == 0 && !strings.Contains(raw, "score") {
		return 0.5, raw, nil
	}
	return out.Score, out.Rationale, nil
}

func (o *Orchestrator) refine(ctx context.Context, sys, goal, draft, critique string) (string, error) {
	prompt := fmt.Sprintf("Improve the draft using the critique.\nGoal: %s\nCritique: %s\nDraft:\n%s", goal, critique, draft)
	return o.chat(ctx, o.LLM.Model, sys, prompt)
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	return s
}

func (o *Orchestrator) chat(ctx context.Context, model, system, user string) (string, error) {
	if o.LLM.APIKey == "" {
		return heuristicReply(user), nil
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.LLM.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.LLM.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm %s: %s", resp.Status, string(rb))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func heuristicReply(user string) string {
	if strings.Contains(strings.ToLower(user), "sub-task") || strings.Contains(strings.ToLower(user), "decompose") {
		return "1. Clarify requirements\n2. Draft minimal solution\n3. Verify against constraints"
	}
	return "(offline mode) Summarized: " + truncate(user, 200) + "\nAdd OPENAI_API_KEY for full LLM harness."
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ApplyTopologyMutation merges JSON diff into harness config (after human approval).
func ApplyTopologyMutation(db *sql.DB, diff string) (rollback string, err error) {
	cur, err := LoadTopology(db)
	if err != nil {
		return "", err
	}
	prev, _ := json.Marshal(cur)
	var patch Topology
	if err := json.Unmarshal([]byte(diff), &patch); err != nil {
		return "", err
	}
	if len(patch.Specialists) > 0 {
		cur.Specialists = patch.Specialists
	}
	if patch.CriticThreshold > 0 {
		cur.CriticThreshold = patch.CriticThreshold
	}
	if patch.MaxCriticRetries > 0 {
		cur.MaxCriticRetries = patch.MaxCriticRetries
	}
	if patch.LeaderModel != "" {
		cur.LeaderModel = patch.LeaderModel
	}
	if err := SaveTopology(db, cur); err != nil {
		return "", err
	}
	return string(prev), nil
}

// ProposeTopologyDiff returns human-readable diff for queue (self-evolution proposal).
func ProposeTopologyDiff(current, proposed Topology) string {
	a, _ := json.MarshalIndent(current, "", "  ")
	b, _ := json.MarshalIndent(proposed, "", "  ")
	return fmt.Sprintf("--- topology\n+++ topology\n@@\n-%s\n+%s\n", string(a), string(b))
}
