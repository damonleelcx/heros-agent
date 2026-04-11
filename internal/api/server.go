package api

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/collective"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/evolve"
	"github.com/heros-foreal/agentd/internal/harness"
	"github.com/heros-foreal/agentd/internal/indexsync"
	"github.com/heros-foreal/agentd/internal/memorylayer"
	"github.com/heros-foreal/agentd/internal/memorytree"
	"github.com/heros-foreal/agentd/internal/observability"
	"github.com/heros-foreal/agentd/internal/platform"
	"github.com/heros-foreal/agentd/internal/promptlayer"
	"github.com/heros-foreal/agentd/internal/scheduler"
	"github.com/heros-foreal/agentd/internal/skillindex"
	"github.com/heros-foreal/agentd/internal/toolindex"
	"github.com/heros-foreal/agentd/internal/tooling"
)

//go:embed static/*
var staticFS embed.FS

type Server struct {
	DB      *sql.DB
	Cfg     config.Config
	RT      *platform.Runtime
	Mux     *http.ServeMux
	Handler http.Handler
}

func New(db *sql.DB, cfg config.Config, rt *platform.Runtime) *Server {
	mux := http.NewServeMux()
	s := &Server{DB: db, Cfg: cfg, RT: rt, Mux: mux}
	if cfg.MetricsEnabled {
		mux.HandleFunc("GET /metrics", observability.MetricsHandler)
	}
	s.routes()
	var h http.Handler = mux
	h = observability.Middleware(h)
	if cfg.AuthMode == "required" {
		reg := auth.NewRegistry(cfg)
		h = auth.Compose(reg, h)
	}
	s.Handler = h
	return s
}

// tenantFrom returns tenant scope for data access. Without auth, empty tenant + admin (see all).
func (s *Server) tenantFrom(r *http.Request) (tenantID string, admin bool) {
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		return p.TenantID, p.IsAdmin()
	}
	return "", true
}

func (s *Server) routes() {
	s.Mux.HandleFunc("GET /health", s.handleHealth)
	s.Mux.HandleFunc("GET /api/proposals", s.handleListProposals)
	s.Mux.HandleFunc("GET /api/proposals/pending", s.handlePending)
	s.Mux.HandleFunc("POST /api/proposals", s.handleSubmitProposal)
	s.Mux.HandleFunc("POST /api/proposals/{id}/approve", s.handleApprove)
	s.Mux.HandleFunc("POST /api/proposals/{id}/reject", s.handleReject)
	s.Mux.HandleFunc("GET /api/skills/graph", s.handleSkillGraph)
	s.Mux.HandleFunc("GET /api/catalog/skills", s.handleCatalogSkills)
	s.Mux.HandleFunc("GET /api/catalog/skills/body", s.handleCatalogSkillBody)
	s.Mux.HandleFunc("GET /api/catalog/tools", s.handleCatalogTools)
	s.Mux.HandleFunc("POST /api/catalog/reindex", s.handleCatalogReindex)
	s.Mux.HandleFunc("POST /api/catalog/tools/registry-to-disk", s.handleCatalogToolsRegistryToDisk)
	s.Mux.HandleFunc("GET /api/memory/sessions", s.handleMemorySessions)
	s.Mux.HandleFunc("GET /api/prompt/system", s.handleSystemPrompt)
	s.Mux.HandleFunc("POST /api/harness/run", s.handleHarnessRun)
	s.Mux.HandleFunc("POST /api/memory/episodic", s.handleEpisodic)
	s.Mux.HandleFunc("POST /api/memory/retrieve", s.handleRetrieve)
	s.Mux.HandleFunc("POST /api/memory/consolidate", s.handleConsolidate)
	s.Mux.HandleFunc("POST /api/memory/optimize-session", s.handleOptimizeSession)
	s.Mux.HandleFunc("POST /api/graph/entity", s.handleGraphEntity)
	s.Mux.HandleFunc("POST /api/graph/link", s.handleGraphLink)
	s.Mux.HandleFunc("GET /api/graph/neighbors", s.handleGraphNeighbors)
	s.Mux.HandleFunc("POST /api/cli/exec", s.handleCLI)
	s.Mux.HandleFunc("GET /api/config/topology", s.handleTopologyGet)
	s.Mux.HandleFunc("GET /api/schedule/jobs", s.handleScheduleList)
	s.Mux.HandleFunc("POST /api/schedule/jobs", s.handleScheduleCreate)
	sub, _ := fs.Sub(staticFS, "static")
	s.Mux.Handle("GET /", http.FileServer(http.FS(sub)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	comps := map[string]string{"sqlite": "ok"}
	if s.RT != nil {
		if s.RT.Qdrant != nil {
			if err := s.RT.Qdrant.Health(ctx); err != nil {
				comps["qdrant"] = "error: " + err.Error()
			} else {
				comps["qdrant"] = "ok"
			}
		}
		if s.RT.Neo != nil {
			if err := s.RT.Neo.Ping(ctx); err != nil {
				comps["neo4j"] = "error: " + err.Error()
			} else {
				comps["neo4j"] = "ok"
			}
		}
		if s.RT.Bus != nil && s.RT.Bus.Conn != nil {
			if !s.RT.Bus.Conn.IsConnected() {
				comps["nats"] = "disconnected"
			} else {
				comps["nats"] = "ok"
			}
		}
	}
	overall := "ok"
	for _, v := range comps {
		if strings.HasPrefix(v, "error") || v == "disconnected" {
			overall = "degraded"
			break
		}
	}
	writeJSON(w, map[string]any{"status": overall, "components": comps})
}

func (s *Server) handleListProposals(w http.ResponseWriter, r *http.Request) {
	tid, admin := s.tenantFrom(r)
	list, err := approval.ListRecent(s.DB, tid, admin, 80)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, list)
}

func (s *Server) handlePending(w http.ResponseWriter, r *http.Request) {
	tid, admin := s.tenantFrom(r)
	list, err := approval.ListPending(s.DB, tid, admin)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, list)
}

type submitBody struct {
	Layer        string `json:"layer"`
	Title        string `json:"title"`
	Rationale    string `json:"rationale"`
	Diff         string `json:"diff"`
	TargetTenant string `json:"target_tenant,omitempty"` // admin only: submit on behalf of tenant
}

func (s *Server) handleSubmitProposal(w http.ResponseWriter, r *http.Request) {
	var b submitBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tid, admin := s.tenantFrom(r)
	submitTenant := tid
	if admin && strings.TrimSpace(b.TargetTenant) != "" {
		submitTenant = strings.TrimSpace(b.TargetTenant)
	}
	p, err := approval.Submit(s.DB, submitTenant, approval.Layer(b.Layer), b.Title, b.Rationale, b.Diff)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.Cfg.CollectiveURL != "" {
		_ = collective.PushProposal(s.Cfg.CollectiveURL, *p)
	}
	if s.RT != nil && s.RT.Bus != nil {
		_ = s.RT.Bus.PublishProposalSubmitted(*p)
	}
	writeJSON(w, p)
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.RT == nil {
		http.Error(w, "runtime not initialized", http.StatusInternalServerError)
		return
	}
	tid, admin := s.tenantFrom(r)
	p0, err := approval.Get(s.DB, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !approval.CanAccess(p0, tid, admin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := evolve.ApplyApprovedProposal(r.Context(), s.RT, id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	p, _ := approval.Get(s.DB, id)
	if s.RT.Bus != nil && p != nil {
		_ = s.RT.Bus.PublishProposalApproved(*p)
	}
	writeJSON(w, p)
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tid, admin := s.tenantFrom(r)
	p0, err := approval.Get(s.DB, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !approval.CanAccess(p0, tid, admin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := approval.Reject(s.DB, id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	p, _ := approval.Get(s.DB, id)
	writeJSON(w, p)
}

func (s *Server) handleSkillGraph(w http.ResponseWriter, r *http.Request) {
	b, err := promptlayer.FullCatalogGraphJSON(s.DB)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Server) handleSystemPrompt(w http.ResponseWriter, r *http.Request) {
	body, ver, err := promptlayer.LoadActiveSystemPrompt(s.DB, s.Cfg.DataDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"version": ver, "body": body, "source": "system/prompt.md preferred"})
}

func (s *Server) handleCatalogSkills(w http.ResponseWriter, r *http.Request) {
	tid, admin := s.tenantFrom(r)
	qTenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	var list []skillindex.Entry
	var err error
	if admin {
		if qTenant != "" {
			list, err = skillindex.ListByTenant(s.DB, qTenant)
		} else {
			list, err = skillindex.List(s.DB)
		}
	} else {
		list, err = skillindex.ListForTenantAndGlobal(s.DB, tid)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"skills": list,
		"root":   "skills/<tenant|_global>/<slug>/SKILL.md (legacy skills/<slug>/SKILL.md → tenant _global)",
	})
}

func (s *Server) handleCatalogSkillBody(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", 400)
		return
	}
	principalTenant, _ := s.tenantFrom(r)
	body, err := promptlayer.LoadSkillMarkdown(s.Cfg.DataDir, s.DB, principalTenant, name)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, map[string]string{"name": name, "body": body})
}

func (s *Server) handleCatalogTools(w http.ResponseWriter, r *http.Request) {
	tid, admin := s.tenantFrom(r)
	qTenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	var list []toolindex.Entry
	var err error
	if admin {
		if qTenant != "" {
			list, err = toolindex.ListByTenant(s.DB, qTenant)
		} else {
			list, err = toolindex.List(s.DB)
		}
	} else {
		list, err = toolindex.ListForTenantAndGlobal(s.DB, tid)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"tools": list,
		"root":  "tools/<tenant|_global>/<slug>/tool.yaml (legacy tools/<slug>/tool.yaml → _global)",
	})
}

func (s *Server) handleCatalogReindex(w http.ResponseWriter, r *http.Request) {
	if err := skillindex.Rebuild(s.DB, s.Cfg.DataDir); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := toolindex.Rebuild(s.DB, s.Cfg.DataDir, toolindex.SyncPolicyFromConfig(s.Cfg)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.RT != nil && s.RT.Neo != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if err := indexsync.SyncSkillsTools(ctx, s.RT.Neo, s.DB); err != nil {
			http.Error(w, "neo4j sync: "+err.Error(), 500)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "reindexed"})
}

func (s *Server) handleCatalogToolsRegistryToDisk(w http.ResponseWriter, r *http.Request) {
	if err := toolindex.PushAllRegistryToDisk(s.DB, s.Cfg.DataDir, toolindex.SyncPolicyFromConfig(s.Cfg)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "registry_flushed_to_disk"})
}

func (s *Server) handleMemorySessions(w http.ResponseWriter, r *http.Request) {
	tid, _ := s.tenantFrom(r)
	list, err := memorytree.ListSessions(s.Cfg.DataDir, tid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"tenant_id": tid,
		"sessions":  list,
		"layout":    "memory/<tenant>/sessions/<session_id>/turns.jsonl",
	})
}

type harnessBody struct {
	Goal string `json:"goal"`
}

func (s *Server) handleHarnessRun(w http.ResponseWriter, r *http.Request) {
	var b harnessBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	o := &harness.Orchestrator{
		DB:      s.DB,
		DataDir: s.Cfg.DataDir,
		LLM: harness.LLMConfig{
			BaseURL: s.Cfg.OpenAIBaseURL,
			APIKey:  s.Cfg.OpenAIAPIKey,
			Model:   s.Cfg.OpenAIModel,
		},
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := o.Run(ctx, strings.TrimSpace(b.Goal))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, res)
}

type episodicBody struct {
	SessionID  string  `json:"session_id"`
	Role       string  `json:"role"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
}

func (s *Server) handleEpisodic(w http.ResponseWriter, r *http.Request) {
	var b episodicBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tid, _ := s.tenantFrom(r)
	id, err := memorylayer.AppendEpisodic(s.DB, s.Cfg.DataDir, tid, b.SessionID, b.Role, b.Content, b.Importance)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

type retrieveBody struct {
	Query string `json:"query"`
	K     int    `json:"k"`
}

func (s *Server) handleRetrieve(w http.ResponseWriter, r *http.Request) {
	var b retrieveBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var inf *memorylayer.VectorInfra
	if s.RT != nil {
		inf = s.RT.VectorInfra()
	}
	tid, _ := s.tenantFrom(r)
	chunks, err := memorylayer.RetrieveSemantic(r.Context(), s.DB, inf, tid, b.Query, b.K)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"chunks": chunks, "backend": retrieveBackend(inf)})
}

func retrieveBackend(inf *memorylayer.VectorInfra) string {
	if inf != nil && inf.Qdrant != nil && inf.Emb != nil {
		return "qdrant"
	}
	return "sqlite"
}

type consolidateBody struct {
	SessionID string  `json:"session_id"`
	Threshold float64 `json:"threshold"`
}

func (s *Server) handleConsolidate(w http.ResponseWriter, r *http.Request) {
	var b consolidateBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var inf *memorylayer.VectorInfra
	if s.RT != nil {
		inf = s.RT.VectorInfra()
	}
	tid, _ := s.tenantFrom(r)
	n, err := memorylayer.RunConsolidation(r.Context(), s.DB, inf, tid, b.SessionID, b.Threshold)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"promoted": n})
}

type graphEntityBody struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Kind  string         `json:"kind"`
	Props map[string]any `json:"props"`
}

func (s *Server) handleGraphEntity(w http.ResponseWriter, r *http.Request) {
	var b graphEntityBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	pj, _ := json.Marshal(b.Props)
	if err := memorylayer.UpsertEntity(s.DB, b.ID, b.Name, b.Kind, string(pj)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.RT != nil && s.RT.Neo != nil {
		if err := s.RT.Neo.UpsertEntity(r.Context(), b.ID, b.Name, b.Kind, string(pj)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

type graphLinkBody struct {
	EdgeID string         `json:"edge_id"`
	Src    string         `json:"src"`
	Dst    string         `json:"dst"`
	Rel    string         `json:"rel"`
	Props  map[string]any `json:"props"`
}

func (s *Server) handleGraphLink(w http.ResponseWriter, r *http.Request) {
	var b graphLinkBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	pj, _ := json.Marshal(b.Props)
	if err := memorylayer.Link(s.DB, b.EdgeID, b.Src, b.Dst, b.Rel, string(pj)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.RT != nil && s.RT.Neo != nil {
		if err := s.RT.Neo.Link(r.Context(), b.EdgeID, b.Src, b.Dst, b.Rel, string(pj)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleGraphNeighbors(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", 400)
		return
	}
	if s.RT != nil && s.RT.Neo != nil {
		rows, err := s.RT.Neo.Neighbors(r.Context(), id, 40)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"source": "neo4j", "neighbors": rows})
		return
	}
	http.Error(w, "neo4j not configured", http.StatusServiceUnavailable)
}

type optBody struct {
	SessionID   string `json:"session_id"`
	BudgetChars int    `json:"budget_chars"`
}

func (s *Server) handleOptimizeSession(w http.ResponseWriter, r *http.Request) {
	var b optBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tid, _ := s.tenantFrom(r)
	sum, fr, err := memorylayer.SessionOptimizer(s.DB, tid, b.SessionID, b.BudgetChars)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"summary": sum, "fragments": fr})
}

type cliBody struct {
	Command string `json:"command"`
}

func (s *Server) handleCLI(w http.ResponseWriter, r *http.Request) {
	var b cliBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	tier, outcome, out, err := tooling.ExecWithPolicy(ctx, s.DB, b.Command, s.Cfg.AllowHighRiskCLI)
	writeJSON(w, map[string]any{
		"risk_tier": string(tier),
		"outcome":   string(outcome),
		"output":    out,
		"error":     errString(err),
	})
}

func (s *Server) handleTopologyGet(w http.ResponseWriter, r *http.Request) {
	t, err := harness.LoadTopology(s.DB)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleScheduleList(w http.ResponseWriter, r *http.Request) {
	tid, admin := s.tenantFrom(r)
	list, err := scheduler.ListJobs(s.DB, tid, admin)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, list)
}

type scheduleCreateBody struct {
	Name         string `json:"name"`
	IntervalSec  int    `json:"interval_sec"`
	Action       string `json:"action"`
	PayloadJSON  string `json:"payload_json"`
	TargetTenant string `json:"target_tenant,omitempty"`
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var b scheduleCreateBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tid, admin := s.tenantFrom(r)
	jobTenant := tid
	if admin && strings.TrimSpace(b.TargetTenant) != "" {
		jobTenant = strings.TrimSpace(b.TargetTenant)
	}
	id, err := scheduler.CreateJob(s.DB, jobTenant, b.Name, b.IntervalSec, b.Action, b.PayloadJSON)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
