package hostedcompile

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// gate.go is the build gate as a HOSTED deployment can actually run it.
//
// # Why this is not GoBuildChecker
//
// proposal.GoBuildChecker shells out to `go build ./...`. The deployed image is
// gcr.io/distroless/base-debian12:nonroot — no Go, no compilers, nothing to exec. worktree's seven
// language verifiers all start with requireTool and would fail the same way. So on this deployment the
// strongest available gate is the one that needs no toolchain at all.
//
// It would also be a decision worth making on purpose rather than by default: `go build` on a
// customer's repository executes their build — cgo directives, toolchain directives, module fetches —
// and internal/sandbox exists because running a customer's code is the platform's sharpest boundary. A
// build gate on the platform belongs inside that isolate, in an image that carries the toolchain. This
// gate deliberately runs no subprocess of any kind.
//
// # What a parse gate actually proves, and what it must never claim
//
// It proves the codemod emitted a file the language's own parser accepts. That is a REAL check with a
// real failure mode: a rewriter that drops a brace, mangles an import block or produces an unbalanced
// call site is caught here, and those are the codemod bugs that reach a customer's pull request as
// obvious garbage.
//
// It does NOT prove the program type-checks. worktree.Strength already has the vocabulary for exactly
// this distinction — StrengthSyntaxChecked is documented as "only parse validity was proved; a type
// error would NOT have been caught" — and Strength.AllowsAutonomousApply is false for it.
//
// 🔴 So a parse pass is reported as UNAVAILABLE, not as Builds=true. `built` is the claim ADR-001 hangs
// delivery on ("a diff that fails to compile is rejected before it is surfaced"), and a parser cannot
// make it. What this gate can do is REJECT — a parse failure is a real build_failed — and otherwise say
// precisely what it did and did not prove.

// ParseGate is a build gate that parses the codemod's output rather than compiling it.
type ParseGate struct {
	// Language is the workflow's discovered language. It selects the parser; a language this gate has
	// no parser for is reported unavailable rather than passed.
	Language string
}

// Check parses every rewritten file.
func (g ParseGate) Check(_ context.Context, patch *transform.Patch) (proposal.BuildResult, error) {
	if patch == nil {
		return proposal.BuildResult{}, fmt.Errorf("hostedcompile: the parse gate requires a patch")
	}
	lang := strings.ToLower(strings.TrimSpace(g.Language))
	if lang != "go" {
		// Every other language's parser lives in a toolchain this image does not carry. Named, not
		// guessed: an operator reading this knows exactly what would have to change.
		return proposal.BuildResult{Unavailable: unavailableFor(lang)}, nil
	}

	// Deterministic order so the log of a multi-file failure is stable run to run.
	names := make([]string, 0, len(patch.Files))
	for name := range patch.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	fset := token.NewFileSet()
	var failures []string
	for _, name := range names {
		if !strings.HasSuffix(name, ".go") {
			// The codemod rewrote a non-Go file in a Go workflow (a config, a manifest). Not parsed and
			// not claimed — silently passing it would let this gate report success over a file it never
			// looked at.
			continue
		}
		if _, err := parser.ParseFile(fset, name, patch.Files[name], parser.ParseComments); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		// A REAL rejection. The codemod produced a file the Go parser will not accept, which would have
		// reached a customer's pull request as visibly broken code.
		return proposal.BuildResult{
			Builds: false,
			Log: "the generated diff does not parse:\n" + strings.Join(failures, "\n") +
				"\n\n(gate: go/parser, in-process; no compiler ran)",
		}, nil
	}

	// Parsed cleanly — and that is NOT `built`. See the file header.
	return proposal.BuildResult{Unavailable: parsedButNotBuilt}, nil
}

// Strength reports what this gate proved for a language, or the zero Strength when it proved nothing.
//
// The zero value is deliberately invalid (worktree.Strength documents that), and it is the right answer
// for a language with no parser here: a code path that forgot to state what it proved must not thereby
// claim the strongest guarantee.
func (g ParseGate) Strength() worktree.Strength {
	if strings.EqualFold(strings.TrimSpace(g.Language), "go") {
		return worktree.StrengthSyntaxChecked
	}
	return ""
}

const parsedButNotBuilt = "The generated diff PARSES (gate: go/parser, in-process). It was not " +
	"compiled or type-checked: this image carries no Go toolchain, so `go build` and the type checker " +
	"could not run. Syntax validity does not establish that the change compiles, which is what " +
	"delivery requires — so the proposal stays unbuilt and the diff is reviewable rather than deliverable."

// unavailableFor names what would have to exist for a language's diff to be gated here.
func unavailableFor(lang string) string {
	if lang == "" {
		lang = "an undetected language"
	}
	return fmt.Sprintf("No build gate ran: this deployment can parse Go in-process and carries no "+
		"toolchain for %s, so neither a compiler nor a type checker was available. The diff is "+
		"generated and reviewable; it is not verified to build.", lang)
}
