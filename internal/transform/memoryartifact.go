package transform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The generated memory artifact (P18 §2, decisions.md D3/D5).
//
// # What ships, and where
//
// Two files, emitted in the SAME patch as the call-site edit so one revert restores everything — the
// discipline P10's bound mode already established for binding artifacts:
//
//	agentmem.py       the module the rewritten call site calls. Dependency-free, deterministic.
//	agentmem.json     the strategy and params, AS DATA. Retuning max_tokens is a document change.
//
// # Why the params are data and not code
//
// ADR-004's data/structure line. A generated module with `max_entries = 5` baked into it makes retuning a
// parameter a CODE change — a new diff, a new review, a new deploy — for something that is not structure.
// The document also means the artifact regenerates byte-identically when only a parameter moved, so a
// re-apply produces no spurious module diff.
//
// # The session id: the one precondition this artifact states out loud
//
// 🔴 decisions.md D1 forbids the runtime from inventing a session id, because a defaulted one silently
// merges conversations that must stay separate — a cross-user leak in a server process, invisible from
// inside it. The generated module therefore REQUIRES a session and **raises** when it has none, naming
// the two ways to supply one.
//
// That is a real precondition on the customer's code, and it is stated rather than smoothed over: a
// process-lifetime default would be the exact merge D1 rejects, and returning empty memory instead would
// run `none` under another strategy's hash. Loud-and-fixable beats quiet-and-wrong, and the materializer
// surfaces it before apply rather than at run time (see memorySessionPrecondition).

// Artifact paths are fixed and deterministic so regeneration overwrites byte-identically.
const (
	pyMemoryModulePath = "agentmem.py"
	memoryDocPath      = "agentmem.json"
	memoryDocSchema    = "heros.agentmem/v1"
)

// memorySessionPrecondition is the sentence every surface uses for the session requirement. One
// spelling, because the refusal a user reads before apply and the exception they would hit at run time
// must not be two different explanations of one fact.
const memorySessionPrecondition = "materialized memory needs a session id to scope it: set HEROS_MEMORY_SESSION, " +
	"or call agentmem.set_session(<id>) at the point your program knows which conversation it is serving. " +
	"The generated module raises rather than defaulting one, because a defaulted session merges " +
	"conversations that must stay separate"

// MemoryDocument is the emitted params document. Its shape mirrors the binding document's: a schema, the
// config hash it was generated for, and per-node data.
type MemoryDocument struct {
	Schema     string                   `json:"schema"`
	ConfigHash string                   `json:"config_hash"`
	Nodes      map[string]MemoryDocNode `json:"nodes"`
}

// MemoryDocNode is one node's memory configuration, as data.
type MemoryDocNode struct {
	Strategy string         `json:"strategy"`
	Params   map[string]any `json:"params"`
}

// memoryNodes returns the nodes whose resolved override binds a NON-IDENTITY memory strategy, sorted.
//
// `none` is excluded deliberately: it is the identity, it emits no call site edit, and generating an
// artifact for it would put a module in the customer's tree that nothing calls.
func memoryNodes(resolved *variantspec.Resolved) []string {
	var out []string
	for nodeID, ov := range resolved.Overrides {
		if ov.Memory == nil || ov.Memory.IsNone() {
			continue
		}
		out = append(out, nodeID)
	}
	sort.Strings(out)
	return out
}

// GenerateMemoryArtifacts builds the memory module and its params document for every node binding a
// memory strategy. It returns an empty map when no node does, so a spec without memory adds nothing.
//
// It is a PURE function of the resolved spec: the same resolved configuration regenerates byte-identical
// output, because the document goes through the RFC-8785 canonicalizer and the module is a constant.
func GenerateMemoryArtifacts(resolved *variantspec.Resolved, language string) (map[string][]byte, error) {
	if resolved == nil {
		return map[string][]byte{}, nil
	}
	nodes := memoryNodes(resolved)
	if len(nodes) == 0 {
		return map[string][]byte{}, nil
	}

	doc := MemoryDocument{Schema: memoryDocSchema, ConfigHash: resolved.ConfigHash, Nodes: map[string]MemoryDocNode{}}
	for _, nodeID := range nodes {
		ov := resolved.Overrides[nodeID]
		params, err := decodeMemoryParams(ov.Memory.Spec.Params)
		if err != nil {
			return nil, fmt.Errorf("transform: node %q memory params: %w", nodeID, err)
		}
		doc.Nodes[nodeID] = MemoryDocNode{Strategy: ov.Memory.Spec.Strategy, Params: params}
	}

	docBytes, err := canonicalMemoryDocument(doc)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(language) {
	case "python":
		return map[string][]byte{
			pyMemoryModulePath: []byte(pythonMemoryModule),
			memoryDocPath:      docBytes,
		}, nil
	case "go":
		// Go's module lands in its own package directory, because Go requires a package per directory —
		// unlike Python, where a flat module beside the caller is importable as-is.
		return map[string][]byte{
			goMemoryModulePath: []byte(goMemoryModule),
			goMemoryDocPath:    docBytes,
		}, nil
	default:
		// 🚫 No default module. A language with no artifact must never receive another language's — the
		// materializer refuses that cell before this is reached, and this is the second line of that
		// defence rather than a fallback.
		return nil, fmt.Errorf("transform: no memory module for language %q", language)
	}
}

func decodeMemoryParams(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("params are not a JSON object: %w", err)
	}
	return out, nil
}

func canonicalMemoryDocument(doc MemoryDocument) ([]byte, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("transform: marshal memory document: %w", err)
	}
	canon, err := confighash.CanonicalizeBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("transform: canonicalize memory document: %w", err)
	}
	return append(canon, '\n'), nil
}

// pythonMemoryModule is the emitted Python module.
//
// 🔴 It is a CONSTANT, not a template. Nothing about a node, a strategy, or a parameter is interpolated
// into it — all of that lives in agentmem.json. That is what makes regeneration byte-identical and what
// keeps a parameter change out of the code diff (FR10, FR12).
//
// 🚫 Its imports are `json`, `os`, and `threading` — standard library only (FR11). It makes no provider
// call: `summary-buffer` and `vector-recall` need a host, and this module RAISES for them rather than
// substituting a cheaper strategy, exactly as the Go runtime does (D3). A generated file that reached a
// provider would need a credential in the customer's process.
//
// The retention semantics here mirror internal/memoryruntime, and a conformance test executes both over
// the same inputs and compares — because a second implementation is only safe if something proves the two
// agree (D5).
const pythonMemoryModule = `"""Generated by heros. DO NOT EDIT.

This module is the memory runtime for the call sites heros rewrote. It reads its configuration from
agentmem.json, which is data: retuning a parameter is a change to that file, not to this one.

It is dependency-free (standard library only) and makes no network or provider call. Strategies that
need a summarizer or an embedder raise rather than silently falling back to a cheaper strategy -- a
summary-buffer that quietly truncates is a scratchpad running under another configuration's name.

Sessions
--------
Memory is scoped by (node, session). This module will not invent a session id: a defaulted one merges
conversations that must stay separate, which is a cross-user leak in a server process. Supply one with
the HEROS_MEMORY_SESSION environment variable or agentmem.set_session(...).
"""

import json
import os
import threading

_HERE = os.path.dirname(os.path.abspath(__file__))
_DOC = os.path.join(_HERE, "agentmem.json")

_lock = threading.Lock()
_entries = {}
_seq = {}
_session = threading.local()
_config_cache = None


class MemorySessionRequired(RuntimeError):
    """Raised when no session id is available to scope memory."""


class MemoryHostRequired(RuntimeError):
    """Raised when a strategy needs a host service that was not supplied."""


class MemoryNotConfigured(RuntimeError):
    """Raised when a node has no entry in agentmem.json."""


def set_session(session_id):
    """Scope subsequent recall/record calls on this thread to session_id."""
    _session.value = session_id


def get_session():
    value = getattr(_session, "value", None) or os.environ.get("HEROS_MEMORY_SESSION")
    if not value:
        raise MemorySessionRequired(
            "no memory session id: set HEROS_MEMORY_SESSION or call agentmem.set_session(<id>). "
            "This module refuses to default one because a defaulted session merges conversations "
            "that must stay separate."
        )
    return value


def _config(node_id):
    global _config_cache
    if _config_cache is None:
        with open(_DOC, "r", encoding="utf-8") as handle:
            _config_cache = json.load(handle)
    node = _config_cache.get("nodes", {}).get(node_id)
    if node is None:
        raise MemoryNotConfigured("no memory configuration for node %s" % node_id)
    return node.get("strategy"), node.get("params", {})


def _key(node_id):
    return (node_id, get_session())


def _stored(key):
    return list(_entries.get(key, ()))


def _append(key, message):
    with _lock:
        _seq[key] = _seq.get(key, 0) + 1
        _entries.setdefault(key, []).append({"seq": _seq[key], "message": message})


def _expire(key, keep_last):
    with _lock:
        current = _entries.get(key, [])
        if len(current) > keep_last:
            _entries[key] = current[len(current) - keep_last:]


def _tokens(messages):
    return sum(len(m.get("content", "").split()) for m in messages)


def _facts(content, keys):
    declared = set(k.strip().lower() for k in keys)
    found = {}
    for line in (content or "").split("\n"):
        cut = -1
        for i, ch in enumerate(line):
            if ch in ":=":
                cut = i
                break
        if cut <= 0:
            continue
        key = line[:cut].strip().lower()
        value = line[cut + 1:].strip()
        if value and key in declared:
            found[key] = value
    return found


def recall(node_id, messages, host=None):
    """Return the message list to send: what memory holds, then the turns you wrote."""
    strategy, params = _config(node_id)
    key = _key(node_id)
    stored = _stored(key)
    incoming = list(messages or [])

    if strategy == "none":
        return incoming

    if strategy == "scratchpad":
        limit = int(params.get("max_entries", 0))
        if limit < 1:
            raise ValueError("scratchpad needs max_entries >= 1")
        kept = stored[-limit:] if len(stored) > limit else stored
        return [e["message"] for e in kept] + incoming

    if strategy == "summary-buffer":
        budget = int(params.get("max_tokens", 0))
        if budget < 1:
            raise ValueError("summary-buffer needs max_tokens >= 1")
        if not stored:
            return incoming
        keep = int(params.get("keep_last_turns", 0))
        keep = max(0, min(keep, len(stored)))
        older = [e["message"] for e in stored[:len(stored) - keep]]
        tail = [e["message"] for e in stored[len(stored) - keep:]]
        if not older:
            return tail + incoming
        summarizer = getattr(host, "summarize", None) if host is not None else None
        if summarizer is None:
            raise MemoryHostRequired(
                "summary-buffer needs a host summarizer; refusing rather than truncating silently, "
                "because dropping the older turns instead would be a scratchpad running under this "
                "configuration's name"
            )
        out = [{"role": "system", "content": summarizer(older, budget)}] + tail
        while len(out) > 1 and _tokens(out) > budget:
            del out[1]
        return out + incoming

    if strategy == "vector-recall":
        top_k = int(params.get("top_k", 0))
        if top_k < 1:
            raise ValueError("vector-recall needs top_k >= 1")
        if not params.get("embedding_ref"):
            raise ValueError("vector-recall needs embedding_ref")
        if not stored:
            return incoming
        scorer = getattr(host, "score", None) if host is not None else None
        if scorer is None:
            raise MemoryHostRequired(
                "vector-recall needs a host embedder; refusing rather than falling back to recency"
            )
        query = incoming[-1].get("content", "") if incoming else ""
        cands = [e["message"].get("content", "") for e in stored]
        scores = scorer(query, cands, params["embedding_ref"])
        if len(scores) != len(cands):
            raise ValueError("embedder returned the wrong number of scores")
        order = sorted(range(len(stored)), key=lambda i: (-scores[i], stored[i]["seq"]))
        picked = sorted(order[:top_k])
        return [stored[i]["message"] for i in picked] + incoming

    if strategy == "entity-memory":
        keys = params.get("entity_keys") or []
        if not keys:
            raise ValueError("entity-memory needs at least one entity key")
        facts = {}
        for entry in stored:
            facts.update(_facts(entry["message"].get("content", ""), keys))
        if not facts:
            return incoming
        lines = ["Known facts:"]
        for key_name in sorted(facts):
            lines.append("%s: %s" % (key_name, facts[key_name]))
        return [{"role": "system", "content": "\n".join(lines)}] + incoming

    raise ValueError("no runtime implementation for strategy %s" % strategy)


def record(node_id, messages, response=None):
    """Record a completed turn: what you sent, and what came back."""
    strategy, params = _config(node_id)
    key = _key(node_id)
    turn = list(messages or [])
    if response is not None:
        turn = turn + [_as_message(response)]

    if strategy == "none":
        return

    if strategy == "scratchpad":
        limit = int(params.get("max_entries", 0))
        if limit < 1:
            raise ValueError("scratchpad needs max_entries >= 1")
        for message in turn:
            _append(key, message)
        _expire(key, limit)
        return

    if strategy in ("summary-buffer", "vector-recall"):
        for message in turn:
            _append(key, message)
        return

    if strategy == "entity-memory":
        keys = params.get("entity_keys") or []
        if not keys:
            raise ValueError("entity-memory needs at least one entity key")
        for message in turn:
            if _facts(message.get("content", ""), keys):
                _append(key, message)
        return

    raise ValueError("no runtime implementation for strategy %s" % strategy)


def _as_message(response):
    """Coerce a provider response into a message without knowing any SDK.

    Only shapes that are already message-like are read. Anything else is stored as its string form
    rather than guessed at -- a wrong guess here would record something the model never said.
    """
    if isinstance(response, dict):
        if "role" in response and "content" in response:
            return {"role": response["role"], "content": response["content"]}
        choices = response.get("choices")
        if isinstance(choices, list) and choices:
            message = choices[0].get("message") if isinstance(choices[0], dict) else None
            if isinstance(message, dict) and "content" in message:
                return {"role": message.get("role", "assistant"), "content": message["content"]}
    content = getattr(response, "content", None)
    if isinstance(content, str):
        return {"role": "assistant", "content": content}
    return {"role": "assistant", "content": str(response)}
`
