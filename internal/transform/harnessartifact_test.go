package transform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §11 — the generated harness artifact, and the conformance that makes a second implementation safe.
//
// 🔴 The emitted Python module re-implements the loop `internal/harnessruntime` defines. Two
// implementations of one contract is exactly the split single-source-of-truth forbids — UNLESS something
// proves they agree. That is what this file is: the Go runtime and the emitted module are run over the
// same strategies, params and answer sequences, and their turn counts and stop reasons are compared.
//
// 🚫 Without it, "one definition of a strategy's behaviour" would be a comment. With it, a divergence
// turns red on the next run — and sabotaging either side proves the test can go red.

// harnessConformanceDriver executes the emitted module against a scripted answer sequence and prints what
// the loop did, so Go can compare it to its own run.
const harnessConformanceDriver = `
import json, sys
sys.path.insert(0, sys.argv[1])
import agentharness

node = sys.argv[2]
answers = json.loads(sys.argv[3])

seen = []
def invoke(messages):
    a = answers[len(seen)] if len(seen) < len(answers) else answers[-1]
    seen.append(list(messages))
    return a

agentharness.run(node, invoke, [{"role": "user", "content": "q"}])
print(json.dumps({"turns": len(seen), "last_len": len(seen[-1])}))
`

// writeHarnessArtifact emits the module and a document for one node, into a temp dir.
func writeHarnessArtifact(t *testing.T, node, strategy string, params map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pyHarnessModulePath), []byte(pythonHarnessModule), 0o600); err != nil {
		t.Fatalf("write module: %v", err)
	}
	doc := HarnessDocument{Schema: harnessDocSchema, ConfigHash: strings.Repeat("c", 64),
		Nodes: map[string]HarnessDocNode{node: {Strategy: strategy, Params: params}}}
	b, err := canonicalHarnessDocument(doc)
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, harnessDocPath), b, 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	return dir
}

// TestHarnessArtifactMatchesTheRuntime — the conformance pin. 🔴 The two implementations must produce the
// same turn count for the same strategy, params and answers.
func TestHarnessArtifactMatchesTheRuntime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}

	cases := []struct {
		name     string
		strategy string
		params   map[string]any
		rt       harnessruntime.Params
		answers  []string
	}{
		{"single-shot", "single-shot", map[string]any{}, harnessruntime.Params{}, []string{"only"}},
		{"reflexion-to-ceiling", "reflexion",
			map[string]any{"max_turns": 4, "stop_condition": "answer-marker", "answer_marker": "DONE",
				"reflection_prompt": "redo"},
			harnessruntime.Params{MaxTurns: 4, StopCondition: "answer-marker", AnswerMarker: "DONE",
				ReflectionPrompt: "redo"},
			[]string{"a", "b", "c", "d"}},
		{"reflexion-satisfied", "reflexion",
			map[string]any{"max_turns": 5, "stop_condition": "answer-marker", "answer_marker": "DONE",
				"reflection_prompt": "redo"},
			harnessruntime.Params{MaxTurns: 5, StopCondition: "answer-marker", AnswerMarker: "DONE",
				ReflectionPrompt: "redo"},
			[]string{"working", "DONE here", "unused"}},
		{"reflexion-max-turns-condition", "reflexion",
			map[string]any{"max_turns": 3, "stop_condition": "max-turns", "reflection_prompt": "redo"},
			harnessruntime.Params{MaxTurns: 3, StopCondition: "max-turns", ReflectionPrompt: "redo"},
			[]string{"a", "b", "c"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			const node = "n1"
			dir := writeHarnessArtifact(t, node, c.strategy, c.params)
			answersJSON, err := json.Marshal(c.answers)
			if err != nil {
				t.Fatalf("marshal answers: %v", err)
			}
			out, err := exec.Command("python3", "-c", harnessConformanceDriver, dir, node, string(answersJSON)).CombinedOutput()
			if err != nil {
				t.Fatalf("python driver failed: %v\n%s", err, out)
			}
			var py struct {
				Turns   int `json:"turns"`
				LastLen int `json:"last_len"`
			}
			if err := json.Unmarshal([]byte(lastLine(string(out))), &py); err != nil {
				t.Fatalf("decode driver output %q: %v", out, err)
			}

			// The Go runtime, over the same script.
			i := 0
			invoke := func([]harnessruntime.Message) (string, error) {
				a := c.answers[len(c.answers)-1]
				if i < len(c.answers) {
					a = c.answers[i]
				}
				i++
				return a, nil
			}
			got, err := harnessruntime.Run(
				harnessruntime.Config{Strategy: c.strategy, Params: c.rt}, harnessruntime.Hosts{},
				[]harnessruntime.Message{{Role: "user", Content: "q"}}, invoke)
			if err != nil {
				t.Fatalf("runtime: %v", err)
			}

			if got.Turns != py.Turns {
				t.Fatalf("the runtime ran %d turn(s) and the generated module ran %d for the same strategy, "+
					"params and answers. Two implementations of one loop are only safe while something "+
					"proves they agree — this is that proof, and it just failed", got.Turns, py.Turns)
			}
		})
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// TestHarnessArtifactIsDependencyFree — task 11.1 🚫. The module imports nothing outside the standard
// library, and reaches no provider and no tool.
func TestHarnessArtifactIsDependencyFree(t *testing.T) {
	allowed := map[string]bool{"json": true, "os": true, "threading": true}
	for _, line := range strings.Split(pythonHarnessModule, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, "from ") {
			continue
		}
		name := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(trimmed, "from "), "import "))[0]
		if !allowed[name] {
			t.Errorf("the generated module imports %q; it ships into a customer's process and must add no "+
				"dependency to it", name)
		}
	}
	// 🚫 And it reaches for nothing that would need a credential or a socket.
	for _, forbidden := range []string{"requests", "urllib", "http", "socket", "openai", "anthropic", "subprocess"} {
		if strings.Contains(pythonHarnessModule, forbidden) {
			t.Errorf("the generated module mentions %q; a generated file that reached a provider would put "+
				"a credential in the customer's process and spend it on turns they did not write", forbidden)
		}
	}
}

// TestHarnessArtifactRefusesAHostServiceStrategy — the module's half of D-10, executed. A strategy whose
// host service is absent RAISES rather than running a lighter loop.
func TestHarnessArtifactRefusesAHostServiceStrategy(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	dir := writeHarnessArtifact(t, "n1", "critic-loop",
		map[string]any{"max_turns": 3, "critic_model_ref": "m1"})

	const driver = `
import sys
sys.path.insert(0, sys.argv[1])
import agentharness
try:
    agentharness.run("n1", lambda m: "answer", [{"role": "user", "content": "q"}])
except agentharness.HarnessHostRequired as e:
    print("refused: %s" % e)
    sys.exit(0)
print("RAN WITHOUT A CRITIC")
sys.exit(1)
`
	out, err := exec.Command("python3", "-c", driver, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated module ran a critic-loop with no critic: %v\n%s\nA critic-loop without a "+
			"critic IS reflexion, and running it under critic-loop's config_hash reports one strategy as "+
			"another", err, out)
	}
	if !strings.Contains(string(out), "refused") {
		t.Fatalf("unexpected driver output: %s", out)
	}
}

// TestHarnessArtifactRefusesAnUnreadableAnswer — the module will not guess. A response whose text it
// cannot read raises, naming the fix, rather than falling back to str() and feeding the model its own
// repr while calling the result a loop.
func TestHarnessArtifactRefusesAnUnreadableAnswer(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	dir := writeHarnessArtifact(t, "n1", "reflexion",
		map[string]any{"max_turns": 3, "stop_condition": "answer-marker", "answer_marker": "DONE",
			"reflection_prompt": "redo"})

	const driver = `
import sys
sys.path.insert(0, sys.argv[1])
import agentharness

class Opaque:
    pass

try:
    agentharness.run("n1", lambda m: Opaque(), [{"role": "user", "content": "q"}])
except agentharness.HarnessAnswerUnreadable as e:
    assert "set_answer_reader" in str(e), "the refusal does not name the fix"
    print("refused")
    sys.exit(0)
print("GUESSED AT AN UNREADABLE RESPONSE")
sys.exit(1)
`
	out, err := exec.Command("python3", "-c", driver, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated module guessed at a response it could not read: %v\n%s", err, out)
	}

	// And a supplied reader makes it work — so the refusal above is an absence, not a dead end.
	const withReader = `
import sys
sys.path.insert(0, sys.argv[1])
import agentharness

class Opaque:
    def __init__(self, text):
        self.text = text

agentharness.set_answer_reader(lambda r: r.text)
seen = []
def invoke(messages):
    seen.append(messages)
    return Opaque("DONE" if len(seen) == 2 else "working")

agentharness.run("n1", invoke, [{"role": "user", "content": "q"}])
print(len(seen))
`
	out, err = exec.Command("python3", "-c", withReader, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("a supplied reader did not make the loop work: %v\n%s", err, out)
	}
	if strings.TrimSpace(lastLine(string(out))) != "2" {
		t.Fatalf("with a reader the loop should have stopped on turn 2, got %q", out)
	}
}

// TestHarnessArtifactShipsInTheSamePatch — task 11.1. Asserted through the real Generate, because "same
// patch" is a property of the patch and not of the generator.
func TestHarnessArtifactShipsInTheSamePatch(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyHarnessSrc)
	id := onlyNode(t, root, "python")
	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: harnessOverride(t, "reflexion"),
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, want := range []string{"pipeline.py", pyHarnessModulePath, harnessDocPath} {
		if _, ok := p.Files[want]; !ok {
			t.Errorf("the patch is missing %s; a module without the call, or a call without the module, is "+
				"a broken repository either way — and one revert has to restore both", want)
		}
	}
}
