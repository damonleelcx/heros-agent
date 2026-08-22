package transform

import (
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Harness materialization for the GO AST engine (P18 §5, narrowed by §11).
//
// Go's DRIVE half works and its DECIDE half does not, and the asymmetry is a fact about STATIC TYPING
// rather than about effort — the same shape the memory materializer next door carries, for the same
// reason. It is worth stating precisely, because "Go is unsupported" would be false and would hide which
// half is actually blocked.
//
// # The drive half would work today
//
// A generic `func Run[T any](nodeID string, maxTurns int, invoke func() (T, error)) (T, error)` re-invokes
// any SDK's call without importing it: a thunk goes in, the same type comes out.
//
// # The decide half cannot be written without the customer's SDK
//
// Deciding whether to take another turn means READING THE ANSWER — does it contain the stop marker, did
// the model stop asking for a tool. In Python a message is a dict and its text is readable without knowing
// any SDK. In Go the response is the customer's own type (`*anthropic.Message`), and the generated module
// would have to import their SDK to read a field off it, or reflect over a shape it has no evidence for.
//
// 🔴 So Go materializes exactly the identity and refuses every multi-turn strategy with
// CauseNotAtCallSite and NO missing artifact. That asymmetry is the point: there is nothing to build, so
// naming an artifact would promise work that would not help. A Go user selecting `reflexion` is told why,
// and told that Python does it — never handed a fixed-turn loop wearing reflexion's name, which would run
// one strategy under another's `config_hash`.

// materializeHarness is the Go AST engine's entry for the harness dimension.
func materializeHarness(site discovery.GoCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimHarness)

	if o.Harness == nil || o.Harness.IsSingleShot() {
		// The identity. One turn is exactly the un-rewritten call site, so nothing is emitted and nothing
		// is refused — the same treatment `none` gets for memory and `full` for context.
		return nil, nil
	}
	strategy := o.Harness.Spec.Strategy

	// 🔴 P34: an EXECUTION ENVELOPE reaches no rewriter. It is not a loop and there is nothing to wrap —
	// its ceilings and host-service provision are checked at RESOLVE (variantspec/envelope.go) and by the
	// sandbox at execution, both of which happen without a codemod.
	//
	// 🚫 It returns (nil, nil) — the no-op — rather than a refusal, and the distinction matters. A refusal
	// would tell the author that binding an envelope failed, when it succeeded completely: it resolved,
	// it hashed, it gated their loop. There is simply no source to change, which is the same answer the
	// identity strategy gets one branch above and for the same reason.
	if strategy == registry.StrategyEnvelope {
		return nil, nil
	}

	// A strategy needing a host service is refused first and identically in every language, because the
	// reason is not about Go: a call site offers no injection point for a tool executor, a planner, or a
	// critic (decisions.md D-10). Asking the language question first would tell a `react-loop` user to
	// wait for a Go materializer that would refuse them anyway.
	if svc := harnessHostService(strategy); svc != "" {
		return nil, refuseHostService(site.NodeID, dim, strategy, svc)
	}

	return nil, refuseNotAtCallSite(site.NodeID, dim,
		"harness strategy %q decides whether to take another turn by reading the ANSWER's text, and at a Go "+
			"call site a response is your SDK's own type — the generated module would have to import your "+
			"SDK to read a field off it, or reflect over a shape it has no evidence for. Go therefore "+
			"materializes only %q, whose single turn is the un-rewritten call site. Python materializes %q "+
			"today, because there a response is a dict and its text is readable without knowing any SDK",
		strategy, registry.StrategySingleShot, strategy)
}

// HarnessStrategyMaterializesIn reports whether a (language, strategy) cell can be materialized. Read by
// the coverage table and by the rewriters, so the claim and the behaviour cannot disagree.
//
// 🔴 The identity is true everywhere and is checked FIRST, before the materializer table, because
// `single-shot` is not something a rewriter does — it is the absence of a rewrite. A user selecting it in
// Rust must not be told their language is uncovered for a change that consists of emitting nothing.
func HarnessStrategyMaterializesIn(language, strategy string) bool {
	if strategy == registry.StrategySingleShot {
		return true
	}
	// 🔴 P34: an EXECUTION ENVELOPE is never materialized at a call site, in any language, and this guard
	// is what stops the loop dispatch from trying. Without it the envelope reaches the branches below,
	// passes them (it needs no host service, and Python has a materializer), and this function claims a
	// call-site rewrite exists for a thing that has no turns to wrap — so the engine would attempt to
	// wrap the call in a loop driven by params that describe a sandbox.
	//
	// The envelope's own coverage cell says the same thing on the harness axis, with the reason; see
	// harnessCoverage.
	if strategy == registry.StrategyEnvelope {
		return false
	}
	if harnessHostService(strategy) != "" {
		return false // needs an injection point no call site has, in any language
	}
	if !HasHarnessMaterializer(language) {
		return false
	}
	// Go's runtime is answer-blind: it can drive the call but cannot read the response's text without
	// importing the customer's SDK, so only the identity survives there. See the file comment.
	return !isGoLanguage(language)
}

func isGoLanguage(language string) bool {
	return strings.EqualFold(strings.TrimSpace(language), "go")
}
