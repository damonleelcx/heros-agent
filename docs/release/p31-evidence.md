# P31 — acceptance evidence

- **Date:** 2026-08-21
- **Subject:** [P31 The Conversational Console](../../openspec/changes/p31-conversational-console/)
- **Acceptance target:** task **6.11** — *"Browser acceptance for A1 — a question reaches a per-surface
  answer with no CLI installed. A green build is not acceptance."*

> 🔴 This document exists because the task list says a green build is not acceptance. Everything below was
> observed in a browser, against a real platform, over a real repository. Where a fence covers the same
> ground it is named — but the fence is not the evidence, the run is.

---

## 1. What was stood up

| | |
|---|---|
| Platform | `cmd/agentd`, real binary, `auth_mode=required` |
| Database | PostgreSQL 16, empty at start — **48 migrations applied on boot** |
| Console | `web/console`, production build, `next start` |
| Identity | P28 password seam, self-serve sign-up on, `CONSOLE_SESSION_STORE=platform` |
| Repository | **`github.com/NousResearch/hermes-agent`**, cloned at `efb6b40f94ebce3c1f0cfe197942b17d68e2136b` |

Boot log line for the surface under test:

```
served  p31_conversational_console (ask in English on /app/ask; typed messages stream back over SSE,
        every finding carries its evidence, and an approval routes to the existing gate)
```

## 2. The path a person actually walked

**No CLI was installed by the person.** The steps in the browser were:

1. `/create-account` — created the organization *Nous Research* and its first owner, with a password.
   Declined optional data collection at the consent banner.
2. `/app/settings/members` — created a machine API key (`hermes CI`).
3. `/app/ask` — asked questions.

Between 2 and 3, standing in for CI, the IR was reported with that key:

```
go run ./cmd/discover -repo …/hermes-agent -workflow-id nousresearch/hermes-agent -out hermes-ir.json
  → discovered 28 node(s) across 6998 package(s)

POST /api/v1/workflow-ir  (contract p11.workflow-ir.v1, machine key)
  → 201 {"accepted":true,"nodes":28,"workflow_id":"nousresearch/hermes-agent"}
```

That step is the CI half of the product and is what `heros link --with-ir` performs. **The person did not
run it, and that is the point of the acceptance**: A1 is *"a question reaches a per-surface answer with no
CLI installed"*, and P32 (repo intake) is the phase that removes the reporting step as well.

## 3. A1 — a question reaches a per-surface answer

**Asked:** *"what does my agent actually do, step by step?"*

The transcript that came back, in order:

```
PLAN                            intent: graph          (before any step ran)
  resolve the workflow and its current source revision   → /app/workflows
  read what /app/workflows knows about this question     → /app/workflows
  check every claim against the artifact behind it       → /app/workflows
  TURN CEILING 4 · TOKEN BUDGET 40,000 · TOOL CALLS 12 · TIME 240s

Working  resolve the workflow …            38,000 tokens · 10 reads · 240s left
FINDING  workflows · Measured
         "this workflow reported 28 nodes at revision efb6b40f (28 python)"
         Evidence → workflow-ir:nousresearch/hermes-agent@efb6b40f94ebce…
Working  read what /app/workflows knows …  37,000 tokens ·  9 reads · 240s left
FINDING  … (as above)
Working  check every claim …               36,000 tokens ·  8 reads · 240s left
FINDING  … (as above)
Verifying  checking each claim against the artifact behind it

RESULT   Finished
         The run finished without reaching any of its limits.
         Completed 3 of 3 planned steps on the graph question.
         resolve-workflow  Done · read-workflows Done · check-evidence Done

Trace    a5da2b01faceecc136c3a1170844bdc8   [Copy]
```

**28 nodes and revision `efb6b40f` are the real hermes-agent**, discovered from the checkout above.

Requirements observed on screen: FR1 (typed kinds), FR2 (every finding carries an evidence reference),
FR16 (phases), FR17 (four limits before the first step), FR19 (every planned step reconciled), FR23 (a
copyable trace id), task 4.11 (the four facts a spinner withholds), task 4.13 (`satisfied` is stated).

## 4. `not measured` is a state, not silence

**Asked:** *"what does this node remember between calls?"*

```
FINDING  memory · Not measured
         "no verdict for memory has been reported on any of this workflow's 28 nodes"
         This was examined and no measurement could be taken. What was missing:
         this workflow's structure is on record, but no per-node verdict for this axis came with it —
         run `heros apply` (or `heros link --with-ir` from a CLI that carries the transform engine)
         so each node's answer for this axis is reported
         Evidence → axis-projection:nousresearch/hermes-agent@efb6b40f…

RESULT   Finished · Completed 0 of 3 planned steps on the memory question.
         resolve-workflow  Not measured …  read-memory  Not measured …  check-evidence  Not measured …
```

Correct and honest: the tenant reported **structure** and no **axis verdicts**, so the memory axis has
nothing measured — and the message names the input that is missing rather than leaving a blank.

## 5. FR26 — out of scope, refused BY NAME

**Asked:** *"change my plan to the enterprise one"*

```
NOT SOMETHING THIS SURFACE DOES
  changing a plan is not something this surface does; /app/billing does changing your plan and
  seeing what it includes
  → Go to the surface that does this                       (links to /app/billing)
  ▾ What this surface can be asked (14)                    (the fourteen, from the intent table)
```

No run was started. The fourteen questions are rendered from `conversation.CanDo()`, so the boundary the
user reads and the table a fence checks are the same list.

## 6. §5.2 — the stream is not buffered

Measured from inside the signed-in page, through the real path (browser → console BFF → platform):

```
content-type       text/event-stream; charset=utf-8
x-accel-buffering  no
10 frames, arriving at 14, 14, 14, 16, 16, 29, 29, 43, 43, 43 ms
                    → 4 distinct arrival moments
```

Four distinct arrivals for ten frames: the edge released bytes as they were produced. A buffering hop
produces **one** arrival moment for all ten.

🔴 **This run corrected the check itself.** `scripts/edge-proof.mjs` originally asserted a minimum elapsed
spread of 250ms — and this turn completed in 43ms, so a perfectly streaming edge would have failed it.
Absolute spread measures how long the work took; it says nothing about the proxy. The check now counts
**distinct arrival moments**, which measures only the thing it is about.

## 7. 🔴 Two defects this run found that every gate had passed

Both were found by opening the surface, both had a green build behind them, and both are recorded here
because they are the shape of failure this acceptance exists to catch.

### 7.1 `@apply` of a project class silently emptied the entire stylesheet

**Symptom:** every page in the console rendered as unstyled HTML.

**Cause:** the new CSS used `@apply chip;` and `@apply stat__label;` — classes the stylesheet defines
itself. Tailwind v4's `@apply` takes **utilities**; given a project class it produces nothing **and takes
the surrounding `@layer components` with it**. The shipped stylesheet went from 114KB to 5KB of font
declarations.

**Why nothing caught it:** `npm run build` succeeded. `scan:tokens` passed. `tsc` passed. All 661 console
tests passed. The only symptom is visual, and no gate looks at pixels.

**Fix:** the declarations spelled out, plus a new rule in `scripts/scan-tokens.mjs` that fails on any
`@apply` naming a class the stylesheet defines. **Mutation-verified**: re-introducing one turns the scan
red, naming the file and the class.

### 7.2 A `not measured` card named the wrong next action

**Symptom:** the memory finding said *"no structure has been reported for this workflow — run
`heros link --with-ir`"* on a card whose own claim, one line above, said *"…on any of this workflow's 28
nodes"*. Two sentences on one card contradicting each other, and the named command was one the reader had
already run.

**Cause:** one string covered two different absences. A projection full of `not-reported` cells looks
identical whether the **structure** is missing or only the **verdicts** are — and they have different
remedies.

**Fix:** two strings, chosen on `projection.NodeCount`. §4 above is the corrected output.

**Why it matters more than it looks:** a "no data" message that names the wrong next action is worse than
one that names none — it costs the reader the time to run the command, plus the time to work out why
nothing changed.

## 8. Reproducing this

```bash
# 1 · Postgres.  ⚠️ Bind 127.0.0.1 explicitly — colima's forwarder does not publish a bare -p mapping.
docker run -d --name heros-p31-pg -e POSTGRES_PASSWORD=heros -e POSTGRES_USER=heros \
  -e POSTGRES_DB=heros -p 127.0.0.1:55561:5432 postgres:16-alpine

# 2 · Platform.  A plan catalog is required or the ACCOUNT SYSTEM does not mount, and without it there
#     is no person — and a conversation is per-person, so the whole surface refuses.
cp deploy/config/plans.json /tmp/p31/plans.json
DATABASE_URL="postgres://heros:heros@127.0.0.1:55561/heros?sslmode=disable" \
HEROS_SELF_SERVE_SIGNUP=true PLAN_CATALOG_PATH=/tmp/p31/plans.json \
  ./agentd -config /tmp/p31/config.json      # auth_mode=required + one console credential

# 3 · Console.
cd web/console && HEROS_RELEASE_OFFLINE=1 npm run build
PLATFORM_API_BASE=http://127.0.0.1:4399 \
CONSOLE_PLATFORM_CREDENTIAL=… CONSOLE_TENANT_IDENTITY=password CONSOLE_SESSION_STORE=platform \
  npx next start --port 4320

# 4 · The repository.
git clone --depth 1 https://github.com/NousResearch/hermes-agent.git
go run ./cmd/discover -repo ./hermes-agent -workflow-id nousresearch/hermes-agent -out hermes-ir.json
# then POST it to /api/v1/workflow-ir with a machine key made in the console
```

Then: `/create-account` → `/app/settings/members` (make a key) → `/app/ask`.

## 9. Five intents, one repository — the final run

Driven against the finished build, through the console's own BFF, on the same hermes-agent checkout.
Five different intents, five different surfaces, five different answers:

| Question | Intent | First finding | Reconciled |
|---|---|---|---|
| what does my agent actually do, step by step? | `graph` | **measured** — *"this workflow reported 28 nodes at revision efb6b40f (28 python)"* | 3 of 3 |
| what did you measure, and what did you not? | `coverage` | **not_measured** — *"no verdict for prompt, model, skills, tools, context, memory, harness has been reported on any of this workflow's 28 nodes"* | 0 of 3 |
| how many turns does it take, and in what loop? | `harness` | **not_measured** — *"no verdict for harness has been reported on any of this workflow's 28 nodes"* | 0 of 3 |
| should these nodes run in this order? | `graph_order` | **not_measured** — *"…28 nodes at revision efb6b40f, but their ordering was not reported"* | 0 of 3 |
| what happened in that run? | `run_history` | **not_measured** — *"no run has been linked to this organization"* | 0 of 3 |

Every turn: `plan → progress → finding → progress → finding → progress → finding → progress → result`,
stop reason `satisfied`, its own trace id.

🔴 **The `graph_order` row is the one to read twice.** `WorkflowIR` carries nodes and no edges, so the
platform genuinely does not know what runs before what. It says so and names the missing input — it does
**not** infer a sequence from the order of a JSON array. A linear list is not a claim about concurrency,
and turning one into the other would be a confident wrong answer about the customer's code, which is the
single most expensive thing this surface could do. [P34](../prd/P34-harness-loop-graph-split.md) is the
phase that makes ordering expressible.

Four of the five answer `not_measured` because this organization reported **structure** and no **axis
verdicts**. That is the honest state of a workflow whose CI ran `--with-ir` and not the transform engine,
and each message names the command that changes it. A surface that answered those four with silence would
read as *"nothing is wrong here"* — which is a claim, about a repository, with nothing behind it.

## 10. The checks that cover the same ground automatically

| | |
|---|---|
| `make go` | every Go fence, including the emitter's refusals and the untrusted-source boundary |
| `make p31-fence-redcheck` | **10 refusals proven able to go red** by removing each check in turn |
| `make intent-holdout` | fourteen per-intent recall rows, abstention precision, redirection recall |
| `make console-test` | the console's design-language, type and 674 behaviour tests |
| `make console-edge-proof` | §5.2 against a running edge, with the corrected measurement |
