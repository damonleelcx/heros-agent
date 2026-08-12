package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// herosagent.go is P30 workstream 7's platform half: serve the active definition to a customer-placed
// tenant, and accept what that tenant's own runner produced.
//
// # Why the ingest is NOT a new route
//
// Task 7.3 is one sentence — "no second transport" — and it is a security requirement rather than a
// tidiness one. A `/api/v1/heros-results` beside the structure ingest would carry the same bytes with
// its own authentication, its own allowlist, its own `DisallowUnknownFields` decision and its own
// chance of disagreeing about what a workflow id means. It would also need its own ingress rule, its
// own exposure classification and its own place in the fence — four artefacts that must agree, where
// today there is one.
//
// So the HEROS result rides the payload that already carries a workflow's shape, on the route that
// already exists, under the contract that already versions it. See handleWorkflowIRIngest.

// HerosAgentSource is the platform's analysis agent, as this package needs it. Optional like every
// other mount: nil answers 503, which is a policy answer rather than a failure.
type HerosAgentSource interface {
	// PlacementFor returns a tenant's EFFECTIVE placement — `disabled` when nobody has set one (Q2).
	//
	// 🔴 It is read here and never taken from a request. A payload that could name its own placement
	// would be a payload that could enable itself.
	PlacementFor(ctx context.Context, tenantID string) (herosagent.Placement, error)
	// ActiveDefinition returns the definition a customer-placed runner would execute, already rendered.
	// ok=false means nothing is published and activated — a real state on a fresh deployment.
	ActiveDefinition(ctx context.Context) (runlink.AgentDefinition, bool, error)
	// Accept ingests a customer-placed result. It applies the confidence floor and refuses an unknown
	// `agent_config_hash` by name.
	Accept(ctx context.Context, sub herosagent.Submission) (herosagent.IngestResult, error)
	// NarrativeFor returns the agent's prose about one workflow, from the inference that produced its
	// inferred facts. ok=false is NO NARRATIVE — a normal outcome, since the agent may produce edges and
	// no prose, and since D2 stores an abstention-only inference too.
	//
	// 🔴 It is a READ of what the agent said, never a summary this platform composes. An `ok` of false
	// renders NOTHING: prose assembled from the composition would appear in the `assessed` treatment,
	// which tells a reader a model wrote it.
	NarrativeFor(ctx context.Context, tenantID, workflowID string) (string, bool, error)
}

// SetAgentReadiness wires the agent's `/readyz` entry (task 9.1).
//
// Separate from MountHerosAgent, and the separation is the point: a deployment can serve the definition
// read without being able to resolve a credential, and one that reported readiness from the fact that
// the surface is mounted would be asserting from configuration — which is precisely what 9.1 forbids.
func (s *Server) SetAgentReadiness(fn func(context.Context) herosagent.Readiness) {
	s.agentReadiness = fn
}

// MountHerosAgent registers the definition read. The INGEST half needs no route of its own — it rides
// the structure ingest, which is the whole of task 7.3.
// The path is written as a LITERAL rather than as `"GET "+runlink.AgentDefinitionPath`, matching
// MountWorkflowIR and every other mount here. It reads like duplication and it is load-bearing:
// `registeredRoutes` in ingress_fence_test.go extracts `HandleFunc` arguments from the SOURCE, and a
// concatenation is invisible to it — a route registered that way is a route the ingress fence cannot
// see, which is the precise blindness that doc describes on the transport side. The drift between this
// literal and the constant is itself fenced from both directions: TestEveryPathTheCLIAddressesIsPublished
// would find the transport addressing a path nothing declares, and TestNoStaleRouteClassifications would
// find a declaration nothing registers.
func (s *Server) MountHerosAgent(src HerosAgentSource) {
	s.herosAgent = src
	s.Mux.HandleFunc("GET /api/v1/agent-definition", s.handleAgentDefinition)
}

func (s *Server) handleAgentDefinition(w http.ResponseWriter, r *http.Request) {
	if s.herosAgent == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "this deployment runs no analysis agent"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{
			Error: "reading the agent definition requires an authenticated tenant"})
		return
	}

	placement, err := s.herosAgent.PlacementFor(r.Context(), principal.TenantID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, specError{Error: "could not read this tenant's placement: " + err.Error()})
		return
	}

	// 🔴 THE PLACEMENT IS ALWAYS ANSWERED, including when everything else is withheld. A 403 with no
	// placement would leave a CLI unable to tell "you are disabled" from "the platform analyses you",
	// and those two send a developer to opposite places — one asks an operator to turn HEROS on, the
	// other goes and looks at the console for an answer that is already there.
	out := runlink.AgentDefinition{
		ContractVersion: runlink.AgentDefinitionContractVersion,
		Placement:       string(placement),
	}
	if placement != herosagent.PlacementCustomer {
		// 🚫 No definition. The prompt crosses exactly where executing it is the point (see
		// runlink/agentdefinition.go), and for these two placements it is not.
		writeJSON(w, http.StatusOK, out)
		return
	}

	def, ok, err := s.herosAgent.ActiveDefinition(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, specError{Error: "could not read the active definition: " + err.Error()})
		return
	}
	if !ok {
		// Nothing published and activated. A real state, and NOT an error: a fresh deployment has no
		// definition, and a customer-placed tenant on it should be told there is nothing to run rather
		// than shown a failure.
		writeJSON(w, http.StatusOK, out)
		return
	}
	def.ContractVersion = runlink.AgentDefinitionContractVersion
	def.Placement = string(placement)
	writeJSON(w, http.StatusOK, def)
}

// agentSubmissionFrom projects the wire payload onto the ingest's own vocabulary.
//
// The mapping lives HERE because this is the layer that sees both: `internal/runlink` must not import
// `internal/herosagent` (the egress package does not import the thing whose output it constrains), and
// putting the wire's shape inside the ingest's rules would make the rules depend on a format.
func agentSubmissionFrom(tenantID string, placement herosagent.Placement,
	p runlink.WorkflowIRPayload) herosagent.Submission {

	sub := herosagent.Submission{
		// 🔴 The AUTHENTICATED tenant, never anything in the body — the payload has no tenant field at
		// all, which is the strongest form of "this cannot widen scope".
		TenantID:        tenantID,
		WorkflowID:      p.WorkflowID,
		SourceRevision:  p.SourceRevision,
		AgentConfigHash: p.AgentConfigHash,
		Placement:       placement,
		NodeIDs:         make([]string, 0, len(p.Nodes)),
		Edges:           make([]herosagent.SubmittedEdge, 0, len(p.Edges)),
	}
	for _, n := range p.Nodes {
		sub.NodeIDs = append(sub.NodeIDs, n.NodeID)
	}
	for _, e := range p.Edges {
		sub.Edges = append(sub.Edges, herosagent.SubmittedEdge{
			From: e.From, To: e.To, Kind: e.Kind, Author: e.Author, Confidence: e.Confidence,
		})
	}
	for _, a := range p.Abstentions {
		sub.Abstentions = append(sub.Abstentions, herosagent.SubmittedAbstention{
			Subject: a.Subject, Cause: a.Cause, Confidence: a.Confidence,
		})
	}
	return sub
}

// acceptAgentFacts runs the HEROS half of a structure ingest, and reports whether the caller may
// continue storing the structure.
//
// 🔴 A refusal here fails the WHOLE request, and nothing is stored — not the inference and not the
// structure. The alternative is worse than it sounds: storing the structure and dropping the inference
// would give the customer a 201 for a submission whose most important half was discarded, and their
// console would then show a graph with no inferred edges and no statement that any were refused.
func (s *Server) acceptAgentFacts(w http.ResponseWriter, r *http.Request,
	tenantID string, p runlink.WorkflowIRPayload) (proceed bool) {

	placement := herosagent.PlacementDisabled
	if s.herosAgent != nil {
		got, err := s.herosAgent.PlacementFor(r.Context(), tenantID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, specError{Error: "could not read this tenant's placement: " + err.Error()})
			return false
		}
		placement = got
	}
	sub := agentSubmissionFrom(tenantID, placement, p)

	// 🔴 A payload with no agent facts is the PRE-P30 shape, and it must pass through untouched. Q2
	// makes `disabled` the default, so on the day this ships every tenant is disabled — an ingest that
	// refused a whole payload for a disabled tenant would break `heros link --with-ir` for the entire
	// fleet at once, which is the same "nothing fills on deploy" hazard read from the other side.
	if !sub.HasAgentFacts() {
		return true
	}

	if s.herosAgent == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment runs no analysis agent, so it cannot verify which agent version " +
				"produced these facts — nothing was stored"})
		return false
	}

	if _, err := s.herosAgent.Accept(r.Context(), sub); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, herosagent.ErrWrongPlacement) {
			// 403 rather than 400: the submission is well-formed and this tenant is not permitted to make
			// it. A 400 would send a developer to look for a malformed payload they do not have.
			status = http.StatusForbidden
		}
		writeJSON(w, status, map[string]any{
			"error":     err.Error(),
			"placement": string(placement),
			"accepted":  false,
		})
		return false
	}
	return true
}

// agentPanelFor builds the graph page's agent panel from the LIVE placement (tasks 8.2, 8.6–8.8).
//
// # Why a failure here returns a panel rather than an error
//
// 🚫 A HEROS failure must never become a full-screen error (task 8.8). Everything else on a graph page —
// the nodes, the edges a frontend established, the rule labels, the composition — was produced without
// the agent and is unaffected by its outage. Replacing all of it with an error would make an OPTIONAL
// subsystem's failure look like a total loss of the customer's data, which is both false and the most
// alarming possible way to be false.
//
// So every path below returns a *ViewAgent. The panel says what happened; the page renders.
func (s *Server) agentPanelFor(r *http.Request, tenantID string,
	view patternclassifier.GraphView) *patternclassifier.ViewAgent {

	if s.herosAgent == nil {
		// 🔴 NIL, not a panel. This deployment runs no agent at all, so there is no state to report and
		// no action to withhold — a panel reading "not analysed" would imply an agent that could be
		// switched on, and on this deployment there is nothing to switch.
		return nil
	}

	placement, err := s.herosAgent.PlacementFor(r.Context(), tenantID)
	if err != nil {
		// We could not read the placement, so we do not know whether anything analysed this workflow.
		// `unavailable`, never `not_analysed`: one is a fault on our side and the other is a claim that
		// nobody has looked, and only one of them is something we actually know.
		return patternclassifier.AgentUnavailable("this organization's analysis setting could not be read")
	}

	inferred := view.Composition.EdgesInferred > 0 || view.Composition.NodesCoveredInferred > 0
	// Read ONLY when something was actually inferred. A narrative attached to a graph carrying no
	// inferred fact would be prose about an analysis whose conclusions are not on the page.
	narrative := ""
	if inferred {
		if got, ok, nerr := s.herosAgent.NarrativeFor(r.Context(), tenantID, view.WorkflowID); nerr == nil && ok {
			narrative = got
		}
		// 🚫 A narrative read that FAILS is not a panel failure. The inferred facts are on the page and
		// are real; losing the prose about them costs a paragraph, and turning that into an
		// `unavailable` panel would hide the facts to report the loss of their commentary.
	}

	switch placement {
	case herosagent.PlacementDisabled:
		return patternclassifier.AgentNotAnalysed(string(placement),
			"Agent analysis is off for this organization, which is the default. Everything on this page "+
				"was established by reading your source. An operator enables analysis per organization.")

	case herosagent.PlacementCustomer:
		panel := &patternclassifier.ViewAgent{
			Placement: string(placement),
			// 🔴 The ACTION is offered even though the platform cannot run it, because the reader CAN
			// (task 8.7). "Your organization runs this itself" with no next step reads as a dead end.
			Action: patternclassifier.ActionRunLocally,
			// 🔴 NO BACKTICKS. The console renders this string as plain text, so a backtick is a
			// backtick — the page read "Run `heros analyse --ir <path>`", which looks like markdown
			// that failed to render and makes the one actionable sentence on the panel look broken.
			// Found by reading the rendered page, not the string.
			ActionReason: "Analysis for this organization runs on your own machine under your own " +
				"provider credential — this platform never holds one. Run heros analyse --ir <path> " +
				"against the IR that heros discover wrote, and the result arrives here.",
		}
		fillAgentState(panel, inferred, narrative,
			"These inferred facts were produced on your own machine and submitted from there. This "+
				"platform did not read your source to obtain them.")
		return panel

	case herosagent.PlacementPlatform:
		panel := &patternclassifier.ViewAgent{
			Placement:    string(placement),
			Action:       patternclassifier.ActionAnalyse,
			ActionReason: "",
		}
		fillAgentState(panel, inferred, narrative,
			"These inferred facts were produced by this platform, reading your source under the "+
				"platform's own provider credential.")
		return panel
	}

	// A placement outside the closed set. It cannot arrive from the store — `ParsePlacement` refuses one
	// — so reaching here means something upstream invented a value, and inventing a reading for it is
	// exactly how a fifth state ships looking like one of the four.
	return patternclassifier.AgentUnavailable(
		"this organization carries an analysis setting this build does not recognise")
}

// fillAgentState resolves the two fields that depend on whether anything was actually inferred.
//
// 🔴 The narrative is carried through verbatim or left EMPTY. 🚫 It is never assembled from the
// composition to fill the space: prose generated from counts would render in the `assessed` treatment —
// which tells a reader a model wrote it — while actually having been written by a template here.
func fillAgentState(panel *patternclassifier.ViewAgent, inferred bool,
	narrative, placementSentence string) {

	if !inferred {
		// Analysis is ON and this graph carries nothing from it. The STATE is `not_analysed` and the
		// SENTENCE is not the switched-off one — see SentenceNotAnalysedYet for the defect that
		// distinction fixes. Found by reading the rendered page for a `platform`-placed organization,
		// which said analysis was off while it was running.
		panel.State = patternclassifier.StateNotAnalysed
		panel.StateSentence = patternclassifier.SentenceNotAnalysedYet
		return
	}
	panel.State = patternclassifier.StateInferred
	panel.StateSentence = patternclassifier.SentenceForState(patternclassifier.StateInferred)
	panel.PlacementSentence = placementSentence
	panel.Narrative = narrative
}
