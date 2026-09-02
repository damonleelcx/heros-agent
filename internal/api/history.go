package api

import (
	"net/http"
	"sort"

	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// pastGoal is one run, as much of it as the console needs to redraw the thread.
type pastGoal struct {
	ID        string   `json:"id"`
	Intent    string   `json:"intent"`
	State     string   `json:"state"`
	Objective string   `json:"objective"`
	Axes      []string `json:"axes,omitempty"`
	Tasks     int      `json:"tasks"`
	Done      int      `json:"done"`
	CostMicro int64    `json:"cost_micro_cents"`
	Ceiling   int64    `json:"ceiling_cents"`
	Reference string   `json:"reference,omitempty"`
	// 🔴 Why it ended that way, and what to do about it. A card that says "failed, 0/10 tasks, $0.00"
	// and nothing else is not a report, it is a shrug: every fact needed to explain it was already in
	// the run record, and none of it was sent. Optional fields rather than a new endpoint — the
	// consumer is the same card.
	Cause      string `json:"cause,omitempty"`
	Detail     string `json:"detail,omitempty"`
	NextAction string `json:"next_action,omitempty"`
}

// handleHistory lists this organization's runs, newest last, so the console can rebuild the
// conversation after a refresh.
//
// # 🔴 What this can and cannot restore, stated because the gap is the interesting part
//
// A refresh used to empty the thread entirely: it lived in browser memory and nothing read it back.
// Runs ARE durable — they are rows — so they come back with their question, their plan, their spend and
// their outcome.
//
// ⚠️ This comment used to go on to say that Tier-B answers do NOT come back and that no endpoint could
// bring them, because storing them "would mean a transcript table, which was weighed and declined".
// That decision was REVERSED: `conversation_turns` exists, and `GET /api/conversation` replays the
// whole thread. Corrected rather than deleted, because the argument it recorded — that a history
// silently omitting half of what was said is worse than one that admits its shape — is still the reason
// this endpoint says out loud what it is redrawing.
//
// 🔴 The two endpoints stay separate, and that is not redundancy. A sentence is FINAL the moment it is
// said, so it replays verbatim from the transcript. A run keeps changing after the sentence that
// started it, so its card must be rebuilt from the goal record — never from a copy frozen into the
// transcript, which would go stale against the run it describes and show a finished run as pending
// forever.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	if s.Root == nil {
		writeJSON(w, http.StatusOK, map[string]any{"goals": []pastGoal{}})
		return
	}
	// The scoped store answers for this organization only; the tenant is never taken from the request.
	goals, err := s.Root.For(tenant).ListGoals("")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Newest last, so the console can append them in order and the thread reads top to bottom the way a
	// conversation does. Goal ids carry their creation time, so sorting by id is chronological.
	sort.Slice(goals, func(i, j int) bool { return goals[i].ID < goals[j].ID })

	// 🔴 Bounded. An organization that has run a hundred goals should not have a hundred cards pushed at
	// it before it can type, and the browser should not have to lay them out. The most recent are the
	// ones anybody is coming back to.
	const most = 12
	if len(goals) > most {
		goals = goals[len(goals)-most:]
	}

	out := make([]pastGoal, 0, len(goals))
	for _, g := range goals {
		p := pastGoal{
			ID: string(g.ID), Intent: g.Intent.String(), State: string(g.State),
			Objective: g.Objective, Axes: g.Axes,
			CostMicro: g.Spend.CostMicroCents, Ceiling: g.Ceilings.MaxCostCents,
			Reference: g.Subject.RepoURL,
		}
		if g.Refusal != nil {
			p.Cause = string(g.Refusal.Cause)
			p.Detail = g.Refusal.Detail
			p.NextAction = g.Refusal.NextAction()
		}
		if d, derr := s.Root.For(tenant).LoadDAG(g.ID); derr == nil && d != nil {
			p.Tasks = len(d.Tasks)
			for _, t := range d.Tasks {
				// 🔴 The task package's own constant, not a string typed here. A literal would be a
				// second definition of "finished" that nothing keeps in step with the first.
				if t.State == task.Succeeded {
					p.Done++
				}
			}
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"goals": out})
}
