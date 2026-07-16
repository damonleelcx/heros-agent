# P1 Discovery — Design: `llm-eval.yaml` schema & arg-map (resolves PRD Q1, Q5)

> **Task:** P1 `tasks.md` §2.2. **Phase:** ② Design (Backend lead, **AI Eng owns arg-map fidelity**).
> **Inputs:** [call-shape catalog §3–§4 (W1–W12)](02-go-call-shape-catalog.md),
> [signature registry `ArgLocator`](04-signature-registry.md), [invariants I4/I5](03-discovery-invariants.md).

## §0 TL;DR

`llm-eval.yaml` is the user's declaration of **in-house LLM wrappers** the signature registry can't see
(the `Complete()`/`Generate()`/`svc.Summarize()` case — catalog W1–W3). It is a co-equal detection source
(D1): each declared entrypoint is resolved to a call site exactly like a registry row, using the **same
`ArgLocator` forms**. **Q1 resolves to: support all four locator forms (index, name, field-path,
option-constructor), not positional-only.** **Q5 resolves to: the file is optional-but-recommended — its
absence is valid and loudly surfaced in the run report, not a hard failure.**

## §1 The problem (PRD Q1 restated)

PRD Q1: *"how are argument positions/names mapped to IR metadata fields for a declared wrapper —
positional index, param name, or both?"* The catalog answered empirically: **both, plus two more.** Real
wrappers pass the model as a struct field (`Do(ctx, Req{Model:…})`, W6), a functional option
(`Complete(ctx, p, WithModel(m))`, W5), a named param, or a positional arg. A positional-only mapping
(the PRD sketch) mis-maps W5/W6 — which are the common shapes. So the schema must express the full locator
set. Reusing the registry's `ArgLocator` (doc 04 §2.4) makes declared and registry-detected entrypoints
**one resolution code path**, not two — avoiding the "two parallel implementations drift" bug the
designpattern library warns about.

## §2 Schema

```yaml
# llm-eval.yaml — user-declared LLM entrypoints (wrappers the signature registry cannot see).
version: "1.0.0"                     # schema semver; validated at load

entrypoints:
  # W2 — free-function wrapper: func Complete(ctx context.Context, prompt string, opts ...Option) (string, error)
  - symbol: "github.com/acme/app/internal/llm.Complete"   # import-path-qualified func (§3.1)
    provider: "anthropic"            # optional static hint -> node.model.provider; omit => unresolved
    args:
      model:  { option: "WithModel" }        # functional option (W5)
      prompt: { name: "prompt" }             # by parameter name
      tools:  { option: "WithTools" }

  # W1 — method on an in-house service: func (s *Service) Summarize(ctx, doc Document) (Summary, error)
  - symbol: "github.com/acme/app/internal/llm.(*Service).Summarize"   # receiver-qualified method (§3.1)
    provider: "openai"
    args:
      model:  { field: "s.cfg.Model" }       # model bound on the receiver (W4) — field path on the receiver
      prompt: { field: "doc.Body" }          # struct-field path on a struct arg (W6)
      tools:  { const: [] }                  # this wrapper binds no tools; declare it explicitly

  # W9 — generic gateway: one call site, model chosen at runtime by req.Model
  - symbol: "github.com/acme/app/internal/gw.(*Gateway).Do"
    args:
      model:  { field: "req.Model" }         # readable, but often a runtime var => extractor marks unresolved (I5)
      prompt: { field: "req.Messages" }
    # no `provider:` => provider unresolved; run report flags this node as a P5 dynamic-trace candidate

  # Declaration with unknown args — still declares "this is an LLM call site" so the node is counted.
  - symbol: "github.com/acme/app/internal/ai.RunAgent"
    detect_only: true                        # emit the node; mark model/prompt unresolved + flag (I5)
    invocation: loop                          # optional: user asserts this is a loop/agent => variable_at_runtime=true
```

### 2.1 Field reference

| Field | Required | Meaning |
|---|---|---|
| `version` | ✅ | Schema semver. Validated at load; unknown MAJOR → fail-loud at config time ([doc 08](08-failure-behavior.md)). |
| `entrypoints[]` | ✅ | The declared call sites. Empty list is legal (means "no wrappers"). |
| `entrypoints[].symbol` | ✅ | Import-path-qualified func or receiver-qualified method (§3.1). |
| `entrypoints[].provider` | — | Static `node.model.provider` hint; omit ⇒ provider unresolved. |
| `entrypoints[].args.{model,prompt,tools}` | — | An **`ArgLocator`** (doc 04 §2.4) — one of `index` / `name` / `field` / `option` / `const`. Omit a target ⇒ that field is `unresolved` + flagged. |
| `entrypoints[].detect_only` | — | `true` ⇒ count the node but resolve nothing (all fields `unresolved` + flag). For opaque wrappers. |
| `entrypoints[].invocation` | — | `single`\|`loop`\|`conditional`; user override for `invocation_semantics.type`. Default `single`; the extractor may upgrade to `loop` if it sees a surrounding loop (never downgrades). |
| `entrypoints[].streaming` | — | Marks a streaming wrapper; node semantics unchanged. |

`args` locators reuse the registry's `ArgLocator` (doc 04) verbatim, plus one declaration-only form
`const:` (the user asserts a literal value, e.g. `tools: {const: []}`) for wrappers that bind a fixed value.

## §3 Key decisions

### 3.1 Decision — `symbol` is import-path-qualified, receiver form `(*T).Method`
**Problem.** A bare `Complete` is ambiguous across packages; a method needs its receiver type.
**Design.** `symbol = <import-path>.<Func>` or `<import-path>.(*Recv).Method` (mirrors `go doc` / stack-trace
syntax Go devs already know). The import path is resolved against the repo's module (`go.mod`).
**Alternatives compared.** (a) *Bare `pkg.Func`* — rejected: collides across modules, can't express methods.
(b) *File+line pointer* — rejected: not stable across edits (the exact churn the node-ID scheme avoids,
[doc 06](06-node-id-scheme.md)), and user-hostile to author. **Effect.** A user writes the same symbol
string they'd see in a stack trace; it resolves deterministically and survives line shifts.

### 3.2 Decision (Q5) — the file is optional-but-recommended; absence is surfaced, not fatal
**Problem.** PRD Q5: hard-fail if `llm-eval.yaml` is absent, or optional? **Design.** Optional. If absent or
empty, discovery runs on the registry alone and the **run report states prominently**: "0 declared
entrypoints — wrapper coverage may be incomplete; N call sites detected by registry only." **Why
appropriate (cost law).** Forcing a mandatory file on every repo — including repos with **no** wrappers —
raises user-operation complexity (level 3) to buy implementation simplicity (level 8): a forbidden
up-level trade (L3 > L8). Honesty is preserved at level 2/4 by the report warning, not by blocking the
user. **Alternatives compared.** *Hard-fail when absent* — rejected: punishes the direct-SDK repo that
needs no declarations, and trains users to write an empty file to silence the tool. **Effect.** A
direct-SDK repo runs with zero config; a wrapper-heavy repo is *told* (via the report) that it should
declare, and can *see* the under-count. The **mechanism** stays mandatory/co-equal (D1); only the **file**
is optional. This is the precise reading of "mandatory" that doesn't violate the UX tier.

### 3.3 Decision — validate at the boundary; deploy-time fail-loud, run-time fail-open
**Problem.** A malformed file vs. a declaration pointing at a deleted symbol are different faults.
**Design.** *Structural* faults (bad YAML, unknown `version` MAJOR, malformed locator) → **fail-loud at
load**, before any discovery, with the file+line — this is the user's config error, cheapest to fix early
(部署期 fail-loud). *Semantic* faults (a `symbol` that resolves to nothing) → **skip that entrypoint,
continue, report a `declaration_diagnostic`** (运行期 fail-open) — one stale declaration must not block the
whole run. **Alternatives compared.** *Hard-fail on a missing symbol* — rejected: a repo mid-refactor
would be un-discoverable because of one stale line (L2 稳定 regression). **Effect.** Config typos are
caught instantly and unmissably; a stale declaration degrades to one visible report line ([doc 08](08-failure-behavior.md), I4).

## §4 Invariant ties

- **I4 (every missing node explainable):** a declared node that fails to resolve its symbol appears in the
  report's `declaration_diagnostics` with `status: not_found` — so "my wrapper isn't in the IR" is always
  answerable ([doc 09](09-run-report-shape.md)).
- **I5 (honest `unresolved`):** an omitted `args` target or `detect_only: true` yields the sentinel + a P5
  flag, never a guess. AI Eng owns whether a declared arg-map is *faithful enough* to drive P2 overrides;
  where it isn't, the honest move is `detect_only` + flag.
- **I6 (additive IR):** `provider`, `detect_only`, `streaming` inform extraction but only frozen node
  fields are emitted; the declaration provenance lives in the run report.

## §5 Consumed by

Implementation §3.4 (declared-entrypoint detector) and §3.5 (merge/dedup with registry hits by node-ID).
Fixture §6.2 (wrapper) toggles a single `entrypoints[]` line to prove the node appears only when declared.
