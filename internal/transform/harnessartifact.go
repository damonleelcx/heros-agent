package transform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The generated harness artifact (P18 §11, decisions.md D-9/D-10)
// ───────────────────────────────────────────────────────────────
//
// Two files, emitted in the SAME patch as the call-site edit so one revert restores everything — the
// discipline P10's bound mode established and the memory artifact next door already follows:
//
//	agentharness.py     the module the rewritten call site drives. Dependency-free, deterministic.
//	agentharness.json   the strategy and params, AS DATA. Retuning max_turns is a document change.
//
// # Why the params are data and not code
//
// ADR-004's data/structure line, and here it has teeth beyond tidiness: `max_turns` is the COST. Baking
// it into the module would make "run three turns instead of five" a code change — a new diff, a new
// review, a new deploy — for a number a user may legitimately want to move after seeing a bill. The
// document also means the artifact regenerates byte-identically when only a parameter moved.
//
// # The precondition this artifact states out loud
//
// 🔴 The loop must be able to read an answer's text to decide whether to take another turn. In Python a
// model response is usually message-like and readable without knowing any SDK — but not always, and this
// module will not GUESS. It reads the shapes it can prove and RAISES `HarnessAnswerUnreadable` otherwise,
// naming the fix (`agentharness.set_answer_reader(fn)`).
//
// That is a real precondition on the customer's code, stated rather than smoothed over — the same call the
// memory module makes about a session id. A module that fell back to `str(response)` would feed the
// model its own repr and call the result a reflexion loop; loud-and-fixable beats quiet-and-wrong.

// Artifact paths are fixed and deterministic so regeneration overwrites byte-identically.
const (
	pyHarnessModulePath = "agentharness.py"
	harnessDocPath      = "agentharness.json"
	harnessDocSchema    = "heros.agentharness/v1"
)

// HarnessDocument is the emitted params document. Its shape mirrors the memory document's: a schema, the
// config hash it was generated for, and per-node data.
type HarnessDocument struct {
	Schema     string                    `json:"schema"`
	ConfigHash string                    `json:"config_hash"`
	Nodes      map[string]HarnessDocNode `json:"nodes"`
}

// HarnessDocNode is one node's harness configuration, as data.
type HarnessDocNode struct {
	Strategy string         `json:"strategy"`
	Params   map[string]any `json:"params"`
}

// harnessNodes returns the nodes whose resolved override binds a NON-IDENTITY harness strategy, sorted.
//
// `single-shot` is excluded deliberately: it is the identity, it emits no call-site edit, and generating
// an artifact for it would put a module in the customer's tree that nothing calls.
func harnessNodes(resolved *variantspec.Resolved) []string {
	var out []string
	for nodeID, ov := range resolved.Overrides {
		if ov.Harness == nil || ov.Harness.IsSingleShot() {
			continue
		}
		out = append(out, nodeID)
	}
	sort.Strings(out)
	return out
}

// GenerateHarnessArtifacts builds the harness module and its params document for every node binding a
// multi-turn strategy. It returns an empty map when no node does, so a spec without a harness adds
// nothing.
//
// It is a PURE function of the resolved spec: the same resolved configuration regenerates byte-identical
// output, because the document goes through the RFC-8785 canonicalizer and the module is a constant.
func GenerateHarnessArtifacts(resolved *variantspec.Resolved, language string) (map[string][]byte, error) {
	if resolved == nil {
		return map[string][]byte{}, nil
	}
	nodes := harnessNodes(resolved)
	if len(nodes) == 0 {
		return map[string][]byte{}, nil
	}

	doc := HarnessDocument{Schema: harnessDocSchema, ConfigHash: resolved.ConfigHash,
		Nodes: map[string]HarnessDocNode{}}
	for _, nodeID := range nodes {
		ov := resolved.Overrides[nodeID]
		params, err := decodeHarnessParams(ov.Harness.Spec.Params)
		if err != nil {
			return nil, fmt.Errorf("transform: node %q harness params: %w", nodeID, err)
		}
		doc.Nodes[nodeID] = HarnessDocNode{Strategy: ov.Harness.Spec.Strategy, Params: params}
	}

	docBytes, err := canonicalHarnessDocument(doc)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(language) {
	case "python":
		return map[string][]byte{
			pyHarnessModulePath: []byte(pythonHarnessModule),
			harnessDocPath:      docBytes,
		}, nil
	default:
		// 🚫 No default module. A language with no artifact must never receive another language's — the
		// materializer refuses that cell before this is reached, and this is the second line of that
		// defence rather than a fallback.
		return nil, fmt.Errorf("transform: no harness module for language %q", language)
	}
}

func decodeHarnessParams(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("params are not a JSON object: %w", err)
	}
	return out, nil
}

func canonicalHarnessDocument(doc HarnessDocument) ([]byte, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("transform: marshal harness document: %w", err)
	}
	canon, err := confighash.CanonicalizeBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("transform: canonicalize harness document: %w", err)
	}
	return append(canon, '\n'), nil
}

// harnessWasMaterialized reports whether any file actually received harness edits, so the artifact is
// emitted only alongside a real call-site rewrite.
func harnessWasMaterialized(editsByFile map[string][]edit) bool {
	for _, edits := range editsByFile {
		if hasHarnessEdit(edits) {
			return true
		}
	}
	return false
}

func hasHarnessEdit(edits []edit) bool {
	for _, e := range edits {
		if e.Dim == string(variantspec.DimHarness) {
			return true
		}
	}
	return false
}

func firstHarnessEdit(edits []edit) edit {
	for _, e := range edits {
		if e.Dim == string(variantspec.DimHarness) {
			return e
		}
	}
	return edit{}
}

// pythonHarnessModule is the emitted Python module.
//
// 🔴 It is a CONSTANT, not a template. Nothing about a node, a strategy, or a parameter is interpolated
// into it — all of that lives in agentharness.json. That is what makes regeneration byte-identical and
// what keeps a parameter change out of the code diff (FR35, FR37).
//
// 🚫 Its imports are `json`, `os` and `threading` — standard library only (FR34). It makes NO provider
// call and dispatches NO tool: `react-loop`, `plan-execute` and `critic-loop` need a host, and this module
// RAISES for them rather than substituting a lighter loop, exactly as internal/harnessruntime does
// (D-10). A generated file that reached a provider would need a credential in the customer's process.
//
// 🔴 The loop semantics here mirror internal/harnessruntime, and a conformance test executes both over the
// same inputs and compares — because a second implementation is only safe if something proves the two
// agree.
const pythonHarnessModule = `"""Generated by heros. DO NOT EDIT.

This module is the harness runtime for the call sites heros rewrote. It reads its configuration from
agentharness.json, which is data: retuning max_turns is a change to that file, not to this one.

It is dependency-free (standard library only) and makes no network or provider call, and it dispatches
no tool. Strategies that need a tool executor, a planner, or a separate critic model raise rather than
silently running a lighter loop -- a critic-loop without a critic is a reflexion loop running under
another configuration's name, at another configuration's price.

Bounded by construction
-----------------------
The turn count is a range bound, not a break inside the body, so no strategy and no parameter can make
this run more turns than its configuration declares.

Reading an answer
-----------------
Deciding whether to take another turn means reading what came back. This module reads the response
shapes it can prove and raises HarnessAnswerUnreadable otherwise -- it will not fall back to str() and
feed the model its own repr while calling the result a reflexion loop. Supply a reader with
agentharness.set_answer_reader(fn) if your SDK's response is not message-like.
"""

import json
import os
import threading

_HERE = os.path.dirname(os.path.abspath(__file__))
_DOC = os.path.join(_HERE, "agentharness.json")

_config_cache = None
_reader = threading.local()

CEILING = 16

STOP_SATISFIED = "satisfied"
STOP_CEILING = "ceiling"
STOP_SINGLE_SHOT = "single-shot"


class HarnessHostRequired(RuntimeError):
    """Raised when a strategy needs a host service that was not supplied."""


class HarnessAnswerUnreadable(RuntimeError):
    """Raised when the loop cannot read an answer's text to decide whether to continue."""


class HarnessNotConfigured(RuntimeError):
    """Raised when a node has no entry in agentharness.json."""


def set_answer_reader(fn):
    """Teach this thread how to read your SDK's response into text."""
    _reader.value = fn


def _config(node_id):
    global _config_cache
    if _config_cache is None:
        with open(_DOC, "r", encoding="utf-8") as handle:
            _config_cache = json.load(handle)
    node = _config_cache.get("nodes", {}).get(node_id)
    if node is None:
        raise HarnessNotConfigured("no harness configuration for node %s" % node_id)
    return node.get("strategy"), node.get("params", {})


def _ceiling(strategy, params):
    if strategy == "single-shot":
        return 1
    turns = int(params.get("max_turns", 0))
    if turns < 1:
        raise ValueError(
            "%s needs max_turns >= 1; a loop with no turns is not a loop, and reading it as 1 would "
            "run a single shot under a multi-turn configuration's name" % strategy
        )
    if turns > CEILING:
        raise ValueError("%s declares max_turns %d, above the ceiling of %d" % (strategy, turns, CEILING))
    return turns


def _require_host(strategy, host):
    if strategy == "react-loop" and getattr(host, "invoke_tool", None) is None:
        raise HarnessHostRequired(
            "react-loop continues by RUNNING the tool the model asked for, and no tool executor was "
            "supplied. It is not degraded to a loop that skips the tool: that loop is a different "
            "strategy, and running it here would report one scaffold under another's config_hash"
        )
    if strategy == "plan-execute" and getattr(host, "execute_step", None) is None:
        raise HarnessHostRequired(
            "plan-execute's first turn produces a plan and the rest EXECUTE its steps, and no planner "
            "was supplied"
        )
    if strategy == "critic-loop" and getattr(host, "critique", None) is None:
        raise HarnessHostRequired(
            "critic-loop continues by calling a SEPARATE model to judge the answer, and no critic was "
            "supplied. The critic is a host service because a generated file may not reach a provider "
            "-- that would put your credential in your own process, spent on turns you did not write"
        )


def answer_text(response):
    """Read a response into text without guessing.

    Only shapes that are already message-like are read. Anything else raises, because falling back to
    str(response) would feed the model its own repr and call the result a reflexion loop.
    """
    reader = getattr(_reader, "value", None)
    if reader is not None:
        return reader(response)
    if isinstance(response, str):
        return response
    if isinstance(response, dict):
        if isinstance(response.get("content"), str):
            return response["content"]
        choices = response.get("choices")
        if isinstance(choices, list) and choices and isinstance(choices[0], dict):
            message = choices[0].get("message")
            if isinstance(message, dict) and isinstance(message.get("content"), str):
                return message["content"]
    content = getattr(response, "content", None)
    if isinstance(content, str):
        return content
    raise HarnessAnswerUnreadable(
        "cannot read this response's text, so the loop cannot decide whether to take another turn. "
        "Supply a reader with agentharness.set_answer_reader(fn). This module refuses to guess: a "
        "fallback to str(response) would feed the model its own repr and call the result a loop."
    )


def _continue(strategy, params, answer):
    if strategy == "reflexion":
        marker = params.get("answer_marker")
        if params.get("stop_condition") == "answer-marker" and marker:
            return not (marker in answer)
        return True
    if strategy == "react-loop":
        if params.get("stop_condition") == "no-tool-call":
            return "tool_call" in answer or "tool_use" in answer
        return True
    if strategy == "plan-execute":
        if params.get("stop_condition") == "plan-complete":
            return "next_step" in answer or "remaining" in answer
        return True
    if strategy == "critic-loop":
        return True
    raise ValueError("no runtime implementation for strategy %s" % strategy)


def _append(strategy, params, answer):
    if strategy == "reflexion":
        return [
            {"role": "assistant", "content": answer},
            {"role": "user", "content": params.get("reflection_prompt", "")},
        ]
    return [{"role": "assistant", "content": answer}]


def run(node_id, invoke, messages, host=None):
    """Run this node's configured control loop, bounded by its declared turn ceiling.

    invoke(messages) performs ONE turn. It is the only thing this module calls, which is what makes
    "the added turns reach nothing new" true by construction: whatever that call could reach before is
    exactly what it can reach now.
    """
    strategy, params = _config(node_id)
    ceiling = _ceiling(strategy, params)
    _require_host(strategy, host)

    turn_messages = list(messages or [])
    response = None
    for turn in range(1, ceiling + 1):
        response = invoke(turn_messages)
        if turn == ceiling:
            break
        if not _continue(strategy, params, answer_text(response)):
            break
        turn_messages = turn_messages + _append(strategy, params, answer_text(response))
    return response


def describe(node_id):
    """Return (strategy, ceiling) for a node -- the observable half, for a caller that wants to log it."""
    strategy, params = _config(node_id)
    return strategy, _ceiling(strategy, params)
`

// harnessStrategyNamesInModule is the strategy set the emitted module implements a loop for. Read by the
// conformance test that pins it against the sealed vocabulary, so a sixth strategy cannot land with the
// module silently missing it.
func harnessStrategyNamesInModule() []string {
	out := make([]string, 0, registry.HarnessStrategySetSize)
	for _, st := range registry.BuiltinHarnessStrategies() {
		name := st.Name()
		if name == registry.StrategySingleShot {
			// The identity is implemented by `_ceiling` returning 1, not by a `_continue` branch.
			out = append(out, name)
			continue
		}
		if strings.Contains(pythonHarnessModule, `strategy == "`+name+`"`) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// generatedImport is one generated module's import requirement for a file.
type generatedImport struct {
	build func(src []byte, language, root string) (edit, error)
}

// generatedImportsFor returns, in a stable order, the import each generated module needs in this file.
//
// 🔴 Per FILE and per MODULE, not per node. Two memory nodes in one file need ONE `import agentmem`, and
// emitting it from the per-site materializer would produce a duplicate the gate then rejects — a refusal
// caused by our own emission rather than by anything the user wrote. A file carrying both a memory and a
// harness materialization needs both imports, which is why this returns a slice rather than an option.
func generatedImportsFor(edits []edit) []generatedImport {
	var out []generatedImport
	if hasMemoryEdit(edits) {
		blame := firstMemoryEdit(edits)
		out = append(out, generatedImport{build: func(src []byte, language, root string) (edit, error) {
			return memoryImportEdit(src, blame.NodeID, blame.Dim, language, root)
		}})
	}
	if hasHarnessEdit(edits) {
		blame := firstHarnessEdit(edits)
		out = append(out, generatedImport{build: func(src []byte, _, _ string) (edit, error) {
			return harnessImportEdit(src, blame.NodeID, blame.Dim)
		}})
	}
	return out
}
