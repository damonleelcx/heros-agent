package discovery

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// P14 §4 — the tools≠skills IR split (decisions.md D-14.1).
//
// Two properties, and the second is the one that could quietly break everything already stored: the
// split must CARRY the classification, and an IR that does not use it must serialise to exactly the
// bytes it did before the fields existed.

// splitFixture writes a one-file Go repo whose call site declares a static tool list plus a platform
// skill reference, and returns the discovered node.
func discoverOne(t *testing.T, src string) IRNode {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/target\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), src)

	res, err := Run(Options{Repo: root, RepoURL: "local://p14", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want exactly one discovered node, got %d", len(res.IR.Nodes))
	}
	return res.IR.Nodes[0]
}

const staticToolsSrc = `package main

import "github.com/anthropics/anthropic-sdk-go"

func run(client *anthropic.Client) {
	client.Messages.New(nil, anthropic.MessageNewParams{
		Model: "claude-sonnet-5",
		Tools: []anthropic.ToolUnionParam{weatherTool, heros.Skill("search_kb")},
	})
}
`

const dynamicToolsSrc = `package main

import "github.com/anthropics/anthropic-sdk-go"

func run(client *anthropic.Client, buildTools func() []anthropic.ToolUnionParam) {
	client.Messages.New(nil, anthropic.MessageNewParams{
		Model: "claude-sonnet-5",
		Tools: buildTools(),
	})
}
`

const noToolsSrc = `package main

import "github.com/anthropics/anthropic-sdk-go"

func run(client *anthropic.Client) {
	client.Messages.New(nil, anthropic.MessageNewParams{Model: "claude-sonnet-5"})
}
`

// ── 4.2 the frontend classifies and populates the split at extraction ────────────────────────────

func TestFrontendPopulatesToolSkillSplit(t *testing.T) {
	n := discoverOne(t, staticToolsSrc)

	if len(n.Tools) != 1 {
		t.Fatalf("want one provider-native tool, got %d: %+v", len(n.Tools), n.Tools)
	}
	if n.Tools[0].Name != "weatherTool" {
		t.Errorf("the tool is identified by what the CALL SITE wrote, got %q", n.Tools[0].Name)
	}
	if len(n.Skills) != 1 || n.Skills[0] != "search_kb" {
		t.Errorf("want the platform skill split out by name, got %v", n.Skills)
	}

	// 🔴 The frozen conflated slice is RETAINED and unchanged — it still carries both entries, exactly
	// as a pre-P14 consumer reads it. Repurposing it to mean only tools would break every such consumer.
	if len(n.ToolsSkills) != 2 {
		t.Errorf("the frozen tools_skills slice must keep BOTH entries, got %v", n.ToolsSkills)
	}
	if !strings.Contains(strings.Join(n.ToolsSkills, " "), "weatherTool") {
		t.Errorf("tools_skills lost the tool: %v", n.ToolsSkills)
	}

	// The classification is recorded, not left for a consumer to infer.
	if !n.ToolsRecorded() {
		t.Error("a node that declares tools must record the split")
	}
	if !n.DeclaresTool("weatherTool") {
		t.Error("DeclaresTool must find a recorded tool — this is the fail-closed check a selection is validated against")
	}
	if n.DeclaresTool("search_kb") {
		t.Error("a platform SKILL must not answer the discovered-TOOL check; that is the conflation the split exists to end")
	}
}

// ── 4.3 the locator, and the load-bearing nil ────────────────────────────────────────────────────

func TestStaticToolRecordsItsLocator(t *testing.T) {
	n := discoverOne(t, staticToolsSrc)
	tool := n.Tools[0]
	if !tool.Locatable() {
		t.Fatal("a tool written out in a static list must record where it is, or a prune has nothing to delete")
	}
	if tool.DeclaredAt.Line <= 0 {
		t.Errorf("the locator must carry a real line, got %d", tool.DeclaredAt.Line)
	}
	if tool.DeclaredAt.Index != 0 {
		t.Errorf("the locator must carry the element's position in the written list, got %d", tool.DeclaredAt.Index)
	}
}

// 🔴 A dynamically-assembled tool set is RECORDED as unlocatable, not dropped.
//
// "this node offers tools we cannot address" and "this node offers no tools" are different facts, and
// only the first one must make a prune refuse (FR14). Dropping the entry would leave a prune reporting
// "no such tool" about a set that is plainly right there in the source.
func TestDynamicToolSetRecordedAsUnlocatable(t *testing.T) {
	n := discoverOne(t, dynamicToolsSrc)

	if len(n.Tools) != 1 {
		t.Fatalf("a runtime-assembled tool set must be recorded as one unlocatable entry, got %+v", n.Tools)
	}
	if n.Tools[0].Locatable() {
		t.Fatal("a runtime-assembled tool set must NOT be locatable; a prune over it has no declaration to delete")
	}
	if !strings.Contains(n.Tools[0].Name, "buildTools") {
		t.Errorf("the recorded entry must name the expression that assembles the set, got %q", n.Tools[0].Name)
	}
	if !n.ToolsRecorded() {
		t.Error("the node records a tool set; ToolsRecorded must say so")
	}
}

// ── 4.1 / 4.4 additivity: absent when empty, byte-identical when unused ──────────────────────────

func TestSplitFieldsOmittedPreP14ByteIdentical(t *testing.T) {
	n := discoverOne(t, noToolsSrc)
	if n.Tools != nil || n.Skills != nil {
		t.Fatalf("a node that declares nothing must carry NIL split fields, got tools=%v skills=%v", n.Tools, n.Skills)
	}

	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{`"tools"`, `"skills"`, `"declared_at"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("a node that uses no split field emitted %s; a pre-P14 IR must serialise byte-identically:\n%s",
				forbidden, raw)
		}
	}
	// The frozen field is still there and still normalised to `[]` — its emptiness is part of the frozen
	// bytes, unlike the new fields whose ABSENCE is what must stay compatible.
	if !strings.Contains(string(raw), `"tools_skills":[]`) {
		t.Errorf("the frozen tools_skills must still emit as []: %s", raw)
	}
}

// A pre-P14 IR document — one written before these fields existed — round-trips through the P14 types
// byte-identically. This is the compatibility direction the golden vectors cannot cover, because they
// were never re-serialised through this struct.
func TestPreP14IRRoundTripsByteIdentical(t *testing.T) {
	// Written by hand as a pre-P14 emitter would have: no tools/skills keys anywhere.
	const preP14 = `{"ir_version":"1.0.0","workflow":{"id":"wf","repo":{"url":"local://x","commit_sha":"abc"},"language":"go"},` +
		`"nodes":[{"node_id":"n1","kind":"static_definition","call_site":{"file":"main.go","symbol":"run","line_start":1,"line_end":2},` +
		`"model":{"provider":"anthropic","model_id":"claude-sonnet-5","params":{}},"prompt":{"inline":"hi","variables":[]},` +
		`"tools_skills":["weatherTool"],"context_assembly":{"policy":"inline_messages","description":"d"},` +
		`"io_contract":{"input_schema":{"type":"object"},"output_schema":{"type":"object"}},` +
		`"invocation_semantics":{"type":"single","variable_at_runtime":false}}],"edges":[]}`

	var ir IR
	dec := json.NewDecoder(strings.NewReader(preP14))
	dec.DisallowUnknownFields() // a field with no home here would be silently dropped; make that loud
	if err := dec.Decode(&ir); err != nil {
		t.Fatalf("a pre-P14 IR no longer fits the P14 types: %v", err)
	}
	if ir.Nodes[0].ToolsRecorded() {
		t.Error("a pre-P14 node records no split; treating it as 'no tools' would let a selection over " +
			"nothing be accepted")
	}
	out, err := json.Marshal(ir)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != preP14 {
		t.Fatalf("a pre-P14 IR did not round-trip byte-identically.\n got: %s\nwant: %s", out, preP14)
	}
}

// ── 7.4 a consumer pinned BELOW the new IR minor parses both ─────────────────────────────────────

// The additive rule has two halves and the second is usually the one that breaks: not only must an old
// document parse under the new types, a consumer written against the OLD contract must keep parsing a
// NEW document. That is what "additive, no MAJOR bump" actually promises, and it is the promise every
// pinned P2/P3.5/P4 consumer depends on.
//
// The pinned consumer is modelled as a struct carrying only the pre-P14 fields — which is exactly what
// a consumer compiled against the old types IS — parsed leniently, as encoding/json does by default.
func TestPinnedConsumerParsesBothIRMinors(t *testing.T) {
	type pinnedNode struct {
		NodeID      string   `json:"node_id"`
		Kind        string   `json:"kind"`
		ToolsSkills []string `json:"tools_skills"`
	}
	type pinnedIR struct {
		IRVersion string       `json:"ir_version"`
		Nodes     []pinnedNode `json:"nodes"`
	}

	for _, src := range []string{staticToolsSrc, noToolsSrc, dynamicToolsSrc} {
		node := discoverOne(t, src)
		raw, err := json.Marshal(IR{IRVersion: IRVersion, Nodes: []IRNode{node}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var pinned pinnedIR
		if err := json.Unmarshal(raw, &pinned); err != nil {
			t.Fatalf("a consumer pinned below the P14 minor failed to parse a P14 document: %v\n%s", err, raw)
		}
		if len(pinned.Nodes) != 1 || pinned.Nodes[0].NodeID != node.NodeID {
			t.Fatalf("the pinned consumer lost the node it was reading: %+v", pinned)
		}
		// 🔴 And it still sees the CONFLATED view, unchanged. If `tools_skills` had been repurposed to
		// mean "only tools", this is the assertion that would go red — silently, in production, for every
		// consumer that never recompiled.
		if len(pinned.Nodes[0].ToolsSkills) != len(node.ToolsSkills) {
			t.Errorf("the frozen tools_skills view changed under a pinned consumer: %v vs %v",
				pinned.Nodes[0].ToolsSkills, node.ToolsSkills)
		}
	}
}
