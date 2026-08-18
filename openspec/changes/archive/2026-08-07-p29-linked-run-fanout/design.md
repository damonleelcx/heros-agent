# P29 — Design

## Context

The customer console has thirteen surfaces that a linked run should reach and reaches none of them.
The investigation found **six** distinct causes, not one, and they need different remedies. Sorting them
by the arbitration law (security > stability > user complexity > operations > evolvability >
extensibility > maintenance > implementation cost) is what orders the waves below: the edge defect is a
reachability failure with a security-shaped fix, so it goes first; the projection is a UX failure whose
wrong fix would be a fabricated claim, so its design spends most of its budget on refusing to guess.

### The six causes, verified in the tree

| # | Cause | Evidence |
|---|---|---|
| C1 | The two CLI paths carrying structure are not published | `deploy/k8s/overlays/prod/ingress.yaml` routes six paths to `agentd`; neither `/api/v1/workflows/{id}/ir` nor `/api/v1/workflows/{id}/source/{rev}` is among them |
| C2 | The fence that should have caught C1 exempts them | `internal/api/ingress_fence_test.go`: `if strings.HasSuffix(path, "/") { continue }`, and `runlink.WorkflowIRPath == "/api/v1/workflows/"` |
| C3 | No enumeration; the console substitutes session memory | `web/console/src/lib/subjects.ts`: *"the platform exposes no enumeration endpoint for any of them"* |
| C4 | Linked runs and executed runs are two tables with one identifier | `GET /api/v1/runs` → `configRuntime.Runs.ListForTenant` (the executor's `run`); a link lands in `run_link` |
| C5 | The axis surfaces are total build facts with no tenant projection | `/api/v1/coverage` from `transform.AxisCoverage()`; `/api/v1/change-delivery` from `changedelivery`; `/api/v1/memory` from `transform.CoverageFor("memory")`. All correct, none tenant-scoped |
| C6 | The studio catalog is process-local; billing has no account | `launch/capabilities.go:130` mounts `api.StudioMatrix{Store: reg}` with `Workflows`, `Binds` and `Runner` nil — `studio.WorkflowCatalog` is filled only by `cmd/demo` and `cmd/proof`. `billingview.Billing` returns `ok=false` when `accounts.Get(tenant)` misses |

---

## Decisions

### D1 — Close the edge hole by making the paths flat, not by widening the fence

The fence skips `/api/v1/workflows/` because an `Exact` Ingress rule cannot match a path with a variable
segment. Three ways out:

| Option | Security | Ops | Evolvability | Verdict |
|---|---|---|---|---|
| **(a) `Prefix` rule under `/api/v1/workflows/`** | publishes `commit`, `orderings`, `orderings/stream`, `validate`, `proposals/generate`, `proposals/{id}/open-pr`, `pattern-graph`, `eval-board` to the internet | free | every future `/api/v1/workflows/*` route is published by default, forever | ❌ level-1 violation traded for level-8 convenience |
| **(b) Traefik regex middleware** | precise | a matcher that lives in an annotation, is not covered by the repo's fence, and differs per ingress controller | pins the deployment to Traefik semantics | ❌ level-4/5 cost, and the fence still cannot read it |
| **(c) Flat paths; identifiers in the body** | each route is one `Exact` rule, exactly as `run-links` and `whoami` already are | free | a new machine route is a new flat name and a new Exact rule — the existing, understood pattern | ✅ |

**(c).** `POST /api/v1/workflow-ir`, `PUT|DELETE /api/v1/workflow-source`,
`POST /api/v1/proposal-verdicts`, `POST /api/v1/transform-receipts`. The workflow id, source revision
and proposal id move into the payloads, where they already partly are — `WorkflowIRPayload` already
carries `workflow_id` and `source_revision`, so the URL segment was duplicating the body.

Then the exemption is **deleted**, and a transport path with a variable segment becomes a *fence
failure* whose message names the remedy. This is the point: after this change there is no way to add a
machine-addressed route that the fence cannot see. Option (a) would have left the fence permanently
blind to the family it most needs to watch.

**Expand-contract, one release.** The parameterised routes stay registered and classified
`ExposureInternal`, so an older CLI reaching them from *inside* a cluster still works; they are never
published. The CLI addresses only the flat paths. The old routes are removed in the release after the
CLI floor moves, and the removal is a task, not a hope — `internal/cli` pins the platform contract
version and the server refuses a contract it does not implement.

### D2 — The verdict is computed on the customer's machine, and the platform never computes one

The projection needs, per node, "does this axis apply here?" The coverage table answers that for
(axis, language, form). It does **not** answer it for a call site's own shape — `call-site-cannot-carry-it`
covers unpacked arguments, a run-time-assembled list, a missing row locator — and that information
lives only in the source, which does not cross the boundary and must not.

So there are exactly two honest designs:

- **Send the source.** Rejected on level 1. `push-source` exists for customers who choose it, and it is
  not going to become the price of a graph.
- **Send the verdict.** The CLI already holds the source, already runs the transform engine, and already
  prints the same table (`heros coverage`, `cov-c19cf0c4 · 128 apply / 123 refuse`). It can answer
  per node with the *real* engine on the *real* code and transmit a **stable identifier**.

Verdicts are identifiers, not sentences, for the same reason coverage causes are: the console branches
on data, and the sentence is rendered from the platform's own catalogue, so a CLI three versions old
cannot put stale copy on a paid surface.

🔴 **The platform is forbidden from deriving a verdict.** Not "does not today" — forbidden, with a fence.
The failure mode being prevented is specific: the platform knows a node's language and could compute the
(axis, language, form) cell, which would be right most of the time and would silently claim `applies`
for exactly the call sites that refuse for their own shape. A projection that is right most of the time
is worse than no projection, because it is the input to a customer's decision about what to author.

A node with no transmitted verdict renders **`not-reported`** — a fourth state beside `applies`,
`refused` and `not-applicable` — and it names the command that would report it. `not-applicable` is a
claim about the customer's code and it is the one thing this data must never say by accident (the
`language-coverage` contract, restated here because a new consumer is the classic place it gets lost).

### D3 — Projection is a read, not a table

`coverage × nodes` is computed at read time from `transform.AxisCoverage()` (in-process, versioned) and
the stored structure. It is **not** materialised. Two reasons, in priority order: a materialised
projection goes stale the moment the coverage table version moves and would then be a second source of
truth for a refusal; and it would be a new table whose entire content is derivable, which
`careful-table-creation` forbids.

Where the stored `coverage_version` differs from the running build's, the projection is labelled
**stale**, the counts are still shown, and they are **excluded from every total** — the same discipline
`unverified` gets in the delta ledger.

### D4 — Two storage changes, and only two

| Change | Shape | Why it is not derivable |
|---|---|---|
| `workflow_ir` gains `coverage_version` (nullable text) | one nullable column, both dialects; `nodes_json` gains `language` and `axis_verdicts` **inside the existing JSON**, so per-node fields need no DDL | the table version verdicts were computed against is a property of the payload, and reading it from `nodes_json` would put a payload-level fact in a per-node blob |
| new `linked_transform` | PK `(tenant_id, config_hash, source_revision)`; per-node outcomes JSON, diffstat integers, coverage version | grain differs from every existing table: `run_link` is per run, `workflow_ir` is per revision, and a transform is per (configuration, revision). Storing it in either would make "which structure is this drawing from" unanswerable, which is the exact reason `workflow_ir` upserts rather than appends |

Nullable, no default backfill, no rewrite of a deployed table — a row written before this change reads
as `not reported`, which is true. Both migrations are dual-dialect and idempotent, and the PG proof runs
them on a real Postgres, because a SQLite-only run is cover for the failures already in the other
dialect.

**No table for the subject index.** Enumeration is a query over `run_link`, `workflow_ir`,
`linked_transform` and the executor's `run`. P26 D7's rule applies unchanged: where a read is not
derivable, say so and name what would make it derivable — and here it *is* derivable.

### D5 — One runs list, two origins, labelled

C4's fix is not "add a second endpoint". `GET /api/v1/runs` returns one list containing executed runs
and linked runs, each carrying an `origin` (`executed` | `linked`). A developer does not have two kinds
of run in their head and should not be given two lists to reconcile.

What a linked run cannot support keeps its existing, correct refusal: no per-node input/output blobs, no
attempt groups, no terminal executor status — `hostedscorecard` already documents why
(`FailureAttribution: unavailable`, because "not to blame" and "not investigated" are opposite
findings). The list row carries what a linked run has: scores with intervals, cost, latency, tool
version, linked-at. The detail view routes by origin.

### D6 — `--with-ir` stays opt-in; the choreography does not

Making the structure payload default-on would be trading level 1 for level 3, and the egress promise is
the product's most load-bearing sentence. It stays opt-in and named.

What is *not* defensible is that opting in costs two commands and a file path. Bare `--with-ir`
discovers in-process from `--repo`; the path form is retained for a pre-computed IR. `--dry-run` renders
the widened payload byte-identically, as it must, and the success output names each surface the link
just filled and each one it did not, with the one command that would fill it. This is the CLI's job per
`interaction-simplicity-first`: when a prerequisite is missing, the message contains the next step.

### D7 — The worked examples stay

The wiring, context, memory and harness surfaces carry worked examples with the transform engine's own
verbatim sentences. It is tempting to replace them with the tenant's data now that there is some.

Do not. A reader meeting a refusal for the first time needs to see the *applied* case next to it to read
"declined" as a boundary rather than a failure — that is the stated reason those pages exist, and it
does not stop being true when the page also has real rows. The projection is added **beside** the
example, under its own heading, with its own denominator. `UI 改版不得丢失既有功能`: this phase adds
panels and removes none.

### D8 — Billing gets an account at first authenticated act, and coverage escapes the billing view

Two separate defects wearing one symptom.

The account: `ensureSeededAccounts` is create-if-absent over the tenants the *config seed* made, gated
on a plan catalog. An organization created any other way, or created before a catalog was configured,
has none — and then a linked run is attributed to a customer the billing read model cannot find. Fix:
provision at the first authenticated act (link, sign-in, or org creation), create-if-absent, Free plan,
never correcting an existing account. The comment in `accountsystem.go` is right and stays right — a
seed that "corrects" an existing account moves a paying customer back to Free on the next restart.

The coverage: `linkCoverageFor` already returns three states correctly, and it is only ever read
*inside* `BillingView`. So when the account is missing, the one number a link certainly produced becomes
unreadable. Coverage is lifted to its own read so the metering surface can answer "we observed N of M
runs you told us about" with no plan, no account and no invoice — and `nil` keeps meaning **unknown**,
which renders distinctly from complete, forever.

---

## Data-model sketch

```
workflow_ir                          -- migration 0042 (additive)
  tenant_id, workflow_id, source_revision   PK
  ir_version, received_at
  coverage_version   text NULL            -- NEW: the table the verdicts were computed against
  nodes_json  jsonb   -- per node, additive keys:
                      --   language      "go" | "python" | …   (frontend fact)
                      --   axis_verdicts [{axis, status, cause?}]  status ∈ applies|refused
  edges_json  jsonb

linked_transform                     -- migration 0043 (new)
  tenant_id, config_hash, source_revision   PK
  workflow_id, received_at, tool_version
  coverage_version   text NULL
  node_outcomes_json jsonb  -- [{node_id, outcome, cause?}]  outcome ∈ applied|refused
  files_changed, lines_added, lines_removed  int   -- statistics, never a diff
  status             text   -- the transform's own terminal state, verbatim
```

## Wire sketch

```
POST /api/v1/workflow-ir            ← was POST /api/v1/workflows/{id}/ir
PUT  /api/v1/workflow-source        ← was PUT  /api/v1/workflows/{id}/source/{rev}
DEL  /api/v1/workflow-source        ← was DELETE …
POST /api/v1/proposal-verdicts      ← was POST /api/v1/proposals/{id}/verdict
POST /api/v1/transform-receipts     ← new

GET  /api/v1/workflows                       -- tenant's structures (replaces the empty studio catalog)
GET  /api/v1/runs                            -- executed + linked, each with `origin`
GET  /api/v1/variants                        -- new
GET  /api/v1/transforms                      -- new
GET  /api/v1/workflows/{id}/axis-projection  -- new: coverage × this workflow's nodes
GET  /api/v1/link-coverage                   -- new: the three-state coverage, outside BillingView
```

## Risks

| Risk | Mitigation |
|---|---|
| The flat-path move breaks a deployed CLI | Expand-contract: both shapes served for one release, parameterised ones `ExposureInternal` and never published. The CLI reports the contract version it implements and the server names the mismatch |
| A projection is read as a promise about a node the platform never saw | `not-reported` is a first-class fourth state with its own visual treatment and its own command hint; the denominator is printed beside every count; a fence asserts the platform emits no verdict it was not sent |
| Widening the opt-in payload widens the egress boundary | Every new field is an identifier, a count or an enum: `language`, a verdict id, a cause id, a diffstat, a coverage-table version. The allowlist test asserts field-by-field, and `--dry-run` renders exactly what is sent. Prompt text, source, diffs and env values remain inexpressible — there is no field they could occupy |
| Enumeration becomes a cross-tenant read | Every list is scoped to the authenticated principal, and a subject belonging to another organization is absent from the list *and* answers 404 by id, exactly as `handleGetRun` already does — the endpoint is not an existence oracle |
| Deleting the fence exemption fails builds that were green | That is the point, and it is a one-time cost paid in a review rather than a 404 paid by a customer. Two of the three exempt paths are the ones this phase is fixing |
| The stale-projection label is never seen because coverage rarely moves | The stale path is tested by pinning a stored `coverage_version` to a value the build does not have, and the fence is verified red before it is verified green |
