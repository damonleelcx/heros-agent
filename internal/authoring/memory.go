package authoring

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// MEMORY AUTHORING (P17 20c, tasks 9.1–9.7; decisions.md D7).
//
// # This axis is governed by a REFUSAL the user cannot route around
//
// On every other axis an authored change reaches the user's source as a diff and is merely *unscored*.
// Here it does not reach the source at all: the transform refuses every `MemoryRef ≠ none` node, in both
// engines, because materializing a store read and written BETWEEN invocations means introducing a store,
// a lifetime, a key scheme, and read/write points across code the user owns (P17 decisions.md D4).
//
// So the shared `authored-change` contract's central allowance — *an authored change may be applied
// without a verdict, because it is the user's own repository* — does not get to fire on this axis, and
// pretending otherwise would be exactly the dishonesty that contract was written to prevent.
//
// # What is refused, precisely: materialization. NOT modeling.
//
// Selecting a strategy resolves, hashes, seals a registry entry, records a `user` origin and a parent
// pointer, and diffs in lineage. That is a real `config_hash` a user can pin, compare, and hand to a
// colleague, and it re-materializes unchanged the day the rewriter lands. Withholding all of that
// because the codemod is missing would confuse "we cannot write this into your source" with "you may not
// express this" — and would throw away the large fraction of the axis's value that needs no codemod.
//
// # The three rules that keep the refusal from becoming a lie
//
//  1. STATED BEFORE THE CHOICE, from the same coverage fact the engine refuses from — never a second
//     sentence written beside the control. See MemoryBoundary: it takes a coverage reader rather than
//     holding prose, because the copy in the UI is always the copy that drifts.
//  2. RAISED AT PREFLIGHT, with the transform's own typed cause, before any worktree, build, or eval
//     spend. That is the existing Preflighter's Materializer probe — no new gate, no second opinion.
//  3. NEVER RENDERED AS SUCCESS. `refused` is its own state; nothing is applied, delivered, or scored.
//
// 🚫 Note what this file does NOT contain: no Force, no ApplyAnyway, no "advanced mode". A memory change
// cannot be applied at M20, and no argument, role, plan, or flag changes that — because the thing that
// is missing is a runtime, and wanting it does not build it.

// MemoryStrategyOption is one strategy a user may choose, as the authoring surface needs it: the wire
// name it will store, the human label it will show, what the strategy trades away, and the schema its
// params must satisfy.
//
// It is a projection of registry.MemoryStrategy rather than the interface itself so the surface layer
// (and its JSON) never depends on a Go interface value — and so the three layers the registry keeps
// separate (wire name, entity, human label) stay separate all the way to the browser.
type MemoryStrategyOption struct {
	// Strategy is the wire name stored in the registry entry. Stable forever.
	Strategy string `json:"strategy"`
	// Title and Description are the human layer. Free to be reworded; never part of any hash.
	Title       string `json:"title"`
	Description string `json:"description"`
	// ParamsSchema is the JSON Schema the params must satisfy. The surface renders its fields, and the
	// SAME schema rejects a violating value server-side — one schema, two readers, so a form that
	// accepted something the seal would reject is not expressible.
	ParamsSchema json.RawMessage `json:"params_schema"`
	// Identity marks the `none` strategy: selecting it is indistinguishable from clearing (P17 FR18), and
	// it is the one option that is NOT refused at transform, because it changes nothing.
	Identity bool `json:"identity"`
}

// MemoryStrategyOptions returns the closed builtin vocabulary a user may choose from, in a stable order.
//
// 🚫 There is no free-text path and no "custom strategy" option, and that is FR17 rather than a missing
// feature: a strategy name outside the closed set cannot resolve, so offering one would be offering a
// choice that fails at seal. What a user tunes is the PARAMS, which the schema bounds.
func MemoryStrategyOptions() []MemoryStrategyOption {
	strategies := registry.BuiltinMemoryStrategies()
	out := make([]MemoryStrategyOption, 0, len(strategies))
	for _, st := range strategies {
		out = append(out, MemoryStrategyOption{
			Strategy:     st.Name(),
			Title:        st.Title(),
			Description:  st.Description(),
			ParamsSchema: st.ParamsSchema(),
			Identity:     st.Name() == registry.StrategyNone,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		// The identity first — it is the baseline a user compares the others against — then alphabetical,
		// so the list is stable across builds and two surfaces render the same order.
		if out[i].Identity != out[j].Identity {
			return out[i].Identity
		}
		return out[i].Strategy < out[j].Strategy
	})
	return out
}

// CoverageReader answers what the transform engine will do with a memory strategy, read from the engine's
// OWN coverage table.
//
// 🔴 It is an interface for the reason Materializer is one: this package must not import the codemod (see
// spine_test.go). But the deeper reason is D7's rule — the sentence a user reads before choosing and the
// refusal the engine raises must come from ONE fact. A hard-coded string here would be a second source of
// truth, and it is the one that would still say "refused" the day the rewriter landed.
type CoverageReader interface {
	// MemoryCoverage returns, for a language, each strategy's status: whether it materializes, and when
	// it does not, the cause class and the artifact whose absence explains it.
	MemoryCoverage(language string) []MemoryCoverageCell
}

// MemoryCoverageCell is one (language, strategy) answer, mirroring transform.CoverageCell's shape
// without importing it.
type MemoryCoverageCell struct {
	Language        string `json:"language"`
	Strategy        string `json:"strategy"`
	Materializes    bool   `json:"materializes"`
	Cause           string `json:"cause,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
}

// MemoryBoundary is what the authoring surface states BEFORE a user selects anything (P17 FR20).
type MemoryBoundary struct {
	// Applicable reports whether ANY non-identity strategy can be written into source in this language.
	// False at M20, everywhere.
	Applicable bool `json:"applicable"`
	// MissingArtifact names what the platform owes. Non-empty exactly when Applicable is false, so a
	// surface can never render "unavailable" with no reason attached.
	MissingArtifact string `json:"missing_artifact,omitempty"`
	// Reason is the engine's own sentence, rendered verbatim.
	Reason string `json:"reason,omitempty"`
	// LanguageIsTheBlocker reports whether this limit is specific to the node's language.
	//
	// 🔴 Always FALSE for memory, and the field exists to say so out loud rather than by omission. What is
	// missing is a memory runtime, not a per-language rewriter, so a surface must not render "your
	// language's support is pending" — that would imply some other language works, sending the user to
	// wait for the wrong thing. The field is here so the surface has to make that statement explicitly
	// instead of inferring it.
	LanguageIsTheBlocker bool `json:"language_is_the_blocker"`
}

// MemoryBoundaryFor derives the pre-selection boundary statement from the coverage table.
//
// 🔴 It considers EVERY non-identity cell, not the first one. Coverage is per (language, strategy) now,
// and a language can materialize some strategies and refuse others — Go materializes the content-blind
// ones and permanently refuses the ones that read message text. Reading only the first cell made the
// answer depend on alphabetical order: `entity-memory` sorts before `scratchpad`, so Go reported the
// whole axis inapplicable while one of its strategies worked. That is over-refusing, which is the same
// defect as over-claiming and the one nobody reports.
//
// A language with no cells at all yields Applicable=false with an explicit reason, never a silent
// "true": absence of evidence about a language must not render as permission.
func MemoryBoundaryFor(cov CoverageReader, language string) MemoryBoundary {
	cells := cov.MemoryCoverage(language)
	if len(cells) == 0 {
		return MemoryBoundary{
			Applicable:      false,
			MissingArtifact: "a coverage answer for this language",
			Reason: fmt.Sprintf("the transform engine records no memory coverage for %q, so whether a "+
				"memory change could be applied here is unknown; an unknown is not a yes", language),
		}
	}

	var anyApplies bool
	var firstRefusal *MemoryCoverageCell
	for i := range cells {
		c := cells[i]
		if c.Strategy == registry.StrategyNone {
			continue // the identity strategy always "applies" by changing nothing; it says nothing about the axis
		}
		if c.Materializes {
			anyApplies = true
			continue
		}
		if firstRefusal == nil {
			firstRefusal = &cells[i]
		}
	}

	switch {
	case anyApplies:
		// Applicable, but the refusing cells are still real. The surface renders per-strategy
		// applicability beside this; what the boundary carries is the headline.
		b := MemoryBoundary{Applicable: true}
		if firstRefusal != nil {
			b.MissingArtifact = firstRefusal.MissingArtifact
			b.Reason = firstRefusal.Note
		}
		return b
	case firstRefusal != nil:
		return MemoryBoundary{
			Applicable:           false,
			MissingArtifact:      firstRefusal.MissingArtifact,
			Reason:               firstRefusal.Note,
			LanguageIsTheBlocker: false,
		}
	default:
		return MemoryBoundary{
			Applicable:      false,
			MissingArtifact: "a non-identity strategy in the coverage table",
			Reason:          "only the identity strategy is covered, so no memory change is applicable",
		}
	}
}

// MemoryValidator seals a chosen (strategy, params) pair into the memory registry and returns its
// version_id. Production wires registry.Store; the interface keeps this package free of a live database
// and lets the surface validate without registering.
type MemoryValidator interface {
	// ValidateMemoryParams checks a strategy name and its params WITHOUT writing anything. It is the same
	// function RegisterMemory calls, which is what makes a form that accepted something the seal would
	// reject impossible rather than merely unlikely.
	ValidateMemoryParams(name, strategy string, params json.RawMessage) (registry.MemoryStrategy, json.RawMessage, error)
}

// ValidateMemorySelection checks a user's choice before anything is stored (P17 FR17).
//
// It rejects a strategy outside the closed set and params that violate the strategy's schema, naming
// what was wrong. It writes nothing: a rejected choice must never acquire a version_id, because an id
// for content that was never stored is an id a spec can reference and never resolve.
func ValidateMemorySelection(v MemoryValidator, name, strategy string, params json.RawMessage) error {
	if v == nil {
		// 🚫 No validator means no validation, and "no validation" must never read as "valid". A surface
		// wired without one fails loudly here rather than accepting anything a user types.
		return fmt.Errorf("authoring: memory selection cannot be validated without a validator")
	}
	if strings.TrimSpace(strategy) == "" {
		return fmt.Errorf("authoring: no memory strategy selected; choose one of %s", strings.Join(memoryStrategyNames(), ", "))
	}
	if _, _, err := v.ValidateMemoryParams(name, strategy, params); err != nil {
		return err
	}
	return nil
}

// MemoryEdit builds the draft edit that SETS a node's memory strategy to a sealed ref.
func MemoryEdit(ref string) Edit {
	r := ref
	return Edit{MemoryRef: &r}
}

// ClearMemoryEdit builds the draft edit that CLEARS a node's memory strategy (P17 FR18).
//
// 🔴 Clearing is an empty ref, not an absent field. The absent field means "leave this dimension alone"
// (Edit's three states); an empty ref means "remove the override", and because NodeOverride.MemoryRef is
// `omitempty` the key then disappears entirely — so the derived spec's bytes, and its `config_hash`,
// return EXACTLY to what they were before the selection. A user can back out with no residue.
func ClearMemoryEdit() Edit {
	empty := ""
	return Edit{MemoryRef: &empty}
}

// MemoryDimension is the dimension a memory edit touches, exported so a surface labels it from the enum
// rather than from a string literal that could drift from it.
const MemoryDimension = string(variantspec.DimMemory)

func memoryStrategyNames() []string {
	out := make([]string, 0, registry.MemoryStrategySetSize)
	for _, st := range registry.BuiltinMemoryStrategies() {
		out = append(out, st.Name())
	}
	sort.Strings(out)
	return out
}
