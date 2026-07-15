# Product: North-Star Journey & Automation-Level Model (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | Product (support to System Designer) |
| Status | Draft — freeze at M0 |
| Tasks | 5.1 (north-star journey), 5.2 (automation-level model) |
| Cross-refs | PRD `P0-foundations.md` §9.5; `docs/decisions/architecture-and-lineage.md`; `docs/decisions/config-hash-spec.md`; `docs/decisions/storage-decision-record.md` |

P0 ships no UI. This is the **north star** every UI phase (P1→P6) builds toward — the outcome the
contracts must serve — plus the **automation-level model** that names the trust contracts and pins down
what each demands of the P0 lineage/reproducibility scheme. Written in the senior-product lens: *anchor
to the outcome, design the unhappy path, align the terms, and make the risky assumption explicit.*

The single risky product assumption, stated up front:

> **Users will trust an automated change to an LLM workflow ONLY if it is verified and reversible.**
> This is *why* the P0 lineage/reproducibility scheme is a product requirement, not just an engineering
> one — `config_hash` lineage is what makes an automated change auditable and a bad one revertible.

---

## 1. Terminology (name dictionary — one source of truth)

Shared vocabulary so UI, docs, and API never drift. (Backend/System-Designer terms are canonical here.)

| Term | Means | Not |
|---|---|---|
| **Workflow** | The user's discovered LLM call graph (the IR). | A single prompt. |
| **Node** | One static LLM call site in the workflow (IR `node_id`). | A runtime invocation (a node may run many times). |
| **Variant** | A stable, human-named configuration of the workflow the user edits over time. | An immutable run — that's a `config_hash`. |
| **config_hash** | The immutable, content-defined identity of one fully-resolved configuration. | A `variant_id`; one variant → many `config_hash`. |
| **Run** | One execution of a variant over an eval set, multi-seed. | A single model call. |
| **Proposal** | A concrete, diff-shaped change the engine suggests (a PR). | An opinion; a proposal carries evidence. |
| **Automation level** | The trust contract governing who applies a proposal (Advisory / Assisted / Autonomous). | A feature flag. |

## 2. North-star user journey (task 5.1)

The through-line: **import → inspect → configure → run → compare → diagnose → apply.** Seven steps; each
names the user's job, what they see, the **unhappy path** (designed, not left to chance), the P0 contract
it leans on, and the phase that builds it.

```mermaid
graph LR
  I[1 Import<br/>point at a repo] --> N[2 Inspect<br/>see the LLM graph]
  N --> C[3 Configure<br/>override a node]
  C --> R[4 Run<br/>variants, multi-seed]
  R --> P[5 Compare<br/>leaderboard + CIs]
  P --> D[6 Diagnose<br/>why it fails]
  D --> A[7 Apply<br/>ship a verified PR]
  A -. new baseline .-> N
  classDef s fill:#1f6feb22,stroke:#1f6feb; class I,N,C,R,P,D,A s;
```

| # | Step | User's job | What they see | Unhappy path (designed) | Leans on (P0) | Built in |
|---|---|---|---|---|---|---|
| 1 | **Import** | Point the tool at a codebase. | "Discovering LLM call sites…" then a node count. | No LLM calls found / unsupported language → say so plainly, link what's supported; never a blank graph. | Workflow IR (`commit_sha` pins the import). | P1 |
| 2 | **Inspect** | Understand the graph. | The node/edge graph; per-node model/prompt/tools; pattern labels. | A node whose I/O couldn't be inferred shows a **permissive/low-confidence** badge, not a false-precise schema. | IR nodes + typed `io_contract` + reserved `pattern_labels`/`subgraphs`. | P1, P3.5 |
| 3 | **Configure** | Override a node (model / prompt / context) to make a variant. | An override panel; the variant's identity updates. | Overriding to an incoherent wiring is flagged by the reorder validator (P5), not silently run. | Config Layer → Variant Spec → `config_hash`. | P2, P5 |
| 4 | **Run** | Execute variants over an eval set, multi-seed. | Progress; live per-node/case results. | No eval set yet → the generator offers to synthesize one and **shows its difficulty/diversity** so a weak set is visibly weak. | Runtime keyed by `{config_hash, seed}`; seven-tag events. | P2, P4 |
| 5 | **Compare** | Decide "is B actually better than A?" | Leaderboard: mean **± confidence interval**, cost, latency; a **tie** when CIs overlap. | Noisy/underpowered result → shown as a **tie or low-confidence**, never a false "winner". | Per-seed tags → CIs; `config_hash` = honest A-vs-B. | P4 |
| 6 | **Diagnose** | Understand *why* it fails. | Failure clusters (sized), per-node attribution, diagnosis cards with the **failing cases attached as evidence**. | An unsupported diagnosis is withheld, not guessed — *diagnosis proposes, verification decides*. | Tag-set slices (per-node/case/cluster); replayable lineage. | P4.5 |
| 7 | **Apply** | Ship the fix at a chosen trust level. | A **proposal = a reviewable PR** with its verification evidence; apply / revert. | A proposal that didn't beat baseline on held-out data is **never surfaced as apply-ready**. | `config_hash + seed` replay = the verification gate; git = audit trail + rollback. | P5.5, P6 |

**Two product invariants the journey encodes** (from the interaction-simplicity-first principle):

- **Evidence travels with every claim.** No leaderboard row, diagnosis, or proposal appears without its
  CI / attribution / verification result attached. "Trust me" is never the UI.
- **Reduce required input.** Discovery infers the graph (no manual wiring); the generator synthesizes an
  eval set when none exists (no mandatory hand-authoring); defaults are chosen and overridable — the user
  is never asked for something the system can infer or replay.

## 3. Automation-level model (task 5.2)

Applying a proposal is a **trust decision**, so it is governed by an explicit, per-workflow
**automation level**. Three levels — each a different *trust contract*, each with a hard requirement on
the P0 lineage/reproducibility scheme.

```mermaid
graph LR
  ADV["Advisory<br/>engine reports · human applies"] --> ASS["Assisted<br/>one-click apply a VERIFIED proposal"]
  ASS --> AUT["Autonomous<br/>bounded closed loop · audit + kill switch"]
  classDef s fill:#2da44e22,stroke:#2da44e; class ADV,ASS,AUT s;
```

| Level | What the engine does | What the human does | Requires from lineage/reproducibility |
|---|---|---|---|
| **Advisory** | Discovers, runs, compares, diagnoses; **proposes**. Applies nothing. | Reads evidence; applies (or not) by hand. | **Auditability:** every proposal cites a replayable `config_hash + seed` so the human can independently re-run and confirm before touching anything. |
| **Assisted** | Everything in Advisory, **plus** a one-click apply of a proposal **already verified on held-out data**. | Clicks apply on a specific, evidence-backed proposal; reviews the PR. | **+ Reversibility:** apply is a git commit (a PR); `git revert` is rollback. The pre-apply state is a known `config_hash`, so "undo" returns to an exact prior configuration. |
| **Autonomous** | A **bounded closed loop**: propose → verify → apply within explicit budgets/guardrails, logging every step. | Sets the bounds; monitors the audit trail; can hit the **kill switch**. | **+ Full audit trail + kill switch:** every automated change is an immutable `config_hash` lineage entry (who/what/why/evidence); the loop is bounded and instantly haltable; any change is revertible via git. |

**The gate is the same at every level: *verification decides.*** No proposal — advisory suggestion or
autonomous action — is ever "apply-ready" unless it beat the baseline on **held-out** data under the
multi-seed + CI comparison. Automation changes *who clicks apply*, never *whether evidence is required*.

**Escalation is the user's choice, and reversible.** A workflow starts **Advisory**; the user opts up to
Assisted, then Autonomous, as trust grows — and can drop back a level at any time. We never auto-escalate
a user into more automation than they chose (interaction-simplicity + safety over convenience).

**Why this is a P0 concern.** Naming these levels now shapes what the IR and lineage must expose: an
Autonomous change is only defensible because `config_hash` lineage (FR12–FR14) makes it **auditable** and
**reversible**. If the lineage scheme couldn't reconstruct exactly what changed and roll it back, the
Autonomous trust contract would be unbuildable — so the product requirement is booked against P0, not P6.

## 4. Acceptance criteria (north-star fit checks for later phases)

- [ ] Every journey step has a **designed unhappy path** (empty/low-confidence/unsupported), never a blank
      or false-precise screen.
- [ ] No **claim** (leaderboard row, diagnosis, proposal) renders without its **evidence** (CI /
      attribution / verification) attached.
- [ ] "B beat A" is only shown when it is **statistically real** (CIs don't overlap); otherwise a **tie**.
- [ ] Every **proposal** is a reviewable diff (PR) with a replayable `config_hash + seed` and its
      held-out verification result.
- [ ] Each workflow has an explicit **automation level**; Autonomous exposes an **audit trail + kill
      switch**, and any applied change is **git-revertible** to a prior `config_hash`.
- [ ] The user is never asked to input what the system can **infer** (graph) or **replay** (a run).

## 5. Dependencies & open questions

- **Unblocks:** every UI phase (P1 inspect, P2 configure, P4 compare, P4.5 diagnose, P5.5/P6 apply) builds
  a slice of this journey; P6's Autonomous loop rests entirely on the §3 Autonomous trust contract.
- **OQ (Product):** the *default* automation level for a new workflow — leaning **Advisory** (earn trust
  before automating), confirmed in P6.
- **OQ (Product):** granularity of Autonomous bounds (per-workflow vs per-node budgets; which guardrails
  are hard vs advisory) — defined with P6.
