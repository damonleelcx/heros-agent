package transform

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Language dispatch (ADR-003 decision 1)
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
// P1 shipped six language frontends; P2's Transform Engine never followed, and was hard-wired to
// `discovery.IndexGoCallSites` and `go/ast`. The platform could FIND an LLM call site in Python and
// change it only in Go — which, since most LLM application code in the world is Python, meant the apply
// path did not work where the market is.
//
// What is per-language is smaller than it looks. The rewrite primitive — replace the bytes of a located
// argument expression — is already language-neutral, and everything downstream of it (edit ordering,
// overlap detection, the minimality gate, the diff, the hash, determinism) is written against bytes and
// does not care what language produced them. Only two things differ:
//
//	how a call site's arguments are LOCATED  -> index
//	what "the result still parses" MEANS     -> reparse
//
// So that is exactly what a langEngine holds, and Generate is language-neutral above it.

// langEngine is everything language-specific about generating a patch.
//
// Both fields are required. There is no "reparse is optional" language: a gate that a new engine could
// forget to supply is a gate that a new engine WILL forget to supply, and the failure would be silent —
// patches shipping unchecked for one language only, indistinguishable in the record from patches that
// were checked. engineFor validates the row rather than trusting it.
type langEngine struct {
	// index locates every rewritable call site in the tree at root, keyed by node_id, with this
	// language's rewriters already bound to each one.
	index func(root string) (map[string]boundSite, error)
	// reparse is gateMinimal's "the result still parses" assertion for this language.
	reparse reparser
}

// boundSite is one located call site with its language's rewriters bound to it.
//
// # Why a closure and not an interface
//
// The two substrates hold genuinely different things — the Go site owns a *ast.CallExpr and a FileSet,
// a syntactic site owns byte spans — and neither is a degenerate case of the other. An interface over
// them would have to be either the union of both (so every Go rewriter would be handed nil spans it must
// remember to ignore, and every span rewriter a nil AST) or so abstract that each rewriter's first act
// is a type assertion back to what it actually needs. Both are the 胶水逻辑 the rules forbid, and both
// put the mistake — reading the wrong half — inside the rewriters, where it compiles.
//
// Binding instead means each language's rewriters keep their OWN concrete site type, fully typed, and
// what crosses into the neutral engine is a function. A Go rewriter cannot be handed a span site; it is
// not expressible.
type boundSite struct {
	fileRel            string
	lineStart, lineEnd int
	// rewrite turns one overridden dimension into byte edits at this call site, or returns a
	// *RewriteError naming the node and dimension it refused.
	rewrite func(dim variantspec.Dimension, src []byte, o variantspec.ResolvedOverride) ([]edit, error)
}

// engines is the per-language table: Discovery's language label -> how to rewrite it.
//
// 🔴 A table, not an if/else chain (禁止为省事用 if/else 穷举替代配置表 / 八级法则 L6). Adding the next
// frontend is a row, and the set of languages the apply path accepts is readable in one place rather
// than reconstructed from control flow.
//
// The keys are Discovery's language labels verbatim (discovery.DefaultFrontends), the same vocabulary
// worktree.verifiersByLanguage is keyed on. Not a parallel spelling — a second "typescript" would make
// a workflow un-rewritable for a reason nobody could see. TestEnginesCoverEveryFrontend holds it.
//
// # Go is not a row like the others, and that is the point
//
// Go keeps go/ast. It is not legacy and it is not a special case to be tidied away later: go/ast is
// STRICTLY STRONGER than a span. It carries real positions, real expression structure, and — the part
// that actually matters — enough of the SDK's shape that rewritePrompt can descend into
// `[]MessageParam{NewUserMessage(NewTextBlock("…"))}` and know the single string it finds is the prompt,
// because Go SDKs carry the message ROLE in a typed constant. Nothing in a span can establish that.
// Replacing Go's path with the syntactic one to make the table look uniform would trade a guarantee for
// symmetry — L1/L2 for L7 — which is the越级 the 核心法则 forbids outright.
//
// # Java, Kotlin and Rust have rows, and they refuse
//
// A row whose rewriters refuse is worth far more than an absent row. Absent, they would fall out of
// engineFor as "unsupported language", which is true of nothing here: Discovery finds their call sites
// perfectly well. The refusal a user needs to read names the REAL cause — no named-argument form, no
// arg_map in the registry rows — and that sentence has to come from a rewriter that got as far as
// looking. See argumentForms in rewrite_span.go, and the report in docs/adr/ADR-003.
var engines = map[string]langEngine{
	"go":         {index: indexGoSites, reparse: reparseGo},
	"python":     syntacticEngine("python"),
	"typescript": syntacticEngine("typescript"),
	"javascript": syntacticEngine("javascript"),
	"kotlin":     syntacticEngine("kotlin"),
	"java":       syntacticEngine("java"),
	"rust":       syntacticEngine("rust"),
}

// ErrLanguageNotRewritable is returned for a language with no engine.
var ErrLanguageNotRewritable = errors.New("transform: no rewrite engine is defined for this language")

// engineFor returns the engine for a Discovery language label.
//
// An unknown language is an ERROR, never a permissive default, and in particular never a fall-back to
// Go. A Go rewriter turned loose on a Python file would not fail — `discovery.IndexGoCallSites` would
// find nothing, every override would be rejected as ErrNodeNotFound, and the user would be told their
// node does not exist at source_revision. That is a lie with a plausible sentence attached, which is
// worse than an error (失败要显眼).
//
// "mixed" arrives here for a polyglot repository (discovery.workflowLanguageLabel) and is correctly
// unknown: this engine rewrites one language per patch, because the single verifier behind it can only
// honestly gate one. That refusal is a real product boundary, reported rather than papered over.
func engineFor(language string) (langEngine, error) {
	key := strings.ToLower(strings.TrimSpace(language))
	if key == "" {
		return langEngine{}, fmt.Errorf("%w: the resolved spec carries no workflow language; it was not "+
			"produced by variantspec.Resolve, which is what reads it off the discovered IR",
			ErrLanguageNotRewritable)
	}
	eng, ok := engines[key]
	if !ok {
		return langEngine{}, fmt.Errorf("%w: %q (rewritable: %s)", ErrLanguageNotRewritable, language,
			strings.Join(RewritableLanguages(), ", "))
	}
	// A half-filled row would skip a safety gate for one language silently. Cheap to check, and the
	// alternative is discovering it from a customer's diff.
	if eng.index == nil || eng.reparse == nil {
		return langEngine{}, fmt.Errorf("transform: the %q engine is incomplete (index=%v reparse=%v); "+
			"a language's engine must supply both how to locate a call site and what 'still parses' means",
			language, eng.index != nil, eng.reparse != nil)
	}
	return eng, nil
}

// RewritableLanguages lists every language with an engine, sorted. For error messages, and for the test
// that asserts the table covers every frontend Discovery ships.
//
// 🚫 "Rewritable" here means "has an engine", NOT "a call site in this language can actually be
// rewritten". Java, Kotlin and Rust are in this list and refuse every override, for reasons that are
// facts about those languages' call sites and about the registry rows — see argumentForms. The
// distinction is the whole point of ADR-003 being honest rather than uniform, and this doc comment is
// load-bearing: a reader who takes this list as a capability claim has been misled by us.
func RewritableLanguages() []string {
	out := make([]string, 0, len(engines))
	for k := range engines {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// indexGoSites binds the go/ast rewriters to every Go call site in the tree.
//
// The Go path below this line is byte-for-byte what it was before ADR-003: same index, same rewriters,
// same refusals. The only change is that the dimension lookup moved from Generate's loop into this
// closure, so Generate no longer knows there is such a thing as a Go rewriter.
func indexGoSites(root string) (map[string]boundSite, error) {
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]boundSite, len(sites))
	for id, s := range sites {
		s := s
		out[id] = boundSite{
			fileRel: s.FileRel, lineStart: s.LineStart, lineEnd: s.LineEnd,
			rewrite: func(dim variantspec.Dimension, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
				rw, ok := rewriters[dim]
				if !ok {
					return nil, unsafeRewrite(s.NodeID, string(dim), "no rewriter for this dimension")
				}
				return rw(s, src, o)
			},
		}
	}
	return out, nil
}

// syntacticEngine builds the engine for one tree-sitter language.
//
// One constructor for all six because they differ in DATA, not in behavior: the spans, and the spelling
// of `=` versus `: ` versus ` = `, are supplied by the analyzer that parsed the file (ArgInsert.Assign
// lives in internal/discovery for exactly this reason). A per-language rewriter here would be six copies
// of one function differing in a table lookup — and six places to fix a splice bug.
func syntacticEngine(language string) langEngine {
	return langEngine{
		index: func(root string) (map[string]boundSite, error) { return indexSpanSites(root, language) },
		reparse: func(rel string, src, out []byte) error {
			return reparseSyntactic(language, rel, src, out)
		},
	}
}

// indexSpanSites binds the span rewriters to every call site one syntactic frontend finds.
func indexSpanSites(root, language string) (map[string]boundSite, error) {
	sites, err := discovery.IndexSpanCallSites(root, language, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]boundSite, len(sites))
	for id, s := range sites {
		s := s
		out[id] = boundSite{
			fileRel: s.FileRel, lineStart: s.LineStart, lineEnd: s.LineEnd,
			rewrite: func(dim variantspec.Dimension, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
				rw, ok := spanRewriters[dim]
				if !ok {
					return nil, unsafeRewrite(s.NodeID, string(dim), "no rewriter for this dimension")
				}
				return rw(s, src, o)
			},
		}
	}
	return out, nil
}

// languageDisplay spells a language label the way a human writes it, for error prose.
//
// A table, so "Go" stays "Go" in the message it has always been in, rather than becoming "go" as a side
// effect of the dispatch landing — a gratuitously changed error string is a broken test at best and a
// broken runbook at worst.
var languageDisplayNames = map[string]string{
	"go": "Go", "python": "Python", "typescript": "TypeScript", "javascript": "JavaScript",
	"kotlin": "Kotlin", "java": "Java", "rust": "Rust",
}

func languageDisplay(language string) string {
	if d, ok := languageDisplayNames[strings.ToLower(strings.TrimSpace(language))]; ok {
		return d
	}
	return language
}
