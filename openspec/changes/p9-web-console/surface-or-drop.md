# Surface-or-drop — the disposition of every unrendered read-model field (P9 §1.3, R12)

> **Why this file exists.** A field the platform computes and nothing renders is one of exactly two
> things: **information the customer needs and is not getting**, or **contract surface nobody is
> maintaining**. Both cost something, and the cost stays invisible until someone asks why the number
> they need is in the API response but not on the screen. R12 therefore requires that every field be
> **rendered** or **recorded here as deliberately unrendered, with a reason and an owning phase**.
>
> **How the list was produced.** Mechanically, not by reading: every `json:"…"` tag reachable from the
> read model each legacy page consumes was extracted from the Go source and searched for in that
> page. The result is below. The same extraction becomes the standing guard (§5 / §9), so a field
> added to a Go view type in a later phase cannot land silently unread.

**Sources scanned:** `internal/evalboard/view.go`, `internal/evalrun` `SpendReport`/`Budget`,
`internal/evalharness` `JudgeStanding`, `internal/patternclassifier` `GraphView`/`Diagnostic`,
`internal/telemetry` `RunMonitor`, `internal/api/p2.go` `runView`/`transformView`/`submitResult`.

---

## 1. Corrections to the inventory (§1.1 outcome)

The mechanical pass agreed with [`feature-inventory.md`](feature-inventory.md) on every field it
listed, and found **two it missed** and **one it overstated**.

| # | Finding | Disposition |
|---|---|---|
| **New** | `transformView.variant_commit` — the exact commit the diff was produced from. The page renders `variant_branch` and never the commit, so a diff is attributable to a branch but not to a revision. | Inventory amended: **P2-27**. Decision below. |
| **New** | `UnmeasuredView.variant_id` — the excluded-variants table renders label and reason only, so an excluded variant cannot be looked up. | Inventory amended: **P4-44**. Decision below. |
| **Overstated** | `View.runs_enqueued` was not in the inventory's unrendered list, and correctly so — `p4board.html:518` renders it in the leaderboard subtitle. Recorded here only so a future reader does not re-open it. | No change. |

Two fields returned a **false positive** in the mechanical pass and are genuinely rendered:
`judge.floor` (the substring `floor` also occurs in the page's *seed floor* copy) and `ViewLabel.source`
(rendered at `p35graph.html:236`). `Diagnostic.source` is **not** rendered — the diagnostics row prints
`stage`, `subgraph_ref`, `raw_pattern` and `reason` only — so the inventory's P35-19 stands.

---

## 2. The decisions

**Owner column** names the phase that owns the *field*, not the phase that renders it. Every "surface"
decision below is dischargeable by P9 with no platform change, which is what makes it a P9 task
(§8.7) rather than a request filed elsewhere.

### Eval board — `internal/evalboard`, `evalrun.SpendReport`, `evalharness.JudgeStanding`

| Field | Decision | Reason | Owner | Task |
|---|---|---|---|---|
| `spend.budget` (`total_usd`, `judge_usd`, `generation_usd`, `max_judge_calls`, `max_generation_calls`) | **Surface** | The board says *budget cap reached* and never says what the cap was, so the reader cannot tell how close they are until they have hit it. Rendered beside the spend total, per kind where the cap is per kind. | P4 | 8.7 |
| `spend.eval_run_id` | **Surface** | Lineage. Spend without the eval run that produced it cannot be reconciled against anything. Rendered as provenance on the spend panel. | P4 | 8.7 |
| `DimensionView.uncovered[]` | **Surface** | Coverage renders a percentage; this is the list of **what is actually uncovered** — the actionable half. Rendered as a disclosure inside each dimension's meter. | P4 | 8.7 |
| `ComponentView.raw_ci_low` / `raw_ci_high` | **Surface** | The composite shows its interval while components show a bare point estimate, which implies more precision than exists. FR31 forbids a figure without its qualifier. | P4 | 8.7 |
| `ComponentView.unit` | **Surface** | A raw value rendered unitless is unreadable — `0.021` is a cost, a rate or a latency depending on a field the page already has. | P4 | 8.7 |
| `judge.percent_agreement` | **Surface** | The Go source states why it is returned: *kappa alone is unreadable when the label distribution is skewed*. It exists to be rendered next to κ. | P4 | 8.7 |
| `judge.floor` | **Rendered** | Already on the page. Listed for completeness. | — | — |
| `coverage.low_confidence` | **Surface** | Currently reachable only by inference from `reasons[]`. It is also the flag R14's reservation keys on — a board the server marked low-confidence must not render its leader with the settled-result emphasis. | P4 | 8.7 |
| `progress.seed_floor` | **Surface** | Rows are marked *provisional — below the seed floor* without the floor ever being named, so the reader cannot tell how far below. | P4 | 8.7 |
| `gate_set` | **Surface** | Which gate set produced these pass/fail outcomes. A gate result whose gate set is unnamed is unattributable, and gate sets are switchable. | P4 | 8.7 |
| `Row.variant_id` | **Surface** | The row's canonical identity, and the key the variant scorecard route is addressed by (§7.4). Rendered as the row's identity and link target rather than as a column of noise. | P4 | 8.7 |
| `UnmeasuredView.variant_id` **(new)** | **Surface** | An excluded variant that cannot be looked up cannot be fixed. The exclusions table is the one place a reader goes *because* something is missing. | P4 | 8.7 |
| `state === 'complete'` | **Surface** | `complete` has no distinct rendering today, so *finished* looks like *in progress with nothing left to do* — the two have opposite next actions. | P4 | 8.7 |
| `ParetoPoint.composite` | 🚫 **Deliberately dropped** | The composite for the same variant is already the leaderboard's Score column, and the frontier's axes are deliberately **raw** quality / cost / latency. Putting the composite on the frontier invites reading the frontier as a ranking — which is exactly what a Pareto frontier is not, and what P4's tie rule exists to prevent. The field stays in the read model (it is not P9's to change); the console does not render it. | P4 | — |

### Pattern graph — `internal/patternclassifier.GraphView`

| Field | Decision | Reason | Owner | Task |
|---|---|---|---|---|
| `ViewNode.symbol` | **Surface** | The node box shows a truncated `node_id` and model. The symbol is the source-level name the engineer actually recognises when matching the graph to their repository. | P3.5 | 8.7 |
| `ViewNode.policy` | **Surface** | The context policy is one of P2's four override dimensions, so it is directly actionable from the surface that shows it. | P3.5 | 8.7 |
| `ViewNode.tools` | **Surface** | Tool count, rendered in the node detail. | P3.5 | 8.7 |
| `ViewNode.region_ids` | **Surface as behavior** | The Go comment states the intent — *so hovering a region can highlight members* — and `.nodebox.hl` is already styled for a selection no code ever applies. The honest way to render this field is the highlight it exists for, with a keyboard-reachable equivalent (R6). | P3.5 | 8.7 |
| `ViewLabel.group` | **Surface** | The taxonomy group is how a reader locates one pattern among twenty. | P3.5 | 8.7 |
| `ViewLabel.provenance` | **Surface** | The exact detector id or LLM run reference that produced the label. On this product provenance is the evidence half of a claim, not decoration — a label whose source cannot be named is an assertion. | P3.5 | 8.7 |
| `Diagnostic.source` | **Surface** | The diagnostics card exists to explain why a region is unlabelled; the source is which layer produced the explanation. | P3.5 | 8.7 |

### Live run monitor — `internal/telemetry.RunMonitor`

| Field | Decision | Reason | Owner | Task |
|---|---|---|---|---|
| `RunMonitor.config_hash` | **Surface** | Every other view leads with the config hash, and the live monitor is the one surface where *which configuration is running right now* matters most. | P2.5 | 8.7 |

### Configure / diff / run — `internal/api/p2.go`

| Field | Decision | Reason | Owner | Task |
|---|---|---|---|---|
| `transformView.variant_commit` **(new)** | **Surface** | The branch is rendered and the commit is not, so a diff is attributable to a branch but not to a revision — and a branch moves. Rendered beside the branch chip. | P2 | 8.7 |
| Per-dimension `hint` strings (page-local constant, not a read model) | 🚫 **Deliberately dropped** | They are a hard-coded array inside `p2.html`, not a platform field. The console's dimension chips carry the same explanation as help copy authored in the console (R4), so nothing is lost and no page-local data source is ported. | P9 | — |

---

## 3. The one gap that is not P9's to close

**Subject enumeration has no read model.** FR10 requires the user to **select** a workflow, run,
variant, board or transform from platform-provided data. The platform exposes **no enumeration
endpoint for any of them** — every customer route is keyed by an identifier the caller must already
hold:

```
GET /api/v1/runs/{run_id}
GET /api/v1/transforms/{config_hash}/{source_revision}
GET /api/v1/workflows/{workflow_id}/pattern-graph
GET /api/v1/workflows/{workflow_id}/eval-board
GET /api/v1/variants/{variant_id}/scorecard
GET /api/v1/workflows/{workflow_id}/proposals
GET /api/v1/customers/{customer_id}/billing
```

P9's standing constraint forbids adding a platform endpoint, and 🔴 `careful-api-creation` makes a new
endpoint a **one-way door** belonging to the owning phase. So P9 **files this** rather than growing
one, and builds selection from what it legitimately has:

1. **Subjects the session has already visited** — a console-local fact recorded by the BFF against the
   session, never a platform statistic, and never shared between sessions or tenants.
2. **Subjects reachable from a read model already on screen** — a board row carries `variant_id` and
   `config_hash`; a run record carries `config_hash` and `source_revision`; the P5.5 surface carries
   proposals; the graph carries nodes. Once one subject is known, its neighbours are navigable
   without typing (§7.5).
3. **Direct identifier entry, as an accelerator** — explicitly permitted by task 7.2 and required by
   R9's shareability, but never the *only* path.

What the console does **not** do, under any of the three, is substitute a default subject: FR10 and R8
are unaffected by this gap, and the `'wf-demo'` behavior is not ported.

**Filed as:** a read-model request against **P1/P2** (workflow and run enumeration for a tenant) and
**P4** (boards for a workflow). Recorded in PRD §14 Q7. **The decision to add an enumeration read model
is not P9's to take** — it is new platform surface area, and the phase that owns the data owns the
contract.
