package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cutover_test.go is P9 §12.5 — the assertion that the legacy pages are gone and stayed gone.
//
// # Why a test rather than a note in a changelog
//
// A removal is a one-way door, and the failure mode after one is not that the file comes back — it is
// that a route quietly starts serving something again, or that a document keeps sending people to a
// URL that now 404s. Both are silent. The first is caught by asking the mux; the second by reading the
// documentation the way a reader would.
//
// The four pages were removed together with their handlers and `go:embed` directives so no route is
// left serving a stale asset, and `static/index.html` — which had no handler and three endpoints that
// never existed — was deleted outright. Every behaviour they carried is in
// `web/console/tests/inventory.test.mjs`, one case per inventory item.

// TestRemovedLegacyRoutesDoNotRespond asks the mux directly, with every subsystem mounted.
//
// Mounting matters: an unmounted subsystem's route is simply unregistered, so a 404 would prove
// nothing about whether the UI handler was removed. With the subsystems mounted, the API routes answer
// and only the UI routes are gone — which is exactly the distinction being asserted.
func TestRemovedLegacyRoutesDoNotRespond(t *testing.T) {
	s := newTestServer(t)

	for _, path := range []string{"/p2", "/p2/", "/p25/monitor", "/p35/graph", "/p4/board"} {
		rec := do(t, s, "GET", path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d — the legacy UI route is still served", path, rec.Code)
		}
	}
}

// TestRemovedLegacyPagesAreGoneFromTheBinary asserts the assets themselves are not embedded anywhere.
//
// A handler can be unregistered while its `go:embed` stays, which leaves the page in the binary and
// one line away from being served again.
func TestRemovedLegacyPagesAreGoneFromTheBinary(t *testing.T) {
	entries, err := os.ReadDir("static")
	if err != nil {
		t.Fatalf("reading static/: %v", err)
	}
	gone := map[string]bool{
		"p2.html": true, "p25monitor.html": true, "p35graph.html": true,
		"p4board.html": true, "index.html": true,
	}
	for _, e := range entries {
		if gone[e.Name()] {
			t.Errorf("static/%s is back — it was removed in the P9 cutover", e.Name())
		}
	}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for name := range gone {
			if strings.Contains(string(body), "//go:embed static/"+name) {
				t.Errorf("%s still embeds static/%s", src, name)
			}
		}
	}
}

// TestNoDocumentationLinksToARemovedRoute reads the repository's docs the way a reader would.
//
// It looks for a runnable INSTRUCTION, not for a mention. Two distinctions matter, and the test learned
// the second one by crying wolf:
//
//  1. The PRDs discuss `p4board.html` at length as the accessibility reference the port was measured
//     against. That history is worth keeping, and it is not an instruction.
//  2. A record that QUOTES a removed URL while explaining that it was removed is also not an
//     instruction. This test's first run flagged the very sentence describing the stale link it had
//     just found — which is the failure mode a guard that cannot tell code from commentary always has:
//     it either cries wolf, or somebody loosens it until it stops catching the real thing.
//
// So an instruction is a URL inside a fenced code block, or a line that begins with a command. A URL
// inside backticks or emphasis in running prose is a quotation.
func TestNoDocumentationLinksToARemovedRoute(t *testing.T) {
	// Routes as they would appear in a runnable instruction — after a host, or in a markdown link.
	removed := []string{"/p25/monitor", "/p35/graph", "/p4/board"}

	var offenders []string
	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable path is not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", ".next", ".git", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr
		}
		inFence := false
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			for _, route := range removed {
				if !strings.Contains(line, "http://") || !strings.Contains(line, route) {
					continue
				}
				trimmed := strings.TrimSpace(line)
				// A command somebody would run: inside a fence, or a bare `open …` / `curl …` line.
				instruction := inFence ||
					strings.HasPrefix(trimmed, "open ") ||
					strings.HasPrefix(trimmed, "curl ") ||
					strings.HasPrefix(trimmed, "$ ")
				// A quotation: the URL is wrapped in backticks or emphasis inside running prose.
				quoted := strings.Contains(line, "`http://") || strings.Contains(line, "*open http://")
				if instruction && !quoted {
					offenders = append(offenders, path+": "+trimmed)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offenders {
		t.Errorf("documentation still sends a reader to a removed route — %s", o)
	}
}
