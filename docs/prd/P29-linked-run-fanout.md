# PRD — P29: Linked Run Fan-out (One Link, Every Surface)

| | |
|---|---|
| **Phase** | P29 |
| **OpenSpec change** | [`p29-linked-run-fanout`](../../openspec/changes/archive/2026-08-07-p29-linked-run-fanout/) |
| **Lead roles** | Backend Dev + Frontend Dev |
| **Support roles** | Product Designer, System Designer, AI Engineer, DevOps, QA, Sales Operations |
| **Upstream** | P11 (run linking, egress boundary) · P12 (delivery route table) · P13 (`language-coverage`, `authored-change`) · P19 (the deployment) · P27 (organization identity, run ownership) |
| **Unblocks** | P7's metering denominator · P6's hosted optimizer input |
| **Status** | Proposed |

---

## 1. Summary

A developer ran a real workflow with the free CLI, signed in, and linked the run. The platform accepted
it. Then they opened the customer console and found **fifteen surfaces, across twelve routes, that had
nothing to say about the thing they had just sent us.**

That is the whole problem, and it is not one problem. It is six, wearing one symptom:

1. The two commands that carry a workflow's *shape* — `heros link --with-ir` and `heros push-source` —
   **cannot reach the platform through the deployed Ingress**, and the fence written to catch exactly
   that skips them by construction.
2. Nothing joins a linked run to the list of a customer's runs — two tables, one identifier.
3. The platform enumerates nothing, so every picker offers only what this browser session already
   opened.
4. The axis surfaces render total, correct **build facts** and no tenant projection.
5. The studio matrix reads a catalogue only a demo binary ever fills.
6. Billing needs an account the organization never got, and link coverage — the one number a link
   certainly produces — is readable only inside the billing view that needs it.

P29 makes **one `heros link` fill every one of those surfaces with the organization's own data, or say
in a named, typed sentence why it cannot** — and it does so without widening the egress promise by one
byte on the default path.

The design's centre of gravity is a refusal. Coverage answers "does this axis apply to a node in
language L with form F?" It cannot answer "does it apply to *this call site*", because that depends on
the source, and the source does not cross the boundary. So the platform **never computes a verdict**.
The customer's machine computes it — with the real engine, on the real code, from the same table
`heros coverage` prints — and transmits a stable identifier. A node the platform was not told about
renders **`not reported`**: a fourth state, beside `applies`, `refused` and `not applicable`. Getting
that fourth state wrong is the only way this phase can ship a lie, so most of its fences exist to
prevent it.

---

## 2. Problem & context

### 2.1 What the customer actually saw, verified in the tree

| Surface | Route | What rendered | Root cause |
|---|---|---|---|
| Workflows, graph, board, proposals | `/app/workflows` and its sub-views | a picker with nothing to pick | `workflow_ir` has zero rows |
| Variants | `/app/variants` | a picker requiring a hand-typed id | no variant enumeration |
| Transforms | `/app/transforms` | a two-field picker | no transform record exists for a locally applied change |
| Delivery | `/app/delivery` | the route table | correct, and it is a build fact |
| Studio | `/app/studio` | an empty matrix | `studio.WorkflowCatalog` is nil in the deployed path |
| Author | `/app/authoring` | a contract explainer | `MountAuthoring(nil)` |
| Wiring, Context, Memory, Harness | four routes | worked examples | by design — no tenant projection existed to add |
| Coverage | `/app/coverage` | `cov-c19cf0c4 · 128 apply / 123 refuse` | correct, and byte-identical to what the CLI prints locally |
| Billing | `/app/billing` | absent | no account for this organization |

### 2.2 The edge defect, and why it is the worst one

`deploy/k8s/overlays/prod/ingress.yaml` routes six paths to `agentd`: `whoami`, `run-links`,
`billing/webhook`, the two device paths and password sign-in. It routes **neither**
`POST /api/v1/workflows/{id}/ir` nor `PUT /api/v1/workflows/{id}/source/{rev}`.

`internal/api/ingress_fence_test.go` exists precisely to prevent this. It derives the CLI-addressed path
set from the transport's own source rather than from a hand-written list — a genuinely good design,
written after this failure mode had already shipped twice. And then:

```go
if strings.HasSuffix(path, "/") {
    continue
}
```

`runlink.WorkflowIRPath` is `"/api/v1/workflows/"`. `runlink.VerdictPath` is `"/api/v1/proposals/"`.
**The three routes that carry a workflow's structure, a source snapshot and a verification verdict are
the exact three the fence was told to ignore.**

The exemption is not careless — an `Exact` Ingress rule genuinely cannot match a path with a variable
segment, and the comment says so honestly. But the consequence is a fence that watches the family where
nothing can go wrong and looks away from the family where everything did. A green build, a healthy
deployment, and three commands that 404 at the edge.

### 2.3 Why the other five causes are one shape

Look at what the platform *does* hold after a link:

- per-node cost, latency, tokens, scores with intervals, gate outcome, case count;
- node ids, symbols, files, line spans, providers, models, context policies, tool counts, edges —
  **if** the developer opted in and the request could reach us;
- `config_hash`, `source_revision`, `tool_version`, timestamps, seeds.

And look at what the console holds: a coverage table over every axis × every registered language × every
form; a delivery route table over every change kind × both routes; a memory strategy vocabulary; a
harness boundary. All total. All correct. All silent about the reader.

Nobody has multiplied the two. That product is not a new claim — it is a fact each side already owns —
and it is the difference between *"128 apply / 123 refuse"* and *"of your 40 nodes, 31 are undeliverable
by both routes, and here they are."*

### 2.4 Why now

The commercial model is that the CLI is free and the console is what a customer pays for. Today the
free surface is complete and the paid surface, for a customer who has done everything right, is empty.
That is not a feature gap; it is the value proposition failing to connect at the one seam that carries
it. Every phase from here — the hosted optimizer, the gainshare ledger, the metering denominator — reads
from the same bridge.

---

## 3. Goals & non-goals

### Goals

- **G1** — One `heros link` (with its named opt-ins) fills all fifteen surfaces with this organization's
  own data, or states a named, typed reason it cannot.
- **G2** — Every path the CLI addresses is reachable from the internet, and the fence that guarantees it
  has no exempt family.
- **G3** — The platform enumerates the subjects it holds, so no picker requires a hand-typed identifier.
- **G4** — Every axis surface answers *"what applies to my nodes?"* with counts, causes and a node list.
- **G5** — The default link payload is **byte-identical** to what it was before this change.
- **G6** — A node the platform was not told about is `not reported`, everywhere, and never anything else.

### Non-goals (with the phase that owns them)

- **Hosted execution of a customer workflow.** The standing refusal is unchanged: this platform learns
  of a run, it does not perform one. The live run monitor stays absent with its existing reason.
- **Hosted evaluation, failure attribution, failure clusters, diagnoses, ablations.** These need
  per-node correctness per case; that is eval data, it does not cross the boundary, and there is no
  field it could occupy. `hostedscorecard`'s `FailureAttribution: unavailable` stays exactly as written.
- **Making the structure payload default-on.** Considered and refused in §8.2 D6.
- **A hosted provider credential.** The platform holds no customer provider key, in any plan, ever.
- **Pattern classification of a linked graph.** The classifier reads prompts and tool names; neither
  crosses. Regions stay `unclassified`, carried as data.
- **P28's ledger governance gap** (§14 Q4) — reported, not fixed here.

---

## 4. Users & personas

| Persona | What they do | What they need from this phase |
|---|---|---|
| **The evaluating developer** | installs the CLI, runs `discover` / `eval` on their own repo with their own keys, links one run to see what the hosted product does | the console to reflect the run within seconds, and to be told plainly what it cannot show and why |
| **The platform champion** | has to justify a paid console to their team | one screen that says something about *their* codebase that the CLI's local output does not |
| **The security reviewer** | approves whether the CLI may transmit anything | a rendered payload, a ratified field list, and the ability to see that opting in is explicit and narrow |
| **The operator** | runs the deployment | a fence that fails the build rather than a customer's command, and an upgrade that does not break a deployed CLI |

---

## 5. User stories

- **S1** — As an evaluating developer, when I link a run, I want the console to list it among my runs,
  so that I do not have to keep the run id in my terminal scrollback.
- **S2** — As an evaluating developer, I want to open my workflow's graph without producing an IR file
  and passing its path, so that seeing my workflow costs one command, not three.
- **S3** — As a platform champion, I want the wiring / context / memory / harness pages to tell me how
  many of *my* nodes each axis can change and why the rest are refused, so that I can decide whether
  this platform can help my codebase before I buy it.
- **S4** — As a platform champion, I want to see how many of my nodes are undeliverable by both routes,
  so that I learn the boundary from the product rather than from a support ticket.
- **S5** — As a security reviewer, I want `--dry-run` to render every byte of every opt-in payload, so
  that approval is a review of data rather than of a promise.
- **S6** — As an evaluating developer who opted into nothing, I want every structure-dependent surface
  to say *not reported* and name the option, so that an empty page is a choice I made rather than a
  product that does not work.
- **S7** — As an operator, I want a route the CLI calls but the Ingress does not publish to fail my
  build, so that I do not learn about it from a customer.
- **S8** — As a finance owner, I want the spend figure to show its link coverage, so that I never read a
  partial number as a complete one.

---

## 6. Functional requirements

### 6.0 The surface contract — what fills each of the fifteen

This table is the phase's acceptance in one page. **Every row must render this organization's own data
after one `heros link` with its named opt-ins.** A row whose live panel cannot be filled by linking says
so in a named, typed refusal on the surface itself — never as a blank.

| # | Surface | Route | What fills it after this phase | Fed by | FRs |
|---|---|---|---|---|---|
| 1 | **Workflows** | `/app/workflows` | this organization's reported workflows, selectable with no hand-typed id | `link --with-ir` → `workflow_ir` | FR17, FR21, FR29, FR46 |
| 2 | **Graph** | `/app/workflows/{id}` | the shape drawn from reported nodes and edges; regions `unclassified`, carried as data | same | FR46, FR55 |
| 3 | **Board** | `/app/workflows/{id}/board` | the Pareto frontier over this organization's linked runs, cost and latency included | `link` → `run_link` | FR21, FR25 |
| 4 | **Proposals** | `/app/workflows/{id}` proposals tab | proposals against a reported structure; where none exist, the reason and the next action | `workflow_ir` + P5.5 store | FR46, FR49 |
| 5 | **Variants** | `/app/variants` | the configurations this organization has measured, enumerated | `run_link.config_hash` | FR21, FR29 |
| 6 | **Transforms** | `/app/transforms/{hash}/{rev}` | per-node applied/refused with cause, plus the diffstat — never a diff | `apply --link-receipt` → `linked_transform` | FR15, FR16, FR21 |
| 7 | **Delivery** | `/app/delivery` | the route table **projected onto your nodes**: how many are undeliverable by both routes, and which | projection | FR41, FR42 |
| 8 | **Studio** | `/app/studio` | matrix columns = your reported nodes with their current models; a provider-credential action refused by name | `workflow_ir` + registry | FR48, FR50, FR51 |
| 9 | **Author** | `/app/authoring` | your authored changes and their preflight verdicts, plus the projection of what is authorable per node | authoring store + projection | FR31, FR52–FR54 |
| 10 | **Wiring** | `/app/wiring` | counts + causes + node list for the wiring axis over your nodes, beside the retained worked example | projection | FR31–FR37, FR43 |
| 11 | **Context** | `/app/context` | same, for the context axis | projection | FR31–FR37, FR43 |
| 12 | **Memory** | `/app/memory` | same, for the memory axis | projection | FR31–FR37, FR43 |
| 13 | **Harness** | `/app/harness` | same, for the harness axis | projection | FR31–FR37, FR43 |
| 14 | **Coverage** | `/app/coverage` | the total build table **plus** your organization's column: applies / refused / not reported per axis, with denominators | projection | FR31, FR33, FR38–FR40 |
| 15 | **Billing** | `/app/billing` | observed spend with its link coverage, for an organization that never had an account seeded | first-act provisioning + coverage read | FR56–FR62 |

Two rows carry an honest partial, stated on the surface rather than in this document:

- **Row 9 (Author)** — the authoring *history* is a console-origin record, so an organization that has
  authored nothing sees an empty history with a named reason. The *projection* panel is populated by the
  link. See §14 Q5.
- **Row 4 (Proposals)** — the platform proposes only where a verified-delta path exists; where it has not
  proposed, the surface says which of the two conditions is missing.

### Platform edge reach (capability `platform-edge-reach`)

- **FR1** — Every platform path the CLI addresses SHALL be published in the deployed customer-hostname
  ingress manifest and declared public in the route classification, **including** paths that carry
  caller-supplied identifiers.
- **FR2** — The reach check SHALL fail when the CLI-addressed path set it scans is smaller than the set
  the transport addresses; it SHALL NOT report success for having found nothing.
- **FR3** — Every published platform path SHALL be matched exactly. A prefix rule SHALL NOT be used, and
  where one would be required the check SHALL name the other routes it would publish.
- **FR4** — A machine-addressed platform route SHALL have a fixed path; caller-supplied identifiers
  SHALL travel in the payload.
- **FR5** — A request whose path and payload name different subjects SHALL be refused, not resolved by
  precedence.
- **FR6** — A path in the manifest SHALL be declared public, and a declared public route SHALL be
  registered. Both directions SHALL be checked.
- **FR7** — During the transition release the parameterised routes SHALL remain served, classified
  internal, and SHALL never appear in the manifest.
- **FR8** — The CLI SHALL distinguish "not reachable at this endpoint" from "the platform refused", and
  SHALL name exactly one next action.

### Run linking (capability `run-linking`, modified)

- **FR9** — The default run-link payload SHALL be byte-identical to the pre-change payload.
- **FR10** — The opt-in structure payload SHALL carry, per node, the language the discovery frontend
  reported. An unreported language SHALL be absent, never derived from a file path.
- **FR11** — The opt-in structure payload SHALL carry, per node and per axis, a verdict computed on the
  customer's machine by the transform engine against that node's real source: `applies`, or `refused`
  with the engine's own stable cause identifier.
- **FR12** — A verdict SHALL be an identifier. No sentence, message or prose explanation SHALL be
  transmitted with it.
- **FR13** — The transmitted verdicts SHALL equal what the local coverage command reports for the same
  repository; a divergence is a defect.
- **FR14** — The structure payload SHALL carry the coverage-table version its verdicts were computed
  against. An absent version SHALL NOT be defaulted to the platform's own.
- **FR15** — A transform receipt SHALL be transmissible under a named opt-in, carrying the configuration
  hash, source revision, workflow identity, per-node outcome with cause, and diff **statistics** —
  never a diff, a file, or a line of source.
- **FR16** — A transform receipt SHALL be idempotent by (configuration, revision).
- **FR17** — The structure opt-in SHALL work with no separately produced artifact, discovering in place
  from the configured repository; the path form SHALL be retained.
- **FR18** — Render-only mode SHALL render all three payloads with full fidelity, and the rendered bytes
  SHALL equal the transmitted bytes.
- **FR19** — The platform SHALL accept a payload omitting any field added by this change and SHALL
  record its absence as absence.
- **FR20** — On success the CLI SHALL name the surfaces its transmission filled and, for each surface it
  did not, the one option that would.

### Subject enumeration (capability `linked-subject-index`)

- **FR21** — The platform SHALL enumerate the workflows, runs, variants and transforms it holds for the
  authenticated organization.
- **FR22** — A subject SHALL appear because a record exists, never because a session opened it.
- **FR23** — Every enumeration SHALL be scoped to the authenticated principal. An organization
  identifier in the request SHALL be ignored.
- **FR24** — Requesting another organization's subject by identifier SHALL answer identically to
  requesting one that does not exist.
- **FR25** — Executed and linked runs SHALL appear in one list, each row carrying its origin.
- **FR26** — A linked run's row SHALL carry only what a linked run has, and no executor field.
- **FR27** — Each enumeration SHALL distinguish empty from read-failed from not-mounted.
- **FR28** — Records with no owning organization SHALL have their count reported alongside every
  enumeration that would otherwise be silently partial, whether or not the list is empty.
- **FR29** — Console pickers SHALL be populated from the enumeration; session memory SHALL order it and
  never add to it.
- **FR30** — Direct entry of a known identifier SHALL be retained.

### Axis projection (capability `axis-node-projection`)

- **FR31** — Every axis surface SHALL render a projection of its coverage onto this organization's
  reported nodes: counts by state, refusals grouped by cause.
- **FR32** — A projected count SHALL be expandable to the node list, each node identified by symbol,
  file and line span.
- **FR33** — A projected cell SHALL be exactly one of `applies`, `refused`, `not applicable`,
  `not reported`, and the four SHALL be distinguishable.
- **FR34** — `not applicable` SHALL NEVER be produced from an absent input.
- **FR35** — The platform SHALL NOT derive a verdict from any property it holds. This SHALL be enforced
  by a check, not by convention.
- **FR36** — A `not reported` cell SHALL name the one command or option that would report it.
- **FR37** — A refusal SHALL carry its cause identifier and its owner, and the surface SHALL branch on
  the identifier, never on prose.
- **FR38** — The projection SHALL compare the stored coverage-table version with the running build's,
  label a mismatch stale, show both versions, and exclude stale counts from every total.
- **FR39** — An absent stored version SHALL be treated as stale, not as current.
- **FR40** — Every count SHALL state its denominator, including how many of the workflow's nodes were
  reported at all. A proportion SHALL never be shown without its counts.
- **FR41** — The delivery route table SHALL be projected per node, and a node refused by both routes
  SHALL be counted and listable as **undeliverable**.
- **FR42** — `undeliverable` SHALL NOT be rendered with a hopeful synonym, and a permanent cause SHALL
  render differently from an unbuilt one.
- **FR43** — The existing worked examples on the axis surfaces SHALL be retained; the projection is an
  addition with its own heading.
- **FR44** — Live data and worked examples SHALL be distinguishable without inspecting values.
- **FR45** — The projection SHALL read the same coverage source the engine refuses from, asserted in
  both directions.

### Hosted workflow catalog (capability `hosted-workflow-catalog`)

- **FR46** — The node list, studio matrix, workflow graph, eval board and scorecard SHALL derive nodes
  from the reported-structure store, and SHALL agree.
- **FR47** — No console-facing surface SHALL derive a workflow from a catalogue loaded at process start.
- **FR48** — The studio matrix's columns SHALL be this organization's reported nodes, each carrying its
  symbol and current model; its rows SHALL be the model registry.
- **FR49** — A workflow with no reported structure SHALL say so and name the command; this SHALL be
  distinct from "no such workflow" and from a read failure.
- **FR50** — A matrix action requiring a provider credential SHALL be refused by name, stating that the
  platform holds no customer provider credential, and naming the local command that does it.
- **FR51** — That refusal SHALL NOT imply that any plan, role or setting would remove it.
- **FR52** — A binding authored in the console SHALL travel the existing preflight → resolve → gate →
  transform spine. No second apply path SHALL exist.
- **FR53** — A change the transform refuses SHALL be refused identically when authored from the console.
- **FR54** — A console-authored change applied without a verdict SHALL be recorded `unverified` and
  contribute zero to every aggregate improvement or savings figure.
- **FR55** — Graph regions SHALL be `unclassified`, carried as data; no pattern label SHALL be inferred
  from a symbol name.

### Link coverage visibility (capability `link-coverage-visibility`)

- **FR56** — An organization SHALL hold an account on a plan that charges nothing from its first
  authenticated act, create-if-absent; an existing account SHALL never be corrected.
- **FR57** — Where no plan catalogue is published, provisioning SHALL be skipped and the surface SHALL
  state that condition and what would change it.
- **FR58** — Link coverage SHALL be readable with no account, no plan and no invoice.
- **FR59** — Coverage SHALL be complete / partial-with-denominator / unknown, and unknown SHALL render
  distinctly and SHALL NEVER render as zero or complete.
- **FR60** — Every derived spend figure SHALL display its link coverage, and unobserved spend SHALL
  NEVER be extrapolated.
- **FR61** — A linked run SHALL be visible in the metering read model for the period its timestamp
  names; a closed period SHALL be refused with a distinguishable cause.
- **FR62** — The metering surface SHALL distinguish no-data from no-account from not-served.

---

## 7. Non-functional requirements

- **NFR1 (security)** — No field added by this change can carry repository-originating free text.
  Prompt text, source, diffs, environment values and credentials remain **inexpressible**: there is no
  field they could occupy, on any path including diagnostics and highest verbosity.
- **NFR2 (security)** — No route becomes publicly reachable except the four named flat paths, each with
  its own `Exact` rule and its own written justification.
- **NFR3 (stability)** — Expand-contract. Both wire shapes are served for one release; a deployed CLI
  keeps working across the upgrade, and rollback is re-apply.
- **NFR4 (stability)** — Both migrations are additive, nullable, dual-dialect and idempotent, with no
  backfill and no rewrite of a deployed table. Both are proven on a real Postgres.
- **NFR5 (reproducibility)** — `config_hash` for a configuration carrying no new axis field hashes
  byte-identically to before this change; P0 golden vectors keep reproducing.
- **NFR6 (UX)** — Seeing a workflow's graph costs **one** command. Every failure message names exactly
  one next action.
- **NFR7 (honesty)** — No surface states a fact about a node the platform was not sent. No count is
  shown without its denominator. No unknown renders as a zero.
- **NFR8 (interface)** — The BFF stays a pass-through: no merging, re-ranking, reformatting or status
  translation. Every state the console branches on arrives as an identifier.
- **NFR9 (interface)** — All new colour, spacing, type and radius values come from the design system;
  `npm run scan:tokens` passes. The fourth state gets a token, not an improvised colour.
- **NFR10 (operations)** — The reach fence covers both deployment substrates from one list, so a path
  published on one and not the other fails the build.
- **NFR11 (latency)** — The projection is computed per read; for a workflow of 500 nodes across six
  axes it renders within the console's existing page budget, or it is paginated — never cached into a
  second source of truth.

---

## 8. System design summary

### 8.1 Shape

```
customer machine                          platform                         console
────────────────                          ────────                         ───────
heros eval  ─┐
             ├─ link ────────► POST /api/v1/run-links ──────► run_link ────► runs, board, scorecard
heros discover                                                                 variants, billing
   │
   └─ verdicts computed here ─┐
        (real engine,         │
         real source)         │
                              ▼
heros link --with-ir ───────► POST /api/v1/workflow-ir ─────► workflow_ir ──► workflows, graph,
                                    (flat path, Exact)         + language      studio, projections
                                                               + verdicts
                                                               + cov version
heros apply --link-receipt ─► POST /api/v1/transform-receipts ► linked_transform ─► transforms,
                                                                                     delivery
heros push-source ──────────► PUT  /api/v1/workflow-source ──► source bundle ──► hosted discovery

                              transform.AxisCoverage()  ×  workflow_ir.nodes
                                          └──────────┬──────────┘
                                                     ▼
                                     GET /api/v1/workflows/{id}/axis-projection
                                     ── computed per read, never materialised ──
```

### 8.2 Decisions

#### D1 — Close the edge hole by flattening the paths, not by widening the fence

Three options, arbitrated by the priority law (security > stability > user complexity > operations >
evolvability > extensibility > maintenance > implementation cost):

| Option | Cost |
|---|---|
| A `Prefix` rule under `/api/v1/workflows/` | Publishes `commit`, `orderings`, `orderings/stream`, `validate`, `proposals/generate`, `proposals/{id}/open-pr`, `pattern-graph` and `eval-board` to the internet — and publishes every future route in that family by default. **Level 1 traded for level 8.** ❌ |
| Traefik regex middleware | Precise, but lives in an annotation the repository's fence cannot read, and pins the deploy to one ingress controller. **Level 4 and level 5.** ❌ |
| **Flat paths, identifiers in the payload** | Each route becomes one `Exact` rule, exactly like `run-links`. The fence's exemption is *deleted*. ✅ |

Chosen: flat paths. `WorkflowIRPayload` already carries `workflow_id` and `source_revision`, so the URL
segment was duplicating the body — this removes a duplication rather than adding an indirection. And
after it, there is no way to add a machine-addressed route the fence cannot see, which is the durable
half of the fix. The one-off implementation cost is level 8 and does not get to outrank any of that.

#### D2 — The verdict is computed on the customer's machine; the platform is forbidden from computing one

Coverage answers for `(axis, language, form)`. It does **not** answer for a call site's own shape —
unpacked arguments, a run-time-assembled list, a missing row locator — and that lives in the source.

Two honest designs exist. *Send the source*: refused on level 1; `push-source` exists for customers who
choose it, and it will not become the price of a graph. *Send the verdict*: the CLI already holds the
source, already runs the transform engine, already prints the same table. It answers per node with the
real engine on the real code and transmits an identifier.

🔴 The prohibition on platform-side derivation is a **fence**, not a habit. The platform knows a node's
language and could compute the `(axis, language, form)` cell. That would be right most of the time and
would claim `applies` for exactly the call sites that refuse for their own shape — and a projection that
is right most of the time is worse than none, because it is the input to a customer's decision about
what to author. This is the `language-coverage` contract's own rule (*absence is not a value*; *the most
specific true cause wins*) applied to a new consumer, which is the classic place such rules get lost.

#### D3 — The projection is a read, never a table

Computed at read time from `transform.AxisCoverage()` and the stored structure. Not materialised, for
two reasons in priority order: a materialised projection goes stale the instant the coverage table
version moves and becomes a second source of truth for a refusal (level 2/5); and its entire content is
derivable, which `careful-table-creation` forbids (level 5/6). Where the stored version differs from the
build's, the projection is **stale**: shown, labelled, and excluded from every total — the same
discipline `unverified` gets in the delta ledger.

#### D4 — Two storage changes, and no more

One nullable column on `workflow_ir` (`coverage_version`), and one new table (`linked_transform`). The
per-node `language` and `axis_verdicts` ride **inside the existing `nodes_json`**, so per-node data needs
no DDL at all. `linked_transform` is a new table because its grain genuinely differs: `run_link` is per
run, `workflow_ir` is per revision, a transform is per (configuration, revision) — and folding it into
either would make *"which structure is this drawing from"* unanswerable, which is the exact reason
`workflow_ir` upserts rather than appends. The subject index gets **no table**: it is a query over
records that already exist (P26 D7's rule, applied where the read *is* derivable).

#### D5 — One runs list, two origins, labelled

Not a second endpoint. `GET /api/v1/runs` returns executed and linked runs together, each carrying its
origin. A developer does not hold two kinds of run in their head and should not be handed two lists to
reconcile. What a linked run cannot support keeps its existing correct refusal — no per-node blobs, no
attempt groups, no executor status, and `FailureAttribution: unavailable` verbatim, because *"not to
blame"* and *"not investigated"* are opposite findings.

#### D6 — `--with-ir` stays opt-in; the choreography does not

Making the structure payload default-on trades level 1 for level 3, and the egress promise is the
product's most load-bearing sentence. It stays opt-in and named.

What is not defensible is that opting in costs two commands and a file path. Bare `--with-ir` discovers
in place. This is `interaction-simplicity-first` applied exactly where it applies: the *decision* stays
explicit; the *ceremony* around the decision goes away. And when a prerequisite is missing, the message
carries the next step — which is why the link's success output names the surfaces it filled and the
option that would fill each one it did not.

#### D7 — The worked examples stay

It is tempting to replace the axis surfaces' worked examples now that there is real data. Do not. A
reader meeting a refusal for the first time needs the applied case beside it to read *declined* as a
boundary rather than a failure — that is the stated reason those pages exist. `UI 改版不得丢失既有功能`:
this phase adds panels and removes none.

#### D8 — Provision the account at the first authenticated act; lift coverage out of the billing view

Two defects, one symptom. `ensureSeededAccounts` is create-if-absent over the tenants the *config seed*
made, gated on a plan catalogue — so an organization created any other way has no account, and a linked
run is attributed to a customer the billing read model cannot find. Provision at the first authenticated
act instead, create-if-absent, never correcting: a seed that "corrected" an account would move a paying
customer back to Free on the next restart.

And `linkCoverageFor` already returns three states correctly — it is only ever read *inside*
`BillingView`. Lifting it to its own read is what lets the metering surface answer "we observed N of M
runs you told us about" with no plan and no invoice. `nil` keeps meaning **unknown**, which renders
distinctly from complete, permanently: a spend figure at 100% coverage and one whose denominator could
not be read look identical as a number and mean opposite things.

### 8.3 Design key points

- **One coverage source, asserted both ways.** A surface may not offer a cell the engine refuses; the
  engine may not materialise a cell no surface offers.
- **Four states, not three.** `not reported` is the state this phase adds, and it is the state most
  likely to be quietly collapsed into one of the others during a refactor. It carries its own token, its
  own copy and its own fence.
- **Every count carries its denominator.** Including "how many of this workflow's nodes were reported at
  all" — because a projection over a subset that does not say so is a claim about the whole.
- **Nothing is inferred from a file path.** Not the language, not the framework, not a pattern label.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The user-visible defect is a **complexity** defect, and it sits at level 3 of the arbitration law:
seeing your own workflow currently costs `discover -out <path>` then `link --with-ir <path>`, and then
still shows nothing because of a network fault the user cannot see. The remedy is the one this role's
rule prescribes — *能减免的用户输入就减免* — applied to the ceremony, not to the consent: bare
`--with-ir` removes the artifact and the path; the opt-in itself stays, because removing *that* would be
buying level 3 with level 1.

The unhappy path is the design, not the leftover. Three states already existed on these surfaces and
were routinely collapsed; this phase adds a fourth and must not repeat that. So:

| State | What the reader is told | Their next action |
|---|---|---|
| `applies` | this axis can change this node | author a change |
| `refused` — *your call site* | the code's shape prevents it | edit the code |
| `refused` — *not in source* | there is nothing to change | nothing; there is no "when" |
| `refused` — *not yet, ours* | the named artifact has not been built | wait, and the thing waited for is named |
| `not reported` | **we were not told about this node** | run the named command |

🚫 Rendering `not reported` as a greyed-out control — the failure the coverage page was built to end —
is the specific regression this phase can cause. It gets its own fence.

**Noun dictionary additions**, so that interface copy, ER entity and code identifier stay three separated
layers with one meaning: *reported node*, *not reported*, *projection*, *undeliverable*, *link coverage*.
"Reported" is chosen over "known" or "synced" deliberately — it names *who did the reporting*, which is
the whole point.

**Scope fidelity.** Two adjacent defects were found and are **reported, not fixed**: `MountAuthoring(nil)`
in the deployed path (in scope — the Author surface is on the list) and P28's absence from the ledger
fence's governed changes (§14 Q4 — out of scope, needs a decision).

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

Three one-way doors were considered and two were closed:

1. **A `Prefix` ingress rule** — closed. It publishes a route family forever, and "we will remember to
   check" is not a control. §8.2 D1.
2. **A materialised projection table** — closed. `careful-table-creation`: its content is derivable, and
   a derived copy of a refusal is a second source of truth for the most consequential sentence the
   product says. §8.2 D3.
3. **A new wire contract** — opened deliberately, expand-contract, with a stated contract version and a
   removal task. `careful-api-creation` says a published contract is hard to withdraw; the answer is not
   to avoid publishing but to publish with a version and a plan, and to serve both shapes for one
   release.

**Event-driven write, idempotent reconciling read** — the standing architecture rule. Each of the three
ingest paths is a write with a matching read that can be re-derived: the structure store upserts on
(tenant, workflow, revision); the receipt store upserts on (tenant, configuration, revision); the
projection is a pure function of the two. A second transmission produces one row and one answer.

**Control plane / data plane.** The customer's machine is the data plane: it holds the source, the keys
and the engine, and it computes the verdict. The platform is the control plane: it stores, scopes,
projects and renders. This phase moves *no* judgement across that line — which is the reason the
platform is forbidden from deriving a verdict rather than merely discouraged from it.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

Every ingest path in this change gets the **four-layer assertion**: the request was accepted → the row
is present in the store → the read model returns it → the surface renders it. The failure this prevents
is precise and has happened here before: linking was accepted, stored durably and counted, and the only
surface that could see the run again was a coverage *count* — `GET /api/v1/runs/{id}` answered "no such
run" because it reads a different table.

**Schema, migration and code land together.** Migration 0042 (one nullable column) and 0043 (one new
table), both dual-dialect, both idempotent by *definition* rather than by object name, no backfill and
no rewrite of a deployed table. Both are proven on a **real Postgres** and re-run to prove idempotence,
because an unrun dialect is not missing coverage — it is cover for the failures already in it.

**Failure classes stay distinguishable.** 503 not-mounted, 404 not-found, read-failure and empty are four
outcomes with four responses on every new endpoint. A read failure is never an empty list; that is how
an outage comes to look like data loss.

**No silent widening.** The allowlist is constructed field by field into a fresh struct. A field added to
`discovery.IRNode` tomorrow is absent from the payload until someone adds it below, on purpose, with a
justification a reviewer reads — and the test asserts the direction, not just the current contents.

### 9.4 Senior Frontend Dev — *three states stay three; four states stay four*

**No improvised styling.** The fourth state's treatment comes from the design system's neutral ramp, not
from a new colour; `npm run scan:tokens` fails the build on a literal outside the two permitted layers.
The hazard palette is reserved for hazard — `not reported` is not a hazard, it is an absence of input,
and rendering it in `--warn` would teach readers that the platform is broken when they simply did not
opt in.

**A 404 is never mapped to a business state**, and **transport failure is not "unauthenticated"**. Both
are standing rules in this codebase and both are load-bearing here: an unreachable projection endpoint
must not render as "no nodes apply".

**The BFF is a pass-through.** Counts, causes, owners, staleness and denominators are all computed
server-side and rendered as received. A client-side recomputation would be a second source of truth for
a refusal.

**Nothing is removed.** Every panel and control present on the twelve routes before this change is
present after it; the projection is added under its own heading. The regression fence enumerates the
pre-change controls.

**Viewport-first.** The axis pages already use one tablist with no nesting because five stacked sections
put four below the fold. The projection becomes a tab, not a sixth stacked section.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

The projection is an aggregate over nodes, which is exactly the shape that hides a per-sample bug. The
discipline that applies:

- **The per-node record is retained and inspectable.** Every count expands to the nodes it counts. A
  count with no drill-down is where a wrong classification lives forever.
- **The comparison is actually run, never narrated.** The fence asserting that transmitted verdicts equal
  what `heros coverage` reports locally runs both and diffs them; it does not assert that both "use the
  same table".
- **The adversarial case is the fixture.** The primary test node is one whose *language and form are
  covered* and whose *call site refuses anyway* — the exact case a table lookup gets wrong and a real
  engine gets right. Testing only the easy cases would make a lookup-based implementation pass.
- **No mock reaches production.** A demo catalogue was reachable from a console-facing surface (`nil` in
  the deployed path is the reason the matrix is empty, and a fixture in that slot is the reason it once
  was not). This phase removes the process-local catalogue from the console-facing path entirely rather
  than leaving a socket a fixture can be plugged into.
- **Diagnosis proposes; verification decides.** Nothing here promotes anything. The projection describes
  applicability; it produces no score, rank, winner or recommendation, and a console-authored change
  stays `unverified` until a held-out verdict says otherwise.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

**Least privilege.** Four new `Exact` rules and not one byte more. Each carries a written justification
of what it accepts, from whom, and why it is safe to publish; the reverse assertion still fails the build
if anything is published that is not declared public. `/api/v1/device/approve` stays unpublished, and
this phase does not touch it.

**Blast radius.** The edge change touches the customer hostname only. The operator hostname keeps zero
`/api/v1` rules; the two consoles' isolation stays the browser's origin boundary.

**Reversible.** Expand-contract: both wire shapes served for one release, so rollback is `kubectl apply
-k` of the previous overlay and a deployed CLI keeps working in both directions. The upgrade rehearsal is
a task, run against a cluster on the previous release.

**The health signal is externally readable.** A deployment where the structure ingest is unmounted says
so on `/readyz` by name, not by a 404 the customer diagnoses. And the reach fence covers **both**
substrates from one list — a path published on Kubernetes and forgotten on Compose fails the build,
because "we did not add it to the other one" is one edit away from being true.

**Automate, do not document.** The remedy for the ingress gap is not a runbook step; it is a fence that
prints the exact YAML block to add. Hand-adding a rule to a running cluster is what deleted the device
paths on the next `apply` and broke CLI login silently — that class of fix is prohibited here.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

**Every fence in this change is verified red before it is verified green**, and the failure output is
recorded in its task. The fence ledger is a deliverable (task 8.6). This phase exists *because* a fence
that had never been seen to fail on the paths that mattered was believed for two releases.

Fences and how each is broken:

| Fence | Broken by | Must print |
|---|---|---|
| Reach: every CLI path published | removing one ingress rule | the path, and the YAML block to add |
| Reach: prefix consequence | adding a `Prefix` rule | the other routes it would publish |
| Allowlist shape | adding an open-ended `string` field | which field, and why it is not an identifier |
| Default payload unchanged | adding a field to the default link | a byte diff |
| No platform-derived verdict | deriving one from language | the call site that derived it |
| `not applicable` from absence | deleting a coverage row | the cell that changed state |
| Staleness exclusion | pinning an unknown stored version | the totals, before and after |
| Cross-tenant scope | scoping a query to a request field | the leaked subject |
| Coverage unknown ≠ zero | returning `(0,0,nil)` on error | the rendered difference |

**Acceptance is a browser, not a build.** The exit walk is performed in one sitting on a real deployment
and the rendered pages are captured. A passing type-check, a green suite and a healthy `/readyz` are all
compatible with a page that renders nothing — which is precisely the state this phase was opened to fix.

**The negative walk is mandatory.** The same walk with *no* opt-ins: every structure-dependent surface
must say `not reported` and name the option. No surface may render `not applicable`, a zero, or an empty
table implying there is nothing to show. An empty collection passes vacuously, so each negative assertion
also asserts the collection is non-empty.

**Fixtures map the real schema.** The projection tests run against rows written by the real ingest path,
not hand-built structs — a fixture that does not match the wire is a test of a system that does not ship.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

**What may be said after this ships:** the console shows the workflow **you chose to send us**, which
axes apply to **your** nodes, which of them are undeliverable and why, and how much of your spend we
actually observed.

**What may not be said, in any deck, ever:**

- "the console shows your workflow" without *the structure you chose to send* — the platform sees
  symbols, files, line spans, models, policies and tool counts, and never a prompt, a diff or a line of
  source. That sentence is the product's strongest differentiator and it is destroyed by overstating it
  once.
- "we analyse your codebase" — we do not, unless you run `push-source`, which is a separate, named,
  revocable act.
- "test your prompts against every model in the console" — a hosted cell run needs a provider credential
  the platform does not hold and will not. The refusal is a **boundary**, not a roadmap item, and it is
  stated on the surface itself so a customer never learns it from a sales call.
- any coverage or savings figure without its denominator.

**Maturity labels** for this phase's claims: *shipped* — projection, enumeration, edge reach, transform
receipts, link coverage. *Boundary, permanent* — hosted execution, hosted eval, failure attribution from
a linked run, hosted provider credentials. There is no *coming soon* row; a maturity table with an empty
third column is the honest outcome here.

**FAQ → requirement backflow.** "Why is my console empty?" was the question that opened this phase. The
answer becomes a documented FAQ entry *and* a line of CLI output, because a question a customer has to
ask support is a defect in the product, not a gap in the documentation.

---

## 10. Dependencies

| Upstream | What is needed | Status |
|---|---|---|
| P11 | run linking, the egress allowlist, `--with-ir`, `push-source` | shipped; modified here |
| P13 | the coverage read model, `language-coverage`, `authored-change` | shipped; read here |
| P12 | the delivery route table | shipped; projected here |
| P19 | the deployment whose ingress manifest is the defect | shipped; corrected here |
| P27 | organization identity, run ownership, the pre-ownership count | shipped; consumed here |
| P10 | the studio matrix and the prompt registry | shipped; re-sourced here |

**Unblocks:** the P7 metering read model gets a denominator that means something for every organization,
not only seeded ones; P6's optimizer gains a hosted structure to propose against; the operator console's
`run-ownership` ledger row gains the per-tenant read it currently lacks.

---

## 11. Risks & mitigations

| Risk | Owner | Mitigation |
|---|---|---|
| The flat-path move breaks a deployed CLI | Backend | Expand-contract for one release; parameterised routes stay served and internal; the CLI reports its contract version and the server names a mismatch |
| A projection is read as a promise about an unreported node | Product + Frontend | `not reported` is a first-class fourth state with its own token, its own copy and its own fence; every count prints its denominator |
| Widening the opt-in payload widens the egress boundary | Backend + Security | Every added field is an identifier, a count or a closed-set value; the allowlist fence asserts the *shape*, not just the contents; `--dry-run` renders every byte |
| Enumeration becomes a cross-tenant read | Backend | Every list scoped to the authenticated principal; another organization's subject is absent from the list *and* answers identically-to-nonexistent by id |
| Deleting the fence exemption fails builds that were green | DevOps | Intended. A one-time cost paid in review rather than a 404 paid by a customer; two of the three exempt paths are what this phase fixes |
| Staleness handling is never exercised because the table rarely moves | QA | The stale path is tested by pinning a stored version the build does not have, and verified red before green |
| The projection is slow on a large workflow | Backend | Computed per read with the node list paginated; never cached into a second source of truth |
| A demo fixture reaches a customer surface | AI Eng | The process-local catalogue is removed from the console-facing path, not merely left empty |

---

## 12. Rollout & test strategy

**Order is not negotiable.** Wave A (the edge) lands first: nothing downstream is *observable* until the
structure can reach the platform, and shipping the projection first would produce a feature that renders
`not reported` for every customer and looks like it does not work.

1. **Wave A — edge.** Fence red → flat routes → classification → transport → ingress → both fence
   extensions red → live 2xx against a deployed cluster.
2. **Wave B — payload.** Allowlist rows, local verdict computation, bare `--with-ir`, `--dry-run`
   fidelity, the transform receipt, the byte-identical-default fence.
3. **Wave C — storage.** 0042 and 0043, proven on real Postgres, re-run, down-and-up.
4. **Wave D — enumeration.** Four lists, cross-tenant fences, picker rewiring with the no-loss
   regression fence.
5. **Wave E — projection.** The read, the no-derived-verdict fence, the four states, staleness, and the
   seven console panels.
6. **Wave F — workflow surfaces.** Studio, graph, transforms, scorecard.
7. **Wave G — metering honesty.** Account provisioning, coverage lifted out of the billing view.
8. **Wave H — governance and proof.** Ledger rows, the exit walk, the negative walk, the older-client
   walk, the upgrade rehearsal, the fence-redness ledger.

**Test layers.** Unit for each read model; contract tests for all three wire payloads including an
older-client payload; PG-proof migrations on a real Postgres; the four-layer assertion on each ingest
path; cross-tenant fences; the fence-redness ledger; and a browser walk that is the actual acceptance.

**Rollback.** Re-apply the previous overlay. The flat routes disappear, the parameterised ones are still
served in-cluster, the CLI's pre-change path still works, and the two migrations are additive so no data
is lost by rolling the binary back.

---

## 13. Success metrics & acceptance criteria

The phase closes when all of the following are true on a **real deployment**, recorded:

- [ ] **A1** — A newly created organization runs `heros login`, `heros eval`, `heros link --with-ir` and
      `heros apply --link-receipt`, and all fifteen surfaces render this organization's own data or a
      named, typed refusal. No surface renders an unexplained empty state.
- [ ] **A2** — The run linked in A1 appears in `/app/runs` labelled as linked, without a hand-typed id.
- [ ] **A3** — Every picker on `/app/workflows`, `/app/variants`, `/app/transforms` offers this
      organization's subjects from the platform's enumeration, in a browser session that has opened none
      of them.
- [ ] **A4** — `/app/wiring`, `/app/context`, `/app/memory`, `/app/harness`, `/app/coverage`,
      `/app/authoring` and `/app/delivery` each show a count over this organization's nodes, with its
      denominator, expandable to the node list.
- [ ] **A5** — `/app/delivery` states how many of this organization's nodes are undeliverable by both
      routes, and lists them.
- [ ] **A6** — `/app/billing` shows observed spend with its link coverage for an organization that has
      never had an account provisioned by the config seed.
- [ ] **A7** — The **negative** walk: the same organization with no opt-ins sees `not reported` on every
      structure-dependent surface, each naming the option that would fill it. No `not applicable`, no
      zero, no empty table implying there is nothing to show.
- [ ] **A8** — The **older-client** walk: a pre-change CLI links successfully; its structure stores with
      the new fields absent; every projection cell reads `not reported`.
- [ ] **A9** — Removing any one of the four new ingress rules fails the build with a message naming the
      path and printing the YAML to add. Verified red.
- [ ] **A10** — Adding a code path that derives a verdict from a platform-held property fails the build.
      Verified red.
- [ ] **A11** — A link with no opt-in produces a payload byte-identical to the pre-change payload.
- [ ] **A12** — `--dry-run` output equals transmitted bytes for all three payloads; a security reviewer
      can approve from the rendering alone.
- [ ] **A13** — `config_hash` golden vectors from P0 reproduce byte-identically.
- [ ] **A14** — The fence-redness ledger lists every fence added by this phase with its observed failure
      output. A fence with no recorded red does not count.
- [ ] **A15** — The upgrade rehearsal passes on a cluster running the previous release, and rollback by
      re-apply leaves the pre-change CLI working.

**Metric of the phase**, chosen so it cannot be satisfied by shipping pages: *the number of console
surfaces that render an unexplained empty state after one `heros link` with every opt-in* — today
fifteen, at exit **zero**. A surface that renders a named, typed refusal counts as explained; a surface
that renders a blank does not.

---

## 14. Open questions

- **Q1 — Should `--with-ir` become the default once the payload's shape has been reviewed by a customer's
  security team?** *Recommendation: no, and not later either.* The opt-in is the promise. A default that
  can be turned off is a different promise from a transmission that never happens unless asked for.
  Needs a Product + Sales sign-off to be closed permanently rather than deferred.
- **Q2 — Should a transform receipt be transmissible for a change the customer applied and then
  reverted?** The receipt is per (configuration, revision) and a revert reproduces the parent hash
  byte-identically, so the two are distinguishable — but whether a reverted change should still appear on
  `/app/transforms` is a product decision, not a technical one.
- **Q3 — What is the retention of a reported structure?** `workflow_ir` upserts per revision and nothing
  prunes it. A long-lived repository accumulates one row per linked revision forever. Needs a retention
  decision from Product and Legal, and it is out of scope here.
- **Q4 — `p28-email-password-identity` is absent from `GOVERNED_CHANGES` in the ledger fence**, so its
  capabilities have no operator-oversight rows and nothing fails. Reported, not fixed: extending this
  change to it would be scope creep, and closing it needs a decision about whether P28's capabilities get
  operator surfaces or documented `no-operator-surface` rows.
- **Q5 — Should `MountAuthoring` be sourced in the deployed path in this phase or the next?** It is on
  the surface list (Author), so it is in scope — but its store is the authoring history, which is a
  console-origin record rather than a linked one, and the honest minimum here may be the projection panel
  plus a named absence for the history. Needs a call before Wave E.
