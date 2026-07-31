---
title: Use the Studio in the console
tier: guide
summary: Explore models and prompts per node in a browser, bind a configuration, and understand why nothing there is ranked.
platform_version: 0.20.0
boundary: The Studio is exploratory. It derives no score, names no winner and offers no promotion path — a bound cell is in force and unverified, and only a multi-seed evaluation turns it into evidence.
claims: console
order: 6
---

The job on this page: **try configurations in a browser without mistaking "I selected it" for "it is
better".**

## Where it is

Sign in to the console and open **Prompt & Model Studio**. It has four tabs and one governing rule.

| Tab | What it is for |
|---|---|
| **Matrix** | Agent nodes across, models down. The landing view: what is bound where. |
| **Prompt library** | Prompt versions and the diff between them. |
| **Bound nodes** | Which nodes read a configured value rather than an inlined one. |
| **Author** | Change a model, a provider parameter or a prompt from here rather than from the CLI. |

They are tabs rather than a long page for a reason that is easy to miss: this console is
**viewport-first**, and a section pushed below the fold is a section people do not find. Nothing was
removed by putting it in a tab.

## 🔴 The rule: nothing here is ranked

**No score. No winner. No promotion path.** A bound cell is *selected and in force*; it is never
*proven best*.

This is the most important sentence about the Studio, and it is deliberately inconvenient. A matrix of
nodes and models invites a "best" column, and a best column would be a ranking derived in a browser from
data the browser did not measure. The console renders what the platform computed and derives nothing of
its own, so it cannot disagree with the system of record — and a ranking it invented would be exactly
such a disagreement.

What the Studio gives you is **exploration**: try things, look at the diff, bind one. What turns a bound
configuration into a reason is a [multi-seed evaluation](/docs/guides/run-an-eval), and nothing else.

## Binding a configuration

Binding writes the value into a **binding document** the call site reads, rather than into the call site
itself. The trade is the same one described in [configure a variant](/docs/guides/configure-a-variant):

- **Inline** — the value is in the source. Simple; every future change is another codemod.
- **Bound** — the value is data. The next change is a data edit that reviews in seconds.

Bound mode is what makes the Studio useful for a value you expect to keep tuning. It is also a small
one-way door: once a call site is bound, unbinding it is another change.

## Reading the Matrix honestly

Three things the Matrix shows and one it does not.

**Shows:** what is bound at each node; which cells could be bound; which cells **refuse**, and by name.
A refusing cell is not blank — it says which of three things is missing. See
[refusals](/docs/concepts/refusals).

**Does not show:** which cell is best. There is no such column, and adding one would be the console
deriving a judgement it has no measurement for.

## From the Studio to a shipped change

The path out is the same one the CLI takes, and it is worth stating so the Studio does not look like a
shortcut around it:

1. Bind a configuration here — it is in force and unverified.
2. [Run an eval](/docs/guides/run-an-eval) over multiple seeds, with your own keys.
3. If the verdict passes and the workflow is at Assisted automation, the change can be
   [delivered as a pull request](/docs/guides/take-delivery) for a person to review and merge.

There is no button that skips to step 3. The absence is the design.

## Why no API key ever reaches your browser

The console holds the platform credential **server-side**, and the build fails if credential material
reaches a shipped browser chunk — the check runs over the JavaScript the browser actually downloads, on
every build.

Your **provider** keys are a separate matter: they never reach the console at all, because evaluation
runs on your machine with your keys. See [bring your own provider
keys](/docs/guides/bring-your-own-keys).
