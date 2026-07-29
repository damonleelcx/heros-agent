package transform

import (
	"fmt"
	"sort"
	"strconv"
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
var memoryResponseForms = map[memoryResponseCell]memoryResponseForm{
	{"go", "anthropic"}: {
		// VERIFIED against the SDK in the module cache, not written from memory: `func (r Message)
		// ToParam() MessageParam` lives in messageutil.go, not message.go, and there is no such method on
		// any other response type in the package. `client.Messages.New` returns (*Message, error), and a
		// value-receiver method is reachable on the pointer.
		convert: func(responseExpr string) string { return responseExpr + ".ToParam()" },
		sdkNote: "anthropic-sdk-go v1 (Message.ToParam, messageutil.go)",
	},
}

// memoryContentBlindStrategies are the strategies whose retention is a function of COUNT rather than of
// message CONTENT. They are the ones a content-blind runtime can implement.
//
// 🔴 This is why Go materializes a strict SUBSET of what Python does, and the asymmetry is a fact rather
// than an omission. Python reads `m["content"]` off any SDK's message because it is a dict; Go cannot
// read a field off the customer's SDK type without importing it. A Go user selecting `summary-buffer` is
// refused with that reason — never handed a `scratchpad` wearing its name, which would run one strategy
// under another's config_hash.
var memoryContentBlindStrategies = map[string]bool{
	"none":       true,
	"scratchpad": true,
}

// MemoryStrategyMaterializesIn reports whether a (language, strategy) cell can be materialized. Read by
// the coverage table and by the rewriters, so the claim and the behaviour cannot disagree.
func MemoryStrategyMaterializesIn(language, strategy string) bool {
	if !HasMemoryMaterializer(language) {
		return false
	}
	if strings.EqualFold(language, "go") {
		// Go's runtime is content-blind (see memoryContentBlindStrategies), and it additionally needs a
		// response conversion, which is per provider and checked at the call site.
		return memoryContentBlindStrategies[strategy]
	}
	return true
}

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
// Both halves or refuse, exactly as in Python — but Go's refusal reasons are its own, because its
// blockers are: a content-reading strategy (the runtime is content-blind), and a provider with no
// verified response conversion.
func materializeMemory(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimMemory)

	if o.Memory == nil || o.Memory.IsNone() {
		return nil, nil
	}
	strategy := o.Memory.Spec.Strategy
	provider := providerHintFor(site)

	// 1. The STRATEGY. A content-reading strategy cannot run on a content-blind runtime, and there is no
	//    honest degradation: handing back a `scratchpad` would run one strategy under another's hash.
	if !memoryContentBlindStrategies[strategy] {
		return nil, refuseNotAtCallSite(site.NodeID, dim,
			"memory strategy %q decides what to keep by reading message TEXT, and at a Go call site a message "+
				"is your SDK's own type — the generated module would have to import your SDK to read a field "+
				"off it, or reflect over a shape it has no evidence for. Go therefore materializes only the "+
				"strategies whose retention is a function of count (%s). Python materializes %q today, because "+
				"there a message is a dict and its content is readable without knowing any SDK",
			strategy, memoryContentBlindDisplay(), strategy)
	}

	// 2. The PROVIDER's response conversion. Recording a turn stores what was sent AND what came back, and
	//    in Go those are different static types.
	form, ok := memoryResponseForms[memoryResponseKey("go", provider)]
	if !ok {
		return nil, refuseNoMaterializer(site.NodeID, dim,
			"recording a turn stores what this call returned, and in Go the response and the stored message "+
				"are DIFFERENT TYPES: this call returns the %s SDK's response value while the store holds its "+
				"message-parameter value. This engine has no verified conversion between them for %s "+
				"(declared today: %s). The read half would work, but it is never emitted alone — a memory that "+
				"recalls from a store nothing fills behaves exactly like `none` while its config_hash claims %q",
			providerDisplay(provider), providerDisplay(provider), memoryResponseProvidersDisplay(), strategy)
	}

	// 3. The RECALL half: the registry row locates the written message list.
	loc := site.ArgMap.Prompt
	if loc == nil {
		return nil, refuseShape(site.NodeID, dim,
			"the registry row for this call site declares no message-list locator, so memory strategy %q has "+
				"no list to prepend recalled turns to", strategy)
	}
	fset := site.File.Fset
	expr, found := discovery.LocateArg(site.Call, loc, fset)
	if !found || expr == nil {
		return nil, refuseShape(site.NodeID, dim,
			"this call site does not write its message list, so memory strategy %q has nothing to read into "+
				"or record from. Memory augments a written list; a call that does not write one cannot carry it",
			strategy)
	}
	start, end, err := byteRange(fset, expr, len(src))
	if err != nil {
		return nil, refuseShape(site.NodeID, dim, "%v", err)
	}

	// 4. The RECORD half, computed BEFORE any edit is emitted — the whole of D2 in code.
	rec, err := goMemoryRecordTarget(site, src, dim, strategy)
	if err != nil {
		return nil, err
	}

	node := strconv.Quote(site.NodeID)
	written := string(src[start:end])
	return []edit{
		{Start: start, End: end, New: fmt.Sprintf("agentmem.Recall(%s, %s)", node, written),
			NodeID: site.NodeID, Dim: dim},
		{Start: rec.insertAt, End: rec.insertAt,
			New:    fmt.Sprintf("; agentmem.Record(%s, %s, %s)", node, written, form.convert(rec.target)),
			NodeID: site.NodeID, Dim: dim},
	}, nil
}

// goMemoryRecordTarget finds the variable this call's response lands in, or refuses.
func goMemoryRecordTarget(site discovery.GoCallSite, src []byte, dim, strategy string) (recordTarget, error) {
	lines := strings.Split(string(src), "\n")
	if site.LineStart < 1 || site.LineStart > len(lines) {
		return recordTarget{}, refuseShape(site.NodeID, dim,
			"this call site's location is outside the file this engine read, so the record half of memory "+
				"strategy %q cannot be placed", strategy)
	}
	stmt := lines[site.LineStart-1]

	name, ok := goAssignTarget(stmt)
	if !ok {
		return recordTarget{}, refuseShape(site.NodeID, dim,
			"memory strategy %q needs to record what this call returns, and that means naming the variable it "+
				"lands in — but this call's result is not assigned at statement level (`%s`). The read half "+
				"alone is NOT emitted: a memory that recalls from a store nothing ever fills behaves exactly "+
				"like `none` while its config_hash claims %q. Assign the call's result and this materializes",
			strategy, strings.TrimSpace(stmt), strategy)
	}

	end := site.LineEnd
	if end < site.LineStart {
		end = site.LineStart
	}
	if end > len(lines) {
		return recordTarget{}, refuseShape(site.NodeID, dim,
			"this call site spans past the end of the file this engine read, so the record half of memory "+
				"strategy %q cannot be placed", strategy)
	}
	offset := 0
	for i := 0; i < end; i++ {
		offset += len(lines[i]) + 1
	}
	offset--
	return recordTarget{insertAt: offset, indent: leadingIndent(stmt), target: name}, nil
}

// goAssignTarget returns the variable a Go statement assigns this call's response to.
//
// Go's shapes differ from Python's: the idiom is `resp, err := call(...)`, so the RESPONSE is the first
// name and the error is the second. Deliberately narrow — it accepts `:=` and `=` with a plain
// identifier list, and rejects everything else, because any other shape puts the response somewhere a
// generated statement cannot name.
//
// 🔴 `_` is rejected as the response name. `_, err := call(...)` discards the very value the record half
// exists to store, so materializing there would emit `agentmem.Record(..., _.ToParam())` — which does not
// compile. Refusing names the reason instead.
func goAssignTarget(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "//") {
		return "", false
	}
	cut := strings.Index(s, ":=")
	width := 2
	if cut < 0 {
		// A plain `=`, but not `==`, `!=`, `<=`, `>=` or an augmented form.
		for i := 0; i < len(s); i++ {
			if s[i] != '=' {
				continue
			}
			if i+1 < len(s) && s[i+1] == '=' {
				return "", false
			}
			if i > 0 && strings.ContainsRune("=!<>+-*/%&|^", rune(s[i-1])) {
				return "", false
			}
			cut, width = i, 1
			break
		}
	}
	if cut <= 0 {
		return "", false
	}
	lhs := strings.TrimSpace(s[:cut])
	_ = width
	if lhs == "" {
		return "", false
	}
	first := strings.TrimSpace(strings.Split(lhs, ",")[0])
	if first == "" || first == "_" {
		return "", false
	}
	for _, r := range first {
		if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return "", false
		}
	}
	return first, true
}

func memoryContentBlindDisplay() string {
	out := make([]string, 0, len(memoryContentBlindStrategies))
	for s := range memoryContentBlindStrategies {
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func memoryResponseProvidersDisplay() string {
	got := MemoryResponseProviders()
	if len(got) == 0 {
		return "none"
	}
	return strings.Join(got, ", ")
}
