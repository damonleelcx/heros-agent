package api

import (
	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/scorecard"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// consoletypes.go is the registry the console's type generator reflects over (ADR-007, P9 task 6.1).
//
// # Why it lives in this package
//
// Four of the console's ten view types are UNEXPORTED — `runView`, `transformView`, `submitResult`
// and `specError` are declared in p2.go and are not part of this package's API. A generator in
// `cmd/` cannot see them. Rather than exporting four types purely to satisfy a tool (which would
// widen the package's real surface for a build-time reason), the registry lives here, in the one
// package that can name them, and hands out zero values.
//
// This is a deliberate, minimal widening of the package's surface, recorded so a future reader does
// not read it as accidental. It exports one function returning `[]any`; it exports no type.
//
// # Why a registry rather than "generate everything"
//
// A generator that walked every struct in the repository would emit a TypeScript file containing the
// executor's internals, the scoring cache, and the sandbox's audit records — none of which the browser
// receives, all of which would then LOOK like a contract. The console's contract is exactly the read
// models it renders, and listing them is how that stays true.
//
// # Adding a type here is a decision
//
// A type in this list becomes a checked-in artifact with a CI drift gate: changing it becomes a
// two-file change, and forgetting the second file becomes a red build. That is the cost, and it is
// the point — the alternative failure is a field renamed in Go arriving in the browser as `undefined`
// and rendering as an em-dash that looks like legitimately absent data.

// ConsoleViewType pairs a zero value with the name the generated TypeScript should use.
//
// The name is stated rather than derived from the Go type name because three of the sources are named
// `View` (`evalboard.View`, `scorecard.View`) or `Surface`, and a generator that used the bare Go name
// would emit two interfaces called `View` and silently keep the last one.
type ConsoleViewType struct {
	// Name is the exported TypeScript interface name.
	Name string
	// Sample is a zero value of the Go type. Reflection reads its structure; the value is never used.
	Sample any
	// Endpoint is the platform route this type is the response of, carried into the generated file as
	// a comment so a reader can get from a TypeScript symbol back to the Go handler in one step.
	Endpoint string
}

// ConsoleViewTypes is every read model the P9 console renders.
func ConsoleViewTypes() []ConsoleViewType {
	return []ConsoleViewType{
		// ── P2 · configure / diff / run ──────────────────────────────────────
		{Name: "RunView", Sample: runView{}, Endpoint: "GET /api/p2/runs/{run_id}"},
		{Name: "TransformView", Sample: transformView{}, Endpoint: "GET /api/p2/transforms/{config_hash}/{source_revision}"},
		{Name: "SubmitResult", Sample: submitResult{}, Endpoint: "POST /api/p2/specs/submit"},
		{Name: "SpecError", Sample: specError{}, Endpoint: "POST /api/p2/specs/{resolve,submit} (rejection)"},

		// ── P2.5 · live run monitor ──────────────────────────────────────────
		{Name: "RunMonitor", Sample: telemetry.RunMonitor{}, Endpoint: "GET /api/p25/runs/{run_id}/monitor"},

		// ── P3.5 · pattern-classified graph ──────────────────────────────────
		{Name: "GraphView", Sample: patternclassifier.GraphView{}, Endpoint: "GET /api/p35/workflows/{workflow_id}/graph"},

		// ── P4 · eval board ──────────────────────────────────────────────────
		{Name: "BoardView", Sample: evalboard.View{}, Endpoint: "GET /api/p4/workflows/{workflow_id}/board"},

		// ── P4.5 · attribution scorecard ─────────────────────────────────────
		{Name: "ScorecardView", Sample: scorecard.View{}, Endpoint: "GET /api/p45/variants/{variant_id}/scorecard"},

		// ── P5.5 · proposals and their verified deltas ───────────────────────
		{Name: "ProposalSurface", Sample: Surface{}, Endpoint: "GET /api/p55/workflows/{workflow_id}/surface"},
		{Name: "PRResult", Sample: PRResult{}, Endpoint: "POST /api/p55/workflows/{workflow_id}/proposals/{proposal_id}/open-pr"},

		// ── P7 · plan, entitlements, spend ───────────────────────────────────
		{Name: "BillingView", Sample: BillingView{}, Endpoint: "GET /api/p7/customers/{customer_id}/billing"},

		// ── P21 · payment collection (plans by name, payment method, checkout) ──
		{Name: "PaymentView", Sample: PaymentView{}, Endpoint: "GET /api/p21/customers/{customer_id}/payment"},
		{Name: "CheckoutView", Sample: CheckoutView{}, Endpoint: "POST /api/p21/customers/{customer_id}/checkout-session"},
		{Name: "PlanChangeView", Sample: PlanChangeView{}, Endpoint: "POST /api/p21/customers/{customer_id}/plan"},

		// ── P12 · forge delivery state + route condition ─────────────────────
		{Name: "DeliveriesView", Sample: DeliveriesView{}, Endpoint: "GET /api/p12/deliveries"},
	}
}
