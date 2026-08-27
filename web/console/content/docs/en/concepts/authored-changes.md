---
title: Authored changes
tier: guide
summary: The rules every axis shares when you make a change yourself — one pipeline, three verdicts, validation at save, unverified until measured, and an exact undo.
platform_version: 0.20.0
boundary: This page states the shared contract. It does not describe any one axis's vocabulary, and it does not change anything — the controls are on the axis surfaces in the console.
order: 8
---

You can set a model, a prompt version, a skill, a tool selection, a context policy or a memory strategy
directly — without waiting for the optimizer to propose one.

The rules below are the **same on every axis**. They are stated once, here, because the day one
restatement drifts is the day a reader learns the rules differ per axis, which they do not.

## One spine, two origins

A change you author is derived, resolved, hashed, gated and transformed by exactly the components that
process one the optimizer proposes. There is no separate path for hand-made changes — which means there
is no gate a hand-made change can skip.

Who authored a change is recorded **on** the change, not **in** its configuration hash. A configuration
you author and an identical one the optimizer proposes are the **same configuration**: they hash the
same and are measured once.

## You do not author the evidence

You choose what to change. Which cases judge it, which cases are held out, and which seeds are used are
derived by the platform.

A person authoring a cheaper model has the same incentive a cost-driven optimizer has, and better tools
for acting on it. A result measured on cases chosen by the party who wants a particular answer is not
evidence — so the held-out split is derived from the configuration itself and is disjoint from whatever
motivated the change.

## The three verdicts

Every draft is checked before submission. The check publishes nothing, writes no diff and spends no
evaluation budget — and it answers with one of three verdicts, never two.

| Verdict | What it means | What to do |
|---|---|---|
| **admissible** | the change can be applied and evaluated | submit it |
| **refused** | the change would be wrong in a way that is not visible at the moment of choosing it | read the cause — it names the node and the field |
| **not yet measurable** | nobody has measured the thing the gate would judge on | submit it anyway; it goes to verification |

The third is the one most easily lost. Rendering it as a refusal would point you at your own
configuration to find a fault that is not there; rendering it as admissible would claim a safety check
that never ran. It is neither, so it says so.

## Validation happens at save

A parameter is bounded by the schema the registry validates against, and a value the schema rejects is
refused **before anything is stored**. An id minted for content that was never written is an id a spec
could reference forever without resolving.

The form's fields are derived from the selected entry's own params schema rather than written by hand.
A hand-written form is a second, staler copy of the schema, and the two disagree in the direction that
lets a value through.

## An option you cannot use is shown, not hidden

Where an option requires a service this deployment does not supply, it is rendered — **disabled, naming
the service it needs**. It is never omitted from the list.

A hidden option is indistinguishable from one that does not exist, and a reader who cannot see it cannot
ask for it.

## Applied is not verified

A change you author can be applied without a verification run. It is then labelled **unverified**,
wherever it appears.

It is your repository, so the platform will not refuse to emit your edit. What it will not do is call it
a result: an unverified change stays outside the verified-delta ledger, contributes nothing to any
improvement or savings figure, and is never merged automatically at any automation level.

A configuration is not an improvement until something measured it.

## What an unapplied change is worth

Where an axis can be authored but not yet written into your source, selecting a value still resolves,
still produces a `config_hash`, is still recorded against your identity with a pointer to the variant it
came from, and still appears in lineage next to every other configuration.

That is a real configuration you can pin, compare and hand to a colleague — and it materializes
unchanged the day the rewriter lands. Withholding all of it because a codemod is missing would confuse
*"we cannot write this into your source"* with *"you may not express this"*.

This is why the controls stay **live** rather than greyed out. A disabled control says nothing about
why, and invites the belief that some other strategy, language or plan would unlock it. The reason is
stated instead, **above** the control, so you meet it before composing a change rather than after
pressing save.

## Every change has an exact undo

Reverting re-derives the parent you started from, rather than applying the inverse of your edits.

The result is byte-identical to the configuration you had — not merely equivalent to it. Applying an
inverse edit is how an undo quietly becomes a third configuration; re-deriving from an immutable parent
cannot drift.

Clearing an override removes it entirely rather than setting it to a default. The key disappears from
the node, so the configuration returns to exactly the bytes it had before — same `config_hash`, no
residue.

## There is no override

No plan, role or setting materialises a change the engine refuses. There is no Force, no advanced mode,
and no upsell beside a boundary — see
[a refusal is not a permission problem](/docs/concepts/refusals#a-refusal-is-not-a-permission-problem).

## Worked examples

**The platform's own fixtures**, not your repository. Your own preflight verdict is rendered on the axis
surface, by the same component that renders these.

| Verdict | Fixture |
|---|---|
| admissible | `config_hash 9f2c41ab77e0`, dimension `model`, node `classify` |
| refused | node `summarize`, field `provider_params` — *"this call site applies inline, where provider parameters are code rather than data; there is no applicable parameter rewriter, so the override is refused rather than dropped from the diff"* |
| not yet measurable | node `answer`, missing `context_drop_ratio` for `hierarchical-summary` |

`context_drop_ratio` is the measurement the drop gate judges on. It is emitted per node, as a ratio, by
`internal/telemetry/context_assembly.go:EmitContextAssembly`, and only by a lossy policy — so a node whose policy cannot lose
anything has no measurement here to be missing.

## Where to go next

- [Refusals are outcomes, not errors](/docs/concepts/refusals)
- [Context policies](/docs/concepts/context-policies) · [Memory strategies](/docs/concepts/memory-strategies) · [Graph and wiring](/docs/concepts/graph-and-wiring)
- [Glossary](/docs/concepts/glossary) — `axis`, `variant`, `config_hash`
