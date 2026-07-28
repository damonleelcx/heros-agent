package transform

import (
	"fmt"
	"regexp"
	"strings"
)

// wiringswap_python.go resolves a Python call site to its enclosing statement (P15 15c §14).
//
// # Why this one is line-based, and why that is not a shortcut
//
// Go gets `go/ast`: a real tree with real binding structure. Python here does not — the frontend is
// syntactic (tree-sitter, error-tolerant, no type resolution), which is the honest floor ADR-003 sets
// for it. So this resolver works on what Python's own syntax makes unambiguous: a statement is a
// whole-line unit, its block membership is its INDENTATION, and a logical line ends where its brackets
// balance. Those are facts about the language, not approximations of a parse.
//
// Everything that is NOT unambiguous at that floor is refused:
//
//	a compound statement (`if`, `for`, `with`, `def`, a decorator, anything ending in `:`)
//	a control-flow statement (`return`, `raise`, `yield`, `break`, `continue`, `assert`, `import`, …)
//	a line continuation (`\`) — the statement's extent would depend on trailing whitespace
//	a statement whose bindings this analysis cannot name (tuple targets, augmented assignment to a
//	  subscript, a starred target)
//
// 🔴 That last class is the one that matters. Refusing "I cannot tell what this binds" is the whole
// discipline: the alternative is assuming independence, and a wrong assumption here produces a program
// that runs and computes something else. A missed swap costs an opportunity; a wrong swap costs
// correctness, and the two are not symmetric.

const (
	tripleDouble = "\"\"\""
	tripleSingle = "'''"
)

var (
	pyIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	// pyAssign finds the top-level `=` of a simple assignment: not `==`, `!=`, `<=`, `>=`, and not the
	// `=` of a keyword argument (which is always inside brackets, so bracket depth answers it).
	pyCompoundHead = regexp.MustCompile(`^(async\s+)?(def|class|if|elif|else|for|while|with|try|except|finally|match|case)\b`)
	pyControlHead  = regexp.MustCompile(`^(return|raise|yield|break|continue|pass|global|nonlocal|import|from|assert|del|lambda)\b`)
)

// pyKeywords are names that are syntax, not data. Counting them as reads would make two statements
// that both contain `not` look dependent.
var pyKeywords = map[string]bool{
	"and": true, "as": true, "assert": true, "async": true, "await": true, "break": true,
	"class": true, "continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "False": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true, "None": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true, "return": true,
	"True": true, "try": true, "while": true, "with": true, "yield": true,
}

// resolvePythonStatement projects the logical statement containing `line` into the swap's shape.
func resolvePythonStatement(src []byte, nodeID string, line int) (stmtBlock, error) {
	lines := splitLines(src)
	if line < 1 || line > len(lines) {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the call for %s is recorded at line %d, which this file does not have", nodeID, line))
	}

	// Walk BACK to the statement's first physical line: a call may sit on a continuation line of a
	// statement that started earlier.
	start := line
	for start > 1 && !pyStatementStarts(lines, start) {
		start--
	}
	// Walk FORWARD until the brackets balance — that is where the logical line ends.
	end, ok := pyLogicalEnd(lines, start)
	if !ok {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"this engine could not determine where the statement for %s ends — its brackets or string "+
				"literals do not resolve from the start of the file. Refusing is the fail-closed direction: a "+
				"move whose boundary is a guess is exactly the change nobody could review", nodeID))
	}
	if strings.HasSuffix(strings.TrimRight(lines[end-1], " \t"), "\\") {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the statement for %s ends in a backslash continuation, whose extent depends on trailing "+
				"whitespace; this engine will not move a statement whose boundary it cannot state exactly", nodeID))
	}

	head := strings.TrimSpace(lines[start-1])
	blk := stmtBlock{
		nodeID: nodeID, startLine: start, endLine: end,
		startByte: pyLineOffset(src, start), endByte: pyLineOffset(src, end+1),
		indent: leadingWhitespace(lines[start-1]),
		binds:  map[string]bool{}, reads: map[string]bool{},
		kind: "statement",
	}
	switch {
	case pyCompoundHead.MatchString(head) || strings.HasPrefix(head, "@"):
		blk.control, blk.kind = true, "compound statement"
	case pyControlHead.MatchString(head):
		blk.control, blk.kind = true, strings.Fields(head)[0]+" statement"
	}
	if err := pyBindsAndReads(nodeID, lines[start-1:end], blk.binds, blk.reads); err != nil {
		return stmtBlock{}, err
	}
	return blk, nil
}

// pyScan is the bracket/string state a line-based Python reader must carry ACROSS lines.
//
// It exists because of triple-quoted strings. A docstring containing an unmatched `(` — which is
// ordinary English prose, e.g. "the client (see below" — shifts a naive bracket counter for the rest of
// the file, and every statement after it then looks unterminated. The first version of this resolver
// did exactly that on hermes-agent and reported four pairs as "does not close its brackets before the
// end of the file", which was not true of the code and was true only of the counter.
//
// 🔴 The failure was FAIL-CLOSED (the pairs were refused, not mis-swapped), which is the design working
// — but a refusal with a wrong reason sends a user to look for a defect in their own file, so the
// reason has to be right too.
type pyScan struct {
	depth  int
	triple string // "" outside a triple-quoted string, otherwise the delimiter that opened it
}

// pyScanLine advances the state across one physical line.
func pyScanLine(st pyScan, line string) pyScan {
	var quote byte
	for i := 0; i < len(line); i++ {
		// Inside a triple-quoted string nothing counts until its closing delimiter.
		if st.triple != "" {
			if strings.HasPrefix(line[i:], st.triple) {
				i += len(st.triple) - 1
				st.triple = ""
			}
			continue
		}
		c := line[i]
		if quote != 0 { // a single-quoted string
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if strings.HasPrefix(line[i:], tripleDouble) || strings.HasPrefix(line[i:], tripleSingle) {
			st.triple = line[i : i+3]
			i += 2
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			return st // the rest of the line is a comment
		case '(', '[', '{':
			st.depth++
		case ')', ']', '}':
			st.depth--
		}
	}
	return st
}

// pyStateAt returns the scan state at the START of a 1-based line.
func pyStateAt(lines []string, line int) pyScan {
	var st pyScan
	for i := 0; i < line-1 && i < len(lines); i++ {
		st = pyScanLine(st, lines[i])
	}
	return st
}

// pyStatementStarts reports whether a physical line begins a logical statement: it is non-blank, and
// no bracket or triple-quoted string is open when it begins.
func pyStatementStarts(lines []string, line int) bool {
	if strings.TrimSpace(lines[line-1]) == "" {
		return false
	}
	st := pyStateAt(lines, line)
	return st.depth <= 0 && st.triple == ""
}

// pyLogicalEnd returns the last physical line of the logical statement starting at `start`.
func pyLogicalEnd(lines []string, start int) (int, bool) {
	st := pyStateAt(lines, start)
	if st.triple != "" {
		return 0, false // the "statement" begins inside a string literal
	}
	st.depth = 0
	for i := start; i <= len(lines); i++ {
		st = pyScanLine(st, lines[i-1])
		if st.depth <= 0 && st.triple == "" {
			return i, true
		}
	}
	return 0, false
}

// pyBindsAndReads fills the name sets for a simple statement, or refuses when it cannot.
//
// The only binding form it claims to understand is a single-target assignment to a plain name or to a
// subscript/attribute of one (`x = …`, `x.y = …`, `x[k] = …`, and their augmented forms). Anything else
// — tuple unpacking, starred targets, chained assignment, a walrus inside an expression — is REFUSED,
// because naming the bindings wrongly is exactly the mistake that produces a program that runs and is
// wrong.
func pyBindsAndReads(nodeID string, stmtLines []string, binds, reads map[string]bool) error {
	joined := strings.Join(stmtLines, "\n")
	if strings.Contains(joined, ":=") {
		return refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the statement for %s contains a walrus assignment, which binds a name inside an expression; "+
				"this engine does not model that and will not guess at it", nodeID))
	}

	head := stmtLines[0]
	if eq := pyTopLevelAssign(head); eq >= 0 {
		target := strings.TrimSpace(head[:eq])
		// Strip an augmented operator (`+=`, `|=`, …): the target still binds, and it also READS.
		augmented := false
		if len(target) > 0 && strings.ContainsAny(target[len(target)-1:], "+-*/%&|^@<>") {
			target = strings.TrimSpace(target[:len(target)-1])
			augmented = true
		}
		if strings.Contains(target, ",") || strings.HasPrefix(target, "*") || strings.Contains(target, "=") {
			return refuseWiringMaterialize(nodeID, fmt.Sprintf(
				"the statement for %s assigns to %q — a tuple, starred, or chained target this engine does "+
					"not model; refusing rather than guessing which names it binds", nodeID, target))
		}
		base := pyIdent.FindString(target)
		if base == "" {
			return refuseWiringMaterialize(nodeID, fmt.Sprintf(
				"the statement for %s assigns to %q, which does not begin with a name this engine can "+
					"identify", nodeID, target))
		}
		binds[base] = true
		if augmented || target != base {
			// `x.y = …` and `x += …` both READ x as well as bind it; a later statement that reads x is
			// dependent on this one.
			reads[base] = true
		}
		// Everything in the target beyond the base (a subscript expression) is read.
		pyCollectReads(target[len(base):], reads)
		pyCollectReads(strings.Join(append([]string{head[eq+1:]}, stmtLines[1:]...), "\n"), reads)
		return nil
	}
	pyCollectReads(joined, reads)
	return nil
}

// pyTopLevelAssign returns the index of the statement's assignment `=`, or -1. Bracket depth is what
// distinguishes it from a keyword argument, and the neighbour characters from a comparison.
func pyTopLevelAssign(line string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			return -1
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '=':
			if depth != 0 {
				continue // a keyword argument
			}
			if i+1 < len(line) && line[i+1] == '=' {
				return -1 // a comparison: this is not an assignment statement
			}
			if i > 0 && strings.ContainsAny(line[i-1:i], "=!<>") {
				return -1
			}
			return i
		}
	}
	return -1
}

// pyCollectReads records every identifier that is not a keyword and not an attribute name (the `b` of
// `a.b`), mirroring the Go resolver's selector rule and for the same reason.
func pyCollectReads(text string, reads map[string]bool) {
	// Strip comments line by line: a name mentioned in a comment is not a read.
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	stripped := b.String()
	for _, loc := range pyIdent.FindAllStringIndex(stripped, -1) {
		name := stripped[loc[0]:loc[1]]
		if pyKeywords[name] {
			continue
		}
		if loc[0] > 0 && stripped[loc[0]-1] == '.' {
			continue // an attribute, not a name in this scope
		}
		reads[name] = true
	}
}

func leadingWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// pyLineOffset returns the byte offset of the first character of a 1-based line; a line one past the
// end returns len(src), which is what the swap's end-exclusive range needs.
func pyLineOffset(src []byte, line int) int {
	if line <= 1 {
		return 0
	}
	n := 1
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			n++
			if n == line {
				return i + 1
			}
		}
	}
	return len(src)
}
