package assessment

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// evidence.go is design D5 as a type: a finding's evidence is a REFERENCE INTO an existing surface,
// never a number this package computed.
//
// # Why a reference and not a value
//
// The alternative — carrying the score, the label, the edge count on the finding — makes the
// assessment a second source of truth for a statistical claim, which is this console's founding
// prohibition with an extra hop. When the board recomputes tomorrow the finding would still say what
// it said, and a reader has no way to know which of the two is current.
//
// # Why exactly three surfaces
//
// They are the three that already hold evidence about a workflow, and each answers a different
// question a reader asks after reading a claim:
//
//	graph      "where in my code is this?"      GET /api/v1/workflows/{id}/pattern-graph
//	board      "how strong is this number?"     GET /api/v1/workflows/{id}/eval-board
//	scorecard  "what exactly did this variant do?"  GET /api/v1/variants/{id}/scorecard
//
// A fourth would be a surface P33 invented, and inventing one is how the index becomes a source.

// Surface is one of the three existing read models a finding can point into.
type Surface string

const (
	// SurfaceGraph — the P3.5 pattern-classified workflow graph. Locator is a workflow id; Fragment,
	// when present, is a node id.
	SurfaceGraph Surface = "graph"
	// SurfaceBoard — the P4 eval board. Locator is a workflow id; Fragment, when present, is an eval
	// set hash.
	SurfaceBoard Surface = "board"
	// SurfaceScorecard — one variant's scorecard. Locator is a variant id.
	SurfaceScorecard Surface = "scorecard"
)

var surfaces = []Surface{SurfaceGraph, SurfaceBoard, SurfaceScorecard}

// Surfaces returns the three. A copy.
func Surfaces() []Surface { return append([]Surface(nil), surfaces...) }

// Valid reports membership.
func (s Surface) Valid() bool {
	for _, v := range surfaces {
		if v == s {
			return true
		}
	}
	return false
}

// String makes Surface printable in an error.
func (s Surface) String() string { return string(s) }

// SurfaceNames returns the three as sorted plain strings, for a schema's `enum`.
func SurfaceNames() []string {
	out := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		out = append(out, string(s))
	}
	sort.Strings(out)
	return out
}

// EvidenceRef points at one place in one existing surface.
//
// Its fields ARE exported, unlike Finding's. The asymmetry is deliberate: a reference has no
// conditional requirements to protect — every field is required except Fragment, and `Validate` is a
// total check with nothing a constructor could enforce that a method cannot. Making it opaque would
// buy nothing and would force an accessor for every field a renderer needs.
type EvidenceRef struct {
	Surface Surface `json:"surface"`
	// Locator identifies the subject WITHIN the surface: a workflow id for graph and board, a variant
	// id for scorecard. It is not a URL — the surface owns its own route shape, and a stored URL is a
	// route this package would have to keep in step with a router it does not own.
	Locator string `json:"locator"`
	// Fragment narrows to one part of the surface: a node id on the graph, an eval set hash on the
	// board. Optional, and its ABSENCE means "the whole surface", never "unknown".
	Fragment string `json:"fragment,omitempty"`
}

// Validate reports whether this reference is well-formed. It does NOT report whether it resolves —
// that needs the stores, and it happens at the write boundary (task 2.4) where a reference that does
// not resolve fails the write rather than being persisted for a reader to discover.
func (e EvidenceRef) Validate() error {
	if !e.Surface.Valid() {
		return fmt.Errorf("assessment: evidence surface %q is not one of %s",
			e.Surface, strings.Join(SurfaceNames(), ", "))
	}
	if strings.TrimSpace(e.Locator) == "" {
		return fmt.Errorf("assessment: an evidence reference into %s carries no locator", e.Surface)
	}
	return nil
}

// Path renders the reference as the platform route that serves it.
//
// 🔴 Derived here rather than stored on the row, so that a route change is a change in ONE place. A
// persisted URL is a copy of the router, and the copy is what serves a 404 to a reader following a
// two-month-old finding.
//
// 🔴 THE LOCATOR IS ESCAPED, and the live run against `nousresearch/hermes-agent` is why. A workflow
// id is customer-chosen text and that repository's is `github.com/nousresearch/hermes-agent` — three
// slashes. Unescaped, this produced
// `/api/v1/workflows/github.com/nousresearch/hermes-agent/pattern-graph`: a path with four extra
// segments that matches no route, and that anything parsing it reads as a workflow called
// `github.com`. Nothing errored; the link simply went somewhere else.
func (e EvidenceRef) Path() string {
	locator := url.PathEscape(e.Locator)
	switch e.Surface {
	case SurfaceGraph:
		return "/api/v1/workflows/" + locator + "/pattern-graph"
	case SurfaceBoard:
		return "/api/v1/workflows/" + locator + "/eval-board"
	case SurfaceScorecard:
		return "/api/v1/variants/" + locator + "/scorecard"
	default:
		// Unreachable through any constructor: Validate refuses an unknown surface before a
		// reference reaches a finding. Returning "" rather than panicking keeps a corrupted row a
		// rendering problem rather than a process one.
		return ""
	}
}
