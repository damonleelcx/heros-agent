# ADR-004 — Two-layer apply: the codemod writes a binding, the binding resolves at runtime

- **Status:** Accepted (2026-07-22)
- **Deciders:** System Design + Product (proposed) + User (ratified)
- **Amends:** [ADR-001](ADR-001-source-transformation-apply-model.md) — it is **not** superseded. The
  apply mechanism is still a deterministic AST source transformation delivered as a reviewable diff.
  What this ADR adds is a second, opt-in **shape** for what that transformation writes.
- **Also amends:** `openspec/changes/archive/2026-07-23-p5.5-proposals-verification/specs/verification/spec.md`
  (the scenario clause *"no runtime shim substitutes parameters at execution time"*) — narrowed, see
  §"What this does not change" and the `MODIFIED` delta in
  [`openspec/changes/archive/2026-08-01-p10-prompt-model-studio/specs/runtime-config-binding/spec.md`](../../openspec/changes/archive/2026-08-01-p10-prompt-model-studio/specs/runtime-config-binding/spec.md).
- **Relates to:** [ADR-002](ADR-002-provider-gateway-serves-platform-callers.md) (the transformed
  program keeps calling its own SDKs — unchanged here).

## Context — what problem this solves

ADR-001 settled that a configuration is applied by rewriting the user's source and shipping a
reviewable diff, explicitly rejecting the original plan's runtime shim on two grounds: a compiled Go
binary has no monkey-patch seam, and a shimmed run does not exercise the code that will actually ship,
so its measurements can diverge from production. Both grounds remain correct and this ADR does not
disturb them.

But ADR-001 also fixed something it did not intend to decide: **where the configured value lives in
the shipped code.** Because the codemod writes the value *inline* at the call site
(`Model: "claude-opus-4-20250514"`), every change to a configured value — even swapping a prompt to a
newer version of the same template — requires a fresh codemod, a fresh build, and a fresh pull
request. That is the right cost for a change to *program structure*. It is a disproportionate cost for
a change to *data*.

P10 makes that cost visible. Its whole premise is that a user edits a prompt in the dashboard, binds
its variables, and selects a model per node. Under inline-only apply, each of those is a PR, and the
"select a model and a prompt version per node" loop the user asked for becomes a build-and-review loop.

Two forces are therefore in tension:

- **Review, reproducibility and measurement fidelity** want the value inline, in the diff, in the
  built artifact (ADR-001's reasoning).
- **Operating a configured system** wants the value to be data that can change without a rebuild.

Resolving this by picking one side loses something real either way. The resolution below is that the
tension is not actually about *when* the value is resolved — it is about **which kinds of facts are
data and which are program structure.** Those are separable, and separating them is what this ADR does.

## Decision

**A configuration is applied in two layers. The codemod (layer 1) writes a reviewable source change
as it always did; what it may now write is an indirection to a generated *binding artifact* that
ships in the same diff. The binding artifact (layer 2) is data, and the values in it may be changed
at runtime without a new codemod.**

### The two apply modes

Apply mode is chosen **per node**, and **`inline` remains the default**. Nothing moves onto the new
indirection unless someone asks for it.

| Mode | What the codemod writes | Changing a value later |
|---|---|---|
| **`inline`** (default, ADR-001 as-is) | The value at the call site: `Model: "claude-opus-4-…"` | A new codemod + diff + build |
| **`bound`** (this ADR) | An indirection: `Model: agentcfg.Node("n_triage").Model()`, plus a generated `agentcfg` package **and a resolved binding document containing the actual values**, all in the same diff | Edit the binding document — no new codemod |

### The line between data and structure

This is the load-bearing part of the decision, and it is what makes "runtime configurable" a precise
claim rather than a vague one. A binding source is either data that lives in the document, or a
source-level expression that must be written into the call site.

| Fact | Where it lives in `bound` mode | Runtime-changeable? |
|---|---|---|
| Model id + inference params (temperature, max_tokens, thinking budget) | binding document | **Yes** |
| Prompt template body / prompt version | binding document | **Yes** |
| A slot bound to a **`literal`** | binding document | **Yes** |
| A slot bound to an **`env`** read | binding document (the variable *name*) | **Yes** |
| A slot bound to an **`expr`** — a call-site expression like `ticket` | the rewritten call site: `agentcfg.Vars{"ticket": ticket}` | **No — needs a new diff** |
| A slot bound to an **`input`** — a typed graph input | the rewritten call site | **No — needs a new diff** |
| Which node calls which (wiring), skills bound, context policy | the source | **No — needs a new diff** |

The reason is not implementation convenience: an `expr` binding names a variable **in the user's
lexical scope**. There is no way to move it into a data file without either re-introducing reflection
(which Go does not offer here) or shipping a string the program must `eval` (which nothing should do).
It is program structure, and it stays program structure.

So the honest one-line statement of what `bound` mode buys is: **the model and the prompt become
data; the wiring between the prompt's holes and the program's values stays code.** That is exactly the
loop P10 needs — try a different model, try a newer prompt version — and it is not a general-purpose
runtime reconfiguration of the program.

### Resolution order at runtime

1. The **binding document embedded in the built artifact** — the one that shipped in the diff. This is
   the default and requires nothing external to exist.
2. A **local override document**, if one is configured by path.
3. A **remote document from the platform**, only if explicitly enabled.

Resolution is **fail-static**: if a configured override source is unreachable, unparseable, or fails
validation, the resolver keeps the last known-good document and reports degraded — it never falls back
to an arbitrary or empty configuration, and it never blocks process startup. A misconfigured remote
must not be able to take a customer's production process down, and must not be able to silently
change which model a node uses.

## The hazards this creates, and what contains each

Moving a value out of the built artifact is not free. Four hazards follow directly, and each one is a
requirement in P10 rather than a caveat in prose.

### H1 — Reproducibility: `config_hash` no longer implies what ran

Today a run is fully determined by `config_hash` + `source_revision`. With a runtime-resolvable
document, a run could execute a different configuration than the one requested and still be recorded
under the requested hash — which would corrupt every comparison built on top of it.

**Containment.** The resolver computes the `config_hash` of the document it actually resolved and
emits it on **every invocation** as part of the P2.5 tag set. The eval harness **asserts observed
equals requested** and **fails the run** on mismatch rather than scoring it. This is an
event-write-reconcile-read invariant with a named, idempotent reconcile point — a run's own telemetry
is the necessary path the data already travels, so the check cannot be skipped by forgetting to call
it. Ordering is not trusted; the reconciliation is explicit.

**Additionally:** during eval and verification runs the resolver is **pinned** — override sources are
disabled in the sandbox entirely. A measurement run reads the embedded document or it does not run.

### H2 — The diff stops showing the value

A reviewer looking at `Model: agentcfg.Node("n_triage").Model()` cannot tell what model that selects.
Review was ADR-001's central benefit and an indirection is exactly how it gets hollowed out.

**Containment.** The **resolved binding document ships in the same diff**, so the values are in the
change — in a different file, not absent from it. A transformation that introduces an indirection
**without** the corresponding resolved values in the same change is **rejected**, on the same footing
as a transformation that fails to build. The pull request renders the **effective resolved values**,
not only the indirection, so what a reviewer approves is a configuration and not a pointer.

### H3 — Drift between what was verified and what runs

P5.5 exists to prove a configuration is better before it is surfaced. If the configuration can change
after merge without a diff, someone can move production onto a configuration that carries no evidence
— which would quietly dissolve the product's core promise.

**Containment.** The binding document records the `config_hash` that carried a **verified delta**.
Resolving to a configuration with **no verified-delta record** is permitted — an operator may need it,
and forbidding it outright would push people to work around the mechanism — but it is **marked
unverified in telemetry at every invocation**, surfaced as unverified in the console, and **refusable
by automation level** (under Autonomous, an unverified resolution does not run). Unverified is a state
that must be visible, not a state that must be impossible.

### H4 — Blast radius inside the customer's production process

An enabled remote fetch puts the platform on the customer's critical path. A platform outage becoming
a customer outage would be a stability degradation traded for configuration convenience.

**Containment.** Local-and-embedded first, remote strictly opt-in, fail-static, never a startup
dependency, and never fail-open. Under the priority ordering this project arbitrates by
(**安全 > 稳定 > UX > 运维 > …**), an L2 stability risk cannot be accepted to buy L3 convenience — so
the convenient design (fetch config at startup, fail if unavailable) is not available, and the
resilient one is the only shape offered.

## What this does not change

Every ADR-001 requirement stands, unweakened:

1. **AST-level, deterministic transforms** — same `config_hash` + same source → byte-identical diff.
2. **Build-preserving** — a transform that fails to build is rejected before it is proposed.
3. **Behavior-preserving except for the intended change.**
4. **Isolated application** — worktree/branch, never the user's working tree in place.
5. **Always reviewable** — and H2 above strengthens this rather than relaxing it.
6. **Clean rollback** — one `git revert`, which now reverts the call site, the generated package, and
   the binding document together, because they are one change.

**ADR-002 is untouched:** the transformed program still calls its own provider SDKs. `agentcfg` is
generated code that ships inside the user's repository and reads a document; it is not our gateway, it
opens no connection to us, and it does not intercept the SDK call. It supplies arguments to a call the
user's program still makes itself.

**The narrowing of p5.5's clause.** That spec's scenario asserts *"no runtime shim substitutes
parameters at execution time."* Its intent — a verification run must measure the code that would ship,
not a shimmed approximation — is preserved exactly, because verification runs with the resolver
**pinned** to the embedded document (H1). What the clause must no longer be read to forbid is a
*generated, in-repo, reviewed, built* binding that is part of the shipped artifact. The distinction is
not cosmetic: the rejected shim was **ours**, injected around the user's call, absent from their
repository and from their build. This binding is **theirs** — in their diff, in their review, in their
build, in their `git revert`. The `MODIFIED` delta restates the requirement header verbatim and
narrows the clause accordingly.

## Alternatives rejected

**Keep inline-only apply.** Cheapest, and it preserves every ADR-001 property with no new hazard
surface. Rejected because it makes P10's central loop — pick a model, pick a prompt version, per node
— a build-and-pull-request cycle per attempt. That is a **UX (L3)** cost paid to avoid a hazard set
that is containable, and the containments above are ordinary engineering rather than novel risk. Note
this alternative is still *available per node*: `inline` remains the default.

**True runtime resolution — the program fetches its configuration from the platform.** Maximum
flexibility, zero diff for any change. Rejected on ADR-001's original grounds, which have not expired:
no interception seam in compiled Go without generated code anyway (so the "no source change" benefit is
illusory), and a run that resolves from a live service is not the artifact that ships — measurement
fidelity, the reason the shim was rejected in the first place, would be lost again. It also maximizes
H4 rather than containing it.

**A binding document that is not in the repository** (platform-hosted only, referenced by id).
Rejected on H2 and on `git revert`: a configuration that is not in the user's history cannot be
reviewed in the pull request that introduces it, and cannot be rolled back with the change that
introduced it. Git being the audit trail and the rollback is an ADR-001 property worth keeping.

## Consequences

**Positive**
- The P10 loop (edit prompt → new version → select per node → try a model) stops requiring a pull
  request per attempt, while the *structural* change that introduced the binding is still reviewed once.
- Reproducibility becomes **checked** rather than assumed: H1's reconciliation asserts that the
  configuration a run recorded is the configuration it executed — a property nothing verifies today,
  including in inline mode.
- Rollback and audit stay in git, because the document is in the repository.

**Negative / new risk surface**
- **Two apply modes to maintain**, and a per-node choice users can get wrong. Mitigated by `inline`
  remaining the default and by the mode being visible in the diff and the console.
- **A generated package inside the user's repository** — a real footprint, and one they now own. It
  must be small, dependency-free, readable, and regenerable.
- **A new failure mode: resolution degraded.** Contained by fail-static behavior and by it being a
  first-class reported state rather than a silent fallback — a degraded resolver that quietly used
  stale configuration without saying so would be worse than the outage it avoided.

## Terminology

| Term | Meaning |
|---|---|
| **Apply mode** | `inline` (value at the call site) or `bound` (value in the binding document). Per node, `inline` by default. |
| **Binding artifact** | The generated in-repo package (`agentcfg` or equivalent) that reads the document and supplies values at the call site. |
| **Binding document** | The resolved configuration data — model, params, prompt template, `literal`/`env` bindings — shipped in the diff and embedded in the build. |
| **Pinned resolution** | Override sources disabled; only the embedded document is read. Mandatory for eval and verification runs. |
| **Fail-static** | On an unreachable/invalid override source: keep last known-good, report degraded. Never fail-open, never fail-closed at startup. |
