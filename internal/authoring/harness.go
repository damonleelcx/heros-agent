package authoring

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// HARNESS AUTHORING (P18 §12, decisions.md D-12).
//
// # What makes this axis different from every other authoring surface
//
// On the other axes, what a user needs to be told before choosing is whether the change can be APPLIED.
// Here they need to be told two things, and the second one is the one nobody else has had to say: a
// heavier scaffold COSTS MORE PER RUN, up to its turn ceiling, on every case — including the ones that
// were already right.
//
// 🔴 That cost is arithmetic and can be stated before anything runs. Its benefit is a measurement and
// cannot. Saying the first and withholding the second is the honest shape, and it is what keeps this
// surface from reading as a recommendation — which the ranking layer, not an authoring control, is
// allowed to make.
//
// # The boundary is PER CELL, not per axis
//
// Memory's boundary was uniform at M20: every strategy refused, everywhere, so one sentence was right for
// everyone. Harness is not uniform, and a uniform sentence would be wrong in BOTH directions:
//
//	single-shot                            materializes everywhere (it is the identity)
//	reflexion                              materializes where an answer's text is readable (Python)
//	react-loop/plan-execute/critic-loop     refuse in EVERY language, permanently — a call site has
//	                                       nowhere to inject a tool executor, a planner, or a critic
//
// So this file reports per strategy, from the engine's own coverage table, and the surface renders that
// rather than a headline. A hard-coded sentence here would be a second source of truth, and it is the one
// that would still say "refused" the day a rewriter landed.
//
// 🚫 What this file does NOT contain: no Force, no ApplyAnyway, no "advanced mode", and no path that
// skips the admissibility gate. A user may author the change; a user may not author the evidence.

// HarnessStrategyOption is one strategy a user may choose, as the authoring surface needs it.
//
// It is a projection of registry.HarnessStrategy rather than the interface itself so the surface layer
// (and its JSON) never depends on a Go interface value — and so the three layers the registry keeps
// separate (wire name, entity, human label) stay separate all the way to the browser.
type HarnessStrategyOption struct {
	// Strategy is the wire name stored in the registry entry. Stable forever.
	Strategy string `json:"strategy"`
	// Title and Description are the human layer. Free to be reworded; never part of any hash.
	Title       string `json:"title"`
	Description string `json:"description"`
	// ParamsSchema is the JSON Schema the params must satisfy. The surface renders its fields, and the
	// SAME schema rejects a violating value server-side — one schema, two readers, so a form that
	// accepted something the seal would reject is not expressible.
	ParamsSchema json.RawMessage `json:"params_schema"`
	// Identity marks `single-shot`: selecting it with no params is indistinguishable from clearing
	// (FR43), and it is the one option that is never refused, because it changes nothing.
	Identity bool `json:"identity"`
	// MaxTurnCeiling is the largest turn count this strategy's schema permits; 1 for the identity.
	//
	// 🔴 It is on the OPTION rather than left to the surface to derive, because it is the cost the user is
	// being asked to accept and a surface that had to compute it from a JSON Schema would eventually
	// compute it differently — or not at all, which is worse.
	MaxTurnCeiling int `json:"max_turn_ceiling"`
	// CostWarning is the sentence stating what a heavier scaffold may cost. Empty for the identity, which
	// costs exactly what the un-rewritten call costs.
	CostWarning string `json:"cost_warning,omitempty"`
}

// HarnessStrategyOptions returns the closed builtin vocabulary a user may choose from, in a stable order.
//
// 🚫 There is no free-text path and no "custom strategy" option (FR42): a strategy name outside the closed
// set cannot resolve, so offering one would be offering a choice that fails at seal. What a user tunes is
// the PARAMS, which the schema bounds — including `max_turns`, which the schema caps.
func HarnessStrategyOptions() []HarnessStrategyOption {
	strategies := registry.BuiltinHarnessStrategies()
	out := make([]HarnessStrategyOption, 0, len(strategies))
	for _, st := range strategies {
		identity := st.Name() == registry.StrategySingleShot
		ceiling := harnessCeilingFromSchema(st.ParamsSchema())
		if identity {
			ceiling = 1
		}
		out = append(out, HarnessStrategyOption{
			Strategy:       st.Name(),
			Title:          st.Title(),
			Description:    st.Description(),
			ParamsSchema:   st.ParamsSchema(),
			Identity:       identity,
			MaxTurnCeiling: ceiling,
			CostWarning:    harnessCostWarning(ceiling),
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

// harnessCostWarning is the sentence a user must read BEFORE choosing a heavier scaffold (FR45, NFR15).
//
// 🔴 It states the ceiling as a MULTIPLIER on cost and latency, and it names who decides whether that is
// worth it. Both halves matter: the first is a fact the user can act on before spending anything, and the
// second is what stops the control reading as a recommendation.
func harnessCostWarning(ceiling int) string {
	if ceiling <= 1 {
		return ""
	}
	return fmt.Sprintf("This scaffold may run up to %d turns, so it can multiply this node's per-run cost "+
		"and latency by up to %d — on every case, including the ones that already pass. Whether that buys "+
		"enough task_success to be worth it is decided by verification on held-out cases, not by this "+
		"selection.", ceiling, ceiling)
}

// harnessCeilingFromSchema reads the `max_turns` maximum out of a strategy's params schema.
//
// It reads the SCHEMA rather than a second table, so the number a user is warned about is the number the
// seal will actually enforce. A schema with no `max_turns` is the identity's, which runs one turn.
func harnessCeilingFromSchema(raw json.RawMessage) int {
	var s struct {
		Properties struct {
			MaxTurns struct {
				Maximum int `json:"maximum"`
			} `json:"max_turns"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return 1
	}
	if s.Properties.MaxTurns.Maximum <= 0 {
		return 1
	}
	return s.Properties.MaxTurns.Maximum
}

// HarnessCoverageReader answers what the transform engine will do with a harness strategy, read from the
// engine's OWN coverage table.
//
// 🔴 An interface for the reason CoverageReader is one: this package must not import the codemod. But the
// deeper reason is D-12's rule — the sentence a user reads before choosing and the refusal the engine
// raises must come from ONE fact.
type HarnessCoverageReader interface {
	// HarnessCoverage returns, for a language, each strategy's status: whether it materializes, and when
	// it does not, the cause class and the artifact whose absence explains it.
	HarnessCoverage(language string) []HarnessCoverageCell
}

// HarnessCoverageCell is one (language, strategy) answer, mirroring transform.CoverageCell's shape
// without importing it.
type HarnessCoverageCell struct {
	Language        string `json:"language"`
	Strategy        string `json:"strategy"`
	Materializes    bool   `json:"materializes"`
	Cause           string `json:"cause,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
}

// HarnessApplicability is what the authoring surface states about ONE strategy, before it is chosen
// (FR44).
type HarnessApplicability struct {
	Strategy string `json:"strategy"`
	// Applicable reports whether this strategy can be written into source in this language.
	Applicable bool `json:"applicable"`
	// Cause is the engine's typed cause class when it cannot; empty when it can.
	Cause string `json:"cause,omitempty"`
	// MissingArtifact names what the PLATFORM owes, and is non-empty ONLY for the class that means we owe
	// work. 🔴 A permanent fact wearing this field would tell a user to wait for something that will not
	// help them.
	MissingArtifact string `json:"missing_artifact,omitempty"`
	// Reason is the engine's own sentence, rendered verbatim.
	Reason string `json:"reason,omitempty"`
	// Permanent reports whether the refusal is a fact rather than a backlog item. A surface renders these
	// differently on purpose: "not yet" and "not ever, here" are different things to tell someone.
	Permanent bool `json:"permanent"`
}

// causeNotAtCallSite mirrors transform.CauseNotAtCallSite. Spelled here rather than imported, for the
// same reason the coverage cell is re-declared — and pinned to the engine's constant by a test, so the
// two cannot drift silently.
const causeNotAtCallSite = "not-expressible-at-a-call-site"

// HarnessApplicabilityFor derives the per-strategy statement from the coverage table.
//
// 🔴 PER STRATEGY, and that is the whole difference from MemoryBoundaryFor's headline. A single verdict
// for the axis would be wrong in both directions here: it would tell a Python user that `reflexion` is
// unavailable, and it would tell every user that `react-loop` is merely pending.
//
// A language with no cells yields an inapplicable answer with an explicit reason, never a silent "true":
// absence of evidence about a language must not render as permission.
func HarnessApplicabilityFor(cov HarnessCoverageReader, language string) []HarnessApplicability {
	cells := cov.HarnessCoverage(language)
	byStrategy := make(map[string]HarnessCoverageCell, len(cells))
	for _, c := range cells {
		byStrategy[c.Strategy] = c
	}

	out := make([]HarnessApplicability, 0, len(HarnessStrategyOptions()))
	for _, opt := range HarnessStrategyOptions() {
		c, ok := byStrategy[opt.Strategy]
		if !ok {
			out = append(out, HarnessApplicability{
				Strategy:        opt.Strategy,
				Applicable:      false,
				MissingArtifact: "a coverage answer for this language",
				Reason: fmt.Sprintf("the transform engine records no harness coverage for %q, so whether "+
					"this strategy could be applied here is unknown; an unknown is not a yes", language),
			})
			continue
		}
		out = append(out, HarnessApplicability{
			Strategy:        opt.Strategy,
			Applicable:      c.Materializes,
			Cause:           c.Cause,
			MissingArtifact: c.MissingArtifact,
			Reason:          c.Note,
			Permanent:       !c.Materializes && c.Cause == causeNotAtCallSite,
		})
	}
	return out
}

// HarnessValidator seals a chosen (strategy, params) pair into the harness registry. Production wires
// registry.Store; the interface keeps this package free of a live database and lets the surface validate
// without registering.
type HarnessValidator interface {
	// ValidateHarnessParams checks a strategy name and its params WITHOUT writing anything. It is the same
	// function RegisterHarness calls, which is what makes a form that accepted something the seal would
	// reject impossible rather than merely unlikely.
	ValidateHarnessParams(name, strategy string, params json.RawMessage) (registry.HarnessStrategy, json.RawMessage, error)
}

// ValidateHarnessSelection checks a user's choice before anything is stored (FR42).
//
// It rejects a strategy outside the closed set and params that violate the strategy's schema — including
// a param the strategy does not declare, which is rejected rather than ignored, because a silently
// dropped `max_turns` is a user believing they bought a loop they did not.
func ValidateHarnessSelection(v HarnessValidator, name, strategy string, params json.RawMessage) error {
	if v == nil {
		// 🚫 No validator means no validation, and "no validation" must never read as "valid".
		return fmt.Errorf("authoring: harness selection cannot be validated without a validator")
	}
	if strings.TrimSpace(strategy) == "" {
		return fmt.Errorf("authoring: no harness strategy selected; choose one of %s",
			strings.Join(registry.HarnessStrategyNames(), ", "))
	}
	if _, _, err := v.ValidateHarnessParams(name, strategy, params); err != nil {
		return err
	}
	return nil
}

// HarnessEdit builds the draft edit that SETS a node's harness strategy to a sealed ref.
func HarnessEdit(ref string) Edit {
	r := ref
	return Edit{HarnessRef: &r}
}

// ClearHarnessEdit builds the draft edit that CLEARS a node's harness strategy (FR43).
//
// 🔴 Clearing is an empty ref, not an absent field. The absent field means "leave this dimension alone";
// an empty ref means "remove the override", and because NodeOverride.HarnessRef is `omitempty` the key
// then disappears entirely — so the derived spec's bytes, and its `config_hash`, return EXACTLY to what
// they were before the selection. A user can back out with no residue.
func ClearHarnessEdit() Edit {
	empty := ""
	return Edit{HarnessRef: &empty}
}

// HarnessDimension is the dimension a harness edit touches, exported so a surface labels it from the enum
// rather than from a string literal that could drift from it.
const HarnessDimension = string(variantspec.DimHarness)
