# ADR-010 — A gradual rollout is a two-armed binding document resolved in the customer's own process

- **Status:** Accepted (2026-07-29)
- **Deciders:** System Design (proposed) + User (ratified)
- **Refines:** [ADR-004](ADR-004-runtime-config-binding.md) — `bound` mode made a configured value
  *data that may change without a new codemod*. It did not decide whether that data may hold **more
  than one value at a time**. This ADR decides that it may, under named bounds, and fixes what the
  second value costs.
- **Constrained by:** [ADR-002](ADR-002-provider-gateway-serves-platform-callers.md) (we hold no place
  in the customer's runtime) and [ADR-005](ADR-005-forge-delivery-and-credential-posture.md) (we hold
  no standing write credential to their repository). Neither is weakened here; together they decide
  the shape below more than anything else does.
- **Relates to:** [ADR-001](ADR-001-source-transformation-apply-model.md),
  [ADR-009](ADR-009-binding-document-format.md)
- **Introduces:** the cross-axis capability `change-delivery`, defined once in
  [`p13-prompt-model-optimization/specs/change-delivery/spec.md`](../../openspec/changes/archive/2026-08-01-p13-prompt-model-optimization/specs/change-delivery/spec.md)
  and referenced by P14–P18.

## Context — what problem this solves

The platform has exactly one way for an accepted change to reach a running agent: a call-site rewriter
produces a diff, [P12](../prd/P12-forge-delivery.md) opens a pull request, a human merges it. That
chain is well specified and it is the right default. It also has a failure mode that the six
optimization axes have made impossible to ignore.

**Most cells refuse.** Coverage is a total function over (axis × language × form)
([`language-coverage`](../../openspec/changes/archive/2026-08-01-p13-prompt-model-optimization/specs/language-coverage/spec.md)),
and the honest reading of that table is that the majority of cells have no materializer: memory refuses
in every language (P17), harness refuses in every language (P18), skill binding materializes in Go for
two providers (P14), context and wiring in two languages (P16, P15). When the rewriter refuses there is
no diff; with no diff there is no pull request; with no pull request **nothing ships at all**. The
platform can prove a change is better and then deliver nothing — and it currently does not even say
that is what happened, because "no delivery route" is stated only for a repository with no forge
configured ([`forge-delivery`](../../openspec/specs/forge-delivery/spec.md)), not for a verified
proposal on an axis that cannot be written.

**And a merge is a slow, total commitment.** Even where the rewriter succeeds, the only exposure
control is binary: unmerged (0% of traffic) or merged (100%). A customer who wants to try a verified
prompt on a tenth of their traffic for a week has no way to express it, and the product has no way to
observe a change under real load before it becomes everyone's default.

So the question this ADR answers is: **is there a second delivery route, and what is it allowed to
cost?**

## The constraint that decides the design

The obvious second route — the platform serves a share of the customer's production traffic from a
configuration we hold — **is already refused**, twice, by accepted decisions.

ADR-002 rejected putting our gateway in the customer's request path, and rejected specifically the
compromise version: *"decide per-node at runtime"* was refused because it *"inherits the blast radius
on any node that opts in, and adds a config axis that changes what a measurement means — two runs of
the same `config_hash` would no longer be comparable."* ADR-005 then extended the same refusal to
their repository. A rollout that we operate on their traffic would re-open both doors at once, and it
would do so to buy convenience — an L3 gain paid for with L1 and L2, which the priority ordering this
project arbitrates by (safety > stability > UX > operability > evolvability > extensibility >
maintainability > implementation; see [ADR-005](ADR-005-forge-delivery-and-credential-posture.md) for
the ordering and its source) does not permit.

What is *not* refused is the seam ADR-004 already built and shipped into the customer's tree: a
generated accessor, compiled into **their** binary, reading a document **they** own, supplying
arguments to a call **their** program still makes itself. That component is already in the request
path — theirs, not ours. A rollout that rides it adds no credential, no network dependency, and no new
blast radius. That is the whole design.

## Decision

**A gradual rollout is a binding document that carries two arms and a share. The generated accessor
assigns each invocation to an arm inside the customer's own process, deterministically and offline.
The rollout is temporary by construction: it expires, it may revert itself locally, and it is never
the way a change becomes permanent.**

### The four properties, and why each is load-bearing

**1. The unit is a share of runs, per workflow.** A rollout binds one workflow's node to a `parent`
arm (what runs today) and a `candidate` arm (the accepted change), plus the candidate's share of
invocations. Assignment is a pure function of the rollout's identity and a caller-supplied stable key
— never a random draw, never wall-clock — so the arm a given unit of work receives is reproducible,
explainable after the fact, and identical on every replica without any coordination between them. A
caller that supplies no key gets per-invocation assignment, which is honest but weaker, and the
document says which it got.

**2. Every invocation is attributed to the arm's own `config_hash`, not the document's.** This is
ADR-002's objection answered rather than dodged. ADR-004's H1 containment already requires the
resolver to emit the `config_hash` it actually resolved on every invocation; a rollout changes only
that there are now two possible answers. Because the arm is emitted, two runs of the same
`config_hash` remain exactly as comparable as they were — the rollout adds runs, it does not blur
them. A resolver that emits a rollout's *document* hash rather than its *arm's* hash is a defect of
the same class as resolving to an unrequested configuration.

**3. A rollout expires, and expiry serves the parent.** A rollout carries a bounded lifetime fixed
when it is written. On expiry the accessor serves the parent arm — the safe direction — and does so
with no network call and no human present. A rollout therefore cannot become a permanent
configuration by being forgotten, which is the failure mode that would otherwise let the customer's
repository stop describing what their agent does.

**4. Making a change permanent still requires the codemod, the pull request, and a human merge.** The
rollout is a **precursor to** the source route, never a substitute for it. This keeps ADR-001's
guarantee that the shipped source is the source of record, keeps ADR-005's never-merge rule intact,
and keeps the product's promise honest: a rollout produces evidence under real load, and evidence is
not delivery.

### Reverting is local; resuming is human

If the candidate arm trips a guard the document declares — an error rate, an exception class, a
latency ceiling — the accessor **falls back to the parent arm in-process and records why**. It does
not ask us. It cannot ask us: a rollout that needed to reach the platform to protect itself would make
our availability their availability, which is the L2 coupling ADR-004's H4 already forbade for the far
weaker case of fetching a value.

Resuming after a guard trip requires a human editing the document — which is a diff, a pull request,
and a merge. Automatic revert with human resume is not a compromise between the two; it is the
asymmetry the priority ordering implies. Reverting moves toward the configuration that was already
running and already reviewed, so automating it costs nothing that safety values. Resuming moves toward
the configuration that just failed under load, so automating it would let the platform re-expose a
customer's traffic to a known regression on its own authority. Those are not the same act and they do
not get the same permission.

### Eligibility is declared per axis, per cell — and most cells refuse

Route eligibility is **not** a property of the platform; it is a property of each (axis, cell), and it
is published in the same shape as language coverage: a total function whose refusals name a cause with
an owner. Three causes, evaluated in this order:

| Cause | Means | Whose backlog |
|---|---|---|
| `notRuntimeResolvable` | The change is program structure, not data. No document can carry it, in any language, ever. | Nobody — it is a permanent boundary, and saying "not yet" about it is a lie |
| `nodeNotBound` | The change is data, but this node is in `inline` mode, so there is no accessor to resolve it | The customer's engineer — a one-time `bound` migration |
| `noRolloutBinding` | The change is data and the node is bound, but this axis has no field in the document schema yet | The platform's backlog, with a named missing artifact |

Ordering matters for the same reason it does in `language-coverage`: telling an engineer to migrate a
node to `bound` mode, when the change they want is a control-loop rewrite that no document can ever
carry, sends them to do work that will not help. The permanent boundary is announced first.

Applied to the six axes, the honest matrix is mostly refusals, and that is the correct outcome rather
than a gap to close:

| Axis | Runtime route | Why |
|---|---|---|
| **P13** model, params, prompt version | **Live** for `bound` nodes | These are exactly the fields ADR-009 already fixed in the document |
| **P14** skill binding, tool prune | `notRuntimeResolvable` for binding; `noRolloutBinding` for the tool set | Constructing a provider SDK tool value is code; selecting among already-written tools is data the schema does not yet carry |
| **P15** node order, parallelism | `notRuntimeResolvable`, permanently | Order is compiled program structure. No document can reorder statements |
| **P16** retrieval params vs. selection policy | Split per cell | A `top_k` or a budget is data; a policy that applies by deleting turns is a source rewrite |
| **P17** memory strategy | `notRuntimeResolvable` today | A strategy needs a store in the customer's process that we do not ship into their tree |
| **P18** harness strategy; `max_turns` | `notRuntimeResolvable` for the scaffold; `noRolloutBinding` for bounded params | A control loop is structure; its turn ceiling is a number |

P15's cell is the one that must not soften. Every other refusal above is a cell that could gain a row.
"Order is compiled" is not a backlog item, and a future version of this table that quietly moves it
into "pending" would be claiming an ability that cannot exist.

## Alternatives considered

| Option | Verdict |
|---|---|
| **A. Platform serves a share of production traffic** | ❌ Refused by ADR-002 and ADR-005 on L1/L2. It is the rejected "decide per-node at runtime" option wearing a rollout's clothing. |
| **B. Two-armed binding document, resolved in the customer's process** (chosen) | ✅ Rides a seam already shipped in their tree. No credential, no network dependency, no new blast radius. Bounded by expiry and by a human merge for permanence. |
| **C. Shadow execution — run both arms, serve the parent** | ❌ Not rejected in principle, but it is a different product: it doubles the customer's provider spend on their bill for evidence the eval harness already produces more cheaply, and it never measures a user-facing outcome. Reconsider only if a customer's traffic proves un-replayable in the harness. |
| **D. Environment promotion (staging → production)** | ❌ Not a rollout. It is a deploy gate with no partial exposure, and it is already expressible as two merges. Adds a concept without adding an ability. |
| **E. Rollout as an alternative to the pull request — the change lives in platform config forever** | ❌ Rejected at L2/L5. The customer's repository would stop describing what their agent does, and every later codemod would be computed against a source that is not what runs. |
| **F. Auto-revert *and* auto-resume** | ❌ Rejected at L1. Automating the direction that re-exposes traffic to a known regression is the platform acting on the customer's behalf against their reviewed configuration. |

## What this does not change

1. **ADR-001** — apply is still a deterministic source transformation delivered as a reviewable diff.
   A rollout ships *as* such a diff: the two-armed document is data in a pull request a human merges.
2. **ADR-002** — the transformed program still calls its own SDKs. The accessor gains a branch; it
   gains no connection to us.
3. **ADR-004 H1–H4** — all four containments hold unchanged, and H1's emitted `config_hash` is the
   mechanism property 2 relies on. H4's "never a startup dependency, never fail-open, fail-static"
   is what makes local guard-tripping the only admissible revert.
4. **ADR-005** — the platform still never merges below the Autonomous level, and a rollout never
   merges anything at all.
5. **Verification fidelity** — the resolver is **pinned** during eval and verification runs
   (ADR-004), so a rollout is inert in a measurement run. A verified delta is never produced by a
   half-exposed configuration.

## Consequences

**Good.** A change on an axis whose rewriter refuses can still be *tried* where the axis is data, and
a change whose rewriter succeeds can be exposed gradually rather than all at once. "No route
delivers this" becomes a reported state with a named cause instead of silence.

**Costly.** The binding document schema gains a rollout block, which ADR-009 already established is a
one-way door the moment it ships in a customer tree — so it is specified before the write path, not
during it. The accessor gains a branch, and every arm-assignment defect is now a defect in generated
code inside a customer's process, which raises the bar on the generator's test suite rather than on
ours.

**Unresolved.** Whether a guard trip should be observable to the platform at all before the customer's
next telemetry export, and whether a rollout on a node with no verified delta should be permitted at
all or only marked (ADR-004 H3 permits-and-marks; a rollout is a stronger act than a resolution).
Both are carried as open questions in the six PRDs rather than settled here.
