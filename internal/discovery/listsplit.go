package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// Splitting a WRITTEN list into the elements the author wrote — one implementation, six languages
// ───────────────────────────────────────────────────────────────────────────────────────────────
//
// Two different consumers need exactly this and nothing more:
//
//	P14 tool pruning  — which written element is which tool, so a prune deletes THAT one and not its
//	                    neighbour. 🔴 The frontend must RECORD it; the pruner may never infer it.
//	P16 context       — which written turns a selection policy retains, so the rest can be deleted.
//
// Both are span arithmetic over the same shape, so there is one implementation and the per-language
// part is DATA (listSyntaxes below). That is the whole reason this lives in `discovery` rather than in
// `transform` beside the rewriter that first needed it: per-language syntax already lives here (see
// ArgInsert.Assign, filled in by each analyzer for the same reason), and `transform` imports `discovery`
// — so putting it here lets one implementation serve both, while putting it there would have forced a
// second copy on the discovery side. 禁止分裂 source-of-truth, applied to the direction of the import.
//
// # What it refuses, and why refusing is the feature
//
// This splitter answers "what did the author write" and never "what will be there at run time". So a
// spread, a comment, a multi-line string and unbalanced brackets are all REFUSALS with their own
// sentence — not best-effort splits. A list that is half-written is the shape that deletes the wrong
// element, and a wrong deletion parses.

// ListElement is one written element of a list, as offsets RELATIVE to the text handed in.
type ListElement struct {
	Start, End int
	Text       string
}

// listForm is one spelling of "a list literal" — an optional call prefix plus a bracket pair.
//
// Python and JavaScript write `[a, b]`; Kotlin writes `listOf(a, b)`; Rust writes `vec![a, b]`. They are
// the same structure with different punctuation, which is exactly the kind of difference that belongs in
// a table rather than in five near-identical functions.
type listForm struct {
	prefix      string // "" for a bare bracket literal; "listOf" / "vec!" / "List.of" otherwise
	open, close byte
}

// listSyntax is everything language-specific about splitting a written list.
type listSyntax struct {
	forms []listForm
	// quotes are the string delimiters whose interior must be skipped, so a comma inside a string is
	// not a separator.
	quotes string
	// tripleQuotes are multi-line string openers. A list containing one is refused: a multi-line element
	// cannot be deleted without changing the file's line count, and gateMinimal forbids that.
	tripleQuotes []string
	// lineComment / blockComment, if present in the list, refuse — a comment binds to a neighbouring
	// element, and deleting the element without it leaves a comment describing something that is gone.
	lineComment  string
	blockComment string
	// spreads are element prefixes that mean "and everything in that other collection". A list carrying
	// one has no known length until run time, so identifying which elements a change touches is
	// impossible — refused by name (P16 task 10.8).
	spreads []string
}

// listSyntaxes is the per-language table. 🔴 A language ABSENT from it records no written list, which is
// a coverage answer (transform.AxisCoverage names the missing splitter), never a silent empty result.
//
// Go is deliberately absent: the Go frontend has go/ast and splits a composite literal from real
// element positions (extract.go classifyToolsSkills), which is strictly stronger than text scanning.
// Adding a row here for Go would be a second, weaker answer to a question that already has one.
var listSyntaxes = map[string]listSyntax{
	"python": {
		forms:        []listForm{{"", '[', ']'}},
		quotes:       "'\"",
		tripleQuotes: []string{`"""`, `'''`},
		lineComment:  "#",
		spreads:      []string{"*"},
	},
	"typescript": {
		forms:        []listForm{{"", '[', ']'}},
		quotes:       "'\"`",
		lineComment:  "//",
		blockComment: "/*",
		spreads:      []string{"..."},
	},
	"javascript": {
		forms:        []listForm{{"", '[', ']'}},
		quotes:       "'\"`",
		lineComment:  "//",
		blockComment: "/*",
		spreads:      []string{"..."},
	},
	"kotlin": {
		// Kotlin's collection literals are function calls; `arrayOf` is included because an SDK taking a
		// vararg is spelled that way at the call site.
		forms:        []listForm{{"listOf", '(', ')'}, {"mutableListOf", '(', ')'}, {"listOfNotNull", '(', ')'}, {"arrayOf", '(', ')'}, {"", '[', ']'}},
		quotes:       `"`,
		tripleQuotes: []string{`"""`},
		lineComment:  "//",
		blockComment: "/*",
		spreads:      []string{"*"}, // Kotlin's spread operator on a vararg
	},
	"java": {
		forms:        []listForm{{"List.of", '(', ')'}, {"Arrays.asList", '(', ')'}, {"java.util.List.of", '(', ')'}},
		quotes:       `"`,
		tripleQuotes: []string{`"""`}, // Java text block
		lineComment:  "//",
		blockComment: "/*",
	},
	"rust": {
		forms:        []listForm{{"vec!", '[', ']'}, {"vec!", '(', ')'}, {"", '[', ']'}},
		quotes:       `"`,
		lineComment:  "//",
		blockComment: "/*",
	},
}

// ListSplitLanguages lists the languages that can split a written list, sorted. Exported so a coverage
// read and a refusal name the same set.
func ListSplitLanguages() []string {
	out := make([]string, 0, len(listSyntaxes))
	for l := range listSyntaxes {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// CanSplitWrittenList reports whether this language has a splitter.
func CanSplitWrittenList(language string) bool {
	_, ok := listSyntaxes[normalizeLang(language)]
	return ok
}

// RecordsToolSplit reports whether a frontend records the node's tools with the location of each
// declaration — the fact P14 tool pruning is actually blocked on.
//
// Go records it from go/ast; every syntactic language records it once it has a list splitter, because
// the split IS the recording. Keeping this derived rather than listed means a language cannot appear
// prunable in coverage while its frontend records nothing.
func RecordsToolSplit(language string) bool {
	l := normalizeLang(language)
	return l == "go" || CanSplitWrittenList(l)
}

// IsWrittenList reports whether text is a list this language's author WROTE, as opposed to a variable,
// a call, or a comprehension. It is the cheap precondition SplitWrittenList repeats internally, exposed
// so a caller can produce its own refusal sentence before paying for the scan.
func IsWrittenList(language, text string) bool {
	syn, ok := listSyntaxes[normalizeLang(language)]
	if !ok {
		return false
	}
	_, _, _, ok = matchListForm(syn, text)
	return ok
}

// SplitWrittenList returns the written elements of a list literal, or a refusal naming what stopped it.
//
// Offsets are relative to text. The elements are TRIMMED — an element's span covers the expression the
// author wrote and not the whitespace around it — because a deletion that swallowed the leading spaces
// of the next line would change indentation the change never asked to touch.
func SplitWrittenList(language, text string) ([]ListElement, error) {
	lang := normalizeLang(language)
	syn, ok := listSyntaxes[lang]
	if !ok {
		return nil, fmt.Errorf("no %s list splitter has landed, so this engine cannot say which written "+
			"elements this list has", languageName(lang))
	}
	inner, innerOff, _, ok := matchListForm(syn, text)
	if !ok {
		return nil, fmt.Errorf("the value is not a written list literal")
	}
	return splitInner(syn, inner, innerOff)
}

// matchListForm finds which spelling of "a list" this text is, and returns its interior.
func matchListForm(syn listSyntax, text string) (inner string, innerOff int, form listForm, ok bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", 0, listForm{}, false
	}
	off := strings.Index(text, t)
	for _, f := range syn.forms {
		if len(t) < len(f.prefix)+2 {
			continue
		}
		if f.prefix != "" && !strings.HasPrefix(t, f.prefix) {
			continue
		}
		body := t[len(f.prefix):]
		// Between a prefix and its bracket a language may allow nothing but whitespace; anything else
		// (`listOfNotNull` matched by the `listOf` prefix, say) is a different call and must not match.
		lead := len(body) - len(strings.TrimLeft(body, " \t"))
		body = body[lead:]
		if len(body) < 2 || body[0] != f.open || body[len(body)-1] != f.close {
			continue
		}
		start := off + len(f.prefix) + lead + 1
		return body[1 : len(body)-1], start, f, true
	}
	return "", 0, listForm{}, false
}

// splitInner is the scan. It is shared by every language, which is the point: the differences are all in
// syn, and the arithmetic — the part that would be subtly wrong five separate times — is written once.
func splitInner(syn listSyntax, inner string, innerOff int) ([]ListElement, error) {
	var out []ListElement
	depth := 0
	start := 0

	flush := func(end int) error {
		seg := inner[start:end]
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" {
			return nil // a trailing separator, or whitespace between two of them
		}
		for _, sp := range syn.spreads {
			if strings.HasPrefix(trimmed, sp) {
				return fmt.Errorf("element %q is a spread, so the list's length is not known until runtime "+
					"and a change over it cannot identify which elements it would touch", trimmed)
			}
		}
		lead := strings.Index(seg, trimmed)
		out = append(out, ListElement{
			Start: innerOff + start + lead,
			End:   innerOff + start + lead + len(trimmed),
			Text:  trimmed,
		})
		return nil
	}

	for i := 0; i < len(inner); i++ {
		if syn.lineComment != "" && strings.HasPrefix(inner[i:], syn.lineComment) {
			return nil, fmt.Errorf("the list carries a comment, which binds to a neighbouring element; " +
				"deleting an element without it would leave a comment describing something that is gone")
		}
		if syn.blockComment != "" && strings.HasPrefix(inner[i:], syn.blockComment) {
			return nil, fmt.Errorf("the list carries a block comment, which binds to a neighbouring element; " +
				"deleting an element without it would leave a comment describing something that is gone")
		}
		c := inner[i]
		if strings.IndexByte(syn.quotes, c) >= 0 {
			for _, tq := range syn.tripleQuotes {
				if strings.HasPrefix(inner[i:], tq) {
					return nil, fmt.Errorf("the list carries a multi-line string, which spans lines by nature; " +
						"a multi-line element cannot be deleted without changing the file's line count")
				}
			}
			j, err := skipQuoted(inner, i)
			if err != nil {
				return nil, err
			}
			i = j
			continue
		}
		switch c {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("the list's brackets are unbalanced; the parse tree and the text disagree")
			}
		case ',':
			if depth == 0 {
				if err := flush(i); err != nil {
					return nil, err
				}
				start = i + 1
			}
		case '\n':
			// A newline inside the list is fine — an element per line is the common formatting. It is only
			// a multi-line ELEMENT (caught above, via the multi-line string) that cannot be deleted.
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("the list's brackets are unbalanced; the parse tree and the text disagree")
	}
	if err := flush(len(inner)); err != nil {
		return nil, err
	}
	return out, nil
}

// skipQuoted advances past a single-line string literal, honouring backslash escapes.
func skipQuoted(s string, i int) (int, error) {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case quote:
			return j, nil
		case '\n':
			return 0, fmt.Errorf("a string literal in the list is not terminated on its own line; the parse " +
				"tree and the text disagree")
		}
	}
	return 0, fmt.Errorf("a string literal in the list is not terminated within the argument; the parse " +
		"tree and the text disagree")
}

func normalizeLang(l string) string { return strings.ToLower(strings.TrimSpace(l)) }

// languageName is the display spelling used in refusals, so "typescript" reads as TypeScript in a
// sentence a human is expected to act on.
func languageName(l string) string {
	switch normalizeLang(l) {
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "typescript":
		return "TypeScript"
	case "javascript":
		return "JavaScript"
	case "java":
		return "Java"
	case "kotlin":
		return "Kotlin"
	case "rust":
		return "Rust"
	case "":
		return "this language"
	}
	return l
}
