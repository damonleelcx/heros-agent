---
title: Configure a variant and read the diff
tier: guide
summary: Change one call site, see the exact source change it would make, and decide before anything runs.
platform_version: 0.20.0
boundary: This produces a reviewable diff in an isolated worktree. It does not commit, does not push, does not run the changed code, and does not tell you whether the change is an improvement — that is what an eval is for.
claims: variant
order: 1
---

The job on this page: **make a change to one node and look at what it would actually do to your source,
before anything runs.**

## The two ways in

`heros author` is the direct one — you name the node and the value, and it answers. `heros apply` takes a
[Variant Spec](/docs/concepts/glossary) you already have and realizes it. Use `author` when you are
deciding; use `apply` when the decision is recorded in a file.

## Preflight a change

Start without `--apply`. This is the default on purpose: the first answer you want is *"would this even
work"*, not a diff.

```bash
heros author --repo . --node n_triage --model anthropic/claude-sonnet-5
```

It answers with one of three verdicts, and the third is the one people misread:

| Verdict | What it means | What to do |
|---|---|---|
| **Admissible** | The change can be expressed at that call site, and applying it will produce a diff. | Re-run with `--apply`. |
| **Refused, by name** | It cannot be expressed there, and the name says which of three reasons applies. | See [refusals](/docs/concepts/refusals) — one of the three is fixable by you. |
| **Not yet measurable** | It can be applied, and nothing has measured whether it helps. | Apply it if you want, then [run an eval](/docs/guides/run-an-eval). |

"Not yet measurable" is not a soft refusal. It means the change is admissible and no evidence exists
about it, which is a different statement from "this is a bad idea".

## Write the diff

```bash
heros author --repo . --node n_triage --model anthropic/claude-sonnet-5 --apply --out change.diff
```

Now you have a unified diff at `change.diff`, and **your working tree is untouched**. The change was
realized in an isolated worktree, so nothing you have open in an editor moved, and no branch was created
in the repository you are standing in.

That isolation is not a convenience. A tool that edited your files in place would make "what did it
change" a question you answer with `git diff` against your own uncommitted work — which is exactly the
moment you cannot tell your edits from its edits.

## Reading the diff

Look for three things, in this order.

**1 · How many call sites moved.** It should be one. A change to a node that touched three call sites
means the node identity is not what you thought it was, and that is worth understanding before you go
further.

**2 · Whether it is `inline` or `bound`.** `--apply-mode` decides. **Inline** writes the value into the
call site: simple, and every future change to it is another codemod. **Bound** writes the value into a
binding document and the call site reads it, so the *next* change is a data edit that ships as a diff a
reviewer reads in seconds.

Inline is the right default for a one-off experiment. Bound is the right default for a value you expect
to keep tuning — and it is a one-way door in the small: once a call site is bound, unbinding it is
another change.

**3 · Whether it builds.** A change that only parses is marked *syntax-checked*, and that marking travels
with it. A change that does not build is never run — not "run and reported as failing", never run.

## Turning a diff into a Variant Spec

The diff is the human artifact; the spec is the machine one. If you want the change to be reproducible —
in CI, next month, by somebody else — record it as a spec and apply that:

```bash
heros apply --repo . --spec variant.json --out change.diff
```

Same output, same isolation. The difference is that `variant.json` is a file you can review, commit and
re-apply against a later revision, and `--node ... --model ...` on a terminal is not.

## What has not happened yet

At this point you have a diff and **no evidence**. Nothing ran, nothing was scored, no model was called
by the tooling, and nothing is known about whether this change is an improvement.

That gap is the point of the next page. A diff is a proposal; a [scorecard](/docs/guides/run-an-eval) is
a reason.

## Common surprises

**The node id is not in your source.** Node ids come from the IR, not from your variable names. Run
`heros discover` first and read the ids out of `ir.json`.

**The change is refused and the reason is `call-site-cannot-carry-it`.** The value is computed at the
call site, or arrives through `**kwargs`, or is passed through a wrapper. This one is yours to fix —
making the value expressible at the call site makes the change applicable.

**Nothing was written.** You left off `--apply`. Without it, `author` previews and writes nothing, which
is the safe default rather than an oversight.
