package registry

import "fmt"

// KIND EXHAUSTIVENESS — a switch that compiles without the new case is a consumer that mis-seals
// ──────────────────────────────────────────────────────────────────────────────────────────────
//
// P34 task 3.3, PRD §9.3. Adding a `Kind` touches the seal path, the DB trigger's argument, and every
// consumer that answers a question per registry. Go's `switch` is never exhaustive-checked: a `switch`
// over `Kind` missing a case compiles, runs, and falls through to whatever the default does. When the
// question is "which table does this kind live in", the default is the difference between sealing a loop
// into `loop_entry` and silently sealing it somewhere else — or nowhere.
//
// 🔴 So the per-Kind answer is not a switch at all. `kindAnswers` takes ONE POSITIONAL ARGUMENT PER
// KIND. Adding a Kind means adding a parameter, which means **every call site fails to build** with
// "not enough arguments in call to kindAnswers". That is the mechanism task 3.3 asks for, and it is a
// build failure rather than a test failure — the distinction that matters, because a test can be
// skipped and a build cannot.
//
// 🚫 It is deliberately NOT an unkeyed struct literal, which is the other Go idiom for this. `go vet`'s
// `composites` analyzer flags unkeyed literals of imported struct types, and `make vet` is a CI gate —
// a fence that makes the build red for the wrong reason gets deleted, and then there is no fence.

// Kinds is the closed set, in a stable order. Exported so a consumer iterating registries cannot
// silently miss one; the ORDER is the order kindAnswers takes its arguments in, and a test pins the two
// together so a caller cannot pass its answers in the wrong order and still compile.
func Kinds() []Kind {
	return []Kind{KindModel, KindPrompt, KindSkill, KindContext, KindMemory, KindHarness, KindLoop}
}

// kindAnswers builds a total map from Kind to T, one argument per Kind, in Kinds() order.
//
// The parameter list IS the fence. Do not add a variadic, a slice, or a map parameter here: each of
// those would let a caller supply six answers for seven kinds and compile.
func kindAnswers[T any](model, prompt, skill, contextKind, memory, harness, loop T) map[Kind]T {
	return map[Kind]T{
		KindModel:   model,
		KindPrompt:  prompt,
		KindSkill:   skill,
		KindContext: contextKind,
		KindMemory:  memory,
		KindHarness: harness,
		KindLoop:    loop,
	}
}

// kindTables is the one place a Kind is turned into the table its entries live in. Every registry file
// reads its table through this, so "a seventh registry" is a parameter here and a compile error
// everywhere else, rather than a string literal somebody remembers to add.
var kindTables = kindAnswers(
	tableModel, tablePrompt, tableSkill, tableContext, tableMemory, tableHarness, tableLoop,
)

// TableFor returns the table a Kind's entries are stored in. Unexported callers use it directly; the
// error path exists for a Kind value that came from stored bytes rather than from this package's
// constants — a corrupt envelope, in other words, which must not resolve to a table by accident.
func tableFor(kind Kind) (string, error) {
	t, ok := kindTables[kind]
	if !ok {
		return "", fmt.Errorf("%w: %q is not a registry kind (known: %v)", ErrCorruptEntry, kind, Kinds())
	}
	return t, nil
}
