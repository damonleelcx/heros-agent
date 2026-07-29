package transform

import (
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Memory materialization for the GO AST engine — and the row that is missing (P18 §4)
// ─────────────────────────────────────────────────────────────────────────────────
//
// Go's recall half is easy and its record half is not, and the asymmetry is a fact about STATIC TYPING
// rather than about effort. It is worth stating precisely, because "Go is unsupported" would be false
// and would hide which half is actually blocked.
//
// # The recall half would work today
//
//	Messages: []anthropic.MessageParam{…}   →   Messages: agentmem.Recall("<node>", []anthropic.MessageParam{…})
//
// A generic `func Recall[T any](nodeID string, msgs []T) []T` is type-safe against ANY SDK's message
// type without importing it: values go in, the same values plus recalled ones come out. The registry row
// already locates the list (`prompt: {field: "params.Messages"}`), so there is nothing to guess.
//
// # The record half needs one thing this engine has no verified spelling for
//
// Recording a turn means storing what was SENT and what came BACK. In Python those are the same shape —
// a dict — so the generated module coerces the response with duck typing. In Go they are DIFFERENT
// TYPES: `client.Messages.New(…)` returns `*anthropic.Message`, while the store holds
// `anthropic.MessageParam`. Converting one to the other is SDK-specific, per provider.
//
// 🔴 So a Go memory materialization needs a per-(language, provider) RESPONSE CONVERSION — exactly the
// shape skillbind.go's `toolValueForms` uses for tool values, and for the same reason: the SHAPE is
// language-neutral, only the SPELLING is per SDK.
//
// # Why this table ships EMPTY rather than with a plausible row
//
// 🚫 This module cannot compile against any real SDK. The Go fixture is committed as `.txt` precisely
// because "a directory of real .go files importing an SDK this module does not depend on would break
// `go build ./...` for the whole repo" (engine_test.go). So a row written here could not be verified to
// compile, let alone to behave — and ADR-001 names exactly that as the top risk: *a bad codemod can
// break a build or subtly change behavior*, with the wrong-but-compiling version the worse half.
//
// An unverified row is not a partial capability. It is a guess that would be emitted into a customer's
// repository, and this table treats an absent provider the same way `toolValueForms` treats Bedrock: a
// provider whose spelling this engine has no evidence for, refused by name rather than guessed at.
//
// What a row costs, so the next person does not have to rediscover it: one entry below, one fixture that
// compiles against that SDK, and the conformance assertion that Go's materialized behaviour matches
// internal/memoryruntime — the same bar the Python module already clears by execution.

// memoryResponseForm is how ONE provider's response value is converted, in ONE language, into the
// message value the store holds.
//
// Both fields are required. A form with a conversion but no note would be a spelling nobody can date to
// an SDK generation, which is how a row silently rots past a major version.
type memoryResponseForm struct {
	// convert renders the expression that turns the response variable into a stored message value.
	// e.g. func(resp string) string { return resp + ".ToParam()" }
	convert func(responseExpr string) string
	// sdkNote names the SDK generation this spelling targets, for a refusal and for the capability doc.
	sdkNote string
}

// memoryResponseCell is one coverage cell: a provider's response conversion IN ONE LANGUAGE.
type memoryResponseCell struct{ language, provider string }

func memoryResponseKey(language, provider string) memoryResponseCell {
	return memoryResponseCell{
		language: strings.ToLower(strings.TrimSpace(language)),
		provider: strings.ToLower(strings.TrimSpace(provider)),
	}
}

// memoryResponseForms is the coverage table: (language, provider) → how to convert a response there.
//
// 🔴 It is deliberately EMPTY, and the emptiness is evidence rather than a TODO. See the file comment:
// nothing here can be compiled against a real SDK, so every row would be an unverified guess emitted
// into a customer's repository. Python needs no row because its message and response shapes are the
// same duck-typed dict; Go needs one per provider because they are different static types.
var memoryResponseForms = map[memoryResponseCell]memoryResponseForm{}

// MemoryResponseProviders lists the (language, provider) pairs with a verified response conversion,
// sorted. Read by the refusal and by the coverage table, so there is one answer.
func MemoryResponseProviders() []string {
	out := make([]string, 0, len(memoryResponseForms))
	for cell := range memoryResponseForms {
		out = append(out, cell.language+"/"+cell.provider)
	}
	sort.Strings(out)
	return out
}

// hasMemoryResponseForm reports whether a cell can convert a response into a stored message.
func hasMemoryResponseForm(language, provider string) bool {
	_, ok := memoryResponseForms[memoryResponseKey(language, provider)]
	return ok
}

// materializeMemory is the Go AST engine's entry for the memory dimension.
//
// It refuses, and the refusal names WHICH HALF is blocked and WHAT would unblock it — which is the whole
// improvement over "Go is unsupported". The recall half is ready; the record half needs a per-provider
// response conversion this engine has no verified spelling for.
func materializeMemory(site discovery.GoCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimMemory)

	// The identity strategy changes nothing, so there is nothing to materialize and nothing to refuse.
	if o.Memory == nil || o.Memory.IsNone() {
		return nil, nil
	}
	strategy := o.Memory.Spec.Strategy
	provider := providerHintFor(site)

	if hasMemoryResponseForm("go", provider) {
		// Unreachable while the table is empty. It is written as a refusal rather than as materialization
		// so that adding a row cannot silently enable an untested emission path: the row's author has to
		// come here, and the sentence tells them what the row still owes.
		return nil, refuseNoMaterializer(site.NodeID, dim,
			"a response conversion is declared for (go, %s) but the Go emission path is not wired to it yet; "+
				"adding a row to memoryResponseForms is necessary and not sufficient, because nothing here "+
				"can be compiled against a real SDK — the row needs a fixture that builds against %s and a "+
				"conformance assertion that the materialized behaviour matches internal/memoryruntime",
			provider, provider)
	}

	return nil, refuseNoMaterializer(site.NodeID, dim,
		"memory strategy %q needs to record what this call returns, and in Go the response and the stored "+
			"message are DIFFERENT TYPES: this call returns the SDK's response value while the store holds "+
			"its message-parameter value. Converting between them is specific to %s, and this engine has no "+
			"verified spelling for it (declared today: %s). The read half would work — a generic recall is "+
			"type-safe against any SDK's message slice without importing it — but the read half ALONE is "+
			"never emitted: a memory that recalls from a store nothing fills behaves exactly like `none` "+
			"while its config_hash claims %q. Python materializes this strategy today, because there a "+
			"response and a message are the same shape",
		strategy, providerDisplay(provider), memoryResponseProvidersDisplay(), strategy)
}

func memoryResponseProvidersDisplay() string {
	got := MemoryResponseProviders()
	if len(got) == 0 {
		return "none"
	}
	return strings.Join(got, ", ")
}
