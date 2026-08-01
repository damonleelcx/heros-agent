package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// GET /api/v1/memory — the read model for the memory-authoring surface (P17 20c, FR17/FR20).
//
// # What this endpoint is NOT
//
// 🚫 It is not an apply path. A memory change is authored through the EXISTING authoring routes
// (`POST /api/v1/authoring/preflight`, `.../submit`) with a `memory_ref` edit, because there is one
// spine and two origins (P13 `authored-change`, P17 decisions.md D7). A second apply path here would be
// a second place for every gate to be wrong, and the gate that matters most on this axis is the refusal.
//
// # What it IS
//
// The two facts a surface needs BEFORE a user chooses:
//
//  1. the closed strategy vocabulary, with each strategy's params schema — so the form renders the
//     fields the seal will validate, and cannot offer a choice the registry would reject;
//  2. the BOUNDARY: whether a memory change can be written into source at all, read from the transform
//     engine's OWN coverage table.
//
// 🔴 (2) is the load-bearing one. At M20 the answer is no, in every language, and the surface must say
// so before the user invests effort — not after, via an empty diff. Deriving it from
// `transform.CoverageFor("memory")` rather than writing a sentence here is what keeps it true: the day a
// memory rewriter lands, this endpoint starts saying yes without anyone remembering to edit copy.
type memoryView struct {
	// Boundary is the pre-selection statement. Rendered before any choice is offered.
	Boundary memoryBoundaryView `json:"boundary"`
	// Strategies is the closed vocabulary. There is no free-text path: a name outside this set cannot
	// resolve, so offering one would be offering a choice that fails at seal.
	Strategies []memoryStrategyView `json:"strategies"`
	// Language is the workflow language the boundary was computed for, echoed so a reader can tell which
	// question was answered.
	Language string `json:"language"`
	// Dimension is the wire name a memory edit carries, sent so the client labels it from the server's
	// enum rather than a literal that could drift.
	Dimension string `json:"dimension"`
}

// memoryBoundaryView is FR20's statement, in the shape a surface renders.
type memoryBoundaryView struct {
	// Applicable is false at M20 in every language.
	Applicable bool `json:"applicable"`
	// MissingArtifact names what the platform owes. Non-empty whenever Applicable is false, so a client
	// can never render "unavailable" with no reason attached.
	MissingArtifact string `json:"missing_artifact,omitempty"`
	// Reason is the engine's own sentence, verbatim.
	Reason string `json:"reason,omitempty"`
	// LanguageIsTheBlocker is always false for memory. It is SENT rather than omitted so the client
	// states it explicitly: what is missing is a runtime, not this language's rewriter, and a surface
	// that implied otherwise would send the user to wait for the wrong thing.
	LanguageIsTheBlocker bool `json:"language_is_the_blocker"`
	// AuthorableAnyway is true, and it is the other half of the honest message: a user may still select a
	// strategy, and the selection resolves, hashes, records and compares. Only materialization is refused.
	// Without this a client would reasonably disable the control, which FR20 forbids.
	AuthorableAnyway bool `json:"authorable_anyway"`
}

type memoryStrategyView struct {
	Strategy     string          `json:"strategy"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	ParamsSchema json.RawMessage `json:"params_schema"`
	// Identity marks `none`: selecting it is indistinguishable from clearing, and it is the one strategy
	// that is NOT refused at transform, because it changes nothing.
	Identity bool `json:"identity"`
	// Applies reports whether THIS strategy can be written into source in this language. Per-strategy
	// rather than one flag for the axis, because the identity strategy genuinely applies while every
	// other refuses — collapsing them would either claim memory works or claim `none` is unavailable.
	Applies bool `json:"applies"`
}

// transformCoverage adapts the transform engine's coverage table to the authoring package's reader.
//
// It exists so `internal/authoring` never imports the codemod (its spine test asserts that), while the
// sentence a user reads still comes from the engine. A translation with no judgment in it: every field
// is copied, none is computed.
type transformCoverage struct{}

func (transformCoverage) MemoryCoverage(language string) []authoring.MemoryCoverageCell {
	var out []authoring.MemoryCoverageCell
	for _, c := range transform.CoverageFor(string(variantspec.DimMemory)) {
		if !strings.EqualFold(c.Language, language) {
			continue
		}
		out = append(out, authoring.MemoryCoverageCell{
			Language:        c.Language,
			Strategy:        c.Form,
			Materializes:    c.Status == transform.CoverageMaterializes,
			Cause:           string(c.Cause),
			MissingArtifact: c.MissingArtifact,
			Note:            c.Note,
		})
	}
	return out
}

// memoryReadModel assembles the surface's payload for one language.
func memoryReadModel(language string) memoryView {
	if strings.TrimSpace(language) == "" {
		// 🚫 No silent default to "go". A boundary computed for the wrong language is a claim about code
		// the reader does not have; the empty language falls through to MemoryBoundaryFor's fail-closed
		// path, which says the answer is unknown rather than guessing yes.
		language = ""
	}
	b := authoring.MemoryBoundaryFor(transformCoverage{}, language)

	applies := map[string]bool{}
	for _, c := range (transformCoverage{}).MemoryCoverage(language) {
		applies[c.Strategy] = c.Materializes
	}

	opts := authoring.MemoryStrategyOptions()
	views := make([]memoryStrategyView, 0, len(opts))
	for _, o := range opts {
		views = append(views, memoryStrategyView{
			Strategy: o.Strategy, Title: o.Title, Description: o.Description,
			ParamsSchema: o.ParamsSchema, Identity: o.Identity, Applies: applies[o.Strategy],
		})
	}

	return memoryView{
		Boundary: memoryBoundaryView{
			Applicable:           b.Applicable,
			MissingArtifact:      b.MissingArtifact,
			Reason:               b.Reason,
			LanguageIsTheBlocker: b.LanguageIsTheBlocker,
			// Always true: modeling is never refused, only materialization is. This is what keeps the
			// control live with the boundary stated, rather than silently disabled.
			AuthorableAnyway: true,
		},
		Strategies: views,
		Language:   language,
		Dimension:  authoring.MemoryDimension,
	}
}

// MemoryReadModelForPreview exposes the read model to the console's preview seeder, so the
// browser-checkable preview is seeded from the ENGINE rather than a hand-written fixture. A fixture
// drifts, and a preview that drifts stops catching anything.
func MemoryReadModelForPreview(language string) any { return memoryReadModel(language) }

// handleMemory serves the read model.
//
// It takes a language and NOTHING else — no tenant, no plan, no role — and that absence is the assertion:
// what the engine can materialize is a property of this build, so no entitlement can move the boundary
// (the same rule the coverage endpoint holds). A future contributor adding a plan input has to change
// this signature.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, memoryReadModel(r.URL.Query().Get("language")))
}
