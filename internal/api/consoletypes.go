package api

import (
	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/harnessruntime"
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

// ConsoleEnum is one CLOSED GO VOCABULARY the console must switch on exhaustively (P31 task 4.5).
//
// # Why an enum needs generating at all, when a field rename already did
//
// ADR-007 generates the console's types because a field renamed in Go arrives in the browser as
// `undefined` and renders as an em-dash that looks like legitimately absent data. A closed vocabulary
// has the SAME failure one level down and it is worse, because the em-dash at least appears: a message
// KIND added in Go and absent from the browser's union lands in a `default:` arm and renders as NOTHING.
// A blank card in a transcript is indistinguishable from a message that was never sent.
//
// So the vocabulary is generated as a TypeScript string-literal union, checked in, and gated by
// `make console-types-check`. Adding a kind in Go without regenerating is a red build; adding one WITH
// regenerating makes every non-exhaustive `switch` in the console a type error, which is the point.
//
// 🔴 `Members` is read from the OWNING PACKAGE's own accessor — `conversation.Kinds()`, not a list
// retyped here. A list retyped here is a second copy of a closed set, and the copy is what goes stale.
type ConsoleEnum struct {
	// Name is the exported TypeScript type name.
	Name string
	// Sample is a zero value of the named Go string type. Reflection keys fields on it.
	Sample any
	// Members is the closed set, in the order the console should read them.
	Members []string
	// Doc is one line, carried into the generated file so a reader of the union knows what it decides.
	Doc string
}

// ConsoleEnums is every closed vocabulary the console renders.
func ConsoleEnums() []ConsoleEnum {
	return []ConsoleEnum{
		{
			Name: "ConversationKind", Sample: conversation.Kind(""),
			Members: stringsOf(conversation.Kinds()),
			Doc: "The closed set of things the conversational console may say (P31 FR1). A kind added " +
				"in Go and missing here fails the console type-check rather than rendering blank.",
		},
		{
			Name: "ConversationProvenance", Sample: conversation.Provenance(""),
			Members: []string{string(conversation.ProvenancePinned), string(conversation.ProvenanceGenerated)},
			Doc: "Whether a message replayed a pinned inference or was generated in this turn. Without " +
				"it P30's determinism guarantee is unfalsifiable, and therefore a claim rather than a guarantee.",
		},
		{
			Name: "ConversationPhase", Sample: conversation.Phase(""),
			Members: stringsOf(conversation.Phases()),
			Doc:     "The five phases a turn advances through. A turn that cannot name its phase is a defect, not a slow turn.",
		},
		{
			Name: "ConversationStepState", Sample: conversation.StepState(""),
			Members: stringsOf(conversation.StepStates()),
			Doc: "How one planned step resolved. Everything except `done` names a reason — a `skipped` " +
				"with no reason is the omission problem with a label on it.",
		},
		{
			Name: "ConversationFindingState", Sample: conversation.FindingState(""),
			Members: stringsOf(conversation.FindingStates()),
			Doc:     "The four states of a finding. They render differently or the component is wrong.",
		},
		{
			Name: "ConversationFailureClass", Sample: conversation.FailureClass(""),
			Members: stringsOf(conversation.FailureClasses()),
			Doc: "503 not-mounted, 404 not-found, transport. Three things a person does three different " +
				"things about; one apologetic sentence tells them to do none.",
		},
		{
			Name: "StopReason", Sample: harnessruntime.StopReason(""),
			Members: stringsOf(harnessruntime.StopReasons()),
			Doc: "Why a run ended, from the harness runtime's own closed set — the SAME vocabulary a node " +
				"loop uses. Expand-only: it participates in `version_id`.",
		},
		{
			Name: "ConversationIntent", Sample: conversation.Intent(""),
			Members: intentNames(),
			Doc:     "The fourteen things this surface can be asked. Equal, by fence, to the set of working surfaces.",
		},
	}
}

// stringsOf converts a slice of named string types to plain strings, so each vocabulary is read from
// its owner's accessor rather than retyped here.
func stringsOf[T ~string](in []T) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, string(v))
	}
	return out
}

func intentNames() []string {
	specs := conversation.Intents()
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, string(s.Intent))
	}
	return out
}

// ConsoleViewTypes is every read model the P9 console renders.
func ConsoleViewTypes() []ConsoleViewType {
	return []ConsoleViewType{
		// ── P2 · configure / diff / run ──────────────────────────────────────
		{Name: "RunView", Sample: runView{}, Endpoint: "GET /api/v1/runs/{run_id}"},
		{Name: "TransformView", Sample: transformView{}, Endpoint: "GET /api/v1/transforms/{config_hash}/{source_revision}"},
		// 🔴 The SAME endpoint's other shape (P29 §6.5). Registered as its own view because the response
		// is a union discriminated by `origin`, and a console that has a declaration for only one arm of
		// a union has a claim that is false half the time — which is how that route came to answer 500.
		{Name: "ReportedTransformView", Sample: reportedTransformView{}, Endpoint: "GET /api/v1/transforms/{config_hash}/{source_revision} (origin: reported)"},
		{Name: "SubmitResult", Sample: submitResult{}, Endpoint: "POST /api/v1/specs/submit"},
		{Name: "SpecError", Sample: specError{}, Endpoint: "POST /api/v1/specs/{resolve,submit} (rejection)"},

		// ── P2.5 · live run monitor ──────────────────────────────────────────
		{Name: "RunMonitor", Sample: telemetry.RunMonitor{}, Endpoint: "GET /api/v1/runs/{run_id}/monitor"},

		// ── P3.5 · pattern-classified graph ──────────────────────────────────
		{Name: "GraphView", Sample: patternclassifier.GraphView{}, Endpoint: "GET /api/v1/workflows/{workflow_id}/pattern-graph"},

		// ── P4 · eval board ──────────────────────────────────────────────────
		{Name: "BoardView", Sample: evalboard.View{}, Endpoint: "GET /api/v1/workflows/{workflow_id}/eval-board"},

		// ── P30 · the eval set behind the board's denominator ────────────────
		{Name: "EvalSetView", Sample: evalboard.EvalSetView{}, Endpoint: "GET /api/v1/workflows/{workflow_id}/eval-set"},

		// ── P4.5 · attribution scorecard ─────────────────────────────────────
		{Name: "ScorecardView", Sample: scorecard.View{}, Endpoint: "GET /api/v1/variants/{variant_id}/scorecard"},

		// ── P5.5 · proposals and their verified deltas ───────────────────────
		{Name: "ProposalSurface", Sample: Surface{}, Endpoint: "GET /api/v1/workflows/{workflow_id}/proposals"},
		{Name: "PRResult", Sample: PRResult{}, Endpoint: "POST /api/v1/workflows/{workflow_id}/proposals/{proposal_id}/open-pr"},

		// ── P7 · plan, entitlements, spend ───────────────────────────────────
		{Name: "BillingView", Sample: BillingView{}, Endpoint: "GET /api/v1/customers/{customer_id}/billing"},

		// ── P21 · payment collection (plans by name, payment method, checkout) ──
		{Name: "PaymentView", Sample: PaymentView{}, Endpoint: "GET /api/v1/customers/{customer_id}/payment"},
		{Name: "CheckoutView", Sample: CheckoutView{}, Endpoint: "POST /api/v1/customers/{customer_id}/checkout-session"},
		{Name: "PlanChangeView", Sample: PlanChangeView{}, Endpoint: "POST /api/v1/customers/{customer_id}/plan"},

		// ── P11 · a run the CLI transmitted, read back ───────────────────────
		{Name: "LinkedRunView", Sample: LinkedRunView{}, Endpoint: "GET /api/v1/runs/{run_id}/link"},

		// ── P12 · forge delivery state + route condition ─────────────────────
		{Name: "DeliveriesView", Sample: DeliveriesView{}, Endpoint: "GET /api/v1/deliveries"},

		// ── P13 13d · total per-axis, per-language coverage ───────────────────
		{Name: "AxisCoverageView", Sample: coverageView{}, Endpoint: "GET /api/v1/coverage"},

		// ── P13 13e · how an accepted change reaches a running agent ──────────
		{Name: "ChangeDeliveryView", Sample: ChangeDeliveryView{}, Endpoint: "GET /api/v1/change-delivery"},

		// ── P20 · how to install it, and what the release actually delivered ──
		{Name: "InstallView", Sample: installView{}, Endpoint: "GET /api/v1/install"},

		// ── P31 · the conversational console ─────────────────────────────────
		//
		// 🔴 `ConversationMessage` is a DISCRIMINATED UNION on the wire: `kind` names which of the eight
		// payload fields is non-null. Generating it as one interface with eight nullable payloads is
		// what lets a consumer narrow — `if (m.kind === "finding") m.finding!.claim` — and what makes a
		// kind added in Go with no payload field a compile error rather than a blank card.
		{Name: "ConversationView", Sample: conversationView{}, Endpoint: "POST /api/v1/conversations"},
		{Name: "ConversationTurnView", Sample: turnView{}, Endpoint: "POST /api/v1/conversation-turns"},
		{Name: "ConversationMessage", Sample: conversation.Message{}, Endpoint: "GET /api/v1/conversation-stream (event: message)"},
		{Name: "ConversationTurnState", Sample: conversation.TurnState{}, Endpoint: "GET /api/v1/conversation-stream (event: state) and GET /api/v1/conversation-trace"},
	}
}
