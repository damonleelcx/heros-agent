package transform

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §3 — the Python call-site rewriter.
//
// Everything here goes through the REAL Generate against a REAL fixture, discovered with the REAL
// registry. The bug this phase could plausibly ship is a recall-only diff, and a test that fed a
// rewriter its own spans could not see it.

// pyMemorySrc is the shape that materializes: a written message list, and a result assigned to a name at
// statement level. Both halves are available.
const pyMemorySrc = `import openai

client = openai.OpenAI()


def chat(question):
    resp = client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": question}],
    )
    return resp
`

// pyMemoryNoAssignSrc writes its messages but RETURNS the call directly. The recall half could land; the
// record half cannot, because no name holds the response. This is the both-halves-or-refuse case.
const pyMemoryNoAssignSrc = `import openai

client = openai.OpenAI()


def chat(question):
    return client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": question}],
    )
`

// defaultMemoryParams are schema-valid params per strategy. A fixture with EMPTY params is not a
// simpler fixture — the runtime rejects `max_entries: 0` because retaining nothing is `none` under
// another name, so an empty-params fixture tests the rejection rather than the materialization.
var defaultMemoryParams = map[string]string{
	"none":           `{}`,
	"scratchpad":     `{"max_entries":4}`,
	"summary-buffer": `{"max_tokens":2000,"keep_last_turns":2}`,
	"vector-recall":  `{"top_k":3,"embedding_ref":"text-embedding-3-small"}`,
	"entity-memory":  `{"entity_keys":["user_name","project"]}`,
}

func memoryOverride(t *testing.T, strategy string) variantspec.ResolvedOverride {
	t.Helper()
	e := memoryEntry(t, strategy)
	params, ok := defaultMemoryParams[strategy]
	if !ok {
		t.Fatalf("no default params for strategy %q", strategy)
	}
	e.Spec.Params = json.RawMessage(params)
	return variantspec.ResolvedOverride{Memory: e}
}

// materializeAt runs the memory materializer against a REAL discovered call site.
//
// It calls spanMaterializeMemory directly rather than going through Generate, which narrows what these
// tests see to the rewriter itself: real discovery, real spans, real edits, and the both-halves gate,
// with no artifact generation or diff assembly in the frame.
//
// 🔴 That is now a focus, not a boundary. This comment used to say the materializer was NOT in the span
// dispatch — true when the import of the generated module was an untargeted line that engine.go's
// minimality gate correctly rejected. The import edit class landed with its own admission rule, the
// dispatch entry is live (rewrite_span.go), and TestMemoryMaterializesEndToEnd drives the whole path
// through Generate. Read that one for what ships; read these for why each edit is the edit it is.
func materializeAt(t *testing.T, src, strategy string) ([]edit, error) {
	t.Helper()
	root := spanTarget(t, "pipeline.py", src)
	sites := spanSites(t, root, "python")
	if len(sites) != 1 {
		t.Fatalf("fixture has %d call sites, want 1", len(sites))
	}
	var site discovery.SpanCallSite
	for _, s := range sites {
		site = s
	}
	return spanMaterializeMemory(site, []byte(src), memoryOverride(t, strategy))
}

// applyTo splices the edits into src, so a test can assert on the RESULTING SOURCE rather than on the
// existence of an edit — the load-bearing assertion, exactly as span_test.go argues.
func applyTo(t *testing.T, src string, edits []edit) string {
	t.Helper()
	out, err := applyEdits([]byte(src), edits)
	if err != nil {
		t.Fatalf("applyEdits: %v", err)
	}
	return string(out)
}

// TestPythonMemoryMaterializesBothHalves — tasks 3.1 and 3.2. One materialization, both halves asserted,
// because they are one unit.
func TestPythonMemoryMaterializesBothHalves(t *testing.T) {
	edits, err := materializeAt(t, pyMemorySrc, "scratchpad")
	if err != nil {
		t.Fatalf("a complete memory cell must materialize: %v", err)
	}
	if len(edits) != 2 {
		t.Fatalf("got %d edit(s), want exactly 2 (recall and record) — a memory strategy is a read AND a "+
			"write, and either alone behaves as `none`", len(edits))
	}
	after := applyTo(t, pyMemorySrc, edits)

	if !strings.Contains(after, `messages=agentmem.recall(`) {
		t.Errorf("the recall half is missing:\n%s", after)
	}
	if !strings.Contains(after, `agentmem.record(`) || !strings.Contains(after, `, resp)`) {
		t.Errorf("the record half is missing or does not name the response variable:\n%s", after)
	}
	// The author's own list survives verbatim inside the wrapper — memory AUGMENTS a call, it does not
	// rewrite what the author asked for.
	if !strings.Contains(after, `[{"role": "user", "content": question}]`) {
		t.Errorf("the author's message list was altered:\n%s", after)
	}
	// 🔴 No newline is introduced: engine.go's line-count invariant holds, so a compiler error stays
	// attributable and "only the targeted lines changed" stays checkable.
	if strings.Count(after, "\n") != strings.Count(pyMemorySrc, "\n") {
		t.Errorf("the materialization changed the file's line count:\n%s", after)
	}
	assertPythonParses(t, after)
}

// TestArtifactShipsWithTheMaterialization — task 2.1. The module and its document are generated for the
// same resolved config the call-site edit came from.
func TestArtifactShipsWithTheMaterialization(t *testing.T) {
	files, err := GenerateMemoryArtifacts(memResolved(t, "n1", "scratchpad", `{"max_entries":5}`), "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{pyMemoryModulePath, memoryDocPath} {
		if _, ok := files[want]; !ok {
			t.Errorf("the artifact set does not carry %s; a rewritten call site would call a module that "+
				"does not exist. Got: %v", want, fileNames(files))
		}
	}
}

// TestKwargsCallSiteRefusesAboutTheCall — task 3.5. hermes-agent's real shape.
func TestKwargsCallSiteRefusesAboutTheCall(t *testing.T) {
	_, err := materializeAt(t, pyKwargsSrc, "summary-buffer")
	if err == nil {
		t.Fatal("a **kwargs call site was materialized; there is no written message list to read from")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("refusal = %v, want a RewriteError", err)
	}
	// 🔴 The CALL is the reason, and it stays true after every materializer lands. Reporting the platform
	// here would send the reader to wait for something that would not help them.
	if re.Cause != CauseCallSiteShape {
		t.Errorf("cause = %q, want %q: this is a fact about the call, not about our backlog", re.Cause, CauseCallSiteShape)
	}
	if !strings.Contains(err.Error(), "**") {
		t.Errorf("the refusal does not name the unpacking: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "no python module has been generated") {
		t.Errorf("the refusal blames the platform for a call-site fact: %v", err)
	}
}

// TestUnassignedResultRefusesWholeRatherThanHalf — the decision the phase turns on, on the shape that
// admits exactly one half.
//
// 🔴 This is the case pySimpleAssignTarget's unit test cannot see. That test asserts one LINE is
// rejected; this one asserts what the materializer does with a whole site whose recall half would land
// perfectly — the message list is written right there — and whose record half cannot, because no name
// holds the response. The tempting outcome is the wrong one: emit the recall edit, skip the record edit,
// report success. The result reads from a store nothing fills, which behaves exactly like `none` while
// the config_hash claims a strategy, and unlike P17's silent drop it ships a real diff that builds and
// looks reviewed.
//
// It is also not hypothetical: agent/bedrock_adapter.py:1516 in hermes-agent is this shape, and it is
// the only one of that repository's 31 nodes that is NOT refused for **kwargs.
func TestUnassignedResultRefusesWholeRatherThanHalf(t *testing.T) {
	edits, err := materializeAt(t, pyMemoryNoAssignSrc, "scratchpad")
	if err == nil {
		t.Fatalf("a call site whose result is never assigned produced %d edit(s) and no error; half a "+
			"memory is not a weaker memory — recall alone reads from a store nothing fills", len(edits))
	}
	if len(edits) != 0 {
		t.Errorf("the refusal still returned %d edit(s); a refused site must leave the source untouched, "+
			"or the caller can apply half of what it was refused", len(edits))
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("refusal = %v, want a RewriteError", err)
	}
	if re.Cause != CauseCallSiteShape {
		t.Errorf("cause = %q, want %q: the platform has the python materializer, and this site's own "+
			"shape is what stops it", re.Cause, CauseCallSiteShape)
	}
	// The sentence must name the half that is missing and the edit that would fix it. "Refused" alone
	// sends the reader to the wrong half — recall is written and fine.
	msg := err.Error()
	for _, want := range []string{"record", "assign"} {
		if !strings.Contains(strings.ToLower(msg), want) {
			t.Errorf("the refusal does not mention %q, so it does not say which half failed or what to "+
				"change: %v", want, msg)
		}
	}
}

// TestPythonMemoryEditIsMinimalAndReparses — task 3.4 🔴.
func TestPythonMemoryEditIsMinimalAndReparses(t *testing.T) {
	edits, err := materializeAt(t, pyMemorySrc, "entity-memory")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	after := applyTo(t, pyMemorySrc, edits)
	assertPythonParses(t, after)

	before := strings.Split(pyMemorySrc, "\n")
	got := strings.Split(after, "\n")

	// Every ORIGINAL line either survives verbatim or is one this change was allowed to touch.
	var changed []string
	for _, line := range before {
		if line == "" {
			continue
		}
		found := false
		for _, g := range got {
			if g == line {
				found = true
				break
			}
		}
		if !found {
			changed = append(changed, line)
		}
	}
	// TWO lines change, and both are the call site's own: the `messages=` argument, and the closing line
	// the record is appended to. Anything else would be an incidental edit.
	if len(changed) != 2 {
		t.Errorf("the change touched %d line(s), want the 2 belonging to this call site: %q\nafter:\n%s",
			len(changed), changed, after)
	}
	for _, c := range changed {
		if !strings.Contains(c, "messages=") && strings.TrimSpace(c) != ")" {
			t.Errorf("the change touched a line outside the call site: %q", c)
		}
	}

	// The imports, the client construction, and the function signature are untouched.
	for _, must := range []string{"import openai", "client = openai.OpenAI()", "def chat(question):", `model="gpt-4o",`} {
		if !strings.Contains(after, must) {
			t.Errorf("the change removed or altered %q:\n%s", must, after)
		}
	}
}

// TestMemoryNoneEmitsNothingInPython — the identity strategy still changes nothing, after materialization
// landed. If this regressed, `none` would become unusable and a user could not back out of a change.
func TestMemoryNoneEmitsNothingInPython(t *testing.T) {
	edits, err := materializeAt(t, pyMemorySrc, "none")
	if err != nil {
		t.Fatalf("`none` was refused: %v", err)
	}
	if len(edits) != 0 {
		t.Errorf("`none` produced %d edit(s); the identity strategy changes nothing", len(edits))
	}
}

// TestPySimpleAssignTarget pins the record half's admission rule directly, because the shapes it must
// REJECT are the ones a fixture-driven test would never think to write.
func TestPySimpleAssignTarget(t *testing.T) {
	accept := map[string]string{
		"    resp = client.create(x)":      "resp",
		"out = f()":                        "out",
		"    self.last = client.create(x)": "self.last",
		"r = create(model=\"a=b\", m=[1])": "r",
	}
	for line, want := range accept {
		if got, ok := pySimpleAssignTarget(line); !ok || got != want {
			t.Errorf("pySimpleAssignTarget(%q) = (%q, %v), want (%q, true)", line, got, ok, want)
		}
	}

	// 🔴 Each rejection is a shape where the response lands somewhere a generated statement cannot name.
	reject := []string{
		"    return client.create(x)",          // no name holds it
		"a, b = client.create(x)",              // tuple unpacking
		"    results.append(client.create(x))", // an argument to another call
		"total += client.create(x)",            // augmented assignment
		"if client.create(x) == y:",            // a comparison
		"    # resp = client.create(x)",        // a comment
		"[client.create(i) for i in xs]",       // a comprehension
	}
	for _, line := range reject {
		if got, ok := pySimpleAssignTarget(line); ok {
			t.Errorf("pySimpleAssignTarget(%q) accepted and returned %q; the response does not land in a "+
				"name a generated statement could reference", line, got)
		}
	}
}

func fileNames(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func assertPythonParses(t *testing.T, src string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	path := filepath.Join(t.TempDir(), "check.py")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("python3", "-c",
		"import py_compile,sys; py_compile.compile(sys.argv[1], doraise=True)", path).CombinedOutput(); err != nil {
		t.Fatalf("the materialized source does not parse: %v\n%s\n--- source ---\n%s", err, out, src)
	}
}
