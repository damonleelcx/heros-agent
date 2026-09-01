package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/discovery"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intake"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/router"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/tools"
)

// Server is the console's HTTP surface.
type Server struct {
	Store    store.Store
	Planners *planner.Registry
	Sup      *Supervisor
	Resolver *intake.Resolver
	Router   router.Router
	Ceilings bounds.Ceilings
	Tenant   string

	// ToolRegistry, Provider and Model let the server rebind the assessment tool to the LOADED corpus.
	//
	// 🔴 The tool needs the repository the conversation is about, and that is chosen after the process
	// starts. A registry built once at boot holds a tool bound to no source — so the source is injected
	// when a subject is loaded, through Registry.Replace, which re-runs the same refusals rather than
	// mutating the map from outside.
	ToolRegistry *toolcontract.Registry
	Provider     provider.Provider
	Model        string
	// Approvals holds Tier-C changes between proposing and deciding.
	Approvals *approvals
	// Episodes is the episodic record, read by run history.
	Episodes memory.Store

	mu      sync.RWMutex
	subject *subjectState
}

// subjectState is the currently-loaded repository. One at a time, deliberately: a conversation is about
// one repository, and "which repo did that answer describe?" is not a question a person should have to ask.
type subjectState struct {
	Source intake.Source
	Corpus discovery.Corpus
	// Index holds the call sites, computed ONCE. Asking a Corpus for nine axes rescans it nine times —
	// 26 seconds on a 2,541-file repository, for an answer identical every time.
	Index *discovery.Index
}

// Routes returns the mux.
func (s *Server) Routes() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/subject", s.handleSubject)
	m.HandleFunc("GET /api/subject", s.handleGetSubject)
	m.HandleFunc("POST /api/ask", s.handleAsk)
	m.HandleFunc("GET /api/goals/{id}/events", s.handleEvents)
	m.HandleFunc("POST /api/decide", s.handleDecideRequest)
	return m
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ── subject ──────────────────────────────────────────────────────────────────────────────────────

type subjectReq struct {
	Ref string `json:"ref"`
}

type subjectResp struct {
	Reference string        `json:"reference"`
	Describe  string        `json:"describe"`
	Kind      string        `json:"kind"`
	Revision  string        `json:"revision"`
	Dirty     bool          `json:"dirty"`
	Files     int           `json:"files"`
	TestFiles int           `json:"test_files"`
	Truncated bool          `json:"truncated"`
	IsAgent   bool          `json:"is_agent"`
	Why       string        `json:"why"`
	Axes      []axisSummary `json:"axes"`
}

type axisSummary struct {
	Axis  string `json:"axis"`
	Found bool   `json:"found"`
	Spans int    `json:"spans"`
	Files int    `json:"files"`
	First string `json:"first,omitempty"`
	Why   string `json:"why,omitempty"`
	Note  string `json:"note,omitempty"`
}

func (s *Server) handleSubject(w http.ResponseWriter, r *http.Request) {
	var req subjectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	src, err := s.Resolver.Resolve(req.Ref)
	if err != nil {
		// 🔴 The intake error text is returned VERBATIM. Every one of them names a next action — run git
		// init, check the repository is public, give a path or a link — and replacing them with a generic
		// "could not load repository" would throw away the only part the person can act on.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	corpus, err := discovery.Walk(src.Root, discovery.Limits{})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ix := discovery.NewIndex(corpus)
	isAgent, why := ix.LooksLikeAnAgent()

	s.mu.Lock()
	s.subject = &subjectState{Source: src, Corpus: corpus, Index: ix}
	s.mu.Unlock()

	// Rebind every source-bound tool to this repository. Without this they would act on whatever was
	// loaded last, which is the worst kind of wrong: confident, well-formed, and about someone else's code.
	if s.ToolRegistry != nil && s.Provider != nil {
		_ = s.ToolRegistry.Replace(tools.AssessAxis{
			Provider: s.Provider, Model: s.Model, Source: ix,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.GenerateCases{
			Provider: s.Provider, Model: s.Model, Source: ix,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.PublishEvalSet{Root: src.Root},
			tools.NewPublishVerifier(src.Root))
		_ = s.ToolRegistry.Replace(tools.ProposeChange{
			Provider: s.Provider, Model: s.Model, Source: ix, Root: src.Root,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.VerifyProposal{
			Provider: s.Provider, Model: s.Model, Root: src.Root,
		}, nil)
		_ = s.ToolRegistry.Replace(tools.OpenPullRequest{Root: src.Root},
			tools.NewDeliveryVerifier(src.Root))
	}

	writeJSON(w, http.StatusOK, s.describeSubject(src, ix, isAgent, why))
}

func (s *Server) describeSubject(src intake.Source, ix *discovery.Index, isAgent bool, why string) subjectResp {
	resp := subjectResp{
		Reference: src.Reference, Describe: src.Describe(), Kind: string(src.Kind),
		Revision: src.Revision, Dirty: src.Dirty, Files: len(ix.Corpus.Files),
		TestFiles: ix.Corpus.Skipped["test-file"], Truncated: ix.Corpus.Truncated,
		IsAgent: isAgent, Why: why,
	}
	for _, axis := range intent.Axes() {
		ev := ix.ForAxis(axis)
		sum := axisSummary{Axis: axis, Found: ev.Found, Spans: len(ev.Spans), Note: ev.Note}
		files := map[string]bool{}
		for _, sp := range ev.Spans {
			files[sp.Path] = true
		}
		sum.Files = len(files)
		if len(ev.Spans) > 0 {
			sum.First, sum.Why = ev.Spans[0].Ref(), ev.Spans[0].Why
		}
		resp.Axes = append(resp.Axes, sum)
	}
	return resp
}

func (s *Server) handleGetSubject(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	sub := s.subject
	s.mu.RUnlock()
	if sub == nil {
		writeJSON(w, http.StatusOK, map[string]any{"loaded": false})
		return
	}
	isAgent, why := sub.Index.LooksLikeAnAgent()
	writeJSON(w, http.StatusOK, s.describeSubject(sub.Source, sub.Index, isAgent, why))
}

// ── ask ──────────────────────────────────────────────────────────────────────────────────────────

type askReq struct {
	Text string `json:"text"`
}

// askResp is what the console renders. Exactly one of the shapes below is populated, and `kind` says
// which — a response that could be two things at once is one the client has to guess about.
type askResp struct {
	Kind string `json:"kind"` // answer | goal | refusal | redirect | abstain

	Intent string `json:"intent,omitempty"`
	Tier   string `json:"tier,omitempty"`

	// answer
	Text     string       `json:"text,omitempty"`
	Episodes []episodeOut `json:"episodes,omitempty"`
	Axis     *axisSummary `json:"axis,omitempty"`
	Spans    []spanOut    `json:"spans,omitempty"`

	// goal
	// Scope is the axis the run was narrowed to, empty when it covers all nine. Sent so the console can
	// SHOW what it understood — a run that silently narrowed is as confusing as one that silently widened.
	Scope        string   `json:"scope,omitempty"`
	GoalID       string   `json:"goal_id,omitempty"`
	Tasks        []string `json:"tasks,omitempty"`
	CeilingCents int64    `json:"ceiling_cents,omitempty"`

	// proposal — a Tier-C change waiting for a person
	ChangeID       string `json:"change_id,omitempty"`
	Path           string `json:"path,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Diff           string `json:"diff,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// refusal
	Cause      string `json:"cause,omitempty"`
	NextAction string `json:"next_action,omitempty"`

	// redirect
	Surface string `json:"surface,omitempty"`
	Does    string `json:"does,omitempty"`

	// abstain
	CanDo []string `json:"can_do,omitempty"`
}

type episodeOut struct {
	Seq     int64  `json:"seq"`
	Kind    string `json:"kind"`
	TaskID  string `json:"task_id,omitempty"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
	At      string `json:"at"`
}

type spanOut struct {
	Ref  string `json:"ref"`
	Why  string `json:"why"`
	Text string `json:"text"`
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}

	// 🔴 Unbounded is checked BEFORE routing. The refusal has to happen before anything is planned, which
	// is the entire point of refusing — "keep going until it is perfect" must not first become a goal.
	if router.Unbounded(req.Text) {
		ref := bounds.Refusal{Cause: bounds.UnboundedRequested, Detail: req.Text}
		writeJSON(w, http.StatusOK, askResp{
			Kind: "refusal", Cause: string(ref.Cause), Text: req.Text, NextAction: ref.NextAction(),
		})
		return
	}

	out := s.Router.Route(req.Text)
	switch {
	case out.Redirect != nil:
		writeJSON(w, http.StatusOK, askResp{
			Kind: "redirect", Surface: out.Redirect.Surface, Does: out.Redirect.Does,
			Text: out.Redirect.Topic,
		})
		return
	case out.Abstained():
		writeJSON(w, http.StatusOK, askResp{Kind: "abstain", CanDo: intent.CanDo()})
		return
	}

	spec, _ := intent.Lookup(out.Intent)

	s.mu.RLock()
	sub := s.subject
	s.mu.RUnlock()
	if sub == nil {
		ref := bounds.Refusal{Cause: bounds.NoSubject}
		writeJSON(w, http.StatusOK, askResp{
			Kind: "refusal", Intent: out.Intent.String(), Cause: string(ref.Cause),
			NextAction: ref.NextAction(),
		})
		return
	}

	switch spec.Tier {
	case intent.TierQuery:
		s.answerQuery(w, spec, sub)
	case intent.TierGoal:
		s.startGoal(w, spec, sub, out.Axis)
	default:
		s.handleEffect(w, spec, sub, out.Axis, req.Text)
	}
}

// answerQuery serves a Tier-B intent from what discovery already read. No model call, no cost.
func (s *Server) answerQuery(w http.ResponseWriter, spec intent.Spec, sub *subjectState) {
	resp := askResp{Kind: "answer", Intent: spec.Intent.String(), Tier: string(spec.Tier)}

	if spec.Intent == intent.RunHistory {
		s.answerRunHistory(w, resp)
		return
	}
	if spec.Axis == "" {
		// Queries about the platform's own record rather than an axis, other than run history. Honest
		// placeholder: the record exists, the rendering does not.
		resp.Text = fmt.Sprintf("I can answer %q from what I have stored, but that view is not built yet. "+
			"Ask me about one of the nine axes and I will show you the code.", spec.Question)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	ev := sub.Index.ForAxis(spec.Axis)
	sum := axisSummary{Axis: spec.Axis, Found: ev.Found, Spans: len(ev.Spans), Note: ev.Note}
	files := map[string]bool{}
	for _, sp := range ev.Spans {
		files[sp.Path] = true
		if len(resp.Spans) < 6 {
			resp.Spans = append(resp.Spans, spanOut{Ref: sp.Ref(), Why: sp.Why, Text: sp.Text})
		}
	}
	sum.Files = len(files)
	if len(ev.Spans) > 0 {
		sum.First, sum.Why = ev.Spans[0].Ref(), ev.Spans[0].Why
	}
	resp.Axis = &sum

	if !ev.Found {
		resp.Text = ev.Note
	} else {
		resp.Text = fmt.Sprintf("Found %d span(s) across %d file(s) governing %s in %s. "+
			"This is read from what I already parsed — nothing ran just now, and it cost nothing.",
			len(ev.Spans), len(files), spec.Axis, shortRef(sub.Source.Reference))
	}
	writeJSON(w, http.StatusOK, resp)
}

// answerRunHistory reads the most recent run's episodes.
//
// 🔴 A query over what a durable run WROTE DOWN, not a re-derivation. That is the whole payoff of
// persisting everything: "what happened in that run?" is a SELECT, and it costs nothing.
func (s *Server) answerRunHistory(w http.ResponseWriter, resp askResp) {
	if s.Episodes == nil {
		resp.Text = "No run history is being recorded on this deployment."
		writeJSON(w, http.StatusOK, resp)
		return
	}
	last, ok, err := s.Store.LatestGoal(s.Tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		resp.Text = "Nothing has run yet. Ask me to look at your repository and I will start something."
		writeJSON(w, http.StatusOK, resp)
		return
	}
	eps, err := s.Episodes.Episodes(string(last.ID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(eps) == 0 {
		resp.Text = fmt.Sprintf("The last run (%s, %s) recorded no episodes.", last.Intent, last.State)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	for _, e := range eps {
		resp.Episodes = append(resp.Episodes, episodeOut{
			Seq: e.Seq, Kind: string(e.Kind), TaskID: e.TaskID,
			Summary: e.Summary, Detail: e.Detail, At: e.At.Format(time.RFC3339),
		})
	}
	resp.Text = fmt.Sprintf("The last run was %s (%s): %d steps, %s spent.",
		last.Intent, last.State, len(eps), provider.FormatCents(last.Spend.CostMicroCents))
	writeJSON(w, http.StatusOK, resp)
}

// criteriaFor returns what it means for THIS goal to be finished.
//
// # 🔴 Why this is per-intent rather than one default
//
// It was one default — "six of nine axes assessed" — applied to every Tier-A goal. An eval-set run has
// no axes, so it could never satisfy it: three generators succeeded, the quality gate passed, the
// artefact was written to the customer's repository, and the goal reported FAILED. Every visible sign
// said the work was done and the record said otherwise.
//
// A completion criterion has to describe the goal it belongs to. An objective borrowed from a different
// intent is not a weaker measure, it is a measure of something else.
func criteriaFor(i intent.Intent, axis string) []goal.Criterion {
	switch i {
	case intent.EvalSet:
		// The published artefact is the product. The gate already enforces its own floors on case count
		// and generator diversity, so the goal only has to see it reach publication.
		return []goal.Criterion{{Kind: goal.EvalCasesGenerated, Threshold: 1}}
	case intent.Compare:
		return []goal.Criterion{{Kind: goal.ComparisonDrawn, Threshold: 1}}
	case intent.Improve:
		// 🔴 A delivered change, not an assessment. Scoring improve on axes assessed let a run that
		// proposed a change, failed to verify it, and delivered nothing report SUCCESS — the same
		// "terminal is not an achievement" mistake in a third place.
		return []goal.Criterion{{Kind: goal.ChangesDelivered, Threshold: 1}}
	default:
		// assess. A narrowed run needs its one axis; a whole-repository run needs most of them, because
		// a report over two axes is not the report that was asked for.
		if axis != "" {
			return []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}}
		}
		return []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 6}}
	}
}

// startGoal admits a durable goal, plans it, and starts driving it.
func (s *Server) startGoal(w http.ResponseWriter, spec intent.Spec, sub *subjectState, axis string) {
	now := time.Now().UTC()
	g := &goal.Goal{
		ID: goal.ID(fmt.Sprintf("g-%d", now.UnixNano())), Tenant: s.Tenant,
		Intent: spec.Intent, State: goal.Draft, Objective: spec.Question,
		Subject: goal.Subject{
			RepoURL:  firstNonEmpty(sub.Source.RemoteURL, sub.Source.Root),
			Revision: sub.Source.Revision,
		},
		Ceilings:  s.Ceilings,
		CreatedAt: now, UpdatedAt: now,
	}
	// 🔴 The scope the person named is CARRIED, not discarded. "How do I improve the prompt?" names one
	// axis; planning nine spends nine times what was asked for, and the run reads as incoherent because
	// it is answering a question nobody put.
	//
	// The completion threshold moves with the scope for the same reason: a one-axis goal that requires
	// six assessed axes can never complete, so it would run to exhaustion and report a stall.
	if axis != "" {
		g.Axes = []string{axis}
		g.Objective = fmt.Sprintf("%s — scoped to the %s axis", spec.Question, axis)
	}
	g.Criteria = criteriaFor(spec.Intent, axis)
	if err := g.Admit(now); err != nil {
		var ref bounds.Refusal
		if asRefusal(err, &ref) {
			writeJSON(w, http.StatusOK, askResp{
				Kind: "refusal", Intent: spec.Intent.String(), Cause: string(ref.Cause),
				Text: ref.Detail, NextAction: ref.NextAction(),
			})
			return
		}
		writeJSON(w, http.StatusOK, askResp{
			Kind: "refusal", Intent: spec.Intent.String(), Cause: "not_admitted", NextAction: err.Error()})
		return
	}
	if err := s.Store.CreateGoal(g); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d, err := s.Planners.Build(g, now)
	if err != nil {
		writeJSON(w, http.StatusOK, askResp{
			Kind: "refusal", Intent: spec.Intent.String(), Cause: "could_not_plan", NextAction: err.Error()})
		return
	}
	if err := s.Store.SaveDAG(d); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ids := sortedTaskIDs(d)
	// 🔴 Driven with context.Background(), NOT the request's context. The request ends when the browser
	// has its goal id; the run outlives it by design. Tying a durable goal's lifetime to one HTTP request
	// is how a refresh cancels an hour of work.
	s.Sup.Start(context.Background(), g.ID)

	writeJSON(w, http.StatusOK, askResp{
		Kind: "goal", Intent: spec.Intent.String(), Tier: string(spec.Tier),
		GoalID: string(g.ID), Tasks: ids, CeilingCents: g.Ceilings.MaxCostCents,
		Text: g.Objective, Scope: axis,
	})
}

// handleDecideRequest decodes an approval decision.
func (s *Server) handleDecideRequest(w http.ResponseWriter, r *http.Request) {
	var req decideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	s.handleDecide(w, req)
}

// ── events ───────────────────────────────────────────────────────────────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := goal.ID(r.PathValue("id"))
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.Sup.Subscribe(id)
	defer s.Sup.Unsubscribe(id, ch)

	// A heartbeat keeps intermediaries from closing an idle stream, and lets the browser tell "the run is
	// quiet" from "the connection died" — which look identical without it.
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case e, open := <-ch:
			if !open {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			if e.Terminal {
				return
			}
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

// sortedTaskIDs returns the plan's task ids in a stable order, so the console renders the same ledger
// on every load rather than a set in map order.
func sortedTaskIDs(d *task.DAG) []string {
	out := make([]string, 0, len(d.Tasks))
	for id := range d.Tasks {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func shortRef(s string) string {
	if len(s) > 48 {
		return "…" + s[len(s)-47:]
	}
	return s
}

func asRefusal(err error, out *bounds.Refusal) bool {
	r, ok := err.(bounds.Refusal)
	if ok {
		*out = r
	}
	return ok
}
