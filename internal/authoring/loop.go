package authoring

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// THE LOOP AND ENVELOPE AUTHORING SURFACES (P34 task 3.7, FR9)
// ────────────────────────────────────────────────────────────
//
// FR9 says new authoring cannot create a legacy loop-bearing harness entry. This file is what makes that
// STRUCTURAL rather than a rule somebody has to remember:
//
//   - a control loop is authored with `LoopEdit`, which writes `loop_ref` and nothing else;
//   - an execution envelope is authored with `EnvelopeEdit`, which writes `harness_ref` and offers only
//     the `envelope` strategy;
//   - `HarnessStrategyOptions` — the control-loop picker — offers the LOOP vocabulary, so nothing a user
//     can select from it produces a harness entry at all.
//
// 🚫 There is no surface, control, or code path here that pairs a loop strategy name with `harness_ref`.
// A user CANNOT reach the legacy shape, which is different from being told not to. Legacy entries stay
// resolvable indefinitely (ADR-014 D1); they are simply no longer authorable.
//
// TestNewAuthoringCannotCreateALoopBearingHarnessEntry is the fence.

// LoopEdit selects a node's iteration policy by loop-registry version_id.
func LoopEdit(ref string) Edit {
	r := ref
	return Edit{LoopRef: &r}
}

// ClearLoopEdit removes a node's loop override, returning it to whatever the call site already does.
//
// 🔴 Clearing must reproduce the pre-selection `config_hash` byte-identically. `loop_ref` is additive and
// omit-when-absent, so the empty string removes the key entirely rather than writing an empty one — which
// is what lets a user back out with no residue in the hash.
func ClearLoopEdit() Edit {
	empty := ""
	return Edit{LoopRef: &empty}
}

// EnvelopeEdit selects a node's execution envelope by harness-registry version_id.
//
// 🔴 It writes `harness_ref`, which is the SAME field the legacy loop-bearing shape used, and that is
// deliberate: ADR-014 re-scopes the harness axis rather than replacing it, so the envelope belongs on the
// field the axis already owns. What stops the legacy shape being authored is not a different field, it is
// that no surface offers a loop strategy for this field — see EnvelopeOptions.
func EnvelopeEdit(ref string) Edit { return HarnessEdit(ref) }

// ClearEnvelopeEdit removes a node's envelope override.
func ClearEnvelopeEdit() Edit { return ClearHarnessEdit() }

// LoopDimension and EnvelopeDimension are the dimensions each edit touches, exported so a surface labels
// them from the enum rather than from a string literal that could drift from it.
const (
	LoopDimension     = string(variantspec.DimLoop)
	EnvelopeDimension = string(variantspec.DimHarness)
)

// EnvelopeOption is the one option the harness axis offers after the split.
//
// 🔴 It is a LIST of one rather than a bare struct, and the shape is the contract: a surface renders a
// list of options here exactly as it does for every other axis, so the day a second envelope shape exists
// nothing on the surface changes. A bare struct would make that a re-write.
type EnvelopeOption struct {
	Strategy     string          `json:"strategy"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	ParamsSchema json.RawMessage `json:"params_schema"`
	// Required names the params an envelope may not omit, sorted. Read from the SCHEMA rather than
	// re-listed, so a surface cannot mark a field optional that the registry will reject.
	Required []string `json:"required"`
	// PolicyNote states what this axis does and does not decide, in the reader's own terms. It is on the
	// option rather than left to the surface for the reason HarnessStrategyOption.CostWarning is: a
	// surface that had to write it would eventually write it differently, or not at all.
	PolicyNote string `json:"policy_note"`
}

// EnvelopeOptions returns what a user may author on the harness axis.
//
// 🚫 The five loop strategies are NOT here, and their absence is FR9 enforced rather than described. A
// harness entry naming one of them is the legacy shape; it stays resolvable forever and cannot be
// created from this surface, because this surface does not offer it.
func EnvelopeOptions() []EnvelopeOption {
	st := registry.EnvelopeHarness{}
	return []EnvelopeOption{{
		Strategy:     st.Name(),
		Title:        st.Title(),
		Description:  st.Description(),
		ParamsSchema: st.ParamsSchema(),
		Required:     requiredParamsOf(st.ParamsSchema()),
		PolicyNote: "An envelope IMPOSES; it does not choose. It states the most turns and the most money " +
			"any loop under it may use, where the node may reach on the network, which second actors it is " +
			"granted, and how many of its steps may overlap. A loop that asks for more than the ceiling is " +
			"refused when the configuration resolves — naming both numbers — rather than quietly clamped, " +
			"because clamping would run a different configuration than the one recorded.",
	}}
}

// requiredParamsOf reads a schema's `required` list, sorted.
//
// It reads the SCHEMA rather than a second table, so what a surface marks required is what the registry
// will actually reject — the same discipline harnessCeilingFromSchema follows for the turn ceiling.
func requiredParamsOf(schema json.RawMessage) []string {
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		// A schema this package ships that does not parse is a programming error, not a user input. Return
		// nothing rather than guessing: a surface that marked nothing required will be corrected by the
		// registry at seal, which is loud. A surface that marked the WRONG things required would not be.
		return nil
	}
	out := append([]string(nil), s.Required...)
	sort.Strings(out)
	return out
}

// EnvelopeSummary renders a one-line description of a resolved envelope for a diff or a review header.
//
// 🔴 It names the three required facts and omits the optional ones when absent, rather than printing
// zeros for them. "retry_budget: 0" and "no retry budget declared" are different statements, and a
// reviewer reading the first would believe a policy exists that does not.
func EnvelopeSummary(e registry.Envelope) string {
	out := fmt.Sprintf("%s · up to %d turns", e.SandboxPosture, derefInt(e.TurnCeiling))
	if e.SpendCeilingUSD != nil {
		out += fmt.Sprintf(" · up to $%.2f", *e.SpendCeilingUSD)
	}
	if len(e.HostServices) > 0 {
		out += " · grants " + registry.HostServiceDisplay(e.HostServices)
	}
	if e.ConcurrencyLimit != nil {
		out += fmt.Sprintf(" · at most %d concurrent", *e.ConcurrencyLimit)
	}
	return out
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
