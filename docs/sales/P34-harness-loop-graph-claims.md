# P34 Harness / Loop / Graph — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-08-22)
- **Audience:** anyone who describes P34 to a customer — a deck, a demo, a scoping call, an SoW, a
  security review, a renewal.
- **Rule:** this phase's strongest claim and its most honest limitation are the *same sentence*. Say
  both halves together, always, and say them before you are asked.

---

## 1. The capability, in one paragraph

The platform used to have one axis called **harness** that meant two things at once — *how many turns a
node runs* and *what that node is allowed to do*. Those are decisions made by different people, reviewed
differently, and changed for different reasons. P34 splits them into **loop** (the iteration policy an
engineer chooses) and **harness** (the execution envelope an operator imposes), and adds a third,
**graph** — concurrency, conditional routing, and how results combine where calls converge.

Three customer sentences that used to land on one axis, or on none:

| The customer says | Before P34 | After |
|---|---|---|
| *"stop after four turns; reflect between them"* | landed on `harness` | **loop** |
| *"never spend more than a dollar and never reach the network"* | landed on `harness` | **harness** |
| *"run these two calls in parallel and merge the results"* | **inexpressible** | **graph** |

---

## 2. ✅ Sayable on ship (task 10.1)

> **Three named axes. For the first time, parallel steps and conditional routing are things this
> platform can configure and verify.**

Specifically, all of these are true today and each has a place to check it:

| Claim | Where it is true |
|---|---|
| A node's control loop and its execution envelope are separate configurations, separately reviewable | `DimLoop` / `DimHarness`; two registry kinds, two sealed entries |
| An operator can set a turn ceiling and a spend ceiling that an engineer cannot exceed | refused at resolve, **naming both numbers** |
| Raising a policy ceiling changes no engineer's configuration and orphans no measurement | the two live in separate sealed entries; asserted by a test |
| A spec can declare that two calls run concurrently, and how their results combine | `graph_groups`, validated against the customer's own typed I/O contracts |
| A conditional edge's predicate is checked against the program's real lexical scope before anything is generated | the same validator a prompt-slot binding uses |
| A loop needing a tool executor, a planner or a critic is refused **before** a change is generated, not when a run reaches the node | moved left from run time to resolve |

**The demo that lands.** Set a four-turn loop on a node; set an envelope with a ceiling of two. The
platform refuses, and the refusal says *"the loop asks for max_turns=4 and the envelope's turn_ceiling
is 2 … either lower max_turns to 2 or less, or ask whoever owns the envelope to raise turn_ceiling."*
Two numbers, two people, one sentence. That is the whole phase in ten seconds.

---

## 3. 🚫 NOT sayable (task 10.2)

> **This platform does not orchestrate anything.**

It **configures and verifies the customer's own graph**. It does not run their workflow, does not
schedule their calls, does not own their runtime, and is not in the path when their agent executes.

### Why the distinction is not pedantry

An orchestrator is a **dependency**: it is in the customer's request path, it can be the reason their
product is down at 3am, and it is a procurement conversation about vendor lock-in. This platform is a
**tool that reads their repository, proposes a change, and proves whether the change was better**. The
change ships as a diff into *their* source and runs on *their* infrastructure whether we exist or not.

Claiming orchestration wins a slide and loses the security review — and it is the claim that gets
quoted back during an incident that had nothing to do with us.

### The specific words to avoid

| Do not say | Say instead |
|---|---|
| "we orchestrate your agents" | "we configure and verify your graph; it runs on your infrastructure" |
| "we run your workflow in parallel" | "we let you declare that two calls may run concurrently, and we verify whether it helped" |
| "our runtime executes your loop" | "the loop runs in your process, in code we wrote into your repository as a reviewable diff" |
| "we control what your agent can reach" | "we let you *declare* an envelope and we refuse configurations that exceed it — the sandbox enforcing it is yours" |

---

## 4. 🔴 The compatibility promise, and its honest half (task 10.3)

Say **both sentences**, in this order, in the same breath.

> **Every configuration authored before this change keeps working, and keeps its measurements.**
>
> **The price is a legacy path that exists permanently.**

### The promise, in the customer's terms

A configuration in this platform is identified by a content hash, and every measurement ever taken is
filed under that hash. The clean way to split an axis would have changed the identity of every
multi-turn configuration in existence. Nothing would have errored. Their board would simply have had
less evidence on it than it did the week before, and nobody would have known why.

So we did not do that. Specs authored before P34 resolve unchanged, produce the same hash, and their
scores still join. **This is a stronger claim than most platforms can make, and it is worth saying out
loud** — most vendors solve this by asking you to re-run your evaluations.

### The honest half, and why saying it first is better

The cost is that the old shape is **permanently supported**. There is no deprecation date, and that is
a decision on the record rather than an oversight:

> A date does not shorten the problem; it hands it to somebody else on a day when the reasoning has been
> forgotten. On that day the arithmetic is unchanged — every historical measurement becomes unreachable.

If a customer asks *"when does the old way stop working?"*, the answer is **"it doesn't"**, and that is
the good answer. Removing it would require amending an architecture decision record, which is a
deliberately high bar.

### If they push on "isn't that technical debt?"

Three sentences, in this order:

1. *"It is a maintenance cost we chose over a stability risk. The alternative was making your historical
   measurements unreachable, and we are not willing to trade your evidence for our tidiness."*
2. *"It costs us a read path. It costs you nothing — you cannot author the old shape, so you will never
   accidentally create one."*
3. *"The decision is written down, with the arithmetic, in ADR-014. We will show it to your architects."*

---

## 5. What is NOT delivered, and must be said before a demo

🔴 **Concurrency, conditional routing and merge can be declared, resolved, hashed, validated against the
customer's typed contracts, compared and proposed — and are written into source in NO language today.**

That is not a bug and it is not a roadmap dodge; the console says it on the page, per form, naming what
is missing. But if a prospect sees "run these two in parallel" on a slide and asks for the diff, and the
first they hear of this is the refusal, we have created the gap ourselves.

**Say it like this:** *"You can declare it, we validate it against your actual I/O contracts, and it
becomes part of the configuration's identity so you can compare it. What we don't do yet is write it
into your source — and we refuse rather than pretending, because a change we claimed to apply and
didn't would give you a measurement that is wrong and looks right."*

That last clause is the one that converts the limitation into the reason to trust the rest.

---

## 6. Claim manifest entries

Two entries were added to `web/console/src/content/capabilities.ts` with `shipped: true`
(`axis-split`, `envelope-ceilings`). A third — topology materialization — is deliberately **absent**:
the public surface physically cannot claim it until a rewriter exists, which is `scan-claims.mjs`
working as designed.
