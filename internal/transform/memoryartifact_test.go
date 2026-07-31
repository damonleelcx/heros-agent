package transform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/memoryruntime"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §2 — the generated artifact.
//
// The load-bearing test in this file is the CONFORMANCE one at the bottom. D5 says a strategy has one
// definition, but the module that ships into a customer's repository is Python and the runtime that
// scores it is Go — so there are unavoidably two implementations, and the decision is only honest if
// something proves they agree. That test executes both over the same inputs and compares.

func memResolved(t *testing.T, nodeID, strategy, params string) *variantspec.Resolved {
	t.Helper()
	st := registry.MemoryStrategyNamed(strategy)
	if st == nil {
		t.Fatalf("%q is not a builtin strategy", strategy)
	}
	return &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "python",
		Overrides: map[string]variantspec.ResolvedOverride{
			nodeID: {Memory: &registry.MemoryEntry{
				VersionID: strings.Repeat("e", 64), Name: "m",
				Spec:     registry.MemorySpec{Strategy: strategy, Params: json.RawMessage(params)},
				Strategy: st,
			}},
		},
	}
}

func TestArtifactRegeneratesByteIdentically(t *testing.T) {
	r := memResolved(t, "n1", "scratchpad", `{"max_entries":5}`)

	first, err := GenerateMemoryArtifacts(r, "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := GenerateMemoryArtifacts(r, "python")
		if err != nil {
			t.Fatalf("generate %d: %v", i, err)
		}
		if len(again) != len(first) {
			t.Fatalf("run %d emitted %d file(s), want %d", i, len(again), len(first))
		}
		for path, want := range first {
			if string(again[path]) != string(want) {
				t.Fatalf("run %d emitted different bytes for %s; a re-apply would produce a spurious diff", i, path)
			}
		}
	}

	// 🔴 A parameter change moves the DOCUMENT and leaves the MODULE untouched (FR12). If the module
	// carried params, retuning max_entries would be a code change — a new diff, a new review, a new
	// deploy — for something that is not structure.
	other, err := GenerateMemoryArtifacts(memResolved(t, "n1", "scratchpad", `{"max_entries":9}`), "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if string(other[pyMemoryModulePath]) != string(first[pyMemoryModulePath]) {
		t.Error("changing a parameter changed the generated MODULE; params must be data, not code")
	}
	if string(other[memoryDocPath]) == string(first[memoryDocPath]) {
		t.Error("changing a parameter did not change the document; the change would not reach the runtime")
	}
}

func TestArtifactReadsParamsAsData(t *testing.T) {
	got, err := GenerateMemoryArtifacts(memResolved(t, "n1", "summary-buffer", `{"max_tokens":2000,"keep_last_turns":4}`), "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var doc MemoryDocument
	if err := json.Unmarshal(got[memoryDocPath], &doc); err != nil {
		t.Fatalf("the emitted document is not valid JSON: %v", err)
	}
	if doc.Schema != memoryDocSchema {
		t.Errorf("schema = %q, want %q", doc.Schema, memoryDocSchema)
	}
	node, ok := doc.Nodes["n1"]
	if !ok {
		t.Fatal("the document carries no entry for the node")
	}
	if node.Strategy != "summary-buffer" {
		t.Errorf("strategy = %q, want summary-buffer", node.Strategy)
	}
	if node.Params["max_tokens"] != float64(2000) {
		t.Errorf("params = %v, want max_tokens 2000", node.Params)
	}

	// 🚫 And the params are NOT in the module.
	if strings.Contains(string(got[pyMemoryModulePath]), "2000") {
		t.Error("the generated module embeds a parameter value; retuning it would be a code change")
	}
}

func TestArtifactIsDependencyFree(t *testing.T) {
	got, err := GenerateMemoryArtifacts(memResolved(t, "n1", "scratchpad", `{"max_entries":5}`), "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	src := string(got[pyMemoryModulePath])

	stdlib := map[string]bool{"json": true, "os": true, "threading": true}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "import ") && !strings.HasPrefix(line, "from ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mod := strings.Split(fields[1], ".")[0]
		if !stdlib[mod] {
			t.Errorf("the generated module imports %q, which is outside the standard library. It ships "+
				"into the customer's repository; a third-party import there is a dependency they did not "+
				"choose and may not have.", mod)
		}
	}

	// 🚫 No network, no provider, no credential — asserted over the EMITTED BYTES rather than by review.
	for _, banned := range []string{"urllib", "requests", "http", "socket", "openai", "anthropic", "api_key", "API_KEY"} {
		if strings.Contains(src, banned) {
			t.Errorf("the generated module mentions %q; it must make no provider call and read no "+
				"credential (D3)", banned)
		}
	}

	// It must be syntactically valid Python, or none of the above matters.
	dir := t.TempDir()
	path := filepath.Join(dir, "agentmem.py")
	if err := os.WriteFile(path, got[pyMemoryModulePath], 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("python3", "-c", "import py_compile,sys; py_compile.compile(sys.argv[1], doraise=True)", path).CombinedOutput(); err != nil {
		t.Fatalf("the generated module does not compile as Python: %v\n%s", err, out)
	}
}

func TestArtifactAbsentWithoutMemory(t *testing.T) {
	// A spec that binds no memory adds nothing to the customer's tree.
	empty := &variantspec.Resolved{ConfigHash: "h", Language: "python", Overrides: map[string]variantspec.ResolvedOverride{}}
	got, err := GenerateMemoryArtifacts(empty, "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a spec with no memory emitted %d file(s)", len(got))
	}

	// And `none` emits nothing either — the identity strategy needs no module, and shipping one that
	// nothing calls would be dead code in a customer's repository.
	noneR := memResolved(t, "n1", registry.StrategyNone, `{}`)
	got, err = GenerateMemoryArtifacts(noneR, "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("`none` emitted %d file(s); the identity strategy needs no artifact", len(got))
	}
}

// TestPythonArtifactConformsToGoRuntime — 🔴 the load-bearing test of §2 (D5).
//
// The generated Python and internal/memoryruntime are two implementations of one definition. That is
// only safe if something proves they agree, and prose cannot: the copy that drifts is always the
// generated one, because it is the one nobody reads after it is written.
//
// So this executes BOTH over the same recorded conversation and compares the recalled messages
// byte-for-byte, for every strategy that needs no host service. The host-needing strategies are covered
// by the refusal assertion below — a refusal is also a behaviour, and it must match too.
func TestPythonArtifactConformsToGoRuntime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}

	// 🚫 `none` is deliberately absent from this table, and its absence is the correct behaviour rather
	// than a coverage gap: the identity strategy emits NO artifact (TestArtifactAbsentWithoutMemory), so
	// there is no second implementation to conform. Adding it here would have forced a module into the
	// customer's tree that nothing calls, just to satisfy a test.
	cases := []struct {
		strategy string
		params   string
		rt       memoryruntime.Params
	}{
		{"scratchpad", `{"max_entries":3}`, memoryruntime.Params{MaxEntries: 3}},
		{"scratchpad", `{"max_entries":1}`, memoryruntime.Params{MaxEntries: 1}},
		{"entity-memory", `{"entity_keys":["user_name","project"]}`,
			memoryruntime.Params{EntityKeys: []string{"user_name", "project"}}},
	}

	turns := [][]memoryruntime.Message{
		{{Role: "user", Content: "user_name: ada"}},
		{{Role: "assistant", Content: "noted"}},
		{{Role: "user", Content: "project: apollo"}},
		{{Role: "assistant", Content: "understood"}},
		{{Role: "user", Content: "user_name: grace"}},
	}
	incoming := []memoryruntime.Message{{Role: "user", Content: "who am I?"}}

	for _, c := range cases {
		t.Run(c.strategy+c.params, func(t *testing.T) {
			// ── the Go runtime ──
			store := memoryruntime.NewMemStore()
			k := memoryruntime.Key{NodeID: "n1", SessionID: "s1"}
			for _, turn := range turns {
				if err := memoryruntime.Record(c.strategy, c.rt, store, k, turn); err != nil {
					t.Fatalf("go record: %v", err)
				}
			}
			goOut, err := memoryruntime.Recall(c.strategy, c.rt, store, k, incoming, memoryruntime.Host{})
			if err != nil {
				t.Fatalf("go recall: %v", err)
			}
			goJSON, _ := json.Marshal(goOut)

			// ── the generated Python ──
			pyJSON := runPythonMemory(t, c.strategy, c.params, turns, incoming)

			if string(goJSON) != pyJSON {
				t.Fatalf("the generated Python and the Go runtime disagree about %s%s.\n  go: %s\n  py: %s\n"+
					"D5 says a strategy has ONE definition. Two implementations that disagree produce a diff "+
					"which behaves differently from the strategy the config_hash names, SCORED AS THAT "+
					"STRATEGY.", c.strategy, c.params, goJSON, pyJSON)
			}
		})
	}

	// 🔴 The refusals must match too. A Python module that silently truncated where Go refuses would be
	// the substitution D3 forbids, reached through the generated path.
	t.Run("host-needing strategies refuse in both", func(t *testing.T) {
		for _, c := range []struct{ strategy, params, want string }{
			{"summary-buffer", `{"max_tokens":50,"keep_last_turns":1}`, "MemoryHostRequired"},
			{"vector-recall", `{"top_k":2,"embedding_ref":"e1"}`, "MemoryHostRequired"},
		} {
			out := runPythonMemoryExpectingError(t, c.strategy, c.params, turns, incoming)
			if !strings.Contains(out, c.want) {
				t.Errorf("%s: the generated module did not refuse without a host (got %q); a silent "+
					"fallback here is scratchpad running under another strategy's name", c.strategy, out)
			}
		}
	})
}

// pythonHarness drives the generated module the way a rewritten call site would.
const pythonHarness = `
import json, sys
sys.path.insert(0, sys.argv[1])
import agentmem
agentmem.set_session("s1")
turns = json.loads(sys.argv[2])
incoming = json.loads(sys.argv[3])
for turn in turns:
    agentmem.record("n1", turn)
print(json.dumps(agentmem.recall("n1", incoming), separators=(",", ":")))
`

func writePythonArtifact(t *testing.T, strategy, params string) string {
	t.Helper()
	files, err := GenerateMemoryArtifacts(memResolved(t, "n1", strategy, params), "python")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runPythonMemory(t *testing.T, strategy, params string, turns [][]memoryruntime.Message, incoming []memoryruntime.Message) string {
	t.Helper()
	dir := writePythonArtifact(t, strategy, params)
	turnsJSON, _ := json.Marshal(turns)
	incomingJSON, _ := json.Marshal(incoming)
	out, err := exec.Command("python3", "-c", pythonHarness, dir, string(turnsJSON), string(incomingJSON)).CombinedOutput()
	if err != nil {
		t.Fatalf("python harness failed: %v\n%s", err, out)
	}
	// The Go side marshals with default separators; normalize by round-tripping the Python output.
	var probe []memoryruntime.Message
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &probe); err != nil {
		t.Fatalf("python output is not a message list: %v\n%s", err, out)
	}
	norm, _ := json.Marshal(probe)
	return string(norm)
}

func runPythonMemoryExpectingError(t *testing.T, strategy, params string, turns [][]memoryruntime.Message, incoming []memoryruntime.Message) string {
	t.Helper()
	dir := writePythonArtifact(t, strategy, params)
	turnsJSON, _ := json.Marshal(turns)
	incomingJSON, _ := json.Marshal(incoming)
	out, _ := exec.Command("python3", "-c", pythonHarness, dir, string(turnsJSON), string(incomingJSON)).CombinedOutput()
	return string(out)
}

// TestGeneratedModuleRefusesWithoutASession — the precondition D1 forces, asserted rather than assumed.
//
// A defaulted session merges conversations that must stay separate, so the module raises. That is a real
// consequence for a customer, which is why the materializer states it before apply — but the RUNTIME
// behaviour is what makes the statement true, and it is checked here.
func TestGeneratedModuleRefusesWithoutASession(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	dir := writePythonArtifact(t, "scratchpad", `{"max_entries":3}`)
	const harness = `
import sys
sys.path.insert(0, sys.argv[1])
import agentmem
try:
    agentmem.recall("n1", [{"role":"user","content":"hi"}])
    print("NO REFUSAL")
except agentmem.MemorySessionRequired as e:
    print("REFUSED:", e)
`
	out, err := exec.Command("python3", "-c", harness, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "REFUSED:") {
		t.Fatalf("the generated module did not refuse without a session: %s\nA defaulted session merges "+
			"conversations that must stay separate — a cross-user leak in a server process, invisible from "+
			"inside it", out)
	}
	if !strings.Contains(string(out), "HEROS_MEMORY_SESSION") {
		t.Errorf("the refusal does not name how to supply a session: %s", out)
	}
}
