package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// PromptOptimize implements a DSPy-style / self-refine prompt optimizer that proposes a prompt edit
// GROUNDED in the specific failing cases attached to a diagnosis — never a generic "make it better"
// (design Decision 3, §2). The produced edit is traceable to those cases via a content-hashed
// grounding bundle, and a rewrite that is not grounded in the attached cases is rejected.
//
// This is the seam. A real optimizer calls an LLM over the failing-case traces; the in-repo
// SelfRefineOptimizer is a deterministic, transparent implementation that synthesizes the format
// constraint the failures imply and records exactly which cases grounded it, so the grounding
// property is testable without a provider.
type PromptOptimizer interface {
	// Optimize proposes a prompt edit from the base prompt and the failing cases. It MUST return
	// ErrUngrounded when it has no failing cases to ground on — an ungrounded rewrite is inert and
	// unfalsifiable, so it is refused rather than emitted.
	Optimize(req PromptOptimizeRequest) (PromptEdit, error)
}

// ErrUngrounded is returned when a rewrite would not be grounded in any attached failing case.
var ErrUngrounded = errors.New("proposal: prompt rewrite is not grounded in any attached failing case")

// FailingCaseGrounding is the minimal, PII-free projection of one failing case the optimizer grounds
// on: the case id, the observed failure reason, and the (already content-hashed) trace reference. The
// raw trace is NEVER inlined here — it lives in the object store keyed by TraceRef (§2.3).
type FailingCaseGrounding struct {
	CaseID string `json:"case_id"`
	// FailureReason is the short, structured reason the P4.5 attribution recorded (e.g.
	// "output missing required field `label`"). It is the signal the rewrite must answer.
	FailureReason string `json:"failure_reason"`
	// TraceRef is the content hash of the full failing trace in the object store.
	TraceRef string `json:"trace_ref"`
}

// PromptOptimizeRequest is the optimizer input.
type PromptOptimizeRequest struct {
	NodeID string `json:"node_id"`
	// BasePromptRef is the current prompt registry version_id (for the grounding record).
	BasePromptRef string `json:"base_prompt_ref"`
	// BasePromptBody is the rendered current prompt the edit refines.
	BasePromptBody string `json:"base_prompt_body"`
	// FailingCases are the specific cases the diagnosis attached as evidence. An empty set is refused.
	FailingCases []FailingCaseGrounding `json:"failing_cases"`
	// RequiredFields, when non-empty, is the output contract the failures violated — the optimizer adds
	// a format constraint pinning them (design Decision 3: "add format-constraint/schema where the
	// contract was violated").
	RequiredFields []string `json:"required_fields,omitempty"`
}

// PromptEdit is the optimizer's output: a new prompt body, the format-constraint it added, and the
// content-hashed grounding bundle that makes it traceable to its generating cases.
type PromptEdit struct {
	// NewPromptBody is the rewritten prompt. In a real deployment this is registered and becomes a new
	// prompt_ref; here it is carried so the caller can register it.
	NewPromptBody string `json:"new_prompt_body"`
	// FormatConstraint is the appended instruction pinning the violated output contract (may be empty
	// when no contract was violated).
	FormatConstraint string `json:"format_constraint,omitempty"`
	// Grounding is the persisted, content-hashed bundle: which cases grounded this edit.
	Grounding GroundingBundle `json:"grounding"`
}

// GroundingBundle is the traceability record for a prompt rewrite — the content-hashed set of failing
// cases the edit was derived from. Persisting it (as a content-hashed blob, §2.3) is what lets
// verification later attribute a held-out gain to a specific, motivated change rather than noise.
type GroundingBundle struct {
	NodeID        string                 `json:"node_id"`
	BasePromptRef string                 `json:"base_prompt_ref"`
	Cases         []FailingCaseGrounding `json:"cases"`
	// Hash is SHA-256 over the canonical (node, base prompt, sorted cases) projection. It is the blob
	// key and the traceability anchor.
	Hash string `json:"hash"`
}

// GroundedIn reports whether the bundle is grounded in the given case id.
func (g GroundingBundle) GroundedIn(caseID string) bool {
	for _, c := range g.Cases {
		if c.CaseID == caseID {
			return true
		}
	}
	return false
}

// newGrounding builds and content-hashes a grounding bundle from the request. Cases are sorted by id
// so the hash is a pure function of the grounding content, not of input order.
func newGrounding(req PromptOptimizeRequest) (GroundingBundle, error) {
	if len(req.FailingCases) == 0 {
		return GroundingBundle{}, ErrUngrounded
	}
	cases := append([]FailingCaseGrounding(nil), req.FailingCases...)
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].CaseID < cases[j].CaseID })
	g := GroundingBundle{NodeID: req.NodeID, BasePromptRef: req.BasePromptRef, Cases: cases}
	payload := struct {
		Node  string                 `json:"node"`
		Base  string                 `json:"base"`
		Cases []FailingCaseGrounding `json:"cases"`
	}{req.NodeID, req.BasePromptRef, cases}
	raw, err := json.Marshal(payload)
	if err != nil {
		return GroundingBundle{}, err
	}
	sum := sha256.Sum256(raw)
	g.Hash = hex.EncodeToString(sum[:])
	return g, nil
}

// SelfRefineOptimizer is the in-repo grounded optimizer. It is deterministic and transparent: it
// derives a format constraint from the failing cases' reasons and required fields, appends it to the
// base prompt, and records the exact cases it grounded on. A real LLM optimizer implements the same
// interface; this one exists so the grounding + traceability properties are provable without a
// provider (the codebase's "the only stub is the provider" discipline).
type SelfRefineOptimizer struct{}

// Optimize refines the base prompt using the attached failing cases. It refuses (ErrUngrounded) when
// no cases are attached.
func (SelfRefineOptimizer) Optimize(req PromptOptimizeRequest) (PromptEdit, error) {
	g, err := newGrounding(req)
	if err != nil {
		return PromptEdit{}, err
	}

	// Build the format constraint from the violated contract fields and the observed failure reasons.
	var b strings.Builder
	if len(req.RequiredFields) > 0 {
		fields := append([]string(nil), req.RequiredFields...)
		sort.Strings(fields)
		b.WriteString("You MUST return a JSON object containing exactly these fields: ")
		b.WriteString(strings.Join(fields, ", "))
		b.WriteString(".")
	}
	// Ground the instruction in the specific observed failures, so the edit is legibly derived from
	// them rather than generic.
	reasons := distinctReasons(g.Cases)
	if len(reasons) > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("Avoid the following observed failures: ")
		b.WriteString(strings.Join(reasons, "; "))
		b.WriteString(".")
	}
	constraint := b.String()

	newBody := strings.TrimRight(req.BasePromptBody, "\n")
	if constraint != "" {
		if newBody != "" {
			newBody += "\n\n"
		}
		newBody += constraint
	}
	return PromptEdit{NewPromptBody: newBody, FormatConstraint: constraint, Grounding: g}, nil
}

func distinctReasons(cases []FailingCaseGrounding) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cases {
		r := strings.TrimSpace(c.FailureReason)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
