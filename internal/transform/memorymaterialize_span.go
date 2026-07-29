package transform

import (
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Memory MATERIALIZATION for the syntactic engines — and the gate that makes it honest
// ───────────────────────────────────────────────────────────────────────────────────
//
// P17 refused every memory change and named what was missing. The runtime is one half
// (internal/memoryruntime); this file is the other: the call-site rewriter that reads and writes it.
//
// # Two edits, and why neither may ship alone
//
// A memory strategy is a READ and a WRITE, and they materialize as different edits:
//
//	recall   messages=[…]        →  messages=agentmem.recall("<node>", […])     an EXPRESSION replacement
//	record   resp = client.…(…)  →  + agentmem.record("<node>", […], resp)      a STATEMENT insertion
//
// The first is squarely inside rewrite.go's rule — replace an expression the call site already wrote —
// and it is the same shape P16 established for context. The second is a new edit class, and it is the
// one that limits coverage: it needs the call to be a simple assignment at statement level, because
// anything else means guessing where the response lands.
//
// 🔴 BOTH HALVES OR REFUSE (decisions.md D2). A call site that can carry the recall but not the record is
// refused WHOLE. This is the decision the phase turns on and the one most exposed to shipping pressure,
// because "we can at least do the read" is always available and always sounds like progress. It is not:
//
//	recall only  → recalls from a store nothing ever fills. Every call recalls nothing. The node behaves
//	               exactly like `none` while its config_hash claims `summary-buffer`.
//	record only  → fills a store nothing ever reads, and grows unboundedly in the customer's process.
//
// Both are P17's *"scored a configuration that never ran"*, one layer down and HARDER to see — because
// unlike a silent drop, something genuinely was emitted: the diff is non-empty, the build passes, and a
// reviewer reads real memory code.
//
// # The refusal ladder, most-specific first (P16's ordering, same reason)
//
//	is the strategy `none`?             → no edit, no refusal (identity)
//	does this language materialize?     → ours, but asked EARLY here — see below
//	does the call write a message list? → if not: the CALL is the reason (**kwargs) — permanent, actionable
//	can the record half land?           → if not: name WHICH half — actionable
//
// 🔴 The language question sits SECOND here rather than last, and that is a deliberate departure from
// P16's ordering with a reason: in P16 every language could in principle select among written turns, so
// "your language is pending" was a promise. Here a language without an emitted artifact has nothing to
// call — `agentmem.recall` does not exist for it — so a call-shape refusal would be true but useless,
// pointing the reader at their own code when nothing they wrote could help. The most specific TRUE cause
// for an uncovered language is the missing artifact.

// memoryMaterializers is the per-language coverage table for memory. A language absent from it has no
// emitted artifact, so it cannot materialize — a fact this table states rather than a gap it hides.
//
// It is THE source coverage.go reads (memoryCoverage), so the table and the rewriter cannot disagree
// about which cells work.
var memoryMaterializers = map[string]bool{
	"python": true,
}

// MemoryMaterializerLanguages lists the languages with a memory materializer, sorted. Read by the
// coverage table and by the console, so there is one answer.
func MemoryMaterializerLanguages() []string {
	out := make([]string, 0, len(memoryMaterializers))
	for lang := range memoryMaterializers {
		out = append(out, lang)
	}
	sortStrings(out)
	return out
}

// HasMemoryMaterializer reports whether a language can materialize a memory strategy.
func HasMemoryMaterializer(lang string) bool { return memoryMaterializers[strings.ToLower(lang)] }

// spanMaterializeMemory is the dispatch entry for the memory dimension in every syntactic engine.
func spanMaterializeMemory(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimMemory)

	// 1. The identity strategy changes nothing, so there is nothing to materialize and nothing to refuse.
	if o.Memory == nil || o.Memory.IsNone() {
		return nil, nil
	}
	strategy := o.Memory.Spec.Strategy

	// 2. The language. Asked early, for the reason in the file comment: without an emitted artifact there
	//    is no `agentmem` module to call, so no fact about the user's call site could change the outcome.
	if !HasMemoryMaterializer(site.Language) {
		return nil, refuseNoMaterializer(site.NodeID, dim,
			"materializing memory strategy %q means emitting a memory module and rewriting this call to read "+
				"and write it, and no %s module has been generated yet (covered today: %s). Until then there "+
				"is nothing for a rewritten call site to call, so this override is REFUSED rather than "+
				"dropped — applying it as the base configuration would score a configuration that never ran",
			strategy, languageDisplay(site.Language), strings.Join(MemoryMaterializerLanguages(), ", "))
	}

	// 3. The RECALL half: the call must write a message list this engine can wrap.
	recallSpan, err := memoryRecallTarget(site, src, dim, strategy)
	if err != nil {
		return nil, err
	}

	// 4. The RECORD half: the call must be a simple statement-level assignment.
	//
	// 🔴 Computed BEFORE any edit is emitted. That ordering is the whole of D2 in code: if the record
	// cannot land, the function returns a refusal and NO edit, rather than returning the recall edit it
	// has already built. A version that emitted as it went would ship recall-only diffs the first time a
	// call site was shaped unusually.
	rec, err := memoryRecordTarget(site, src, dim, strategy)
	if err != nil {
		return nil, err
	}

	// Both halves are available. Emit them together.
	//
	// 🔴 The record is appended to the assignment's LAST LINE with a `;`, not inserted as a new line, and
	// that is a deliberate submission to an existing gate rather than a style choice. engine.go refuses
	// any edit containing a newline, because two things depend on the line count holding: a compiler error
	// stays attributable to the node that caused it, and "only the targeted lines changed" stays
	// checkable at all. A statement insertion breaks both.
	//
	// The alternatives were weighed and rejected. Weakening that gate for this rewriter would be exactly
	// the trade P15 refused — "a NEW EDIT CLASS with its own admission rule, never a loosened old one" —
	// and a new class here would have to relax line-count stability for every rewriter, forever, to serve
	// one. Wrapping the whole call expression instead would need a call span the analyzers do not expose,
	// and deriving one by scanning for balanced parens is precisely the guess rewrite_span.go declines.
	//
	// 🚫 The cost is stated rather than hidden: `x = f(); g()` trips a style linter (flake8 E702). That is
	// a visible, fixable annoyance in the customer's tree, not a correctness problem — and it is the right
	// side of the trade against loosening a safety gate. A call span from the analyzers is the clean fix
	// and is named as the follow-up.
	node := pyQuote(site.NodeID)
	written := string(src[recallSpan.Start:recallSpan.End])
	recallText := fmt.Sprintf("agentmem.recall(%s, %s)", node, written)
	recordText := fmt.Sprintf("; agentmem.record(%s, %s, %s)", node, written, rec.target)

	return []edit{
		{Start: recallSpan.Start, End: recallSpan.End, New: recallText, NodeID: site.NodeID, Dim: dim},
		{Start: rec.insertAt, End: rec.insertAt, New: recordText, NodeID: site.NodeID, Dim: dim},
	}, nil
}

// memoryRecallTarget locates the written message list, or refuses with the most specific true reason.
func memoryRecallTarget(site discovery.SpanCallSite, src []byte, dim, strategy string) (discovery.ArgSpan, error) {
	// A call that unpacks its arguments has written no message list for any rewriter to wrap. This is a
	// fact about the CALL, it is actionable, and it stays true after every materializer lands — which is
	// why it is reported as itself rather than as a platform gap.
	if u := site.KeywordUnpacking; u != nil {
		if _, written := site.Keywords["messages"]; !written {
			return discovery.ArgSpan{}, refuseShape(site.NodeID, dim,
				"this call site passes %s, so the request — including its message list — is assembled "+
					"elsewhere in your program; there is no written list here for memory strategy %q to read "+
					"from or append to. Materializing memory here would mean guessing what that mapping "+
					"contains. Write the messages at this call site, or apply the memory strategy where the "+
					"mapping is built",
				u.Text, strategy)
		}
	}

	arg, ok := site.Keywords["messages"]
	if !ok {
		return discovery.ArgSpan{}, refuseShape(site.NodeID, dim,
			"this call site names no `messages` argument, so memory strategy %q has nothing to read into or "+
				"record from. Memory augments a written message list; a call that does not write one cannot "+
				"carry it",
			strategy)
	}
	// 🔴 The value must be a WRITTEN LIST. This is the same narrowness P16 draws around a selection
	// policy, and for a stricter reason: memory PREPENDS recalled turns to whatever it is given, so
	// wrapping a value that is not a list produces code that type-errors at run time — a diff that parses
	// and then breaks the customer's agent. Checking the first byte is a syntactic floor's honest check;
	// anything more would be inferring a type this engine cannot see.
	if val := strings.TrimSpace(string(src[arg.Value.Start:arg.Value.End])); !strings.HasPrefix(val, "[") {
		return discovery.ArgSpan{}, refuseShape(site.NodeID, dim,
			"this call site's `messages` argument is `%s` rather than a written list, so memory strategy %q "+
				"has no list to prepend recalled turns to. Wrapping a non-list would emit code that parses "+
				"and then fails at run time. Write the messages as a list literal here, or apply the strategy "+
				"where the list is built",
			truncateForMessage(val, 60), strategy)
	}
	return arg.Value, nil
}

// memoryImportName is the module the materialized call site calls. One constant, read by the import
// edit and by the emitted call text, so the two cannot name different modules.
const memoryImportName = "agentmem"

// memoryImportEdit builds the single import line a materialized Python file needs, or refuses.
//
// # Where the line goes, and why it is not offset 0
//
// 🔴 Inserting at the top of the file is wrong in two ways that a passing test would not show:
//
//   - before a module DOCSTRING, the docstring stops being one. `"""..."""` is only `__doc__` when it
//     is the first statement; put an import above it and it silently becomes a bare string expression,
//     and the module loses its documentation with no error anywhere.
//   - before `from __future__ import ...`, the file stops compiling — a future import must be the first
//     statement, and Python raises SyntaxError.
//
// So the anchor is the first TOP-LEVEL import that is not a `__future__` import, and the line goes
// immediately before it. That position is past any docstring (a docstring precedes imports) and past
// every future import (they are skipped), in every file, without parsing.
//
// A file with no such import is REFUSED rather than guessed at. It is also a file that is not calling an
// SDK, so the refusal costs nothing real.
func memoryImportEdit(src []byte, nodeID, dim string) (edit, error) {
	lines := strings.Split(string(src), "\n")
	offset := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isImport := strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ")
		topLevel := len(line) == len(strings.TrimLeft(line, " \t"))
		future := strings.HasPrefix(trimmed, "from __future__")
		if isImport && topLevel && !future {
			return edit{
				Start: offset, End: offset,
				New:    "import " + memoryImportName + "\n",
				NodeID: nodeID, Dim: dim, Import: true,
			}, nil
		}
		offset += len(line) + 1
	}
	return edit{}, refuseShape(nodeID, dim,
		"this file declares no top-level import, so there is no position where `import %s` is certainly "+
			"legal: above a module docstring it would silently stop being one, and above a `__future__` "+
			"import it would not compile. A file with no imports is also not calling an SDK, so there is "+
			"nothing here for memory to attach to",
		memoryImportName)
}

// hasMemoryEdit / firstMemoryEdit identify a file that needs the import.
func hasMemoryEdit(edits []edit) bool {
	for _, e := range edits {
		if e.Dim == string(variantspec.DimMemory) {
			return true
		}
	}
	return false
}

// memoryWasMaterialized reports whether any file actually received memory edits, so the artifact is
// emitted only alongside a real call-site rewrite.
func memoryWasMaterialized(editsByFile map[string][]edit) bool {
	for _, edits := range editsByFile {
		if hasMemoryEdit(edits) {
			return true
		}
	}
	return false
}

func firstMemoryEdit(edits []edit) edit {
	for _, e := range edits {
		if e.Dim == string(variantspec.DimMemory) {
			return e
		}
	}
	return edit{}
}

// truncateForMessage shortens a source fragment for a refusal sentence without hiding what it was.
func truncateForMessage(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// recordTarget is where and how the record statement lands.
type recordTarget struct {
	// insertAt is the byte offset the statement is inserted at — the end of the assignment's line.
	insertAt int
	// indent is the assignment's own indentation, so the inserted statement sits in the same block.
	indent string
	// target is the variable the response was assigned to.
	target string
}

// memoryRecordTarget finds the simple statement-level assignment this call's result lands in, or refuses.
//
// 🔴 The shape is checked, never assumed. A call inside a comprehension, a return, an argument to another
// call, or a chained expression has no variable holding the response at statement level — so there is
// nowhere to record from without inventing one. Inventing one is a construction, and a construction that
// compiles is the failure mode with no downstream net.
func memoryRecordTarget(site discovery.SpanCallSite, src []byte, dim, strategy string) (recordTarget, error) {
	lines := strings.Split(string(src), "\n")
	if site.LineStart < 1 || site.LineStart > len(lines) {
		return recordTarget{}, refuseShape(site.NodeID, dim,
			"this call site's location is outside the file this engine read, so the record half of memory "+
				"strategy %q cannot be placed", strategy)
	}
	stmt := lines[site.LineStart-1]

	name, ok := pySimpleAssignTarget(stmt)
	if !ok {
		return recordTarget{}, refuseShape(site.NodeID, dim,
			"memory strategy %q needs to record what this call returns, and that means a statement after it "+
				"referring to the response — but this call's result is not assigned to a name at statement "+
				"level (`%s`), so there is nothing to record from. The read half alone is NOT emitted: a "+
				"memory that recalls from a store nothing ever fills behaves exactly like `none` while its "+
				"config_hash claims %q. Assign the call's result to a variable and this materializes",
			strategy, strings.TrimSpace(stmt), strategy)
	}

	// The insertion point is the end of the call's LAST line, so a multi-line call is followed rather
	// than split. LineEnd is inclusive and 1-based.
	end := site.LineEnd
	if end < site.LineStart {
		end = site.LineStart
	}
	if end > len(lines) {
		return recordTarget{}, refuseShape(site.NodeID, dim,
			"this call site spans past the end of the file this engine read; the record half of memory "+
				"strategy %q cannot be placed", strategy)
	}
	offset := 0
	for i := 0; i < end; i++ {
		offset += len(lines[i]) + 1 // +1 for the newline
	}
	offset-- // land at the END of the last line, just before its newline

	return recordTarget{insertAt: offset, indent: leadingIndent(stmt), target: name}, nil
}

// pySimpleAssignTarget returns the variable a simple `name = …` statement assigns to.
//
// Deliberately narrow. It accepts a bare identifier or a dotted/indexed target only when the whole
// left-hand side is one expression and the operator is a plain `=`. It rejects tuple unpacking,
// augmented assignment, walrus, comparison, and anything with no assignment at all — each of which would
// put the response somewhere the record statement cannot name.
func pySimpleAssignTarget(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	eq := -1
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '"', '\'':
			// Skip a string literal wholesale; an `=` inside one is not an assignment.
			q := s[i]
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case '=':
			if depth != 0 {
				continue // a keyword argument, not the statement's assignment
			}
			// Reject ==, !=, <=, >=, and augmented forms (+=, //=, etc.).
			if i+1 < len(s) && s[i+1] == '=' {
				return "", false
			}
			if i > 0 && strings.ContainsRune("=!<>+-*/%&|^:@", rune(s[i-1])) {
				return "", false
			}
			eq = i
		}
		if eq >= 0 {
			break
		}
	}
	if eq <= 0 {
		return "", false
	}
	target := strings.TrimSpace(s[:eq])
	if target == "" || strings.ContainsAny(target, ",()[]{} \t") {
		// Tuple unpacking, a subscripted target with a complex key, a call — none of which this engine
		// will name in a generated statement.
		return "", false
	}
	for _, r := range target {
		if r != '_' && r != '.' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return "", false
		}
	}
	return target, true
}

func leadingIndent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// pyQuote renders a Python string literal. The node id is a hex-ish identifier by construction, but it
// is quoted through a real escaper rather than concatenated — a generated literal is still a literal.
func pyQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
