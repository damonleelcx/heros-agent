package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

// p6.go mounts the P6 autonomous-optimizer governance surface, following the p55.go pattern: one Mount
// method, a nil-able source interface, 503 when unmounted, the monitor page embedded.
//
// The surface's load-bearing rules are structural at the HTTP boundary:
//   - The authority GRANT names the tradeoff and records the constraints in the audit trail before the
//     loop may merge anything (spec Requirement "Autonomous SHALL be a distinct automation level with an
//     explicit, recorded authority grant").
//   - The STOP action is wired to the kill switch and takes effect immediately — no candidate merges
//     after it fires.
//   - Every merged change exposes a `git revert` ROLLBACK; the server performs the revert and audits it.
//   - The monitor reads the loop's own P2.5 progress signal (current iteration / cumulative spend / PRs
//     merged / streaming audit), never a derived state that can drift from the ledger.

//go:embed static/p6.html
var p6HTML []byte

// P6Source is what the API asks of the optimizer controller: the live monitor for a run, and the four
// governance actions (grant, stop, re-arm, rollback). Each action returns the refreshed monitor so the
// UI never renders a state the server did not just compute.
type P6Source interface {
	Monitor(runID string) (Monitor, bool)
	Grant(req GrantRequest) (Monitor, error)
	Stop(runID, actor string) (Monitor, error)
	Rearm(runID, actor string) (Monitor, error)
	Rollback(runID, mergeCommit, actor string) (Monitor, error)
}

// GrantRequest is the Autonomous authority-grant payload (task 7.1): the user sets the hard constraints
// at grant time. The server records them and names the tradeoff before the loop may merge.
type GrantRequest struct {
	RunID             string   `json:"run_id"`
	WorkflowID        string   `json:"workflow_id"`
	Actor             string   `json:"actor"`
	BudgetCeilingUSD  float64  `json:"budget_ceiling_usd"`
	ProviderAllowlist []string `json:"provider_allowlist"`
	MinImprovement    float64  `json:"min_improvement"`
	MaxIterations     int      `json:"max_iterations"`
}

// Monitor is the whole P6 read model for one run — the live governance view. Every field is read from
// the loop's structured progress + the change ledger, so nothing here can contradict the audit trail.
type Monitor struct {
	RunID           string `json:"run_id"`
	WorkflowID      string `json:"workflow_id"`
	AutomationLevel string `json:"automation_level"` // advisory | assisted | autonomous
	// State is the first-class run state, each visually distinct in the UI (task 7.6):
	// running | converged | max_iter | halted_regression | halted_budget | stalled | stopped | rolled_back.
	State        string `json:"state"`
	StateLabel   string `json:"state_label"`
	MergeEnabled bool   `json:"merge_enabled"`
	// DisarmReason / StopReason are content-as-interface (task 7.2): "halted — quality regressed on
	// cluster X, apply disarmed", not "error".
	DisarmReason string `json:"disarm_reason,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	// MissingPrereqs names any un-armed prerequisite; non-empty means the run is a dry-run.
	MissingPrereqs []string `json:"missing_prereqs,omitempty"`
	DryRun         bool     `json:"dry_run"`

	BudgetCeilingUSD   float64  `json:"budget_ceiling_usd"`
	CumulativeSpendUSD float64  `json:"cumulative_spend_usd"`
	MaxIterations      int      `json:"max_iterations"`
	CurrentIteration   int      `json:"current_iteration"`
	MinImprovement     float64  `json:"min_improvement"`
	ProviderAllowlist  []string `json:"provider_allowlist,omitempty"`
	PRsMerged          int      `json:"prs_merged"`

	Merges []MergeView `json:"merges"`
	Audit  []AuditView `json:"audit"`
}

// MergeView is one merged change with its `git revert` rollback control and the evidence the control
// shows: the motivating diagnosis, the verified delta, and the cost/latency impact (task 7.5).
type MergeView struct {
	Idx            int     `json:"idx"`
	FromConfigHash string  `json:"from_config_hash"`
	ToConfigHash   string  `json:"to_config_hash"`
	MergeCommit    string  `json:"merge_commit"`
	PRRef          string  `json:"pr_ref"`
	DiagnosisID    string  `json:"diagnosis_id"`
	Operator       string  `json:"operator"`
	Node           string  `json:"node_id"`
	VerifiedDelta  float64 `json:"verified_delta"`
	CostDelta      float64 `json:"cost_delta"`
	LatencyDelta   float64 `json:"latency_delta"`
	RolledBack     bool    `json:"rolled_back"`
	RevertCommit   string  `json:"revert_commit,omitempty"`
}

// AuditView is one streaming audit-trail row (change ledger + git history), virtualized in the UI for
// long runs (task 7.6).
type AuditView struct {
	Seq         int    `json:"seq"`
	Type        string `json:"type"`
	Summary     string `json:"summary"`
	MergeCommit string `json:"merge_commit,omitempty"`
	TS          string `json:"ts"`
}

// MountP6 registers the P6 governance UI, its monitor JSON endpoint, and the four audited actions.
func (s *Server) MountP6(src P6Source) {
	s.p6 = src
	s.Mux.HandleFunc("GET /p6/monitor", s.handleP6UI)
	s.Mux.HandleFunc("GET /api/p6/runs/{run_id}/monitor", s.handleP6Monitor)
	s.Mux.HandleFunc("POST /api/p6/grant", s.handleP6Grant)
	s.Mux.HandleFunc("POST /api/p6/runs/{run_id}/stop", s.handleP6Stop)
	s.Mux.HandleFunc("POST /api/p6/runs/{run_id}/rearm", s.handleP6Rearm)
	s.Mux.HandleFunc("POST /api/p6/runs/{run_id}/rollback", s.handleP6Rollback)
}

func (s *Server) handleP6UI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p6HTML)
}

func (s *Server) handleP6Monitor(w http.ResponseWriter, r *http.Request) {
	if s.p6 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p6 surface is not mounted on this server"})
		return
	}
	m, ok := s.p6.Monitor(r.PathValue("run_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no run " + r.PathValue("run_id")})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleP6Grant(w http.ResponseWriter, r *http.Request) {
	if s.p6 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p6 surface is not mounted on this server"})
		return
	}
	var req GrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid grant payload"})
		return
	}
	m, err := s.p6.Grant(req)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleP6Stop(w http.ResponseWriter, r *http.Request) {
	s.p6Action(w, r, func(runID, actor string) (Monitor, error) { return s.p6.Stop(runID, actor) })
}

func (s *Server) handleP6Rearm(w http.ResponseWriter, r *http.Request) {
	s.p6Action(w, r, func(runID, actor string) (Monitor, error) { return s.p6.Rearm(runID, actor) })
}

func (s *Server) handleP6Rollback(w http.ResponseWriter, r *http.Request) {
	if s.p6 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p6 surface is not mounted on this server"})
		return
	}
	var body struct {
		MergeCommit string `json:"merge_commit"`
		Actor       string `json:"actor"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	m, err := s.p6.Rollback(r.PathValue("run_id"), body.MergeCommit, body.Actor)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// p6Action is the shared shape for the stop/re-arm POSTs: decode the actor, run the action, return the
// refreshed monitor.
func (s *Server) p6Action(w http.ResponseWriter, r *http.Request, fn func(runID, actor string) (Monitor, error)) {
	if s.p6 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p6 surface is not mounted on this server"})
		return
	}
	var body struct {
		Actor string `json:"actor"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	m, err := fn(r.PathValue("run_id"), body.Actor)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}
