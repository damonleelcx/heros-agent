package discovery

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// TestTheLiteralGateChangesNothing is the fence that makes the optimisation safe to keep.
//
// # 🔴 Why a comparison rather than unit tests on the hints
//
// The gate is a set of literal strings claiming "if none of these appear, this pattern cannot match".
// A hint that is too narrow does not fail loudly — it silently drops findings, and this package reports
// absence as a FINDING, so a dropped signal becomes a confident statement that the customer's agent has
// no memory strategy. Nothing about that looks wrong.
//
// So the hints are not trusted. Every pattern is run over a real corpus with the gate on and with it
// off, and the evidence must be byte-identical. A hint that is wrong turns this red instead of quietly
// shrinking a report.
func TestTheLiteralGateChangesNothing(t *testing.T) {
	root := repo(t, map[string]string{
		"agent/bot.py": `import openai
from langchain.memory import ConversationBufferMemory

SYSTEM_PROMPT = "You are a support agent."
memory = ConversationBufferMemory(session_id="abc")
TOOLS = [{"name": "lookup"}]

@tool
def lookup(order_id: str) -> str:
    return db.get(order_id)

def build_messages(history):
    messages = [{"role": "system", "content": SYSTEM_PROMPT}]
    messages += history[-6:]
    return messages

def answer(history, max_turns=5):
    while True:
        try:
            resp = openai.chat.completions.create(
                model="gpt-4o", temperature=0.7, max_tokens=800,
                messages=build_messages(history), tools=TOOLS, timeout=30)
        except Exception:
            continue
        if "DONE" in resp: break
    return resp
`,
		"agent/graph.py": `from langgraph.graph import StateGraph

g = StateGraph(dict)
g.add_node("triage", triage)
g.add_edge("triage", "escalate")
supervisor = g.compile(checkpointer=saver)
`,
		"web/client.ts": `export const client = new ChatOpenAI({
  model: "gpt-4o-mini",
  temperature: 0,
  maxRetries: 3,
});
`,
	})
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	compareGated(t, c)
}

// TestTheLiteralGateChangesNothingOnARealRepository runs the same comparison over a large real tree,
// which is where a hint that is subtly too narrow would actually show up.
func TestTheLiteralGateChangesNothingOnARealRepository(t *testing.T) {
	root := os.Getenv("HEROS_TEST_REPO")
	if root == "" {
		t.Skip("HEROS_TEST_REPO unset; the large-corpus comparison did not run")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("HEROS_TEST_REPO=%s is not readable", root)
	}
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	t.Logf("comparing over %d files", len(c.Files))
	compareGated(t, c)
}

func compareGated(t *testing.T, c Corpus) {
	t.Helper()

	gateEnabled = false
	t0 := time.Now()
	ungated := NewIndex(c)
	slow := time.Since(t0)

	gateEnabled = true
	t1 := time.Now()
	gated := NewIndex(c)
	fast := time.Since(t1)

	t.Logf("ungated %s   gated %s   (%.1fx)", slow.Round(time.Millisecond),
		fast.Round(time.Millisecond), float64(slow)/float64(max64(int64(fast), 1)))

	for axis := range axisPatterns {
		a, b := ungated.ForAxis(axis), gated.ForAxis(axis)
		if a.Found != b.Found {
			t.Errorf("%s: found=%v without the gate, %v with it — the gate is DROPPING evidence, and a "+
				"dropped signal becomes a confident claim that the agent has no %s handling",
				axis, a.Found, b.Found, axis)
			continue
		}
		if !reflect.DeepEqual(a.Spans, b.Spans) {
			t.Errorf("%s: %d spans without the gate, %d with it", axis, len(a.Spans), len(b.Spans))
			for i := range a.Spans {
				if i >= len(b.Spans) || a.Spans[i] != b.Spans[i] {
					t.Errorf("  first divergence at %d: %+v", i, a.Spans[i])
					break
				}
			}
		}
		if a.Note != b.Note {
			t.Errorf("%s: note differs\n  without: %s\n  with:    %s", axis, a.Note, b.Note)
		}
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// TestEveryPatternDeclaresHints. A pattern with none would be dropped entirely by the gate; the init
// panics, and this states the requirement where somebody adding a pattern will read it.
func TestEveryPatternDeclaresHints(t *testing.T) {
	for axis, pats := range axisPatterns {
		for _, p := range pats {
			if len(p.hints) == 0 {
				t.Errorf("%s: pattern %q declares no hints", axis, p.why)
			}
		}
	}
}

// TestTheCallSiteGateChangesNothing.
//
// 🔴 A separate fence from the axis one because the failure is different in KIND. A too-narrow axis hint
// shrinks one report; a too-narrow call-site hint shrinks the node count, which feeds
// LooksLikeAnAgent — so it could turn "987 call sites across 219 files" into "this is not an agent",
// and the system would decline to assess a repository it can perfectly well read.
func TestTheCallSiteGateChangesNothing(t *testing.T) {
	root := os.Getenv("HEROS_TEST_REPO")
	if root == "" {
		root = repo(t, map[string]string{
			"a/one.py":   "import openai\nr = openai.chat.completions.create(model='gpt-4o')\n",
			"a/two.ts":   "const c = new ChatOpenAI({model:'gpt-4o'});\nawait c.invoke(msgs);\n",
			"a/three.py": "from anthropic import Anthropic\nresp = client.messages.create(model='claude-3')\n",
			"a/four.py":  "out = chain.run(q)\nres = llm.complete(p)\nx = litellm.completion(**kw)\n",
			"a/none.go":  "package a\n\nfunc Add(x, y int) int { return x + y }\n",
		})
	}
	c, err := Walk(root, Limits{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	gateEnabled = false
	ungated := Nodes(c)
	gateEnabled = true
	gated := Nodes(c)

	if len(ungated) != len(gated) {
		t.Fatalf("%d call sites without the gate, %d with it — the gate is dropping call sites, which "+
			"feeds LooksLikeAnAgent and could make a real agent look like a plain service",
			len(ungated), len(gated))
	}
	for i := range ungated {
		if ungated[i] != gated[i] {
			t.Fatalf("call site %d differs:\n  without: %+v\n  with:    %+v", i, ungated[i], gated[i])
		}
	}
	t.Logf("%d call sites, identical with and without the gate", len(gated))
}
