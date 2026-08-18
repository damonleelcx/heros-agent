# config_hash & Lineage Specification (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | System Designer (lead) |
| Status | Draft — freeze at M0 |
| Tasks | 1.6 (this spec), 2.4 (golden vectors), 1.7 (storage placement) |
| Cross-refs | `docs/prd/P0-foundations.md` §6 (FR12–FR15), §8.4; `openspec/changes/archive/2026-07-15-p0-foundations/specs/storage-and-lineage/spec.md`; `docs/decisions/storage-decision-record.md` |

This spec defines `config_hash`: the immutable, content-defined identifier that makes every result on
the platform reproducible and attributable. It is precise enough to implement identically in any
language (the P2 Config Layer computes it; every store keys on it). Golden vectors:
[`schemas/samples/config-hash.golden.json`](../../schemas/samples/config-hash.golden.json).

Written in the senior-system-designer lens: *what problem → why this design → why it fits → alternatives
rejected → resulting effect.*

---

## 1. What problem this solves

Every later phase asks two questions that are only answerable if runs have a stable, content-defined
identity:

- **Eval / Improvement (P4–P6):** "did variant B beat variant A?" is only honest if A and B are two
  *exact, replayable* configurations, not two fuzzy labels.
- **Reproducibility (NFR2):** given a result, can we reconstruct the precise configuration that
  produced it and re-run it? "Verification decides" (the platform's core principle) is meaningless if
  a result cannot be replayed.

`config_hash` is that identity. `variant_id` is a *logical* label a human edits over time; a
`config_hash` is the *immutable content hash* of one fully-resolved configuration. One `variant_id`
maps to many `config_hash` values across its edit history; each `config_hash` is replayable forever.

## 2. Definition

```
config_hash = SHA-256( canonical_json( resolved_config ) )
```

- **Hash function:** SHA-256. Output is **lowercase hex, full 64 characters**, stored in full
  everywhere. The UI MAY display a **12-character prefix** for readability. (Resolves PRD OQ2: full
  SHA-256, display-truncated — never store the truncation.)
- **`canonical_json`:** [RFC 8785 JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785)
  (see §4).
- **`resolved_config`:** the fully-resolved configuration object (see §3) — every ref resolved to an
  exact registry version, nothing left to defaulting.

## 3. The include set (what is hashed)

`resolved_config` is the object below. **Every field here is part of the identity of a configuration;
changing any of them MUST change the hash.**

```jsonc
{
  "ir_version": "1.0.0",                       // the IR contract version the config was built against
  "nodes": [                                   // one entry per static definition, in graph order
    {
      "node_id":        "…",                    // stable call-site id from the IR
      "model_ref":      "provider/model_id",    // resolved model binding
      "prompt_ref":     "prompt://…@<version>", // resolved prompt-registry ref WITH version
      "skill_refs":     ["skill@<version>", …], // resolved skill/tool bindings WITH versions
      "context_policy": "…",                    // context-assembly policy name
      "context_params": { … },                  // its parameters
      "provider_params":{ "temperature": 0, … } // inference params that change model behavior
    }
  ],
  "edges": [ { "from_node_id": "…", "to_node_id": "…", "kind": "data|control" } ]  // ordering / graph
}
```

Rationale per field:

| Included | Why it is identity-bearing |
|---|---|
| `ir_version` | The same bindings under a different IR MAJOR may mean something different. |
| `model_ref` | A different model is a different configuration. |
| `prompt_ref` **with version** | Registry refs are versioned so a hash always resolves the exact bytes (FR14). Repointing `@3 → @4` is a new config. |
| `skill_refs` **with versions** | Same reasoning as prompts. |
| `context_policy` + `context_params` | The context strategy materially changes behavior. |
| `provider_params` | temperature/max_tokens/top_p change outputs; part of the config. |
| `edges` / node ordering | Re-arrangement (P5) changes the graph; the wiring is part of identity. |

## 4. Canonicalization (RFC 8785 JCS)

The bytes hashed are produced by **RFC 8785 (JSON Canonicalization Scheme)**:

1. **Object keys sorted** recursively by UTF-16 code unit (for our ASCII keys this equals byte order).
2. **No insignificant whitespace** — element separator `,`, key separator `:`, nothing else.
3. **UTF-8** output; strings use minimal JSON escaping.
4. **Numbers** in their shortest round-tripping form (the RFC 8785 / ECMA-262 `Number.toString`
   serialization). `0` stays `0`, `0.2` stays `0.2`.
5. **No `NaN`/`Infinity`** — `resolved_config` contains only strings, finite numbers, booleans,
   arrays, objects, and (rarely) `null`.

**Worked canonical form** (base vector; whitespace added here only for reading — the real bytes have
none):

```
{"edges":[{"from_node_id":"classify_intent@app/triage.py:classify:41","kind":"data",
"to_node_id":"resolve_agent@app/triage.py:resolve:77"}],"ir_version":"1.0.0","nodes":[…]}
```

→ `config_hash = 5427bc41fdb34b639274457365ff2e71e00dee7cb800306acdb8729b96c13345`
(display: `5427bc41fdb3`). Full bytes and hash in the golden fixture.

## 5. The exclude set (what is NOT hashed)

`resolved_config` **MUST NOT** contain any run-time-only value:

| Excluded | Why |
|---|---|
| `seed` | The same configuration under seeds 1..5 MUST share one `config_hash` so multi-seed results roll up under one config for confidence intervals (FR13, NFR2). |
| `run_id` | Identifies an execution batch, not a configuration. |
| `timestamp` | Wall-clock must not fragment identical configs; the hash is stable across days. |

These three live on the **metric event** and in Postgres columns — never inside the hash input. This
is the single most common way a hash scheme silently breaks (a stray timestamp), so it is called out
as its own requirement with a golden vector guarding it.

## 6. Lineage: resolving a hash back to bytes

A `config_hash` is only useful if it *resolves*. The lineage store (Postgres `config` row + registries
+ content-addressed blobs) MUST let anyone reconstruct the exact configuration from the hash alone:

```
config_hash ─▶ config row (ir_version, per-node resolved refs@version, ordering, provider_params)
                 ├─ model_ref@ver     ─▶ model registry (exact entry)
                 ├─ prompt_ref@ver    ─▶ prompt registry ─▶ blob(content_hash)   [object store]
                 └─ skill_refs@ver    ─▶ skill registry
```

Blobs (prompts/artifacts) are content-addressed by **SHA-256 of their bytes** and referenced, never
inlined (FR15). Reproducibility unit = **`config_hash` + `seed`** (NFR2): the hash pins *our* inputs;
the seed pins *our* sampling choices. It does **not** promise bit-identical provider output — providers
are non-deterministic; multi-seed statistics absorb the residual (stated openly, PRD §8.6).

## 7. Golden vectors (the tests, task 2.4)

[`schemas/samples/config-hash.golden.json`](../../schemas/samples/config-hash.golden.json) pins four
properties a P2 implementation MUST reproduce:

| Property | Assertion |
|---|---|
| **Determinism** | `sha256(canon(base.resolved_config))` == `5427bc41fdb3…c13345`. |
| **Canonicalization** | A key-reordered copy of `base` produces the **same** hash. |
| **Version-sensitivity** | Repointing `nodes[0].prompt_ref @3 → @4` produces `c13b5afda2b2…8944` (**different**). |
| **Seed-invariance** | `seed`/`run_id`/`timestamp` are absent from `resolved_config`, so different seeds share one hash (there is no field to vary). |

These were computed with the canonicalization in §4 and are the frozen expected outputs for CI.

## 8. Alternatives rejected

| Rejected | Why (8-level arbitration: reproducibility/evolvability > implementation convenience) |
|---|---|
| **Hash the raw serialized spec** (no canonicalization) | Key ordering, whitespace, and number formatting differ across languages/serializers, so identical configs would get different hashes — destroys attribution. Rejected. |
| **Invent a bespoke canonical subset** | A home-grown rule set is a new truth source to maintain and get subtly wrong (number formatting is the classic trap). RFC 8785 is a published standard with test vectors. Chosen the standard. |
| **Include `seed`/`timestamp`** for "more precise" identity | Fragments one configuration into N hashes, breaking multi-seed roll-up and cross-day stability. Rejected (this is FR13). |
| **Truncated hash stored** (e.g. 12 hex chars) for readability | ~48 bits invites collisions across the platform's lifetime; a collision silently conflates two configurations — a correctness/stability risk (levels 1–2) traded for cosmetics (level 8). Store full SHA-256; truncate only for display. Rejected as a storage form. |
| **Reference prompts/skills without versions** | A later registry edit would silently change what a past hash resolves to — non-reproducible. Refs are versioned. Rejected. |

## 9. Open questions (tracked, not blocking freeze)

- **OQ2 (resolved here):** full SHA-256 stored, 12-char display prefix.
- **OQ3:** JSON Schema dialect for `io_contract` is draft 2020-12; strictness of partially-inferred
  schemas is a P1 concern, not a hash concern.
- **OQ5:** blob GC for content-hashed blobs no longer referenced by any `config_hash` — deferred, but
  the reference-by-hash design does not preclude it.
