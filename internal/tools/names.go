package tools

import (
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// names.go answers one question per language: which module-qualified names does this source use that
// nothing brings into scope?
//
// # 🔴 Why the answer is compared against the ORIGINAL rather than judged on its own
//
// Because the question that matters is not "is this file clean" but "did this change break it". A file
// may already reference something conditionally imported, generated at build time, or injected by a
// framework — none of which this scanner understands. Judging the candidate alone would reject every
// correct change to such a file, and a check that punishes people for pre-existing code is one they
// learn to route around.
//
// So both versions are scanned and only the DIFFERENCE is reported. A name the change introduced and
// did not bring into scope is the failure; a name that was already there is somebody else's business.
//
// # 🚫 What this is not
//
// Not a type checker, not a linter, not a parser for anything but Python. For JavaScript and TypeScript
// it is a deliberately conservative scanner: it errs towards saying nothing, because a false rejection
// costs a correct change and a false acceptance costs one more round of the loop that already exists.

// unresolvedNames returns the module-qualified names in source that nothing binds, and whether the check
// was able to run at all.
//
// 🔴 The second return distinguishes "nothing unresolved" from "not checked". Collapsing them would let
// a missing interpreter read as a clean bill of health.
func unresolvedNames(source, lang string) (names []string, checked bool) {
	switch lang {
	case "python":
		return pythonUnresolved(source)
	case "typescript", "javascript":
		return scriptUnresolved(source), true
	}
	return nil, false
}

// pythonUnresolved uses Python's own parser, which is exact.
func pythonUnresolved(source string) ([]string, bool) {
	cmd := exec.Command("python3", "-c", pyNameCheck)
	cmd.Stdin = strings.NewReader(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// A syntax error here is not this check's business — `parses` already reported it — and a missing
		// interpreter is "not checked", not "clean".
		return nil, false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, true
	}
	return strings.Split(line, ", "), true
}

// ── javascript and typescript ────────────────────────────────────────────────────────────────────

var (
	// binders are the ways a name enters scope. Deliberately broad: every pattern here REDUCES what the
	// scanner reports, and under-reporting is the safe direction.
	binders = []*regexp.Regexp{
		regexp.MustCompile(`\bimport\s+([A-Za-z_$][\w$]*)`),            // import X from
		regexp.MustCompile(`\bimport\s*\*\s*as\s+([A-Za-z_$][\w$]*)`),  // import * as X
		regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)`), // const X =
		regexp.MustCompile(`\b(?:const|let|var)\s*\[([^\]]*)\]`),       // const [a, b] = — array destructuring
		regexp.MustCompile(`\bfunction\s*\*?\s*([A-Za-z_$][\w$]*)`),    // function X
		regexp.MustCompile(`\bclass\s+([A-Za-z_$][\w$]*)`),             // class X
		regexp.MustCompile(`\bas\s+([A-Za-z_$][\w$]*)`),                // { a as X }
		regexp.MustCompile(`\benum\s+([A-Za-z_$][\w$]*)`),              // TS enum
		regexp.MustCompile(`\bnamespace\s+([A-Za-z_$][\w$]*)`),         // TS namespace
		regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\s*:`),                 // params, object keys, TS types
		regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\s*=[^=]`),             // any assignment target
		regexp.MustCompile(`\(([^)]*)\)\s*=>`),                         // arrow parameters
	}

	// bindingBraces capture identifiers that a brace genuinely brings into scope: an import list and a
	// destructuring target.
	//
	// 🔴 NOT every `{ ... }`. The first version matched all braces, so an object literal bound every
	// identifier inside it — including the module references in its own values. `{ model: path.join(...) }`
	// bound `path`, and the check went quiet exactly where configuration lives, which is where changes
	// happen. Over-binding is the safe direction for false positives and the useless direction for
	// finding anything.
	bindingBraces = []*regexp.Regexp{
		regexp.MustCompile(`\bimport\s*\{([^{}]*)\}`),
		regexp.MustCompile(`\b(?:const|let|var)\s*\{([^{}]*)\}\s*=`),
	}

	// paramList captures anything that looks like a parameter list: a bracketed group immediately
	// followed by a body or an arrow. Covers functions, methods, arrows and `catch (err)`.
	paramList = regexp.MustCompile(`\(([^()]*)\)\s*(?:=>|\{)`)

	identifier = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// scriptKeywords are language keywords. A keyword followed by a dot is never a module reference —
// `import.meta`, and a `return` at the end of a line before a chained `.then(...)` — and treating them
// as one produced the two largest sources of false positives on a real 1,438-file corpus.
var scriptKeywords = map[string]bool{
	"import": true, "return": true, "await": true, "yield": true, "typeof": true, "new": true,
	"delete": true, "void": true, "instanceof": true, "in": true, "of": true, "as": true,
	"case": true, "throw": true, "else": true, "do": true, "default": true, "export": true,
	"extends": true, "satisfies": true, "keyof": true, "infer": true, "asserts": true,
}

// scriptGlobals are names that are always in scope. 🔴 Generous on purpose: a name wrongly missing from
// this list becomes a false rejection of a correct change, which is the expensive direction.
var scriptGlobals = map[string]bool{
	"console": true, "JSON": true, "Math": true, "Date": true, "Object": true, "Array": true,
	"String": true, "Number": true, "Boolean": true, "Promise": true, "Error": true, "Map": true,
	"Set": true, "WeakMap": true, "WeakSet": true, "RegExp": true, "Symbol": true, "BigInt": true,
	"Reflect": true, "Proxy": true, "Intl": true, "globalThis": true, "process": true,
	"Buffer": true, "URL": true, "URLSearchParams": true, "TextEncoder": true, "TextDecoder": true,
	"window": true, "document": true, "navigator": true, "location": true, "history": true,
	"localStorage": true, "sessionStorage": true, "fetch": true, "crypto": true, "performance": true,
	"this": true, "super": true, "arguments": true, "module": true, "exports": true, "require": true,
	"__dirname": true, "__filename": true, "AbortController": true, "Headers": true, "Request": true,
	"Response": true, "FormData": true, "Blob": true, "Event": true, "CustomEvent": true,
}

// scriptUnresolved scans JavaScript or TypeScript for module-qualified names nothing binds.
func scriptUnresolved(source string) []string {
	stripped := stripStringsAndComments(source)

	bound := map[string]bool{}
	for _, re := range binders {
		for _, m := range re.FindAllStringSubmatch(stripped, -1) {
			// An arrow's parameter list is a group of identifiers, not one.
			for _, id := range identifier.FindAllString(m[1], -1) {
				bound[id] = true
			}
		}
	}
	for _, re := range bindingBraces {
		for _, m := range re.FindAllStringSubmatch(stripped, -1) {
			for _, id := range identifier.FindAllString(m[1], -1) {
				bound[id] = true
			}
		}
	}
	for _, m := range paramList.FindAllStringSubmatch(stripped, -1) {
		for _, id := range identifier.FindAllString(m[1], -1) {
			bound[id] = true
		}
	}

	seen := map[string]bool{}
	var out []string
	for _, name := range qualifiedRoots(stripped) {
		if bound[name] || scriptGlobals[name] || scriptKeywords[name] || seen[name] {
			continue
		}
		// A capitalised bare name is very often a type, an enum or a namespace declared elsewhere in the
		// project; flagging those would reject correct changes constantly. Only lower-camel names — the
		// shape of an imported module or a local binding — are reported.
		if name[0] >= 'A' && name[0] <= 'Z' {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// qualifiedRoots returns each identifier that is followed by `.` and NOT preceded by one.
//
// # 🔴 Why this is a walk and not a regular expression
//
// The regex version read `client.chat.completions.create` as four names and reported `completions` and
// `create` as unresolved modules. It only looked forward — "is this followed by a dot" — and every
// segment of a property chain answers yes. Go's regexp has no lookbehind, and the overlapping-match
// rules make "not preceded by a dot" genuinely awkward to express; a walk states it directly.
//
// Only the ROOT of a chain can be a module reference. Everything after the first dot is a property of
// whatever came before it, and properties are not in anybody's scope to import.
func qualifiedRoots(src string) []string {
	var out []string
	for i := 0; i < len(src); {
		if !isIdentStart(src[i]) {
			i++
			continue
		}
		start := i
		for i < len(src) && isIdentPart(src[i]) {
			i++
		}
		name := src[start:i]

		// Preceded by a dot (skipping spaces) means this is a property, not a root.
		j := start - 1
		for j >= 0 && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n') {
			j--
		}
		if j >= 0 && src[j] == '.' {
			continue
		}
		// Followed by a dot means it is being used as a namespace.
		k := i
		for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n') {
			k++
		}
		if k < len(src) && src[k] == '.' {
			out = append(out, name)
		}
	}
	return out
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// stripStringsAndComments removes literals and comments so their contents are not read as code.
//
// 🔴 Without this, a URL in a string ("https://x.example/a.b") and a name in a comment both become
// "unresolved names", and the check would reject changes for mentioning things.
func stripStringsAndComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	const (
		code = iota
		lineComment
		blockComment
		single
		double
		backtick
	)
	state := code
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case code:
			switch {
			case c == '/' && i+1 < len(s) && s[i+1] == '/':
				state, i = lineComment, i+1
			case c == '/' && i+1 < len(s) && s[i+1] == '*':
				state, i = blockComment, i+1
			case c == '\'':
				state = single
			case c == '"':
				state = double
			case c == '`':
				state = backtick
			default:
				b.WriteByte(c)
			}
		case lineComment:
			if c == '\n' {
				state = code
				b.WriteByte(c)
			}
		case blockComment:
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				state, i = code, i+1
			}
		case single, double, backtick:
			if c == '\\' {
				i++ // skip the escaped character
				continue
			}
			if (state == single && c == '\'') || (state == double && c == '"') ||
				(state == backtick && c == '`') {
				state = code
			}
		}
	}
	return b.String()
}

// introducedBy returns the names unresolved in the candidate that were not already unresolved before.
func introducedBy(before, after []string) []string {
	had := map[string]bool{}
	for _, n := range before {
		had[n] = true
	}
	var out []string
	for _, n := range after {
		if !had[n] {
			out = append(out, n)
		}
	}
	return out
}
