## Why

P31 shipped a conversation that **routes to** the console. It does not replace it.

Read from the code rather than from P31's report: the router is strong (14/14 intents at 100% recall
over 76 held-out questions, abstention precision 100%), and the intent set is exactly the twelve
task-domain routes in the left navigation, fenced against drift. But the depth is one sentence. Every
answer is a single aggregate `Claim` — *"N of M (node × axis) pairs can be edited"* — over a projection
the reader built in full and then discarded. There is no node scoping: `SurfaceReader.Read` takes a
tenant and a workflow and nothing narrower. There is no history: `Router.Route` sees one sentence, so a
follow-up is refused by construction.

And the conversation **depends on the pages existing**. A `plan` step links to its surface, a `finding`
links to its surface, a `refusal` links to its surface, and one reader hardcodes a route into its own
prose (`internal/api/conversationreader.go:308` renders *"…available to compare on /app/variants"*).
Delete the routes today and the agent recommends a 404.

Underneath that sits the sharper problem. The fence that stops the intent set drifting away from the
product — `TestIntentSetEqualsTheWorkingSurfaceSet` — is **anchored to the console route table**.
Deleting ten routes breaks its anchor, and the two cheapest ways to restore green are to delete the
intents alongside the routes, or to relax the fence to skip removed ones. Either restores green by
removing the protection that P26's fourteen phases of operator-console rot produced. So the first thing
this change does is not a feature: it re-anchors the fence on something a deleted route cannot satisfy,
and it does that **before** the first route is deleted.

## What Changes

- **ADDED** `BackedByReader` — an intent may name a registered reader instead of a console route, and a
  reader registry becomes the fence's anchor. An intent with no reader, or a reader with no intent,
  fails the build.
- **ADDED** a `Detail` field on `finding`, carrying a **closed** union of four shapes — `grid`,
  `table`, `diffstat`, `record` — so a message can carry a surface's content rather than a summary of
  it. Each reader declares exactly one shape; a declared-but-empty shape is refused by the emitter, the
  same way an evidence-less finding is refused today.
- **ADDED** node scoping: `Read` takes a `Subject`. A question naming a node the reported IR does not
  contain **refuses naming the string the person typed** and lists the nodes that exist — it does not
  fall back to the workflow-wide answer.
- **ADDED** single-conversation carry-forward: the router receives the prior turn's resolved
  `(intent, subject)`, and a carried-forward subject is **stated in the `plan`**. Silent inheritance is
  refused as a design.
- **MODIFIED** every reader to return its declared depth (§6.6 of the PRD), bounded by a server-side
  cell ceiling that carries the count it omitted.
- **REMOVED** ten read-only console routes — `/app/workflows`, `/app/runs`, `/app/variants`,
  `/app/transforms`, `/app/delivery`, `/app/wiring`, `/app/context`, `/app/memory`, `/app/harness`,
  `/app/coverage`, with their sub-routes — and their navigation entries.
- **REMOVED** the hardcoded `/app/variants` reference from `compare`'s prose, and every `/app/*` href
  pointing at a deleted surface.

## What does NOT change

🔴 Stated because the temptation to widen here is the phase's main risk.

- **The act path stays absent.** `proposal`, `approval_request` and prose `answer` remain unemitted in
  production. Their render paths in `messages.tsx` are **retained deliberately** — P40 needs them.
- **`/app/studio`, `/app/authoring` and the proposal routes stay mounted.** They are the only surfaces
  through which a customer changes anything. Deleting them before the conversation can propose removes
  the capability from the product, and no gate in this repository would go red when it happened.
- **The effect table is untouched.** `finding` gains a payload; it does not gain an effect.
- **`assess` and `improve` stay unmounted.** P33 and P35 own them.

## Impact

| Area | Change |
|---|---|
| `internal/conversation` | `Backing`, `Subject`, reader registry, `Detail` union, emitter refusal, router carry-forward |
| `internal/api/conversationreader.go` | every reader returns depth; the route reference is deleted |
| `web/console/src/lib/routes.ts` | `WORKING_SURFACES` narrows to the two surviving routes |
| `web/console/src/components/conversation/` | four detail renderers; evidence chip stops being a link |
| `web/console/src/app/app/` | ten route trees deleted |
| `internal/eventname` | `console.route.retired_hit` |
| Docs | [PRD P39](../../../docs/prd/P39-conversation-only-console.md) · sales claims ladder |

**Blast radius:** every logged-in customer's navigation, in one deploy. **Reversible** within a 30-day
retirement window during which a deleted route explains where its surface went and pre-fills the Ask box
with that surface's question. The window closes on a counter, not on the calendar alone.
