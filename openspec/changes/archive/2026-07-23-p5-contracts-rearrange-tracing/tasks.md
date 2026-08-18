# Tasks — P5: Typed I/O contracts + Re-arrangement + Dynamic tracing + Behavioral classification

## 1. System Designer + Backend — Ordering-coherence validator
- [x] 1.1 Specify `Satisfies(output_schema, input_schema) → {ok | mismatch(fields)}` — structural
      subtyping over the P0 `io_contract` JSON Schemas (consumer-required fields present + type-
      compatible in the producer output; extra producer fields permitted).
- [x] 1.2 **Share the predicate with the P2 Executor** so static validation and runtime enforcement use
      the identical rule (static/runtime parity); refactor the Executor's runtime check to call it.
- [x] 1.3 Implement `ValidateOrdering(ir, ordering, catalog) → {coherent | adapted(adapters) |
      rejected(diagnostics)}` — apply `Satisfies` to **every** producer→consumer data edge; the verdict
      is **total** (no "unknown" bucket) and **pure/deterministic**.
- [x] 1.4 Classify each mismatch **adaptable** (a catalog adapter bridges it) vs. **incoherent** (none
      does); never admit a mismatching edge as coherent without an adapter or a rejection.
- [x] 1.5 **No silently-broken reorder:** an incoherent, un-adaptable ordering is **rejected** with a
      typed diagnostic naming producer, consumer, and the mismatching field(s); it is **not persisted**
      as a runnable Variant Spec.
- [x] 1.6 A coherent (possibly adapter-augmented) ordering produces a new Variant Spec + new
      `config_hash` with lineage to the parent.
- [x] 1.7 **Validation gates transform generation (ADR-001):** the verdict runs **before any codemod is
      generated** — a rejected ordering yields no source transformation (codemod/diff/PR); only a
      coherent verdict is handed to the source-transformation engine (§2).
- [x] 1.8 Test: a known-incoherent reorder (consumer before its producer) → **rejected**, not coherent,
      and **no diff generated**; determinism — same reorder over same IR yields same verdict + adapters
      twice.

## 2. Backend — Typed adapter catalog + source-transformation engine (ADR-001)
- [x] 2.1 Define the **fixed adapter catalog**: field rename, projection, wrap/unwrap, default-fill,
      declared format coercion — each an `Adapter{kind, in_schema, out_schema, emit_codemod}` carrying
      its own `io_contract` and **emitting a deterministic codemod**, not a runtime coercion.
- [x] 2.2 Insert an adapter as an **explicit, inspectable node** on the mismatching edge in the
      resulting Variant Spec **and materialize it as a generated code change** (the adapter node's source
      is inserted and the call sites rewired) — not a hidden coercion.
- [x] 2.3 **Validate the adapter itself**: its input satisfied by the upstream producer, its output
      satisfying the downstream consumer; **refuse** any adapter that would silently drop a consumer-
      required field or lose data without flagging the loss (and generate no code change for it).
- [x] 2.4 Implement `GenerateTransform(ir, variant_spec, source)` — a **deterministic, AST-level codemod**
      that rewrites the affected call sites / node wiring to match the spec; same `config_hash` + same
      source → **byte-identical, content-hashed diff**.
- [x] 2.5 **Build-preserving gate:** a codemod that fails to compile/build the target is **rejected
      before it is proposed** (`RejectedTransform(build_error)`); no broken diff reaches the user.
      **Behavior-preserving:** only the reordered wiring + any inserted adapter change — no incidental
      edits.
- [x] 2.6 **Isolated + reviewable + revertible:** apply the codemod to a **worktree/branch** (never the
      user's working tree in place); deliver as a **reviewable diff/PR**; rollback is a single
      `git revert`. Below the P6 Autonomous level, no diff merges without human approval + the build/eval
      verification gate.
- [x] 2.7 Test: an adaptable rename → adapter inserted **as a reviewable diff that builds**, the
      transformed working copy **runs end-to-end without a runtime contract halt**; an adapter that would
      drop a required field → **refused**, not inserted; a codemod that fails to build → **rejected before
      proposal**, not applied; the diff is byte-identical on re-generation.

## 3. Frontend + Product — Interactive graph editor (unhappy path first)
- [x] 3.1 Product: design the **invalid-reorder UX first** — the mismatch legible on the offending
      edge (both nodes + specific fields), the adapter **previewed** when adaptable, the breakage
      **explained in plain language** when not. Content is the interface (name the adapter, name the
      breakage).
- [x] 3.2 Frontend: editor exposes the IR; **add/remove/reorder/swap** nodes → a **candidate** Variant
      Spec, validated through `typed-contracts` **before commit** (and before any codemod is generated);
      never silently committed broken.
- [x] 3.3 First-class states: loading / valid / **adapter-inserted** (preview the adapter **and the
      source diff it would generate** + accept/reject) / **rejected** (blocked, edge-anchored diagnostic)
      / **rejected-transform** (the codemod would not build) — each visually distinct, **not color-only**.
- [x] 3.4 **Keyboard-operable**: full add/remove/reorder/swap without a pointer; labeled controls;
      managed focus across a reorder; **screen-reader announcement** of the validation verdict.
- [x] 3.5 **Responsive on large IRs**: virtualized/canvas rendering; **incremental per-edge
      re-validation** (< 200 ms perceived on a single reorder), not whole-graph re-validation.
- [x] 3.6 A committed edit produces a new Variant Spec with **lineage + diff** for P4 comparison **and a
      reviewable source diff (an AST-level codemod rewriting node wiring)** that must **build** before it
      is proposed and is applied on an isolated worktree/branch, never the user's working tree in place.
- [x] 3.7 Verify: drive the editor against a live IR — reorder into an incoherent state (blocked +
      legible, **no diff generated**), into an adaptable state (adapter + **generated diff** preview,
      committed reorder emits a reviewable diff that builds and whose transformed copy runs with no
      contract halt), and perform every edit **by keyboard only**.
- [x] 3.8 **Dual-orientation view (FR13a):** the editor supports **both a vertical and a horizontal**
      node-graph layout, user-toggleable and **persisted**; arrow-key navigation follows the active
      orientation (Up/Down vs. Left/Right), `aria-orientation` reflects it, the change is
      **screen-reader-announced**, and both orientations carry the **same first-class validation states**
      (not color-only). Verify both orientations in a browser, keyboard-only.
- [x] 3.9 **Drag-and-drop (FR13b):** nodes are **draggable** in both orientations, as an **addition to —
      not a replacement for** — keyboard operation (WCAG 2.5.7). A drop produces the same **candidate**
      validated through `typed-contracts` **before commit** (never silently commits broken), surfaces the
      same rejected/adapter states, shows a **not-color-only** drop indicator, and **announces** the drop
      verdict. Verify a drag into an incoherent state is blocked in the browser.
- [x] 3.10 **Non-graph agent adaptation (FR13c):** when the IR has nodes but no framework edges,
      **recover topology** (P4.5 `linkage`: shared-state / trace inference), write edges back additively
      with **provenance + confidence**, and render them as **inferred hypotheses (not framework-certain)**;
      the validator checks a recovered edge exactly as a framework edge. **Verified in the browser against
      the real hermes-agent `call_llm` cluster** (5 nodes, 10 recovered edges → coherent / adapter /
      rejected states on real node IDs).
- [x] 3.11 **Python adapter emission (FR7b):** the adapter catalog emits a **reviewable Python source
      change** (in addition to Go), so a Python workflow (hermes-agent) receives a real generated adapter
      instead of a "language unsupported" refusal; an unsupported language is still **refused with a named
      reason**. Verified: the generated Python adapter **parses and runs**, renaming `answer→response`
      end-to-end.
- [x] 3.12 **Arrangement explorer (FR13d):** `internal/arrangements` enumerates orderings, validates +
      **scores** each, and ranks them **approved-first, then by score**; enumeration is **bounded and the
      bound is surfaced** (considered vs. n!, never a silent cap). Deterministic; unit-tested.
- [x] 3.13 **Live streaming discovery + animation (FR13d):** the `orderings/stream` endpoint emits
      NDJSON (meta → one line per arrangement as discovered → done, flushed live); the editor **animates**
      each arrangement into its ranked slot on arrival, keeps the DOM bounded (top-N drawn, the rest
      counted), and lets the user **apply any arrangement by keyboard or click**. Verified in the browser
      against hermes-agent (120 orderings → 1 approved on top, 119 rejected by score) and the compact
      fixture.

## 4. Backend + DevOps — Dynamic-tracing interceptor
- [x] 4.1 Implement an **OTel-style interceptor** wrapping the signature-registry SDK entrypoints;
      extend the P2.5 substrate (GenAI semantic conventions), correlate to spans.
- [x] 4.2 Log **every real LLM call, its inputs, and its call stack**, tagged with the P0 tag set
      `{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}`.
- [x] 4.3 **Passive + async**: the interceptor never alters the run's outputs; logging is best-effort so
      a logging failure never fails the run. Assert **identical outputs traced vs. untraced**.
- [x] 4.4 **Redact secrets/PII**: inputs stored as content-hashed blobs; secrets from the manager only,
      never in trace logs, stacks, or the reconciliation report. Instrumented run executes in the P3
      sandbox with no ambient credentials.
- [x] 4.5 Bound interceptor overhead (target < 5% wall-clock, no added provider calls).

## 5. Backend + AI Engineer — Reconciler (static candidate ↔ real run)
- [x] 5.1 Match each observed call to a **static candidate**; mark candidates **confirmed** /
      **unconfirmed** and observed calls **matched** / **runtime-only**.
- [x] 5.2 Surface a **runtime-only edge/node static analysis missed** (conditional branch, loop-back,
      wrapper dispatch) and reconcile it into the IR **additively**.
- [x] 5.3 Map one **static definition** to its **many runtime invocations** (`invocation_index` 0..n−1)
      — a loop is one definition with n invocations, never n definitions.
- [x] 5.4 **Reproducibility**: a `{config_hash, seed}` traced run reconciles to the same
      confirmed/unconfirmed/runtime-only verdicts; the report is content-addressed.
- [x] 5.5 Test: the conditional-router fixture flags the branch edge **runtime-only** and adds it
      additively; the loop fixture yields one definition ↔ n invocations.

## 6. AI Engineer — Behavioral pattern confirmation + anti-pattern detection
- [x] 6.1 **Rules over trace signatures** (rules-first, same discipline as P3.5/P4.5): iteration count
      > 1 on a self-edge → **Reflection**; planning node's task list consumed downstream → **Planning**;
      sample-N-then-vote → **Self-Consistency (Reasoning Techniques)**; memory R/W between turns →
      **Memory Management**; human-approval pause → **HITL**.
- [x] 6.2 **Upgrade** the matching P3.5 structural candidate to a **confirmed** label
      (`source = behavioral`) at the same `ir_version` MAJOR (additive write-back); a one-shot self-edge
      is **not** confirmed Reflection.
- [x] 6.3 **Wire** confirmed pattern → metric-set / failure-taxonomy / eval-targeting (reuse the P3.5
      mapping — a confirmed Reflection selects iteration-count / convergence / quality-gain-per-revision).
- [x] 6.4 Constrained **LLM-as-classifier** for the ambiguous residue only (fixed 20-pattern taxonomy,
      structured output, confidence) — never overrides a confident rule/behavioral label.
- [x] 6.5 **Anti-pattern detectors** emit typed diagnoses with evidence: never-improving reflection
      loop; router sending (nearly) all traffic one way; parallelization with no real independence; plan
      never followed — consumable by P5.5.
- [x] 6.6 Test: self-edge iterating > 1 → confirmed Reflection with the right metric-set; a one-shot
      self-edge → not confirmed; the never-improving loop → typed anti-pattern with per-iteration
      quality evidence.

## 7. AI Engineer — Eval-set generation enrichment
- [x] 7.1 **Seed from real traces**: activate the P4 generator's seed-from-real-traces interface —
      mine observed trace inputs as realistic seed cases.
- [x] 7.2 **Per-path targeting**: generate cases that force each reconciled path, including
      **runtime-only edges** and loop **min/typical/max** iteration counts (feeds back into P4 coverage).
- [x] 7.3 Test: real trace inputs appear as seed cases; a per-path target generates a case that forces
      the reconciled runtime-only edge.

## 8. System Designer — Data model, lineage, IR write-back
- [x] 8.1 Extend `variant_spec` with `parent_variant_id` (lineage) and an `inserted_adapter` list;
      add `reconciliation` / `recon_node` / `recon_call` / `recon_edge`, `behavioral_label`,
      `anti_pattern` tables (expand-only migration). *(migration 0011; lineage is a standalone
      nullable ADD COLUMN `parent_config_hash`; struct fields also ride in `spec_json`.)*
- [x] 8.2 **Additive IR write-back**: reconciled runtime edges/nodes + confirmed behavioral labels
      written at the same `ir_version` MAJOR; a pre-P5 consumer still parses the enriched IR.
- [x] 8.3 **Schema refinement**: refine permissive `io_contract` schemas (`{"type":"object"}`) from
      observed trace shapes additively (tightening coherence without a schema-version break); **surface**
      which nodes remain permissive and which orderings a refinement would affect (never silent).
- [x] 8.4 Content-hash logged inputs, stacks, adapter defs, and reconciliation reports; DB holds hashes
      + tags only.

## 9. Testing & review
- [x] 9.1 Fixtures: an **incoherent-reorder** chain (consumer requires a field the swapped-in producer
      lacks); an **adaptable** pair (field rename); a **loop/self-edge** with variable runtime iteration
      count; a **conditional router** with a runtime-only branch; a **never-improving** reflection loop.
      *(each named fixture has a dedicated test: typedcontract/variantspec/api for reorder+adapter;
      reconcile for loop + router; behavioral for the never-improving loop.)*
- [x] 9.2 Typed-contract tests: incoherent reorder rejected + not runnable **+ no diff generated**;
      adaptable → explicit adapter emitted as a **reviewable diff that builds**, transformed copy runs with
      **no runtime contract halt**; drop-required-field adapter refused; verdict determinism.
- [x] 9.2a Source-transformation tests (ADR-001): a coherent reorder → **deterministic, byte-identical
      diff** on re-generation; a codemod that fails to build → **rejected before proposal**, not applied;
      the change is applied on an isolated worktree/branch (user tree untouched) and is revertible by a
      single `git revert`; the diff touches only reordered wiring + any inserted adapter (behavior-
      preserving).
- [x] 9.3 Re-arrangement UX tests: incoherent drag blocked + edge-legible + announced; adaptable drag
      previews adapter (accept/decline both never commit broken); full keyboard operation; large-IR
      responsiveness (per-edge re-validation < 200 ms). *(browser-verified: keyboard reorder, drag-and-drop,
      vertical + horizontal layouts; API tests for verdicts + 400-node < 200 ms.)*
- [x] 9.4 Dynamic-tracing tests: every LLM call logged with inputs + stack + P0 tags; router branch
      flagged runtime-only + added additively; loop → one definition ↔ n invocations; traced vs.
      untraced outputs identical.
- [x] 9.5 Behavioral-classification tests: iteration > 1 → confirmed Reflection + metric-set; one-shot →
      not confirmed; never-improving loop → typed anti-pattern with evidence.
- [x] 9.6 Eval-enrichment test: trace inputs seed the P4 generator; per-path target forces the
      runtime-only edge.
- [x] 9.7 Security: instrumented run in the P3 sandbox, no ambient credentials; no secret/PII in trace
      logs, stacks, or reconciliation reports.
- [x] 9.8 Confirm the M7 exit checklist (PRD §13) is green.
