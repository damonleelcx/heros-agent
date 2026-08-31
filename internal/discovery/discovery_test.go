package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo writes a small source tree and returns its root.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const agentPy = `import openai

SYSTEM_PROMPT = "You are a support agent."

def build_messages(history):
    messages = [{"role": "system", "content": SYSTEM_PROMPT}]
    messages += history
    return messages

def answer(history):
    return openai.chat.completions.create(
        model="gpt-4o",
        temperature=0.7,
        messages=build_messages(history),
    )
`

// TestTestFilesAreExcludedAndCounted.
//
// 🔴 Learned from two real repositories, not reasoned about: nearly every call site and axis span landed
// in test files, because tests instantiate models and set temperatures more explicitly than production
// code does. An assessment built from them describes the test suite while appearing to describe the
// agent — with real file:line references the reader can follow to real code, which is the most
// convincing way to be wrong.
func TestTestFilesAreExcludedAndCounted(t *testing.T) {
	root := repo(t, map[string]string{
		"agent/bot.py":           agentPy,
		"agent/test_bot.py":      agentPy,
		"agent/bot_test.py":      agentPy,
		"tests/integration.py":   agentPy,
		"web/app.test.ts":        `const x = 1;`,
		"internal/thing_test.go": `package internal`,
	})
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, f := range c.Files {
		if isTestPath(f.Path) {
			t.Errorf("test file %s was read", f.Path)
		}
	}
	if len(c.Files) != 1 || c.Files[0].Path != "agent/bot.py" {
		t.Fatalf("read %d files (%v); only the production file should remain", len(c.Files), paths(c))
	}
	if c.Skipped["test-file"] != 5 {
		t.Errorf("skipped test-file = %d, want 5 — the exclusion must be visible, because a repository "+
			"whose only model calls are in tests is a real thing a report has to be able to describe",
			c.Skipped["test-file"])
	}
}

func paths(c Corpus) []string {
	var out []string
	for _, f := range c.Files {
		out = append(out, f.Path)
	}
	return out
}

// TestTheWalkStaysInsideTheRoot. An agent that can be pointed at a directory and follows a symlink out
// of it is a data-exfiltration tool with a friendly name.
func TestTheWalkStaysInsideTheRoot(t *testing.T) {
	outside := repo(t, map[string]string{"secret.py": "API_KEY = 'sk-real'\n"})
	root := repo(t, map[string]string{"agent/bot.py": agentPy})
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, f := range c.Files {
		if strings.Contains(f.Path, "secret") {
			t.Fatalf("the walk followed a symlink out of the root and read %s", f.Path)
		}
		for _, line := range f.Lines {
			if strings.Contains(line, "sk-real") {
				t.Fatal("content from outside the root was read")
			}
		}
	}
}

// TestVendoredTreesAreNotRead. They are most of the bytes and none of the customer's decisions.
func TestVendoredTreesAreNotRead(t *testing.T) {
	root := repo(t, map[string]string{
		"agent/bot.py":                    agentPy,
		"node_modules/pkg/index.js":       `const openai = require('openai');`,
		".venv/lib/site.py":               agentPy,
		"vendor/x/y.go":                   `package x`,
		"__pycache__/bot.cpython-311.pyc": "x",
	})
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(c.Files) != 1 {
		t.Fatalf("read %v; only the agent's own code should be read", paths(c))
	}
}

// TestAbsenceIsAFindingAndSaysWhichKind.
//
// 🔴 "Your agent has no memory strategy" and "I could not read your repository" are different
// conclusions, and a reader who cannot tell them apart will act on the wrong one.
func TestAbsenceIsAFindingAndSaysWhichKind(t *testing.T) {
	root := repo(t, map[string]string{"agent/bot.py": "def add(a, b):\n    return a + b\n"})
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	ev := ForAxis(c, "memory")
	if ev.Found {
		t.Fatal("found memory evidence in a file that has none")
	}
	if ev.Note == "" {
		t.Fatal("an absence with no explanation is indistinguishable from a failure to look")
	}
	if !strings.Contains(ev.Note, "That is a finding") {
		t.Errorf("the note does not frame absence as a finding: %q", ev.Note)
	}
	// The Excerpt contract: false, so an unreadable axis fails loudly rather than assessing "".
	if _, ok := c.Excerpt("memory"); ok {
		t.Fatal("Excerpt returned ok for an axis with no evidence; the tool would assess an empty string")
	}
}

// TestATruncatedWalkSaysSoRatherThanReportingAbsence. Absence of evidence is not evidence of absence,
// and the corpus knows which one it is looking at.
func TestATruncatedWalkSaysSoRatherThanReportingAbsence(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 30; i++ {
		files[filepath.ToSlash(filepath.Join("pkg", "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".py"))] =
			"def f():\n    return 1\n"
	}
	root := repo(t, files)
	c, err := Walk(root, Limits{MaxFiles: 5})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !c.Truncated {
		t.Fatal("the walk hit its file limit and did not say so")
	}
	ev := ForAxis(c, "memory")
	if !strings.Contains(ev.Note, "absence of evidence") {
		t.Errorf("a truncated walk reported a plain absence: %q", ev.Note)
	}
}

// TestCallSitesAreFoundAndCommentsAreNot. Reporting a commented-out call sends a reader to a line that
// does nothing, which costs more trust than the finding was worth.
func TestCallSitesAreFoundAndCommentsAreNot(t *testing.T) {
	root := repo(t, map[string]string{
		"agent/bot.py": agentPy + "\n# openai.chat.completions.create(model='old')\n",
	})
	c, _ := Walk(root, Limits{})
	nodes := Nodes(c)
	if len(nodes) != 1 {
		t.Fatalf("found %d call sites, want 1 (the commented one must not count)", len(nodes))
	}
	if nodes[0].Enclosing != "answer" {
		t.Errorf("enclosing = %q, want answer", nodes[0].Enclosing)
	}
	if nodes[0].Span.Line == 0 {
		t.Error("a span with no line cannot be navigated to, so it cannot be checked")
	}
}

// TestLooksLikeAnAgentSeparatesWrongSubjectFromNineWeaknesses.
//
// A nine-axis report over a repository that never calls a model is nine paragraphs about nothing — and
// every axis would honestly report "no signal", which reads as nine weaknesses rather than one wrong
// subject. Verified against two real repositories: a Go service returned false, an agent framework
// returned true with 987 call sites.
func TestLooksLikeAnAgentSeparatesWrongSubjectFromNineWeaknesses(t *testing.T) {
	agent := repo(t, map[string]string{"agent/bot.py": agentPy})
	c, _ := Walk(agent, Limits{})
	if ok, why := c.LooksLikeAnAgent(); !ok {
		t.Fatalf("a repository that calls a model was not recognised: %s", why)
	}

	service := repo(t, map[string]string{"srv/handler.go": "package srv\n\nfunc Handle() error { return nil }\n"})
	c2, _ := Walk(service, Limits{})
	ok, why := c2.LooksLikeAnAgent()
	if ok {
		t.Fatal("a plain service was reported as an agent")
	}
	if !strings.Contains(why, "calls a model") {
		t.Errorf("the reason does not say what was missing: %q", why)
	}
}

// TestSpansPreferFilesThatCallAModel.
//
// 🔴 Regression fence. The first version sorted by path and truncated to the first twelve, so on a large
// repository every sample came from whichever directory sorts first, regardless of where the agent
// lives. The report still read as evidence — real file:line references the reader could follow — drawn
// from a directory chosen by the alphabet.
func TestSpansPreferFilesThatCallAModel(t *testing.T) {
	files := map[string]string{"zz_agent/bot.py": agentPy}
	// Twenty earlier-sorting files that match the model axis but never call anything.
	for i := 0; i < 20; i++ {
		name := filepath.ToSlash(filepath.Join("aaa_config", "c"+string(rune('a'+i))+".py"))
		files[name] = "temperature = 0.1\nmodel = \"gpt-4o\"\n"
	}
	root := repo(t, files)
	c, _ := Walk(root, Limits{})
	ev := ForAxis(c, "model")
	if !ev.Found || len(ev.Spans) == 0 {
		t.Fatal("no model evidence found")
	}
	if !strings.HasPrefix(ev.Spans[0].Path, "zz_agent/") {
		t.Fatalf("the first span is %s; a file that CALLS a model must rank above twenty "+
			"alphabetically-earlier files that merely mention one", ev.Spans[0].Path)
	}
	if !strings.Contains(ev.Note, "proximity to a call site") {
		t.Errorf("the sample does not disclose how it was ranked: %q", ev.Note)
	}
}

// TestEverySpanCanBeNavigatedTo. A finding a reader cannot check is one they must take on faith.
func TestEverySpanCanBeNavigatedTo(t *testing.T) {
	root := repo(t, map[string]string{"agent/bot.py": agentPy})
	c, _ := Walk(root, Limits{})
	for _, axis := range []string{"model", "prompt", "context"} {
		ev := ForAxis(c, axis)
		for _, s := range ev.Spans {
			if s.Path == "" || s.Line < 1 || s.Text == "" || s.Why == "" {
				t.Errorf("%s: incomplete span %+v", axis, s)
			}
			if !strings.Contains(s.Ref(), ":") {
				t.Errorf("%s: unnavigable ref %q", axis, s.Ref())
			}
		}
	}
}
