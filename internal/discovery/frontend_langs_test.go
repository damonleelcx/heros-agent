package discovery

import (
	"path/filepath"
	"testing"
)

// runLang runs discovery over a temp repo of {relpath: content} and returns the result.
func runLang(t *testing.T, files map[string]string) Result {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, rel), content)
	}
	res, err := Run(Options{Repo: root, RepoURL: "local://l", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func providersOf(res Result) map[string]IRNode {
	m := map[string]IRNode{}
	for _, n := range res.IR.Nodes {
		m[n.CallSite.Symbol] = n
	}
	return m
}

// 10.7 — TypeScript: detects Anthropic/OpenAI/Vercel calls, resolves object-literal model strings, flags
// loops, and honestly leaves a non-literal model (Vercel's openai('gpt-4o')) unresolved.
func TestTypeScriptFrontend(t *testing.T) {
	res := runLang(t, map[string]string{
		"src/a.ts": `import Anthropic from "@anthropic-ai/sdk";
import OpenAI from "openai";
import { generateText } from "ai";
const anthropic = new Anthropic();
const openai = new OpenAI();
async function classify(text: string) {
  return anthropic.messages.create({ model: "claude-sonnet-4-5", messages: [] });
}
async function loopAgent(items: string[]) {
  for (const it of items) { await openai.chat.completions.create({ model: "gpt-4o", messages: [] }); }
}
async function vercel() { return generateText({ model: openai("gpt-4o"), prompt: "summarize" }); }
`,
	})
	if res.IR.Workflow.Language != "typescript" {
		t.Fatalf("language: %q", res.IR.Workflow.Language)
	}
	if len(res.IR.Nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(res.IR.Nodes))
	}
	by := providersOf(res)
	if by["classify"].Model.ModelID != "claude-sonnet-4-5" || by["classify"].Model.Provider != "anthropic" {
		t.Fatalf("classify: %+v", by["classify"].Model)
	}
	if by["loopAgent"].InvocationSemantics.Type != "loop" || !by["loopAgent"].InvocationSemantics.VariableAtRuntime {
		t.Fatalf("loopAgent must be loop/variable, got %+v", by["loopAgent"].InvocationSemantics)
	}
	if by["vercel"].Prompt.Inline != "summarize" {
		t.Fatalf("vercel prompt should resolve to 'summarize', got %q", by["vercel"].Prompt.Inline)
	}
	if by["vercel"].Model.ModelID != UnresolvedSentinel {
		t.Fatalf("vercel model (a call, not a literal) must be unresolved, got %q", by["vercel"].Model.ModelID)
	}
}

// 10.9 — Rust: detects an async_openai method call and flags the in-loop call variable_at_runtime.
func TestRustFrontend(t *testing.T) {
	res := runLang(t, map[string]string{
		"src/main.rs": `use async_openai::Client;
async fn ask(client: &Client) {
    let _ = client.chat().create(req).await;
    for _ in 0..3 { client.chat().create(req2).await.unwrap(); }
}
`,
	})
	if res.IR.Workflow.Language != "rust" {
		t.Fatalf("language: %q", res.IR.Workflow.Language)
	}
	if len(res.IR.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(res.IR.Nodes))
	}
	var loops int
	for _, n := range res.IR.Nodes {
		if n.Model.Provider != "openai" {
			t.Fatalf("rust node provider: %q", n.Model.Provider)
		}
		if n.InvocationSemantics.Type == "loop" {
			loops++
		}
	}
	if loops != 1 {
		t.Fatalf("want exactly 1 loop node, got %d", loops)
	}
}

// 10.8 — Java: detects a langchain4j model call; the model is unresolved at the floor (builder-bound).
func TestJavaFrontend(t *testing.T) {
	res := runLang(t, map[string]string{
		"Svc.java": `import dev.langchain4j.model.openai.OpenAiChatModel;
class Svc {
  void run(OpenAiChatModel model) { String r = model.generate("summarize this"); }
}
`,
	})
	if res.IR.Workflow.Language != "java" {
		t.Fatalf("language: %q", res.IR.Workflow.Language)
	}
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(res.IR.Nodes))
	}
	if res.IR.Nodes[0].Model.ModelID != UnresolvedSentinel {
		t.Fatalf("java builder-bound model must be unresolved, got %q", res.IR.Nodes[0].Model.ModelID)
	}
}

// 10.6 — the Python LangGraph framework reader derives the declarative graph (nodes + data/control
// edges) and tags the framework source.
func TestPythonLangGraphFramework(t *testing.T) {
	res := runLang(t, map[string]string{
		"graph.py": `from langgraph.graph import StateGraph
def build():
    g = StateGraph(dict)
    g.add_node("classify", classify_fn)
    g.add_node("answer", answer_fn)
    g.add_edge("classify", "route")
    g.add_conditional_edges("route", pick, {"faq": "answer", "esc": "escalate"})
    g.set_entry_point("classify")
`,
	})
	if len(res.Report.FrameworkSubgraphs) != 1 {
		t.Fatalf("want 1 framework subgraph, got %d", len(res.Report.FrameworkSubgraphs))
	}
	sg := res.Report.FrameworkSubgraphs[0]
	if sg.FrameworkSource != "langgraph" {
		t.Fatalf("framework source: %q", sg.FrameworkSource)
	}
	want := map[string]bool{"classify": true, "answer": true, "route": true, "escalate": true}
	for _, n := range sg.Nodes {
		delete(want, n)
	}
	if len(want) != 0 {
		t.Fatalf("missing framework nodes %v (got %v)", want, sg.Nodes)
	}
	var data, control int
	for _, e := range sg.Edges {
		switch e.Kind {
		case "data":
			data++
		case "control":
			control++
		}
	}
	if data != 1 || control != 2 {
		t.Fatalf("want 1 data + 2 control edges, got %d/%d: %+v", data, control, sg.Edges)
	}
}

// 10.11 — the CrewAI reader recognizes Agent(role=...) nodes and the crew, behind the same reader
// interface as LangGraph.
func TestPythonCrewAIFramework(t *testing.T) {
	res := runLang(t, map[string]string{
		"crew.py": `from crewai import Agent, Task, Crew
researcher = Agent(role="researcher", goal="find facts")
writer = Agent(role="writer", goal="write it up")
crew = Crew(agents=[researcher, writer], tasks=[])
def go(): crew.kickoff()
`,
	})
	var crew *FrameworkGraph
	for i := range res.Report.FrameworkSubgraphs {
		if res.Report.FrameworkSubgraphs[i].FrameworkSource == "crewai" {
			crew = &res.Report.FrameworkSubgraphs[i]
		}
	}
	if crew == nil {
		t.Fatalf("want a crewai subgraph, got %+v", res.Report.FrameworkSubgraphs)
	}
	want := map[string]bool{"researcher": true, "writer": true}
	for _, n := range crew.Nodes {
		delete(want, n)
	}
	if len(want) != 0 {
		t.Fatalf("crewai nodes should be agent roles, missing %v (got %v)", want, crew.Nodes)
	}
}

// A mixed-language repo (Go + Python + TypeScript) yields ONE IR spanning frontends (10.3 / §10.12).
func TestMixedLanguageRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/mix\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), `package main
import "github.com/anthropics/anthropic-sdk-go"
func main() { var c *anthropic.Client; c.Messages.New(nil, anthropic.MessageNewParams{}) }
`)
	mustWrite(t, filepath.Join(root, "svc.py"), `import anthropic
client = anthropic.Anthropic()
def f(): client.messages.create(model="claude", messages=[])
`)
	mustWrite(t, filepath.Join(root, "web.ts"), `import OpenAI from "openai";
const openai = new OpenAI();
async function g() { return openai.chat.completions.create({ model: "gpt-4o", messages: [] }); }
`)
	res, err := Run(Options{Repo: root, RepoURL: "local://mix", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.IR.Nodes) != 3 {
		t.Fatalf("mixed repo: want 3 nodes (go+py+ts), got %d", len(res.IR.Nodes))
	}
	if res.IR.Workflow.Language != "mixed" {
		t.Fatalf("mixed repo: want workflow.language=mixed, got %q", res.IR.Workflow.Language)
	}
	files := map[string]bool{}
	for _, n := range res.IR.Nodes {
		files[filepath.Ext(n.CallSite.File)] = true
	}
	for _, ext := range []string{".go", ".py", ".ts"} {
		if !files[ext] {
			t.Fatalf("mixed repo missing a %s node (got files %v)", ext, files)
		}
	}
}
