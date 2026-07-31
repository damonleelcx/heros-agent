// Package authoringwire is the SEAM between the authoring contract and the codemod.
//
// `internal/authoring` must not import `internal/transform` — that is the structural guarantee behind
// "one spine, two origins": authoring cannot reach the codemod directly, so it can only get to a diff
// through the shared compiler every operator candidate also goes through. A test in that package reads
// its own imports and fails if the rule is broken.
//
// But preflight still has to answer "would the codemod refuse this?", and the only honest way to answer
// it is to ASK THE CODEMOD. So the adapter lives here, in a package that imports both, and authoring
// holds only the interface.
//
// # Why one package rather than an adapter in each caller
//
// The CLI and the API both need this, and they must give the same answer with the same words. Two
// adapters would be two chances to translate a refusal differently — and the whole point of moving the
// refusal earlier is that preflight and the transform CANNOT disagree. One implementation is what makes
// that true rather than aspirational.
package authoringwire

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Materializer probes the real codemod and discards what it produces.
//
// Running the actual generator, rather than re-deriving "can this be applied?" from the resolved config,
// is the entire design. A second predicate would drift from the engine the first time a refusal was
// added to one and not the other, and it would drift SILENTLY — the editor would start offering changes
// the codemod refuses, which is the failure preflight exists to prevent.
type Materializer struct {
	// Root is the checked-out source tree. The probe only READS it: transform.Generate returns a patch
	// value and writes nothing, so a probe leaves no diff, no worktree, and no file behind.
	Root string
}

// Probe reports the refusal the codemod would raise, or a zero Refusal when it would emit.
//
// A refusal is a VALUE here, not an error. The distinction is the same one the change surface already
// makes: "the platform declined this change and said why" is an answer the user asked for, while
// "something went wrong" is a failure. Returning the first as an error would push it into every
// caller's error path, where the named cause becomes a log line instead of a sentence on a screen.
func (m Materializer) Probe(_ context.Context, r *variantspec.Resolved) (authoring.Refusal, error) {
	if r == nil {
		return authoring.Refusal{}, errors.New("authoringwire: probe requires a resolved configuration")
	}
	_, err := transform.Generate(r, m.Root)
	if err == nil {
		return authoring.Refusal{}, nil
	}

	// An unsafe-rewrite error is the codemod's own refusal, and it already names the node and the
	// dimension. It is rendered VERBATIM — re-wording it here would put a second author between the
	// engine and the person who has to act on it.
	var re *transform.RewriteError
	if errors.As(err, &re) {
		return authoring.Refusal{
			Cause:  err.Error(),
			NodeID: re.NodeID,
			Field:  re.Dim,
			Shape:  shapeFor(re.Dim),
		}, nil
	}

	// A spec error (an unresolved ref, an un-applying slot, a tool the node does not offer) is also a
	// refusal with a name, raised before the codemod rather than by it.
	var se *variantspec.SpecError
	if errors.As(err, &se) {
		return authoring.Refusal{Cause: err.Error(), NodeID: se.NodeID, Field: string(se.Dim)}, nil
	}

	// Anything else is a genuine failure — the tree could not be read, a file vanished. It travels as an
	// error so it is not mistaken for a decision the platform made.
	return authoring.Refusal{}, err
}

// shapeFor names the KIND of change that could not be materialized, for surfaces that group refusals.
//
// It is deliberately coarse and derived from the dimension rather than parsed out of the message: a
// shape parsed from prose would break the moment the prose improved, and the prose is written to be
// read by a person, not by this function.
func shapeFor(dimension string) string {
	switch variantspec.Dimension(dimension) {
	case variantspec.DimSkills:
		return "skill binding"
	case variantspec.DimTools:
		return "tool selection"
	case variantspec.DimContext:
		return "context policy"
	case variantspec.DimModel:
		return "model or parameters"
	case variantspec.DimPrompt:
		return "prompt version"
	default:
		// "wiring" and anything a future dimension adds arrive here. Naming the dimension is better than
		// naming nothing, and better than guessing at a friendlier label for something unrecognised.
		return dimension
	}
}

// Coverage answers the language boundary from the transform's OWN table (P14 14c task 9.6).
//
// 🔴 This is the whole of NFR8. The authoring surface's "can this node carry a skill?" and the codemod's
// refusal must be the SAME answer, and the only way to guarantee that is for both to read one table.
// A second list — a constant in the console, a row in a doc, a switch in this package — would drift, and
// it would drift in the dangerous direction: the editor offering a binding the codemod then refuses,
// after the user has chosen and ordered several.
//
// It is a type with no fields on purpose. A cache, a filter, or a "supported languages" override would
// each be a place for the second answer to grow back.
type Coverage struct{}

// Materializes reports whether a skill binding can be materialized for this language.
func (Coverage) Materializes(language string) bool {
	want := strings.ToLower(strings.TrimSpace(language))
	for _, row := range transform.MaterializerCoverage() {
		if strings.ToLower(row.Language) == want {
			return true
		}
	}
	return false
}

// Languages lists every covered language, deduplicated and sorted, for a refusal that tells the reader
// what WOULD have worked.
func (Coverage) Languages() []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range transform.MaterializerCoverage() {
		if !seen[row.Language] {
			seen[row.Language] = true
			out = append(out, row.Language)
		}
	}
	sort.Strings(out)
	return out
}

// WiringGate delegates to the SAME `GateReorder` → `ValidateOrdering` a compile runs (P15 15d task 19.3).
//
// 🔴 This is the whole of NFR8 for the wiring axis. The verdict the editor shows and the verdict the
// compiler produces must be one gate's output; a second validator would let the editor bless what the
// compiler rejects, and the author would find out after rearranging a graph.
type WiringGate struct {
	// IR is the discovered graph the ordering is validated against.
	IR *discovery.IR
	// Catalog is the typed-contract adapter catalog. Nil uses the default, exactly as ValidateOrdering does.
	Catalog *typedcontract.Catalog
}

// Check validates a candidate ordering and translates the gate's own verdict into the authoring shapes.
//
// The translation is lossless in the direction that matters: every diagnostic's consumer, producer and
// field survive, because those three names are what the refusal exists to deliver.
func (g WiringGate) Check(order []string, edges []authoring.WiringEdge) ([]authoring.CoherenceBreak, []authoring.InsertedAdapter) {
	spec := &variantspec.VariantSpec{Order: order}
	for _, e := range edges {
		kind := e.Kind
		if kind == "" {
			// Fall back to the kind the DISCOVERED graph records for this edge, and to "data" when the IR
			// has no such edge. Defaulting to "control" (or to empty) would make the gate skip the edge and
			// report a broken graph as coherent — failing OPEN on the one check that must fail closed.
			kind = g.discoveredKind(e.From, e.To)
		}
		spec.Edges = append(spec.Edges, variantspec.Edge{FromNodeID: e.From, ToNodeID: e.To, Kind: kind})
	}

	// The gate returns the RUNNABLE spec, with any adapters already recorded on it. Reading them from
	// there rather than re-deriving their ids keeps this adapter from becoming a second opinion about
	// what was inserted — the spec is what the compile would carry, so it is what the preview shows.
	gated, verdict := variantspec.GateReorder(g.IR, spec, g.Catalog)

	if !verdict.IsCoherent() {
		breaks := make([]authoring.CoherenceBreak, 0, len(verdict.Diagnostics))
		for _, d := range verdict.Diagnostics {
			// A diagnostic may name several fields; each is its own break, because a reader fixes fields
			// one at a time and a list collapsed into one sentence is a list they have to re-parse.
			fields := d.Fields
			if len(fields) == 0 {
				fields = []string{""}
			}
			for _, f := range fields {
				breaks = append(breaks, authoring.CoherenceBreak{
					Consumer: d.ToNodeID, Producer: d.FromNodeID, Field: f, Detail: d.Message,
				})
			}
		}
		return breaks, nil
	}

	if gated == nil {
		return nil, nil
	}
	adapters := make([]authoring.InsertedAdapter, 0, len(gated.InsertedAdapters))
	for _, a := range gated.InsertedAdapters {
		adapters = append(adapters, authoring.InsertedAdapter{
			NodeID: a.AdapterNodeID, From: a.FromNodeID, To: a.ToNodeID, Kind: a.CatalogKind,
			Rationale: fmt.Sprintf("a %s adapter reconciles %s -> %s", a.CatalogKind, a.FromNodeID, a.ToNodeID),
		})
	}
	return nil, adapters
}

// WiringMaterializability answers, for the authoring surface, which wiring SHAPES the transform can emit
// (P15 15d task 19.6).
//
// 🔴 It answers from the same facts the engine acts on: exactly ONE shape materializes — a transposition
// of two adjacent, independent sibling statements — and only in a language that has a statement
// materializer. Every other shape keeps the interim refusal.
//
// The list of shapes is deliberately NOT derived by asking the planner to plan each one: planning needs a
// concrete tree and two concrete call sites, which a surface does not have while a user is still
// dragging. So this states the boundary, and `Materializer.Probe` is what enforces it against the real
// tree at submit — two checks, one answer, and a test asserts they agree on the shape that matters.
type WiringMaterializability struct{}

// CanMaterialize reports whether a shape can be emitted for a language, and why not otherwise.
func (WiringMaterializability) CanMaterialize(shape authoring.WiringShape, language string) (bool, string) {
	if shape != authoring.ShapeTransposition {
		// The shape is named as a NOUN PHRASE rather than dropped into "A %s is…", which produced
		// "A edge change" and "A more than one transposition". Refusal copy is read by the person who has
		// to act on it, and a sentence that reads as machine-assembled invites them to stop reading.
		return false, fmt.Sprintf(
			"the call-site rewriter emits exactly one wiring shape today — a transposition of two adjacent, "+
				"independent statements. This change is %s: modelled and hashed, but no rewriter moves, "+
				"fuses or deletes a call at the source yet, so it is refused rather than applied as a no-op "+
				"that would let its configuration be scored against unrearranged code", shape)
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if !transform.HasStatementMaterializer(lang) {
		return false, fmt.Sprintf(
			"no statement materializer has landed for %s (covered today: %s)",
			displayLang(language), strings.Join(transform.StatementMaterializerLanguages(), ", "))
	}
	return true, ""
}

func displayLang(l string) string {
	if strings.TrimSpace(l) == "" {
		return "this workflow's language"
	}
	return l
}

// discoveredKind returns the kind the IR records for an edge, defaulting to "data".
//
// The default is the SAFE direction: a data edge is checked, a control edge is not, so treating an
// unknown edge as data means an unrecognised edge is validated rather than waved through.
func (g WiringGate) discoveredKind(from, to string) string {
	if g.IR != nil {
		for _, e := range g.IR.Edges {
			if e.FromNodeID == from && e.ToNodeID == to && e.Kind != "" {
				return e.Kind
			}
		}
	}
	return "data"
}
