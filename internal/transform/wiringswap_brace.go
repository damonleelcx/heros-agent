package transform

import (
	"fmt"
	"regexp"
	"strings"
)

// Statement resolution for the BRACE languages — one resolver, five rows
// ──────────────────────────────────────────────────────────────────────
//
// P15 Decision 9 chose Go and Python first and said every other language "refuses BY NAME". That was an
// ORDERING decision (decisions.md D-5's scope note), and the sentence it produced —
// "no wiring rewriter for your language" — described our backlog while reading as a fact about the
// customer's code. Nothing about TypeScript, Kotlin, Java or Rust makes an adjacent transposition
// unsound: tree-sitter gives their statement boundaries as readily as Python's whitespace does.
//
// 🔴 What must NOT change, and is the whole of D-5: the resolver is the ONLY per-language part. The plan
// (planWiringSwap's literal one-adjacent-pair check), the edge-set comparison, the coherence gate, and
// the permutation invariant are produced by the same neutral code for every language. Five languages
// arriving as five resolvers is a table growing rows; five languages arriving as five gates would be one
// safety property becoming five dialects, of which the weakest would be the least reviewed.
//
// # What a statement is here, and what it refuses to be
//
// A brace-language statement runs from its first line to the line where it terminates: a `;` at depth
// zero, or the `}` that closes a block it opened. That is a complete answer for the shapes this engine
// moves and an honest refusal for the rest — a statement whose terminator this scanner cannot find is
// REFUSED, never bounded by a guess, because a move whose extent is a guess is exactly the change nobody
// could review.

// braceSyntax is everything language-specific about finding a statement's boundaries.
//
// 🚫 Note what is NOT here: no gate, no invariant, no independence rule. Those are neutral by
// construction, and a field for one of them would be the first crack in D-5.
type braceSyntax struct {
	// lineComment / blockComment are skipped while scanning for a terminator.
	lineComment, blockComment string
	// quotes are the string delimiters whose interior is skipped.
	quotes string
	// rawStringPrefixes open a multi-line string (a Java text block, a Kotlin raw string, a JS template
	// literal). A statement containing one is refused: its extent depends on lexing this engine does not
	// do, and a move whose boundary is a guess is not reviewable.
	rawStrings []string
	// binders are the keywords that introduce a NEW name: `const x = …`, `val x = …`, `let x = …`.
	binders []string
	// controlHeads are statement heads whose POSITION is part of their meaning. Two of them are never
	// exchangeable however independent their data — the same rule Python's resolver applies to `return`.
	controlHeads []string
	// terminator is the character that ends a simple statement.
	terminator byte
	// terminatorOptional marks a language where a newline may end a statement without punctuation
	// (Kotlin, and JavaScript under ASI). There, a statement that does not terminate on its own line is
	// refused rather than extended by inference.
	terminatorOptional bool
}

// braceSyntaxes is the per-language table. A language absent from it has no statement resolver, which
// transform.AxisCoverage reports as a named gap.
var braceSyntaxes = map[string]braceSyntax{
	"typescript": {
		lineComment: "//", blockComment: "/*", quotes: "'\"`", rawStrings: []string{"`"},
		binders:      []string{"const", "let", "var"},
		controlHeads: []string{"return", "throw", "break", "continue", "yield", "await import"},
		terminator:   ';', terminatorOptional: true,
	},
	"javascript": {
		lineComment: "//", blockComment: "/*", quotes: "'\"`", rawStrings: []string{"`"},
		binders:      []string{"const", "let", "var"},
		controlHeads: []string{"return", "throw", "break", "continue", "yield"},
		terminator:   ';', terminatorOptional: true,
	},
	"java": {
		lineComment: "//", blockComment: "/*", quotes: "'\"", rawStrings: []string{`"""`},
		binders:      []string{"final", "var"},
		controlHeads: []string{"return", "throw", "break", "continue"},
		terminator:   ';',
	},
	"kotlin": {
		lineComment: "//", blockComment: "/*", quotes: `"`, rawStrings: []string{`"""`},
		binders:      []string{"val", "var"},
		controlHeads: []string{"return", "throw", "break", "continue"},
		terminator:   ';', terminatorOptional: true,
	},
	"rust": {
		lineComment: "//", blockComment: "/*", quotes: `"`, rawStrings: []string{`r#"`},
		binders:      []string{"let"},
		controlHeads: []string{"return", "break", "continue"},
		terminator:   ';',
	},
}

// braceResolver builds a statementResolver for one brace language. It is a closure over the syntax row
// rather than five copies of the function, which is the mechanical form of "the resolver is the only
// per-language part".
func braceResolver(language string) statementResolver {
	syn := braceSyntaxes[language]
	return func(src []byte, nodeID string, line int) (stmtBlock, error) {
		return resolveBraceStatement(syn, language, src, nodeID, line)
	}
}

func resolveBraceStatement(syn braceSyntax, language string, src []byte, nodeID string, line int) (stmtBlock, error) {
	lines := splitLines(src)
	if line < 1 || line > len(lines) {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the call for %s is recorded at line %d, which this file does not have", nodeID, line))
	}

	// Walk BACK to the statement's first physical line: a call may sit on a continuation line of a
	// statement that started earlier. A line is a statement START when everything before it, within the
	// enclosing block, has terminated.
	start := line
	for start > 1 && !braceStatementStarts(syn, lines, start) {
		start--
	}
	end, ok := braceStatementEnd(syn, lines, start)
	if !ok {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"this engine could not determine where the statement for %s ends — its brackets, strings or "+
				"terminator do not resolve. Refusing is the fail-closed direction: a move whose boundary is a "+
				"guess is exactly the change nobody could review", nodeID))
	}
	stmt := strings.Join(lines[start-1:end], "\n")
	for _, raw := range syn.rawStrings {
		if strings.Contains(stmt, raw) {
			return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
				"the statement for %s contains a multi-line string (%s), whose extent this engine does not "+
					"lex; moving a statement whose boundary it cannot state exactly is not reviewable", nodeID, raw))
		}
	}

	blk := stmtBlock{
		nodeID: nodeID, startLine: start, endLine: end,
		startByte: pyLineOffset(src, start), endByte: pyLineOffset(src, end+1),
		indent: leadingWhitespace(lines[start-1]),
		binds:  map[string]bool{}, reads: map[string]bool{},
		kind: "statement",
	}
	head := strings.TrimSpace(lines[start-1])
	// 🔴 A construct this resolver can LOCATE but does not MODEL is refused as a fact about the source
	// (P15 20.8) — never as a missing rewriter, which would send the author to wait for work that would
	// refuse them again.
	if k, isControl := braceControlKind(syn, head); isControl {
		blk.control, blk.kind = true, k
	}
	braceBindsAndReads(syn, stmt, blk.binds, blk.reads)
	return blk, nil
}

// braceLineScan is what one line's scan reports.
//
// 🔴 The distinction that makes this correct is between CONTINUATION depth (parentheses and brackets,
// which carry a single statement across lines) and BLOCK depth (braces, whose contents are statements in
// their own right). Counting them together is the bug the first version of this file had: a function's
// opening `{` left every line inside it at depth ≥ 1, so no line ever looked like a statement start and
// the resolver walked back to line 1 every time.
type braceLineScan struct {
	contDepth  int  // parentheses/brackets still open at end of line
	braceDelta int  // net brace change on this line
	ends       bool // this line ends a statement
	// ambiguous marks a line this engine will not bound: a trailing `{` on a line that also assigns is
	// either a block head or a multi-line object literal, and telling them apart needs a parse this
	// resolver does not do. Refusing is the fail-closed direction.
	ambiguous bool
}

// braceStatementStarts reports whether line n begins a statement — i.e. the line above it ended one.
//
// It re-scans from the top of the file rather than carrying state, exactly as the Python resolver does,
// because a scanner that starts mid-file cannot know whether it is inside a string.
func braceStatementStarts(syn braceSyntax, lines []string, n int) bool {
	if n <= 1 {
		return true
	}
	cont := 0
	ended := true
	for i := 0; i < n-1; i++ {
		sc := braceScanLine(syn, lines[i], cont)
		cont = sc.contDepth
		if strings.TrimSpace(lines[i]) == "" {
			continue // a blank line neither ends nor continues a statement
		}
		ended = sc.ends
	}
	return cont <= 0 && ended
}

// braceStatementEnd finds the last line of the statement starting at `start`.
func braceStatementEnd(syn braceSyntax, lines []string, start int) (int, bool) {
	cont, brace := 0, 0
	for i := start - 1; i < len(lines); i++ {
		sc := braceScanLine(syn, lines[i], cont)
		if sc.ambiguous {
			return 0, false
		}
		cont = sc.contDepth
		brace += sc.braceDelta
		if cont < 0 || brace < 0 {
			return 0, false
		}
		if cont == 0 && brace == 0 && sc.ends {
			return i + 1, true
		}
	}
	return 0, false
}

// braceScanLine advances a line and reports whether it ends a statement.
//
// A statement ends when, at continuation depth zero, the line's last non-comment character is the
// language's terminator, the `}` that closed a block this statement opened, or a `{` that opens one.
// Under an optional terminator (Kotlin, JS ASI) a line that ends in neither an operator nor a comma also
// ends the statement — the conservative reading, since a continuation always leaves a visible marker.
func braceScanLine(syn braceSyntax, line string, cont int) braceLineScan {
	out := braceLineScan{contDepth: cont}
	inString := byte(0)
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inString != 0 {
			b.WriteByte(c)
			if c == '\\' {
				if i+1 < len(line) {
					b.WriteByte(line[i+1])
					i++
				}
				continue
			}
			if c == inString {
				inString = 0
			}
			continue
		}
		if syn.lineComment != "" && strings.HasPrefix(line[i:], syn.lineComment) {
			break
		}
		if syn.blockComment != "" && strings.HasPrefix(line[i:], syn.blockComment) {
			if j := strings.Index(line[i+2:], "*/"); j >= 0 {
				i += 2 + j + 1
				continue
			}
			break
		}
		if strings.IndexByte(syn.quotes, c) >= 0 {
			inString = c
			b.WriteByte(c)
			continue
		}
		b.WriteByte(c)
		switch c {
		case '(', '[':
			out.contDepth++
		case ')', ']':
			out.contDepth--
		case '{':
			out.braceDelta++
		case '}':
			out.braceDelta--
		}
	}
	trimmed := strings.TrimRight(b.String(), " \t")
	if trimmed == "" || out.contDepth > 0 {
		return out
	}
	last := trimmed[len(trimmed)-1]
	switch {
	case last == syn.terminator:
		out.ends = true
	case last == '}':
		out.ends = true
	case last == '{':
		// A block head ends a statement; a multi-line object literal does not. The two are
		// indistinguishable to this scanner when the line also assigns, so that case refuses.
		if braceTopLevelAssign(syn, trimmed) >= 0 {
			out.ambiguous = true
		} else {
			out.ends = true
		}
	case syn.terminatorOptional:
		if !strings.ContainsRune("+-*/%&|^<>=,.?:(", rune(last)) {
			out.ends = true
		}
	}
	return out
}

// braceControlKind reports whether a statement head is one whose POSITION carries meaning.
func braceControlKind(syn braceSyntax, head string) (string, bool) {
	for _, k := range syn.controlHeads {
		if head == k || strings.HasPrefix(head, k+" ") || strings.HasPrefix(head, k+";") {
			return k + " statement", true
		}
	}
	// A block-opening head (`if`, `for`, `while`, `try`, `match`) is a compound statement: its body's
	// position is part of its meaning, so it is never exchanged.
	if braceCompoundHead.MatchString(head) {
		return "compound statement", true
	}
	return "", false
}

var braceCompoundHead = regexp.MustCompile(`^(if|for|while|do|try|catch|finally|switch|match|when|synchronized|unsafe)\b`)

// braceIdent matches an identifier the dependency check can reason about.
var braceIdent = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)

// braceBindsAndReads fills the statement's bound and read names.
//
// Deliberately conservative in the SAFE direction: a name it is unsure about is recorded as READ, which
// can only make two statements look dependent — refusing a swap that might have been legal. The
// opposite mistake (missing a read) would admit a swap that changes behaviour, which is the failure the
// independence check exists to prevent.
func braceBindsAndReads(syn braceSyntax, stmt string, binds, reads map[string]bool) {
	head := strings.TrimSpace(strings.SplitN(stmt, "\n", 2)[0])
	// `const x = …` / `let mut x = …` / `val x = …`
	for _, kw := range syn.binders {
		if !strings.HasPrefix(head, kw+" ") {
			continue
		}
		rest := strings.TrimSpace(head[len(kw):])
		rest = strings.TrimPrefix(rest, "mut ") // Rust
		if eq := braceTopLevelAssign(syn, rest); eq >= 0 {
			rest = rest[:eq]
		}
		if name := lastIdentOf(rest); name != "" {
			binds[name] = true
		}
		break
	}
	// A bare `x = …` binds x and, for an augmented form, also reads it.
	if eq := braceTopLevelAssign(syn, head); eq >= 0 {
		target := strings.TrimSpace(head[:eq])
		augmented := len(target) > 0 && strings.ContainsAny(target[len(target)-1:], "+-*/%&|^")
		if augmented {
			target = strings.TrimSpace(target[:len(target)-1])
		}
		// 🔴 The bound name is the LAST identifier of the target, not the first. `const a = …` and
		// `String a = …` both put a keyword or a TYPE in front of the name, and taking the first
		// identifier would record every statement as binding "const" or "String" — which makes any two
		// declarations look like they bind the same name, and refuses every swap that was legal.
		if name := lastIdentOf(target); name != "" {
			binds[name] = true
			if augmented || strings.ContainsAny(target, ".[") {
				reads[name] = true
			}
		}
	}
	for _, id := range braceIdent.FindAllString(stmt, -1) {
		if !binds[id] {
			reads[id] = true
		}
	}
}

// lastIdentOf returns the final identifier of an assignment target's base — the NAME, after any
// declaration keyword or type. `String a` -> "a"; `const a` -> "a"; `a.b` -> "a" (the object is what a
// later statement depends on, not the field).
func lastIdentOf(target string) string {
	base := target
	if i := strings.IndexAny(base, ".["); i >= 0 {
		base = base[:i]
	}
	ids := braceIdent.FindAllString(base, -1)
	if len(ids) == 0 {
		return ""
	}
	if base != target {
		// A qualified target: the OBJECT is the dependency, so the first identifier is the name.
		return ids[0]
	}
	return ids[len(ids)-1]
}

// braceTopLevelAssign returns the index of a statement's assignment `=`, or -1. Bracket depth
// distinguishes it from a named argument or an object key; the neighbour characters from `==`/`<=`/`=>`.
func braceTopLevelAssign(syn braceSyntax, line string) int {
	depth := 0
	inString := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inString != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inString {
				inString = 0
			}
			continue
		}
		if strings.IndexByte(syn.quotes, c) >= 0 {
			inString = c
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '=':
			if depth != 0 {
				continue
			}
			if i+1 < len(line) && (line[i+1] == '=' || line[i+1] == '>') {
				i++
				continue
			}
			if i > 0 && strings.ContainsAny(line[i-1:i], "=!<>+-*/%&|^") {
				continue
			}
			return i
		}
	}
	return -1
}
