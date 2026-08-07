# P29 — Linked Run Fan-out: one link, every surface

## Why

A developer ran a real workflow with the CLI, authenticated, and linked the run. The platform accepted
it. Then they opened the customer console and found **fifteen surfaces that had nothing to say about
the thing they had just sent us** — and every one of them was empty for a *different* reason, none of
which the screen stated:

| Surface | What the reader saw | The actual cause |
|---|---|---|
| Workflows, graph, board, proposals | nothing to select | `workflow_ir` has 0 rows: `--with-ir` and `push-source` address `POST /api/v1/workflows/{id}/ir` and `PUT /api/v1/workflows/{id}/source/{rev}`, and **neither path is published on the customer Ingress** |
| Variants, Transforms, Delivery, Studio, Author, Wiring, Context, Memory, Harness | a picker with nothing in it, or a worked example | these are **authoring** surfaces: they fill when a Variant Spec is submitted, and linking travels the other way |
| Coverage | a full table — `cov-c19cf0c4 · 128 apply / 123 refuse` | correct, and it is a fact about **the build**, byte-identical to what `heros coverage` prints locally. It says nothing about the reader's nodes |
| Billing | absent | no billing account exists for this organization — and link coverage, the one number a link certainly produces, is only readable *inside* the billing view |

Every one of those is defensible in isolation and the composite is indefensible: the product's free
surface is the CLI, the paid surface is the console, and **the bridge between them carries almost
nothing**. A customer who does exactly what the documentation tells them to do sees a console that
looks like the platform lost their run.

Two of the causes are worse than "not built yet", and they are the reason this is a phase rather than a
backlog item:

1. **The edge fence has a hole shaped exactly like the paths that matter.**
   `internal/api/ingress_fence_test.go` derives the CLI-addressed path set from the transport's source
   — a genuinely good design — and then skips every path ending in `/`:
   `if strings.HasSuffix(path, "/") { continue }`. `runlink.WorkflowIRPath` is `"/api/v1/workflows/"`.
   So the workflow-structure POST, the source PUT and the proposal-verdict POST are **exempt by
   construction** from the only fence that would have noticed they are unreachable. The build is green,
   the deployment is healthy, and three commands 404 at the edge.
2. **Linking has no read side beyond one run.** `GET /api/v1/runs` reads the executor's `run` table; a
   linked run lands in `run_link`. Two tables, one identifier, nothing joining them — so the run a
   developer linked ninety seconds ago is not in the list of their runs. The console's own
   `subjects.ts` documents the wider version of this: *"the platform exposes no enumeration endpoint
   for any of them"*, so a picker offers only the subjects **this browser session already opened**.

The remaining causes are a single missing join. Coverage, the delivery route table, the memory strategy
vocabulary and the harness boundary are all **total build facts** and all correct. The customer's nodes
arrive in the opt-in structure payload. Nobody has ever multiplied the two. `coverage × your nodes` is
not a new claim — it is a fact each side already owns — and it is the difference between *"128 apply /
123 refuse"* and *"of your 40 nodes, 31 are undeliverable by both routes, and here they are."*

## What Changes

- **The edge fence covers parameterised paths, and the CLI stops using them.** The four
  machine-addressed platform routes that carry caller-supplied identifiers move those identifiers into
  the request body and get flat, `Exact`-publishable paths (`/api/v1/workflow-ir`,
  `/api/v1/workflow-source`, `/api/v1/proposal-verdicts`, and the new `/api/v1/transform-receipts`).
  The `HasSuffix(path, "/") → continue` exemption is **deleted**, not widened: a transport path with a
  variable segment now fails the fence with the sentence "publish it Exact, which means giving it a
  flat shape". A `Prefix` rule under `/api/v1/workflows/` would publish `commit`, `orderings`,
  `proposals/generate` and `validate` beside the two wanted routes, so it is refused. **Breaking** for
  the CLI↔platform wire; expand-contract, both shapes served for one release.
- **`--with-ir` needs no second artifact.** Bare `--with-ir` discovers in-process from `--repo`. The
  flag stays **opt-in and named** — the boundary is not widened by default, because the egress promise
  outranks the convenience — but a developer no longer choreographs `discover -out` → `link --with-ir
  <path>` to get a graph.
- **The opt-in structure payload carries per-node applicability**, computed on the customer's machine
  by the same coverage table `heros coverage` reports, and transmitted as **stable identifiers** —
  `applies`, or a refusal cause — never prose, never source. Plus the node's language (a frontend fact)
  and the coverage-table version the verdicts were computed against.
- **A transform receipt becomes transmissible**: `config_hash`, `source_revision`, per-node
  applied/refused with cause, and diff **statistics**. Never a diff. This is what makes
  `/app/transforms/{config_hash}/{source_revision}` resolve for a change the customer applied locally.
- **The platform enumerates subjects.** Workflows, runs, variants and transforms are listable for the
  authenticated organization, from records that already exist. The runs list merges executed and linked
  runs into one list, each row labelled by origin. `subjects.ts` demotes to an ordering hint.
- **Every axis surface gains a projection panel**: the axis's own coverage rows crossed with this
  tenant's reported nodes, with the counts, the named causes and the node list. The worked examples
  stay — they are what makes a refusal legible — and the projection sits beside them. A node the
  platform was never told about renders `not reported`, which is a **fourth state**, and the platform
  never computes a verdict it was not sent.
- **The studio matrix, the workflow graph, the eval board and the scorecard read one structure store**
  — the linked one. The matrix's columns become the tenant's nodes. A cell action that needs a provider
  credential is refused **by name**: the platform holds no customer provider key and will not.
- **Billing answers from the first link.** An organization gets its account at its first authenticated
  act rather than only from the config seed, and link coverage plus observed spend render whether or
  not a plan charges. Unknown coverage stays visually distinct from complete coverage, permanently.

## Impact

- **Affected capabilities:** `platform-edge-reach` (new), `run-linking` (modified), `linked-subject-index`
  (new), `axis-node-projection` (new), `hosted-workflow-catalog` (new), `link-coverage-visibility` (new).
  Read-only touches on `delivery-record`, `language-coverage`, `context-policy`, `memory-policy`,
  `model-selection`, `node-wiring`, `run-linking`'s egress contract.
- **Affected code/systems:** `internal/runlink` + `internal/runlink/transport` (allowlist, flat paths),
  `internal/cli` (`link`, `push-source`, `report-verdict`, `apply`), `internal/api`
  (`publicroutes.go`, `ingress_fence_test.go`, `runlinking.go`, `studiomatrix.go`, `configruntime.go`,
  new projection + enumeration handlers), `internal/linkingest` (widened structure store, new transform
  receipt store), `internal/launch/capabilities.go` (studio matrix and authoring sources),
  `internal/billingview` + `internal/launch/accountsystem.go`, `web/console` (twelve surfaces),
  `deploy/k8s/overlays/prod/ingress.yaml`, migrations 0042–0043.
- **Dependencies:** P11 (run linking, egress boundary), P13 (`language-coverage`, `authored-change`,
  coverage read model), P12 (delivery route table), P19 (the deployment whose Ingress is the defect),
  P27 (organization identity and run ownership). Unblocks: the P7 metering read model getting a real
  denominator, and P6's optimizer having a hosted structure to propose against.
- **Not in scope, deliberately:** hosted execution of a customer workflow (P25's standing refusal is
  unchanged — the platform learns of a run, it does not perform one), hosted eval, failure attribution
  from a linked run (the eval data does not cross and there is no field it could occupy), and any new
  claim about a node the platform was not told about.
