package discovery

import (
	"strings"
)

// Locating a value the program bound BEFORE the call (P13 FR52/FR53)
// ──────────────────────────────────────────────────────────────────
//
// Every locator form before these two addressed something inside the call's own argument list. That
// assumption is invisible while it holds — for Go and Python it always does — and it is exactly what
// made Java, Kotlin and Rust look unrewritable. They are not. Their SDKs state the model somewhere else:
//
//	OpenAiChatModel.builder().modelName("gpt-4o").build()      // Kotlin/Java: a BUILDER
//	CreateChatCompletionRequestArgs::default().model("gpt-4o") // Rust: a REQUEST value
//
// Kotlin is the proof that this is a locator problem rather than a language one: Kotlin HAS named
// arguments and a working analyzer, and still refused, because no row could point at a model set two
// statements earlier. So this file finds that statement, and the rewriter then does what it always
// does — replace an expression the program already wrote.
//
// # Two rules, both fail-closed, both ratified as PRD open questions
//
//  1. 🔴 EXACTLY ONE occurrence in the unit, or nothing (P13 §14 Q11). One builder can feed several call
//     sites; rewriting it would change every node that uses it, so a per-node override would silently
//     alter a sibling node — a false measurement dressed as a change. Ambiguity is RECORDED (the count),
//     never resolved by picking the nearest, because "nearest" is a guess that compiles.
//  2. 🔴 The occurrence must be BEFORE the call (P13 §14 Q10). A `.model(...)` written after the call
//     does not bind the value the call used.
//
// # Why this is text scanning and not a tree walk
//
// The parse tree is closed by the time detection runs (`defer tree.Close()`) — the same constraint that
// made ArgValue carry spans rather than nodes. What is being matched here is lexically identical in
// every language that has the shape (`.name(` … `)`), so the per-language part is only which quotes and
// comments to skip — the same shape as listSyntaxes, and for the same reason it is DATA.

// BindingScan is what a locator search found, including the reason it found nothing usable.
//
// It is a struct rather than an (ArgValue, bool) pair because "not written" and "written in more than
// one place" need different refusals: the first tells the author to bind at the call site, the second
// tells them the builder is shared and a per-node change would touch their other nodes.
type BindingScan struct {
	// Value is the located value expression, when exactly one binding was found.
	Value ArgValue
	// Found reports whether Value is usable.
	Found bool
	// Occurrences is how many bindings were seen before the call. >1 means shared and is why Found is
	// false; the refusal quotes it.
	Occurrences int
}

// LocateBindingSite finds the value a builder-chain call or request-field setter bound before a call
// site, in a unit's source.
//
// `binder` is the method or field name the registry row declares (ArgLocator.Builder / .Request).
// `callOffset` is the byte offset of the call being rewritten; only bindings before it count.
func LocateBindingSite(src []byte, language, binder string, callOffset int) BindingScan {
	if binder == "" || callOffset <= 0 {
		return BindingScan{}
	}
	syn, ok := listSyntaxes[normalizeLang(language)]
	if !ok {
		// No syntax profile: this language's quoting rules are unknown, so scanning its text would be a
		// guess. Fail closed — coverage reports the missing profile.
		return BindingScan{}
	}
	if callOffset > len(src) {
		callOffset = len(src)
	}
	region := string(src[:callOffset])

	needle := "." + binder
	var found BindingScan
	for i := 0; i+len(needle) < len(region); i++ {
		if !strings.HasPrefix(region[i:], needle) {
			continue
		}
		// The character after the name must open an argument list, and the one before the dot must not be
		// part of a longer identifier — `.model(` must not match inside `.modelName(`.
		j := i + len(needle)
		for j < len(region) && isSpaceByte(region[j]) {
			j++
		}
		if j >= len(region) || region[j] != '(' {
			continue
		}
		if i > 0 && isIdentByte(region[i-1]) {
			continue
		}
		inner, end, ok := argOf(syn, region, j)
		if !ok {
			continue
		}
		found.Occurrences++
		if found.Occurrences > 1 {
			// 🔴 Shared. Record the count and stop trusting either site; see rule 1.
			found.Found = false
			continue
		}
		text := strings.TrimSpace(inner)
		lead := strings.Index(inner, text)
		start := j + 1 + lead
		found.Value = ArgValue{
			Value: ArgSpan{Start: start, End: start + len(text)},
			Kind:  bindingArgKind(syn, text),
			Text:  staticTextOf(syn, text),
		}
		found.Found = true
		i = end
	}
	if found.Occurrences > 1 {
		found.Found = false
	}
	return found
}

// argOf returns the single argument text inside the parentheses opening at `open`, its end offset, and
// whether the parentheses closed. A call with more than one argument is NOT a binding site this engine
// will rewrite: `.model("x", opts)` sets more than the value, and replacing the first argument would
// leave the rest addressing a different configuration.
func argOf(syn listSyntax, s string, open int) (inner string, end int, ok bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(syn.quotes, c) >= 0 {
			j, err := skipQuoted(s, i)
			if err != nil {
				return "", 0, false
			}
			i = j
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				body := s[open+1 : i]
				if strings.Contains(body, ",") && topLevelComma(syn, body) {
					return "", 0, false
				}
				if strings.TrimSpace(body) == "" {
					return "", 0, false
				}
				return body, i, true
			}
			if depth < 0 {
				return "", 0, false
			}
		}
	}
	return "", 0, false
}

// topLevelComma reports whether body has a comma outside brackets and strings — i.e. more than one
// argument. A nested comma (inside a map literal) is not a second argument.
func topLevelComma(syn listSyntax, body string) bool {
	depth := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		if strings.IndexByte(syn.quotes, c) >= 0 {
			j, err := skipQuoted(body, i)
			if err != nil {
				return true // unreadable: treat as multi-argument, which refuses
			}
			i = j
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// bindingArgKind classifies a located binding value the same way the analyzers classify a keyword
// argument, so a rewriter's existing rules about what may be replaced apply unchanged.
func bindingArgKind(syn listSyntax, text string) ArgKind {
	if len(text) >= 2 && strings.IndexByte(syn.quotes, text[0]) >= 0 && text[len(text)-1] == text[0] {
		if strings.ContainsAny(text, "${") && (strings.Contains(text, "${") || strings.Contains(text, "{")) {
			return ArgInterpolatedString
		}
		return ArgLiteralString
	}
	return ArgExpression
}

// staticTextOf reads the static text out of a binding value, for the resolution floor. Empty for
// anything the floor cannot read — a variable, a call, an interpolation.
func staticTextOf(syn listSyntax, text string) string {
	if len(text) >= 2 && strings.IndexByte(syn.quotes, text[0]) >= 0 && text[len(text)-1] == text[0] {
		body := text[1 : len(text)-1]
		if strings.Contains(body, "${") || strings.Contains(body, "\\") {
			return ""
		}
		return body
	}
	return ""
}

func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// locateRowBindings resolves every binds-before-the-call locator a matched row declares, for one call
// site. Nothing is recorded for the ordinary forms, so a call site whose row points inside the argument
// list carries an empty map and every existing path behaves exactly as before.
func locateRowBindings(src []byte, language string, s DetectedCallSite) map[string]BindingScan {
	if len(src) == 0 {
		return nil
	}
	locs := map[string]*ArgLocator{"model": s.ArgMap.Model, "prompt": s.ArgMap.Prompt, "tools": s.ArgMap.Tools}
	var out map[string]BindingScan
	for dim, loc := range locs {
		if loc == nil || !loc.Form.BindsBeforeTheCall() {
			continue
		}
		binder := loc.Builder
		if binder == "" {
			binder = loc.Request
		}
		scan := LocateBindingSite(src, language, binder, callOffsetOf(src, s.LineStart))
		if out == nil {
			out = map[string]BindingScan{}
		}
		out[dim] = scan
	}
	return out
}

// callOffsetOf converts a 1-based line to the byte offset of that line's start, which is the boundary
// "before the call" is measured against. Line granularity is deliberate: a binding written on the same
// line as the call (`client.model("x").generate(p)`) is still before it in evaluation order, and
// excluding it would refuse a chain that is perfectly rewritable.
func callOffsetOf(src []byte, line int) int {
	if line <= 1 {
		return len(src)
	}
	cur, off := 1, 0
	for off < len(src) && cur < line {
		if src[off] == '\n' {
			cur++
		}
		off++
	}
	// Include the call's own line, so a same-line builder chain counts.
	for off < len(src) && src[off] != '\n' {
		off++
	}
	return off
}
