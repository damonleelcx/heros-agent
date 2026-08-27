package herosagent

import (
	"fmt"
	"strings"
)

// placement.go is task 7.5: which host may run a tenant's inference, answered in ONE function that both
// runners call and neither can skip.
//
// ── 🔴 P36 REVIEWED THIS FILE AND CHANGED NOTHING. That is a decision, not an omission. ──────────
//
// P36 task 3.1 moves every file carrying a shape assumption about the agent — `definition.go`,
// `axiseditor.go`, `inferencestore.go`, `placement.go`, `caps.go` and the fences — TOGETHER, because
// the type system will not catch a package that is half-moved. This file's shape assumption turned out
// to be one worth keeping, so it is recorded here rather than left to the next reader to re-derive.
//
// PRD §14 Q3 asked whether `placement` should be PER NODE: a definition could run cheap extraction
// customer-side and expensive analysis platform-side. The answer is NO (decisions.md D-36.3).
//
// The reason is the paragraph immediately below. `MayRun` is a gate both runners call and neither can
// skip — "there is no path to a provider that does not pass through it". Per node, the FUNCTION would
// survive and the PROPERTY would not: today "may this run here" is answered once, before anything, for
// the whole assessment. Per node it is answered N times, and a definition whose node 3 is
// platform-placed and node 4 customer-placed is an assessment whose data crosses a security boundary
// mid-run. That is not a placement any more; it is a distributed execution with a boundary inside it.
//
// 🚫 What is genuinely lost is stated rather than glossed: cheap extraction customer-side and expensive
// analysis platform-side is a real capability, and it is DEFERRED rather than refused. It needs a
// data-crossing story — what leaves the customer's machine, under whose consent, recorded where — and
// inventing one as a side effect of a topology change is how a boundary gets moved by accident.
//
// # Why this is a gate on the runner and not a check at the call site
//
// Nothing wires platform-side inference yet — §9 does. So a placement check written "where inference is
// triggered" would be a check written against a call site that does not exist, in a phase where it
// cannot be exercised, for a caller somebody else will write. That is the shape of a rule everybody
// agrees with and nobody runs.
//
// Instead the Runner carries its Host, `Infer` calls MayRun before anything else, and a caller that
// forgets the check cannot exist: there is no path to a provider that does not pass through it. The
// SAME function answers for both hosts, so `customer` refusing platform-side and `platform` refusing
// customer-side are one rule with two readings rather than two implementations that can drift apart.

// Placements returns the closed set, so a consumer's switch can be proved exhaustive and a console can
// render every option rather than the ones somebody remembered.
func Placements() []Placement {
	return []Placement{PlacementPlatform, PlacementCustomer, PlacementDisabled}
}

// ParsePlacement reads a stored or submitted placement, refusing anything outside the set.
//
// 🔴 An empty string is NOT defaulted to `disabled` here, even though `disabled` IS the default. The
// default belongs to the store that has never been written for a tenant — see adminops.PlacementSource,
// which exists to keep "nobody has decided" distinguishable from "somebody decided off". A parser that
// silently turned "" into `disabled` would erase that distinction at every boundary it is used on, and
// the erasure would look exactly like the correct answer.
func ParsePlacement(s string) (Placement, error) {
	for _, p := range Placements() {
		if string(p) == s {
			return p, nil
		}
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%w: no placement was given. An unset placement is `%s` in the STORE, "+
			"where the absence of a row is a fact somebody can act on; it is not a value a caller may "+
			"pass in and have read as a decision", ErrInvalidDefinition, PlacementDisabled)
	}
	return "", fmt.Errorf("%w: %q is not a placement. It is one of %s",
		ErrInvalidDefinition, s, joinPlacements())
}

func joinPlacements() string {
	names := make([]string, 0, len(Placements()))
	for _, p := range Placements() {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}

// MayRun answers whether this host may run an inference for a tenant at this placement (task 7.5).
//
// 🔴 The four refusals below are each a DIFFERENT sentence, and that is not decoration. An operator
// reading "refused" learns nothing; an operator reading "this tenant analyses on its own machine — the
// result arrives through the structure ingest" knows both why nothing ran here and where to look for
// what did.
func (h Host) MayRun(p Placement) error {
	switch {
	case h != HostPlatform && h != HostCustomer:
		return fmt.Errorf("%w: %q is not a host", ErrInvalidDefinition, h)

	case p == PlacementDisabled:
		// The default, and therefore the refusal that will fire most often on a fresh deployment. It is
		// NOT an error condition — CodeDisabled exists precisely so a surface renders this as a state.
		return fmt.Errorf("%w: HEROS is `%s` for this tenant, which is the default (Q2) — nothing "+
			"analyses it on either host until somebody sets a placement", ErrWrongPlacement, PlacementDisabled)

	case h == HostPlatform && p == PlacementCustomer:
		return fmt.Errorf("%w: this tenant is placed `%s`, so its analysis runs on its own machine "+
			"under its own credential. The platform runs nothing for it; the result arrives through the "+
			"structure ingest", ErrWrongPlacement, PlacementCustomer)

	case h == HostCustomer && p == PlacementPlatform:
		return fmt.Errorf("%w: this tenant is placed `%s`, so the platform analyses it under the "+
			"platform's credential. Running it here would spend a second credential on an answer the "+
			"platform already has, and submit it under a placement that contradicts the one recorded",
			ErrWrongPlacement, PlacementPlatform)
	}
	return nil
}

// PlacementOf is the host's own placement — the value a runner stamps on what it stores.
//
// A function rather than a field, so a stored inference cannot claim a placement its host could not
// have run under. `Stored.Placement` is what the console attributes on the graph (task 8.6), and a
// mis-stamped one would attribute a platform-side inference to a customer's machine.
func (h Host) PlacementOf() Placement {
	if h == HostCustomer {
		return PlacementCustomer
	}
	return PlacementPlatform
}
