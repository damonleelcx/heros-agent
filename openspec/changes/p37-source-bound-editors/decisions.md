# Decisions — P37 §2 (System Designer)

> Records tasks **2.1**–**2.5**. Each entry states the problem, the decision, why it is the right shape,
> what was rejected and what the decision costs. `design.md` carries the phase's reasoning; this file
> carries the calls that had to be made before code could be written, in the form the next reader needs.

---

## D-37.1 — The subject is `(workflow_id, node_id)`, and both are carried

**Task 2.1 · PRD §14 Q1 · implemented in [`web/console/src/lib/axisSubject.ts`](../../../web/console/src/lib/axisSubject.ts)**

**The problem.** A `node_id` is unique **within a workflow**, not across them. `heros discover` derives
node ids from the call site (`n_9f1c04ab`) or from the enclosing symbol (`recall`, `history`), and two
workflows in one repository routinely contain a node called `answer`. Nothing in the IR prevents it, and
nothing should — the ids describe the customer's code.

**The decision.** The subject is a pair. Both identifiers are required; a half-filled subject is refused
by `isAxisSubject` rather than tolerated.

**Why.** A subject carrying only the node id resolves to whichever workflow was read first. That is
silent, order-dependent, and different on two devices — and the thing it silently gets wrong is *which
of the reader's call sites they are editing*. This is R4 in the PRD's risk table and it is rated High for
that reason.

**Rejected.** *Node id alone.* One field to keep in step instead of two, and a picker with one column.
It is wrong for the reason above, and the failure is invisible: both nodes render, both are called
`answer`, and only the diff tells the reader which one they changed.

**The cost, and where it is paid.** Two fields must stay in step. They are only ever produced together,
by the resolver, and never assembled at a call site — so there is one place to get it wrong rather than
seven.

**🔴 Identity is not display.** Carrying both does not mean showing both. `subjectLabel` names the node
alone, and adds the workflow **only** when another candidate shares the node's display name. Showing
both by default would put a second identifier in front of every reader who has exactly one workflow —
which is the common case, and the case `interaction-simplicity-first` says to optimise for.

---

## D-37.2 — The resolution order, and why the last step is a question rather than a default

**Task 2.2 · implemented in `lib/subjectResolver.ts` (§3.4)**

The resolver answers in this order and stops at the first that applies:

| # | Input | Result | Why here |
|---|---|---|---|
| 1 | an **explicit selection** — the `?node=`/`?workflow=` link a `finding` carries (FR18), or the shell's own control | `resolved` | the reader, or a finding they clicked, said which one. Nothing outranks that. |
| 2 | the remembered choice in `SUBJECT_COOKIE`, **if it is still in the enumeration** | `resolved` | continuity across surfaces (FR2). Validated against the live list, never trusted on its own — `enumeration.ts`'s discard rule: a remembered subject the platform does not contain is dropped, because a picker must never offer a door that does not open. |
| 3 | no connected repository and no reported structure | `not_connected` | the customer's own boundary (D-37.5) |
| 4 | the most recently reported workflow has **exactly one** node | `resolved`, `sole: true` | FR3 — a reader with one candidate is asked nothing. `loadProjection` already picks the most recently reported *workflow* without asking; this extends the same resolution one level down. |
| 5 | more than one candidate and none chosen | `ambiguous` | FR2 — asked **once**, in the shell, and the answer applies to every axis surface |

**🔴 Step 4 is `resolved`, not "defaulted", and the difference is on the screen.** The name is displayed
even when there was exactly one candidate. Being *told which node was chosen* is not the same as being
*defaulted into one*: the first is a fact the reader can check in half a second, the second is R4.

**Rejected.** *A picker on each page.* Less plumbing, and it asks the common reader — one workflow, one
node — a question they did not arrive with. `lib/projection.ts` already argues this one level up:
*"Putting a workflow picker on each of them would make seven surfaces ask a question the reader did not
come there with."*

**Rejected.** *Silently defaulting at step 5 to the first candidate.* It removes a question and creates
the exact failure R4 describes. An `ambiguous` state that asks once is the cheaper end of that trade.

---

## D-37.3 — `axis-node-projection` is modified, not overridden

**Task 2.3 · delta at [`specs/axis-node-projection/spec.md`](specs/axis-node-projection/spec.md)**

P29 wrote *"The worked examples on each axis surface SHALL be retained"* to stop a redesign silently
dropping panels off the axis surfaces. That protection is **correct and is kept**.

What P29 could not anticipate is a rewrite that *relocates* a worked example rather than deleting it.
Its scenario reads "every panel present before is still present", which a **move** fails — even though
nothing was lost. Read literally, the requirement forbids P37 entirely; read loosely, it forbids nothing.

**The decision.** The requirement's **unit** changes from *the same page* to *a named destination*.
Nothing is removed; a panel with no destination is not cut. The delta restates the requirement header
verbatim, so the modification is unambiguous rather than a near-miss that folds as a second requirement.

**And it is strengthened, not only relaxed.** The second scenario gains a structural test: after P37 a
worked example may not appear in **the position the reader's own data occupies**. Before, example and
live data were distinguishable by labelling; now they are distinguishable by position, which survives a
copy edit.

**Why this is not a fence removal in disguise.** The protection P29 bought was *"a redesign may not
silently drop a panel"*. After this change that is still enforced, by a stricter artifact: §4.5's PR
enumeration plus fence 6.10, which fails the build on a destination link that does not resolve. The old
scenario could be satisfied by leaving a panel in place; the new one cannot be satisfied by leaving a
panel *anywhere* unaccounted for.

**Escalation.** Task 9.2 requires this to be reviewed by whoever signed off P29, separately from the
surface rewrite that motivates it. That review is a sign-off item precisely because "the phase that
needs the requirement relaxed is the phase proposing the relaxation" is the shape that should never pass
on its own authority.

---

## D-37.4 — No new table; the subject is a cookie

**Task 2.4 · PRD §14 Q5**

**Confirmed: this phase adds no database table, no migration and no new endpoint shape.** Every question
the seven surfaces ask is already answered by the IR (`linkingest.WorkflowIR` → `runlink.WireIRNode`) and
by the registries. The one genuinely new persisted thing is *which node the reader was last looking at*,
and that is per-person UI state rather than a domain object.

`careful-table-creation`: a table is a one-way door. The alternatives were evaluated and one of them is
strictly better here.

| Option | What it buys | What it costs |
|---|---|---|
| **A cookie (chosen)** | readable during server render, so the subject is named on the first byte; disappears with the browser; no schema | forgotten on a device change |
| `localStorage` | the same, minus the cookie header | **cannot be read while rendering.** The name would arrive after paint — the reader sees one node, then another, which is a worse version of R4. Fixing that needs a blocking inline `<script>` in `<head>`, which this console's CSP (`default-src 'self'`, no `unsafe-inline`) does not admit. |
| A per-person server row | survives a device change | a new table, a migration, a retention question and a deletion path — for a preference somebody changes twice a minute |

**Why not `subjects.ts`.** That module is this session's *ordering hint*, and its own header states what
it must never become: *"A cache of platform data… a stale status offered from here would be the console
telling the user something that was true once."* The subject cookie stores **two identifiers and
nothing else** — no symbol, no file, no language. Everything else on `AxisSubject` is re-resolved from
the platform on each render, so the cookie cannot become a stale copy of anything.

**The one thing this does not survive.** A reader who switches laptops starts at step 4 of D-37.2, which
is where a first-time reader starts. That is the whole cost, and it is level 7 at worst.

---

## D-37.5 — `not_connected` is the fourth state, and it is a 200

**Task 2.5 · `SUBJECT_STATES` in `axisSubject.ts`**

`loadProjection` already keeps three transport treatments deliberately distinct, and its comment states
why: *"a 404 would be indistinguishable from a transport failure and would send the reader to look for a
broken deployment when the truth is that they have not opted in."*

`not_connected` is a **fourth**, beside them and never collapsed into one:

| State | Whose fact it is | What the reader does next |
|---|---|---|
| `not_mounted` | the deployment's | nothing — the capability is not served here |
| `read_failed` | **ours** | retry; nothing has been lost |
| `not_reported` | the customer's — structure not sent | run `heros link --with-ir` |
| **`not_connected`** | the customer's — **no repository connected at all** | connect a repository, and meanwhile read the document |

**Why it is not folded into `not_reported`.** They have different next actions and different owners. A
reader who has connected a repository but sent no IR needs a CLI command; a reader who has connected
nothing needs the connection flow. Rendering the first sentence to the second reader sends them to a
terminal to run a command that cannot work yet.

**Why a 200.** Because it is a **business state**, and `web-console`'s standing rule is that no 404 is
mapped to one. A 404 says "this route does not exist here", which is a deployment fact; a reader who
meets one goes looking for a broken ingress. `not_connected` is the opposite — everything is working and
the reader has not opted in — and it is delivered as a 200 carrying that word.

**The consequence that makes it worth building.** The disconnected reader is the **first-time** reader
(PRD §4, row 4). The right destination for them is the reading surface, so `not_connected` links there
as well as to the connection flow. That is what makes moving the explanation an improvement for that
reader rather than a loss — the single most important sentence in this design's argument, because it is
the one that answers "did you just delete the docs?"

---

## D-37.6 — "in the shell" means one resolver, not `app/app/layout.tsx`

**Recorded during §3 · implemented in `web/console/src/components/axisFrame.tsx`**

**The conflict, stated rather than averaged.** Design D1 says the subject is resolved *"in the shell"*.
The console's shell says something else, in its own words:

> *"It renders no subject and no data … the shell holds no fetch at all — so a slow platform cannot delay
> the chrome, and the reader always has somewhere to go."*

**Both are right, and they are about different things.** D1's requirement is that the question is asked
ONCE and answered by ONE resolver. P9's rule is that navigation cannot be held hostage by a platform
read. Putting `resolveSubject()` in `app/app/layout.tsx` would satisfy D1's letter and break P9's rule
for **all thirty routes**, including the twenty-three that have no subject at all.

**The decision.** The resolution lives in `AxisFrame`, the component the seven axis surfaces share. One
resolver, one call per request, the answer displayed on every surface — and the chrome still renders
while the platform is slow.

**Rejected.** *A Next.js route group `(axis)/`.* It gives a real nested layout and does not change any
URL, which is genuinely the tidier shape. It also moves seven directories that a dozen tests address by
path, for a property `AxisFrame` already has. Blast radius without benefit.

**How the type system carries FR4.** `AxisFrame`'s `children` is a **function of the resolved subject**,
not a node. There is no way to render a surface's editor without a subject, so there is no code path on
which a fixture could occupy the reader's data position. Fence 6.2 proves it at runtime; the signature
prevents it at compile time.

## What §2 deliberately did not decide

- **Where an axis's current value comes from when the IR has no field for it.** That is §5.3's answer —
  `not_measured` with a named missing input — and it is a backend decision about a read, not a
  system-design one about a boundary. Stated here only so it is not mistaken for an omission: today
  `WireIRNode` carries `ContextPolicy`, `Provider`, `ModelID`, `ToolCount` and `Language`, and it does
  **not** carry a memory strategy, a harness envelope or a loop. Four of the seven axes therefore have
  **no** resolvable current value, and the honest render for them is `not_measured`, not a default.
- **The visual design of the kit.** §3.1 extracts it from a panel that already works. A design decision
  taken here would be a design decision taken twice.
