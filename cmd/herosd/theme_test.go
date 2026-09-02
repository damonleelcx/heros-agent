package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The four pages the file server answers, plus the stylesheet they all link.
var themedPages = []string{
	"index.html",
	"app/index.html",
	"signin/index.html",
	"signup/index.html",
}

// TestTheTwoLightPalettesAgree.
//
// # 🔴 The bug this exists for
//
// The light palette is written TWICE in heros.css, and it has to be:
//
//	:root[data-theme="light"]                                    — the visitor chose light
//	@media (prefers-color-scheme:light){ :root:not([data-theme="dark"]) }
//	                                                             — the visitor chose nothing; their OS did
//
// The single-copy alternative is to have heros-theme.js write the RESOLVED theme onto <html>, and that
// costs more than it saves: JavaScript becomes load-bearing for a DEFAULT, the answer freezes at load
// if the OS flips at dusk, and the attribute lands after first paint, which is the flash.
//
// So there are two copies, and two copies drift. The failure is quiet in the worst way: whichever copy
// is wrong is the one NOBODY IS LOOKING AT — the maintainer edits with the switch set to light and
// never sees what a visitor on a light-preferring device gets, or vice versa. Nothing throws, nothing
// renders blank, one population just gets a half-converted page.
//
// This is the fence that makes the duplication safe. If it goes red, do not delete a block: copy the
// declaration you meant into both.
func TestTheTwoLightPalettesAgree(t *testing.T) {
	css := readCSS(t)

	chosen, err := cssBlock(css, `:root[data-theme="light"]{`)
	if err != nil {
		t.Fatalf("the explicit light palette is unreadable: %v", err)
	}
	inherited, err := cssBlock(css, `:root:not([data-theme="dark"]){`)
	if err != nil {
		t.Fatalf("the OS-default light palette is unreadable: %v", err)
	}

	a, b := declarations(chosen), declarations(inherited)
	if len(a) == 0 {
		t.Fatal("the explicit light palette declares nothing — the fence would pass on an empty block")
	}

	for _, name := range union(a, b) {
		av, aok := a[name]
		bv, bok := b[name]
		switch {
		case !bok:
			t.Errorf("%s is set to %q for a visitor who CHOSE light, and not set at all for one whose "+
				"device asked for light — they will see two different pages", name, av)
		case !aok:
			t.Errorf("%s is set to %q for a visitor whose DEVICE asked for light, and not set at all "+
				"for one who chose it in the switch", name, bv)
		case av != bv:
			t.Errorf("%s disagrees between the two light palettes: chosen=%q, from the device=%q",
				name, av, bv)
		}
	}
}

// TestNoColourLiteralOutsideThePalettes.
//
// # 🔴 The bug this exists for
//
// Every hairline, wash and tint used to be written as a hex literal with alpha digits appended —
// `#ffffff0d` for a table rule, `#2ecfa814` for a badge fill. That form is invisible to a theme: white
// at 5% is a hairline on black and NOTHING on paper, and the obvious repair (`var(--fg)0d`) is not a
// colour at all, so the browser drops the whole declaration and the border silently disappears while
// the page still renders perfectly well otherwise.
//
// So colour is stated in exactly one place — the palette blocks at the top of heros.css — and
// everything else refers to it: `rgba(var(--fg-rgb),α)` for surfaces built out of the foreground, and
// `color-mix(…, transparent)` for a tint of a named colour, so that changing `--bad` moves every tint
// of it.
//
// A literal that creeps back in is a colour that will be right in one theme and wrong in the other, and
// a code review will not catch it, because the page it was written against looks correct.
func TestNoColourLiteralOutsideThePalettes(t *testing.T) {
	// Anchored on a delimiter so `href="#does"` and `id="#start"` are not read as colours.
	literal := regexp.MustCompile(`[:,(\s]#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b`)

	root := repoRoot(t)
	for _, page := range themedPages {
		body, err := os.ReadFile(filepath.Join(root, defaultWebRoot, page))
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		for _, hit := range literal.FindAllString(stripCSSComments(string(body)), -1) {
			t.Errorf("%s contains the colour literal %s. A page states no colours; it refers to the "+
				"palette in heros.css. Add a token there if none of them fits.",
				page, strings.TrimLeft(hit, ":,( \t\n"))
		}
	}

	// heros.css may hold literals, but ONLY inside the palette blocks — which is the whole point of
	// having them.
	css := stripCSSComments(readCSS(t))
	for _, sel := range []string{`:root{`, `:root[data-theme="light"]{`, `:root:not([data-theme="dark"]){`} {
		block, err := cssBlock(css, sel)
		if err != nil {
			t.Fatalf("%s is unreadable: %v", sel, err)
		}
		css = strings.Replace(css, block, "", 1)
	}
	for _, line := range strings.Split(css, "\n") {
		// A mask's colour is not a colour: `#000` in a mask-image gradient is an ALPHA of 1, meaning
		// "keep this pixel". It is the same value in both themes because it is not a theme decision.
		if strings.Contains(line, "mask-image") {
			continue
		}
		if hit := literal.FindString(line); hit != "" {
			t.Errorf("heros.css states the colour %s outside the palette blocks, on: %s",
				strings.TrimLeft(hit, ":,( \t\n"), strings.TrimSpace(line))
		}
	}
}

// TestEveryPageAppliesTheThemeBeforeItPaints.
//
// # 🔴 The bug this exists for
//
// heros-theme.js sets `data-theme` on <html>. Loaded with `defer` — which is what every other script on
// these pages does, and therefore what the next person will copy — it runs AFTER the document is
// parsed and after the browser has already painted, so a visitor who chose light watches the page
// arrive dark and then correct itself. That flash is the entire reason the file is separate from
// heros-auth.js and heros-reveal.js, and nothing about the page looks wrong afterwards, so it survives
// review easily.
//
// A page that forgets the script altogether is worse: it silently ignores the switch.
func TestEveryPageAppliesTheThemeBeforeItPaints(t *testing.T) {
	root := repoRoot(t)
	tag := regexp.MustCompile(`<script[^>]*heros-theme\.js[^>]*>`)

	for _, page := range themedPages {
		body, err := os.ReadFile(filepath.Join(root, defaultWebRoot, page))
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		found := tag.FindString(string(body))
		if found == "" {
			t.Errorf("%s does not load heros-theme.js, so the theme switch does nothing on it", page)
			continue
		}
		if strings.Contains(found, "defer") || strings.Contains(found, "async") {
			t.Errorf("%s loads heros-theme.js as %s. Deferred, it applies the theme after the first "+
				"paint and the page visibly flashes the other theme.", page, found)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────────

func readCSS(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), defaultWebRoot, "heros.css"))
	if err != nil {
		t.Fatalf("heros.css: %v", err)
	}
	return string(body)
}

// cssBlock returns the text between the braces of the first rule opening with the given selector.
func cssBlock(css, opener string) (string, error) {
	i := strings.Index(css, opener)
	if i < 0 {
		return "", &notFound{opener}
	}
	rest := css[i+len(opener):]
	depth := 1
	for j, r := range rest {
		switch r {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return rest[:j], nil
			}
		}
	}
	return "", &notFound{opener + " (unclosed)"}
}

type notFound struct{ what string }

func (e *notFound) Error() string { return e.what + " is not in heros.css" }

// declarations reads `--name:value` pairs out of a block, ignoring comments and whitespace.
func declarations(block string) map[string]string {
	out := map[string]string{}
	for _, decl := range strings.Split(stripCSSComments(block), ";") {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "--") {
			continue
		}
		out[name] = strings.Join(strings.Fields(value), " ")
	}
	return out
}

func stripCSSComments(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i:], "*/")
		if j < 0 {
			return b.String()
		}
		s = s[i+j+2:]
	}
}

func union(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestStaticAssetsAreAlwaysRevalidated.
//
// # 🔴 The bug this exists for
//
// The console is four files that must agree with each other — index.html and the stylesheet and two
// scripts it links. http.FileServer sends only `Last-Modified`, and with no `Cache-Control` a browser
// invents its own expiry per file and serves each from cache without asking. The files then expire
// INDEPENDENTLY, so a deploy can leave an open tab holding a new index.html and an old heros.css. The
// page renders as neither version, and nothing is logged anywhere, because the server never sees a
// request for the stale half — it came out of the browser. Reported on 2026-09-02 after three deploys
// in one hour, and not reproducible on a cold load, which is what makes it worth a fence.
//
// Two ways to break this that both look like fixes:
//
//   - dropping the wrapper (back to heuristic caching — the original bug)
//   - "hardening" it to `no-store`, which forbids STORING the bytes and re-downloads the 229KB
//     portrait on every navigation. `no-cache` stores and revalidates; `no-store` does not store.
//     The test asserts the exact token for that reason.
//
// It also asserts the 304, because a wrapper that set headers the wrong way round could suppress
// conditional handling and turn every revalidation back into a full download — the cost this design
// is spending one round trip to avoid.
func TestStaticAssetsAreAlwaysRevalidated(t *testing.T) {
	root := repoRoot(t)
	srv := httptest.NewServer(alwaysRevalidate(http.FileServer(http.Dir(filepath.Join(root, defaultWebRoot)))))
	defer srv.Close()

	for _, path := range []string{"/", "/heros.css", "/heros-theme.js", "/heros-arc.js", "/avatar.jpg"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		got := res.Header.Get("Cache-Control")
		lastMod := res.Header.Get("Last-Modified")
		res.Body.Close()

		if got != "no-cache" {
			t.Errorf("%s served Cache-Control %q, want \"no-cache\". Without it the browser invents an "+
				"expiry per file and the console's files go stale independently of each other.", path, got)
			continue
		}
		if lastMod == "" {
			t.Errorf("%s has no Last-Modified, so \"revalidate\" has nothing to revalidate against", path)
			continue
		}

		// The revalidation itself: an unchanged file must come back 304 with no body.
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("If-Modified-Since", lastMod)
		again, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s conditional: %v", path, err)
		}
		again.Body.Close()
		if again.StatusCode != http.StatusNotModified {
			t.Errorf("%s revalidated with %d, want 304 — every reload would re-download the whole file",
				path, again.StatusCode)
		}
	}
}
