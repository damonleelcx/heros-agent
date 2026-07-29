package authoring

import (
	"fmt"
	"sort"
	"strings"
)

// FAIL-CLOSED SELECTION for the skills and tools axis (P14 14c, tasks 9.3–9.7).
//
// # The rule, and why it is not paranoia
//
// A skill is BOUND by constructing an SDK tool value from a sealed contract. A tool is SELECTED from
// what discovery found the node already offers. Neither is free text, and the reason is the same one
// that makes `env` validate against `DeclaredEnv` and `expr` against `in_scope`:
//
//	a tool the frontend did not locate is not a tool the codemod can delete — the emitted diff removes
//	nothing, or removes the wrong span, and both are silent;
//
//	a skill bound without a PINNED version means the shape of the constructed value is whatever the
//	registry happens to hold at apply time — which is exactly the "compiles and then degrades quality
//	invisibly" failure `refuseSkills` was written to prevent.
//
// So an authoring surface offers only what the platform sealed or discovered, and a submission naming
// anything else is refused by name. The offer and the acceptance run through the SAME functions here,
// because a picker that offers one set while the validator accepts another is two sources of truth with
// a gap between them.
//
// # Where the language boundary is decided
//
// It is NOT decided here. `LanguageCoverage` is supplied by the caller and comes from
// `transform.MaterializerCoverage()` — the one table the transform's own refusal reads. A second list
// in this package would be the drift the coverage doc exists to prevent, and it would drift in the
// dangerous direction: the editor offering what the codemod refuses.

// 🚫 No sentinel errors here, deliberately. Every function in this file returns a `Refusal` VALUE,
// because on this path "the platform declined this and said why" is an answer the caller asked for
// rather than a failure — the same distinction the change surface already makes. A parallel set of
// sentinels would be a second vocabulary for the same outcomes, and the two would drift the first time
// one gained a case.

// SealedSkill is one skill an authoring surface may offer: a registry entry with a pinned version.
type SealedSkill struct {
	// Ref is the pinned reference the spec will carry.
	Ref string
	// Name is what a person reads.
	Name string
	// VersionID is the pinned version. Empty means unpinned, which is refused.
	VersionID string
}

// Pinned reports whether this skill names a specific version.
func (s SealedSkill) Pinned() bool { return s.VersionID != "" }

// NodeSelection is everything the platform already knows about one node's selectable surface. It is
// supplied by the caller from discovery and the registry — this package derives none of it, because
// deriving it would be a second answer to a question those two already answer.
type NodeSelection struct {
	NodeID string
	// Language is the node's discovered language.
	Language string
	// SealedSkills are the registry-sealed, pinned skills that exist. A surface offers a subset of these
	// and nothing else.
	SealedSkills []SealedSkill
	// DiscoveredTools are the tools the frontend located at this call site, in IR order.
	DiscoveredTools []string
	// ToolSetDynamic is true when the frontend could not locate the tool set as a static declaration.
	// Distinct from an empty DiscoveredTools: "this node offers no tools" and "we could not see what
	// this node offers" are different facts with different answers.
	ToolSetDynamic bool
}

// LanguageCoverage answers "can this language carry a skill binding?".
//
// It is an interface over the transform's coverage table rather than a copy of it. The implementation
// in production reads `transform.MaterializerCoverage()`; a test may supply its own, but the production
// wiring is asserted to use the real one.
type LanguageCoverage interface {
	// Materializes reports whether a skill binding can be materialized for this language.
	Materializes(language string) bool
	// Languages lists every covered language, for a refusal that says what WOULD have worked.
	Languages() []string
}

// SkillOffer is what a surface may present for a node.
//
// It returns the boundary as DATA rather than as an empty list. An empty picker reads as "this node has
// no skills available" — a fact about the catalogue — when the truth is "this language cannot carry one
// yet", a fact about the platform. Those lead a reader to different places.
type SkillOffer struct {
	// Skills is what may be offered. Empty when the language cannot carry a binding.
	Skills []SealedSkill
	// Refused is set when the language boundary blocks the whole node.
	Refused Refusal
}

// Offerable reports whether any skill may be offered here.
func (o SkillOffer) Offerable() bool { return o.Refused.Cause == "" && len(o.Skills) > 0 }

// OfferSkills decides what a surface may present for a node, and why it may not present more.
func OfferSkills(sel NodeSelection, cov LanguageCoverage) SkillOffer {
	if cov == nil {
		return SkillOffer{Refused: Refusal{
			Cause:  "authoring: no materializer-coverage source was wired, so the language boundary cannot be decided",
			NodeID: sel.NodeID, Field: "skills"}}
	}
	if !cov.Materializes(sel.Language) {
		return SkillOffer{Refused: Refusal{
			Cause: fmt.Sprintf(
				"node %q, dim skills: binding a skill means constructing SDK tool values at the call site, "+
					"and no materializer for %s has landed yet (covered today: %s)",
				sel.NodeID, displayLanguage(sel.Language), strings.Join(cov.Languages(), ", ")),
			NodeID: sel.NodeID, Field: "skills", Shape: "skill binding"}}
	}
	// Only sealed AND pinned entries are offerable. An unpinned entry is not a lesser offer; it is not an
	// offer at all, because the value its binding would construct is undetermined.
	var out []SealedSkill
	for _, s := range sel.SealedSkills {
		if s.Pinned() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return SkillOffer{Skills: out}
}

// ValidateSkillBinding accepts or refuses an authored skill binding, by the same rules OfferSkills uses
// to decide what to show.
//
// `args` is validated against the PINNED version's contract through the caller-supplied validator — not
// against the registry's current head. A binding that validated against a newer, laxer contract is a
// shape error waiting for the first live call.
func ValidateSkillBinding(sel NodeSelection, cov LanguageCoverage, refs []string,
	args map[string]any, validate func(ref string, args any) error) Refusal {

	if offer := OfferSkills(sel, cov); offer.Refused.Cause != "" {
		return offer.Refused
	}
	sealed := map[string]SealedSkill{}
	for _, s := range sel.SealedSkills {
		sealed[s.Ref] = s
	}
	for _, ref := range refs {
		s, known := sealed[ref]
		if !known {
			return Refusal{
				Cause: fmt.Sprintf(
					"node %q, dim skills: %q is not a sealed registry entry — a skill is bound from a "+
						"registered contract, never from a name typed at the call site",
					sel.NodeID, ref),
				NodeID: sel.NodeID, Field: "skills", Shape: "skill binding"}
		}
		if !s.Pinned() {
			return Refusal{
				Cause: fmt.Sprintf(
					"node %q, dim skills: %q names no version — an unpinned binding constructs whatever "+
						"shape the registry holds at apply time, which is not a configuration anyone reviewed",
					sel.NodeID, ref),
				NodeID: sel.NodeID, Field: "skills", Shape: "skill binding"}
		}
		if validate != nil && args != nil {
			if err := validate(ref, args); err != nil {
				return Refusal{
					Cause: fmt.Sprintf("node %q, dim skills: %v", sel.NodeID, err),
					// The FIELD is the failing argument where the validator named one, so the refusal points
					// at what to fix rather than at the whole binding.
					NodeID: sel.NodeID, Field: failingField(err), Shape: "skill binding"}
			}
		}
	}
	return Refusal{}
}

// ValidateToolSelection accepts or refuses an authored tool selection, fail-closed against the node's
// DISCOVERED set.
func ValidateToolSelection(sel NodeSelection, keep []string) Refusal {
	if sel.ToolSetDynamic {
		return Refusal{
			Cause: fmt.Sprintf(
				"node %q, dim tools: this node's tool set is assembled at run time, so there is no static "+
					"declaration to prune — the deletion site is not inferred",
				sel.NodeID),
			NodeID: sel.NodeID, Field: "tools", Shape: "tool selection"}
	}
	discovered := map[string]bool{}
	for _, t := range sel.DiscoveredTools {
		discovered[t] = true
	}
	for _, t := range keep {
		if !discovered[t] {
			return Refusal{
				Cause: fmt.Sprintf(
					"node %q, dim tools: %q is not among the tools this node was discovered to offer (%s) — "+
						"a selection over a tool that is not there would apply to nothing",
					sel.NodeID, t, strings.Join(sel.DiscoveredTools, ", ")),
				NodeID: sel.NodeID, Field: "tools", Shape: "tool selection"}
		}
	}
	return Refusal{}
}

// OfferTools returns what a surface may present for pruning: the discovered set, or a refusal when the
// set is not statically visible.
func OfferTools(sel NodeSelection) ([]string, Refusal) {
	if sel.ToolSetDynamic {
		return nil, ValidateToolSelection(sel, nil)
	}
	out := append([]string(nil), sel.DiscoveredTools...)
	sort.Strings(out)
	return out, Refusal{}
}

// failingField extracts the argument the validator named, when it named one. It falls back to the
// dimension rather than guessing — a field invented by parsing a message is a field that moves when the
// message improves.
func failingField(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "field "); i >= 0 {
		rest := msg[i+len("field "):]
		if j := strings.IndexAny(rest, " :,"); j > 0 {
			return strings.Trim(rest[:j], `"'`)
		}
	}
	return "skills"
}

func displayLanguage(l string) string {
	if l == "" {
		return "this call site's language"
	}
	return l
}
