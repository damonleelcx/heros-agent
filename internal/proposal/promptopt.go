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

// ErrNoChange is returned when a strategy is grounded but finds nothing to change in the base prompt —
// no exemplar to curate, no redundancy to remove, no slack to compress. It is the "silent" half of
// grounded-or-silent (P13 §2): an operator that has nothing to do emits no candidate, and this is not
// an error the batch reports. Distinct from ErrUngrounded (no cases to ground on at all).
var ErrNoChange = errors.New("proposal: prompt strategy is grounded but has no change to make")

// PromptStrategy names the deeper prompt operator a rewrite realizes (P13 §2, design Decision 1). It
// parameterizes the OPTIMIZER seam — NOT an operator mode: each strategy is driven by its own catalog
// row with its own admissibility, so the four operators stay four independently-admissible rows while
// sharing one grounded, immutable-publish emission path.
type PromptStrategy string

const (
	// StrategyFormatConstraint is the original grounded rewrite: pin the violated output contract and
	// avoid the observed failures. It is the default (empty) strategy for backward compatibility.
	StrategyFormatConstraint PromptStrategy = ""
	// StrategyInstructionHarden hardens an under-specified prompt with explicit, imperative constraints
	// grounded in the observed failures.
	StrategyInstructionHarden PromptStrategy = "instruction_harden"
	// StrategyFewShotCurate removes dead / duplicate exemplars from a few-shot prompt.
	StrategyFewShotCurate PromptStrategy = "few_shot_curate"
	// StrategyCompress reduces tokens (collapse blank runs, trailing whitespace) WITHOUT dropping any
	// live {{slot}} — a slot-dropping compression is refused at resolve (task 2.6), never here.
	StrategyCompress PromptStrategy = "prompt_compress"
	// StrategyRedundancyRemove drops exact-duplicate instruction lines.
	StrategyRedundancyRemove PromptStrategy = "redundancy_remove"
)

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
	// Strategy selects which deeper prompt operator this edit realizes (P13 §2). Empty is the original
	// format-constraint rewrite.
	Strategy PromptStrategy `json:"strategy,omitempty"`
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

// Optimize refines the base prompt using the attached failing cases, dispatching on the request's
// strategy. It refuses (ErrUngrounded) when no cases are attached — grounded-or-silent applies to every
// strategy — and declines (ErrNoChange) when a grounded strategy has nothing to change.
func (SelfRefineOptimizer) Optimize(req PromptOptimizeRequest) (PromptEdit, error) {
	g, err := newGrounding(req)
	if err != nil {
		return PromptEdit{}, err
	}

	var newBody, constraint string
	switch req.Strategy {
	case StrategyInstructionHarden:
		newBody, constraint, err = hardenInstructions(req, g)
	case StrategyFewShotCurate:
		newBody, err = curateFewShot(req)
	case StrategyCompress:
		newBody, err = compressPrompt(req)
	case StrategyRedundancyRemove:
		newBody, err = removeRedundancy(req)
	default:
		newBody, constraint = formatConstraintRewrite(req, g)
	}
	if err != nil {
		return PromptEdit{}, err
	}
	return PromptEdit{NewPromptBody: newBody, FormatConstraint: constraint, Grounding: g}, nil
}

// formatConstraintRewrite is the original grounded rewrite: pin the violated output contract and name
// the observed failures. Kept byte-for-byte so the existing prompt-rewrite behavior is unchanged.
func formatConstraintRewrite(req PromptOptimizeRequest, g GroundingBundle) (newBody, constraint string) {
	var b strings.Builder
	if len(req.RequiredFields) > 0 {
		fields := append([]string(nil), req.RequiredFields...)
		sort.Strings(fields)
		b.WriteString("You MUST return a JSON object containing exactly these fields: ")
		b.WriteString(strings.Join(fields, ", "))
		b.WriteString(".")
	}
	reasons := distinctReasons(g.Cases)
	if len(reasons) > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("Avoid the following observed failures: ")
		b.WriteString(strings.Join(reasons, "; "))
		b.WriteString(".")
	}
	constraint = b.String()
	newBody = strings.TrimRight(req.BasePromptBody, "\n")
	if constraint != "" {
		if newBody != "" {
			newBody += "\n\n"
		}
		newBody += constraint
	}
	return newBody, constraint
}

// hardenInstructions appends an explicit, imperative constraint that answers the observed
// under-specification failures. Grounded-or-silent: newGrounding already refused a caseless request, so
// a grounded harden always has at least one observed failure to answer.
func hardenInstructions(req PromptOptimizeRequest, g GroundingBundle) (newBody, constraint string, err error) {
	reasons := distinctReasons(g.Cases)
	var b strings.Builder
	b.WriteString("Follow every instruction above exactly and completely; do not omit, reinterpret, or ")
	b.WriteString("add steps.")
	if len(reasons) > 0 {
		b.WriteString(" Specifically address the under-specified cases that produced: ")
		b.WriteString(strings.Join(reasons, "; "))
		b.WriteString(".")
	}
	constraint = b.String()
	newBody = strings.TrimRight(req.BasePromptBody, "\n")
	if newBody != "" {
		newBody += "\n\n"
	}
	newBody += constraint
	return newBody, constraint, nil
}

// curateFewShot removes exact-duplicate exemplar lines (a dead exemplar repeated verbatim carries no
// signal and only spends context). Silent when the prompt has no exemplars to curate.
func curateFewShot(req PromptOptimizeRequest) (string, error) {
	lines := strings.Split(req.BasePromptBody, "\n")
	seenExemplar := map[string]bool{}
	out := make([]string, 0, len(lines))
	removed := 0
	sawExemplar := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if isExemplarLine(trimmed) {
			sawExemplar = true
			key := strings.ToLower(trimmed)
			if seenExemplar[key] {
				removed++
				continue // drop the duplicate exemplar
			}
			seenExemplar[key] = true
		}
		out = append(out, ln)
	}
	if !sawExemplar || removed == 0 {
		return "", ErrNoChange
	}
	return strings.Join(out, "\n"), nil
}

// compressPrompt reduces tokens by collapsing runs of blank lines to one and trimming trailing
// whitespace, WITHOUT touching any line that carries a {{slot}} (dropping a live slot is refused at
// resolve, task 2.6 — never silently here). Silent when there is no slack to reclaim.
func compressPrompt(req PromptOptimizeRequest) (string, error) {
	lines := strings.Split(req.BasePromptBody, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	changed := false
	for _, ln := range lines {
		trimmed := strings.TrimRight(ln, " \t")
		if trimmed != ln {
			changed = true
		}
		if trimmed == "" {
			if prevBlank {
				changed = true
				continue // collapse consecutive blank lines
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out = append(out, trimmed)
	}
	compressed := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if compressed != strings.TrimRight(req.BasePromptBody, "\n") {
		changed = true
	}
	if !changed || compressed == req.BasePromptBody {
		return "", ErrNoChange
	}
	return compressed, nil
}

// removeRedundancy drops exact-duplicate non-empty instruction lines, keeping the first occurrence and
// preserving order. Silent when no line repeats. A line carrying a {{slot}} is never dropped as a
// duplicate — its runtime binding is load-bearing even if the literal text repeats.
func removeRedundancy(req PromptOptimizeRequest) (string, error) {
	lines := strings.Split(req.BasePromptBody, "\n")
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	removed := 0
	for _, ln := range lines {
		key := strings.TrimSpace(ln)
		if key != "" && !strings.Contains(ln, "{{") && seen[key] {
			removed++
			continue
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, ln)
	}
	if removed == 0 {
		return "", ErrNoChange
	}
	return strings.Join(out, "\n"), nil
}

// isExemplarLine reports whether a trimmed line opens a few-shot exemplar. Conservative: it matches the
// common "Example" / "Example N:" lead-ins, so curation only touches lines that are clearly exemplars.
func isExemplarLine(trimmed string) bool {
	low := strings.ToLower(trimmed)
	return strings.HasPrefix(low, "example:") || strings.HasPrefix(low, "example ") ||
		strings.HasPrefix(low, "e.g.")
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
