# P1 Discovery — Design: Failure behavior per fault

> **Task:** P1 `tasks.md` §2.5. **Phase:** ② Design (Backend lead). **Inputs:**
> [invariants I4/I5/I7](03-discovery-invariants.md), [failure sources across docs 04–07](README.md).
> **Discipline:** every fault has **one decided outcome** — never a crash, never a silent drop
> (I4/I7). Three policies only.

## §0 The three policies

| Policy | When | Behavior | Timing posture |
|---|---|---|---|
| **P-A · fail-loud-at-config** | The user's *configuration* is structurally wrong (before any repo analysis). | Stop with a precise error (file+line); emit nothing. | 部署期 fail-loud — cheapest to fix, must be unmissable. |
| **P-B · skip-and-report** | A *structural* fault in one unit (a file, a package, a declaration) that must not block the rest. | Skip that unit, record a diagnostic, continue everything else. | 运行期 fail-open — one bad unit ≠ dead run. |
| **P-C · mark-unresolved-and-flag** | A *value* can't be resolved statically (model/prompt/tools). | Emit the frozen node with an **`unresolved` sentinel**, add a P5 ambiguity flag with a reason. | Honest gap > confident guess (I5). |

The split is arbitrated by the cost law: config errors are the user's and caught early (P-A); a structural
fault in one file must not drop *all* nodes from the run (P-B protects L2 稳定 / L4 运维); an unresolvable
value must never become a confident-wrong value (P-C protects L2 稳定 downstream — a wrong prompt misleads
P2/P4 worse than an honest gap).

## §1 The decision table

| # | Fault | Policy | IR effect | Run-report effect | Test that must go red |
|---|---|---|---|---|---|
| F1 | **Unparseable Go file** (syntax error, invalid UTF-8) | **P-B** | file contributes no nodes; other files unaffected | `file_diagnostics[]: {file, severity:error, code:PARSE_ERROR, msg}` | §6.5 malformed-file fixture: broken file skipped, **other files still discovered**, diagnostic present |
| F2 | **Malformed `llm-eval.yaml`** (bad YAML, unknown `version` MAJOR, malformed locator) | **P-A** | none emitted | n/a (fails before run) | load-time test: bad file → precise error at file+line, exit non-zero, **no IR written** |
| F3 | **`llm-eval.yaml` symbol not found** (declares a func that doesn't exist / was renamed) | **P-B** | that entrypoint yields no node; others unaffected | `declaration_diagnostics[]: {symbol, status:not_found}` | test: stale declaration → run completes, node absent, diagnostic explains why ([doc 05 §3.3](05-llm-eval-yaml-schema.md)) |
| F4 | **Import cycle / unresolvable import** in target | **P-B** | affected package's type resolution degrades; call sites it *can* resolve still emit; unresolved ones → P-C | `file_diagnostics`/`package_diagnostics: {code:TYPE_RESOLUTION_PARTIAL}` | test: package with a broken import → discovery continues, partial nodes, diagnostic present, **no panic** |
| F5 | **Symlink cycle / huge directory** in repo walk | **P-B** | loader skips the cyclic path | `file_diagnostics: {code:SYMLINK_CYCLE_SKIPPED}` | robustness test: symlink loop → bounded walk, skipped-path diagnostic, no infinite loop |
| F6 | **Unresolved model arg** (runtime var, construction/env-bound, `detect_only`) | **P-C** | `model.provider="unresolved"`, `model.model_id="unresolved"`, `params={}` (sentinel) | `ambiguity_flags[]: {node_id, field:"model", reason, p5_candidate:true}` | test: model = runtime variable → sentinel emitted, flag with reason, **never a guessed model** |
| F7 | **Unresolved prompt** (inter-procedural assembly, `text/template`, opaque `[]byte` body) | **P-C** | `prompt.inline=""` (sentinel) + `variables:[]` | `ambiguity_flags: {node_id, field:"prompt", reason}` | test: Bedrock `InvokeModel` body → prompt sentinel + flag; Anthropic literal → resolved (contrast) |
| F8 | **Huge literal / deeply-nested expression** (resolution DoS) | **P-C** (+ bounded budget) | field over-budget → sentinel | `ambiguity_flags: {reason:"resolution_budget_exceeded"}` | robustness test: deep nesting / megabyte literal → bounded time+memory, field flagged, **no OOM/stack-overflow** |
| F9 | **Unknown framework version** | **degrade-to-flag** (a P-B/P-C blend, [doc 07 §3.2](07-framework-reader.md)) | reads structurally-certain nodes/edges; uncertain parts → sentinel; **no call-order inference** | `framework_subgraphs[]: {framework, version, recognized:false, degraded:true}` | test: bumped framework version → partial subgraph, degraded flag, **no crash, no mis-inferred edges** |
| F10 | **FrameworkReader panic** (reader bug) | **P-B** (recover) | that subgraph absent; rest of run intact | `framework_subgraphs: {status:reader_error}` | test: a reader that panics → recovered to diagnostic, discovery completes |
| F11 | **Would-execute target code** (any path that could run repo code) | **hard structural block** (I1) | — | — | §7.1: `init()` side-effect never fires; spawn denied — this is not a runtime fault, it's a build-time impossibility |
| F12 | **Duplicate detection** (registry + declared hit the same call site) | **not a fault** — merge | one node (dedup by node-ID, [doc 06](06-node-id-scheme.md)) | `dedup_merges[]: {node_id, sources:[registry, declared]}` | §6.6 dedup fixture: one node, both sources credited |

## §2 Key decisions

### 2.1 Decision — malformed config fails loud (P-A), a stale declaration fails open (P-B)
**Problem.** F2 and F3 are both "bad `llm-eval.yaml`," but one is a *structural* error and one is a *stale
reference*. **Design.** Structural (F2) → P-A (block at load); stale symbol (F3) → P-B (skip one entry,
continue). **Alternatives compared.** *Treat both as hard-fail* — rejected: a repo mid-refactor with one
renamed wrapper would be undiscoverable (L2 稳定). *Treat both as skip-and-report* — rejected: a genuinely
malformed YAML would silently run with **zero** declarations, hiding a config error as "no wrappers found"
(a silent-drop-class failure, I4). **Effect.** The user's typo is caught instantly; the user's stale
reference degrades to one visible report line.

### 2.2 Decision — value faults never fall back to a guess (P-C, not "best-effort inference")
**Problem.** F6–F8 tempt a "probable value" heuristic. **Design.** Always sentinel + flag; never infer.
**Alternatives compared.** *Emit a best-effort probable model/prompt* — rejected under D3/I5: a
confident-wrong value corrupts P2 overrides and P4 attribution, and the honest gap is exactly what P5
dynamic tracing consumes. **Effect.** Every unresolved field is a typed, reasoned P5 candidate, not a
liability.

### 2.3 Decision — a bounded resolution budget converts a DoS into a P-C flag (F8)
**Problem.** Adversarial/huge source could make intra-procedural resolution run unboundedly (NFR5, an
attack surface since source is untrusted). **Design.** Resolution has a **bounded budget** (depth + node
count, the concrete numbers are the PRD Q3 tuning left to §4.1 implementation); exceeding it is not an
error — it's F8, sentinel + `reason:"resolution_budget_exceeded"`. **Effect.** Hostile input degrades to an
honest `unresolved`, never a hang or OOM (I7).

## §3 Invariant ties (this table *is* the enforcement of I4/I5/I7)

- **I4** — every row that removes/omits a node (F1, F3, F4, F5) writes a diagnostic ⇒ every missing node is
  explainable from the report. F12 records merges so a "vanished" node is traceable to its survivor.
- **I5** — F6/F7/F8/F9 all route through the sentinel + flag path; the sentinel is one documented constant
  (ratified here: `provider="unresolved"`, `model_id="unresolved"`, `prompt.inline=""` with a report flag).
- **I7** — F1/F4/F5/F8/F10 all "degrade, never crash"; each has a red-able robustness test.

## §4 Consumed by

Implementation §4.1–§4.2 (extractor + ambiguity flags realize P-C), §3.1–§3.2 (loader/parser realize
P-B/F1/F4/F5), §3.4 + config load (P-A/F2, P-B/F3), §4.4 (F9/F10). The report fields named here are
specified in [doc 09](09-run-report-shape.md).
