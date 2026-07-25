# ADR-009 — The binding document is JSON data plus a generated, type-checked accessor

- **Status:** Accepted (2026-07-24)
- **Deciders:** System Design (proposed) + User (ratified)
- **Refines:** [ADR-004](ADR-004-runtime-config-binding.md) — ADR-004 decided that `bound` mode ships
  a *binding document* and a *binding artifact* in one diff. It did **not** fix the on-disk **format**
  of either, and the format is itself a public contract the moment it ships in a customer repository.
  This ADR fixes it. (P10 tasks.md §1.1; PRD §14 Q1.)

## Context — what problem this fixes

In `bound` mode the customer's repository gains two new files it now owns forever:

1. a **binding document** — the resolved values (model id, inference params, prompt template, the
   `literal` and `env` bindings), and
2. a **binding artifact** — the generated accessor the rewritten call site reads
   (`agentcfg.Node("n_triage").Model()`).

Both are one-way doors the instant a released version writes them into a customer tree: their shape
becomes a contract we cannot silently change, because a customer's build compiles against the
accessor and their operators hand-edit the document. So the format is not an implementation detail to
settle in code review — it is a decision to make deliberately, before the write path ships (which is
exactly why tasks.md §1.1 asks for it as an ADR, and §1.3 warns "a stored fact is one-way").

Three forces are in tension:

- **The document must be diffable and hand-editable as data.** Its entire reason to exist (ADR-004)
  is that a model or prompt swap becomes a data edit, reviewed as a diff, not a code change. That
  wants a plain, boring, structured text format — not a Go source file, not a binary blob.
- **The accessor must be safe to call.** A call site that reads a key the document does not contain
  must fail at the customer's **build**, not at 3am in their production process. That wants a
  **type-checked** surface, not `map[string]any` lookups that compile regardless.
- **Neither may drag in a dependency.** ADR-004's H-set requires the artifact be dependency-free and
  byte-identically regenerable, small enough that a reviewer actually reads it.

## Decision

**The binding document is a single JSON document. The binding artifact is generated source in the
target language (`agentcfg` for Go) whose accessors are typed, that embeds the document at build
time, and that carries no dependency outside the target's own standard library.**

### The document — JSON, canonical, self-describing

```jsonc
{
  "schema": "heros.agentcfg/v1",
  "config_hash": "b3f1…",            // hash of THIS document's resolved configuration
  "verified_config_hash": "b3f1…",   // the hash that carried a verified delta; omitted if none
  "nodes": {
    "n_triage": {
      "model_id": "anthropic/claude-sonnet-5",
      "params": { "max_tokens": 1024, "temperature": 0.2 },
      "prompt_template": "Triage this ticket:\n{{ticket}}\nTier: {{tier}}",
      "literal_bindings": { "tier": "gold" },
      "env_bindings": { "region": "AWS_REGION" }
    }
  }
}
```

- **JSON, not a Go file.** The values are data; a customer edits `model_id` and reviews a one-line
  diff. A Go source file would make "change the model" a code change again — the exact cost ADR-004
  exists to remove — and it could not be produced or read by a non-Go toolchain later.
- **Canonical bytes (RFC 8785 / the existing `internal/confighash` canonicalizer).** The document is
  serialized through the *same* canonicalizer that produces `config_hash` and registry version ids, so
  regeneration is byte-identical (ADR-004's determinism requirement) and a hand-edit that changes a
  value changes the bytes visibly.
- **`config_hash` and `verified_config_hash` are recorded in the document itself.** The resolver emits
  the resolved hash on every invocation (H1) and marks a resolution `unverified` when
  `verified_config_hash` is absent or does not match (H3) — both need the fact to travel *in* the
  document, not alongside it.
- **`expr` and `input` bindings are deliberately absent from the document.** They live in the
  rewritten call site (ADR-004's data/structure line). The document holds only what is genuinely
  runtime-changeable.

### The accessor — generated, typed, embedding the document

The generated `agentcfg` package (Go) exposes **typed methods per node** —
`agentcfg.Node("n_triage").Model() string`, `.Params() Params`, `.PromptTemplate() string` — over the
document it **embeds at build time** (`go:embed`). Reading a node or a field the document does not
contain is a **compile error against the generated accessor**, because the accessor's methods are
generated from the document's actual node set — not a runtime map miss. That is the "type-checked by
the customer's own build" property task 1.1 asks for.

The package imports nothing outside the standard library. Regenerating it from the same configuration
produces byte-identical source (deterministic field order, no timestamps, no map iteration in output).

## Alternatives rejected

**A Go source file holding the values directly (`var nodes = map[string]Node{…}`).** Simplest to
generate and needs no separate document. Rejected: it re-welds the value to the build. Changing a
model would be a code edit and a recompile — undoing ADR-004 — and the "document" could never be read
or written by anything but a Go toolchain, so a later Python target or an operator's script is locked
out. It fails the **evolvability (L5)** test to save **implementation cost (L8)**: a forbidden trade.

**A binary/protobuf document.** Compact and schema'd. Rejected on **UX/review (L3)**: the whole point
is that an operator reads and hand-edits the resolved values and a reviewer sees them in the PR. A
binary blob is neither diffable nor hand-editable, so it defeats H2 (review) and the operator loop.

**`map[string]any` accessors over a plain JSON read (no codegen).** Less generated code. Rejected on
**stability (L2)**: a mistyped node name or a missing field would then fail at *runtime* in the
customer's process instead of at *build*. A typed accessor moves that failure left to the compile the
customer already runs — the safe direction, paid for in generated code (L8), the cheapest axis.

## Consequences

- **Positive.** The document is reviewable and editable as data; the accessor makes a bad reference a
  build failure, not a production incident; regeneration is byte-identical; no dependency is added.
- **Negative.** Two generated files instead of one, and a JSON schema (`heros.agentcfg/v1`) that is now
  a public contract — mitigated by `schema` versioning the document and by the additive-only discipline
  the rest of the platform already follows (expand-contract, never a breaking rename).
- **This ADR fixes format only.** *When* the resolver reads the document, and the fail-static ordering,
  are ADR-004's; the eval-time pinning and reconciliation are P10 §9.
