package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/herosagent"
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
