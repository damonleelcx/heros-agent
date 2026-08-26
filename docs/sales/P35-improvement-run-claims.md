# P35 The Improvement Run — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-08-26)
- **Audience:** anyone who describes P35 to a customer — a deck, a demo, a scoping call, an SoW, a
  security review, a renewal.
- **Rule:** this phase has **two boundaries that must be stated out loud, before you are asked**, and
  both of them are strengths when you say them first and liabilities when a customer finds them.

---

## 1. The capability, in one paragraph

A person types a sentence. It becomes a **bounded plan** — which surfaces will be looked at, how many
changes will be tried, what it may spend, and when it stops — and that plan is shown **before anything
runs**. Each candidate change is applied in an isolated worktree and scored on **held-out** data. Only
the ones that pass the gate are offered, each on its own card with its measured delta and its diff, and
each approved **individually**. On approval the change is applied and **measured a second time**; if
that second measurement disagrees with the first, the change is **withdrawn before it reaches the
repository**. What survives becomes a pull request with the evidence attached.

**The one-line version:** *ask in English; when a change proves itself on held-out data, the agent opens
a pull request with the evidence attached — you review and merge.*

---

## 2. ✅ Sayable on ship (task 10.1)

> **When a change proves itself on held-out data, the agent opens a pull request with the evidence
> attached. You review and merge.**

Every clause of that sentence is load-bearing and every one of them is checkable:

| Claim | Where it is true |
|---|---|
| A question becomes a plan with a scope, a candidate cap, a spend budget and a stopping condition, **shown before anything runs** | `improvementrun.Translate`; the plan and the run are two separate API calls, and the first one spends nothing |
| A question that cannot be bounded is **refused**, not run with defaults | six named refusal causes, each with its own next action; `TestEveryRefusalCauseHasItsOwnSentenceAndANextAction` |
| Above a disclosure threshold, nothing runs until a person has acknowledged the plan | the acknowledgement is recorded against a plan id derived from every bound, so it cannot authorize a different plan |
| Only a change that passed the **held-out** gate is offered | delivery reads the verdict from the authoritative oracle, never from a flag on the proposal |
| A high-scoring change that fails a gate is **not** offered, however high its score | `TestConversationalRun_GateFailingHighScorerNotDelivered` |
| The measured delta travels with the proposal **with its confidence interval and the number of cases behind it**, everywhere it renders | a proposal cannot be constructed without them |
| Each change is approved **on its own**, and declining one continues the run | there is no bulk control anywhere, and a reflection test asserts no method takes a list |
| Every approved change is **re-measured after it is applied** | and a disagreement withdraws it — see §4 |
| The pull request carries the axis, the node, the delta with its interval, how decisive the eval set was, and **how to revert** | `forgedelivery.RenderPRBody`, contract version `pr-body/v2` |
| Asking twice returns the **same** pull request rather than opening a second | idempotent per `(config_hash, source_revision, target)` |

**The demo that lands.** Ask *"fix what you can prove."* Show the plan — nine axes, a cap, a budget, a
stopping condition — and say *nothing has been spent yet*. Run it. Show two verified proposals and one
axis that generated a candidate and verified none, and say *that is the gate doing its job*. Approve
one. Show the pull request. Then run the withdrawal demo (§4). **The withdrawal is the demo**, not the
pull request.

---

## 3. 🚫 NOT sayable (task 10.2)

### 3.1 It does not merge

> **The platform opens pull requests. It does not merge them.**

Auto-merge is the **Autonomous** automation level and is **Enterprise-only**. Below it the platform
never merges, whatever the forge would permit — tested at every automation level, and structurally
unreachable from the console path because delivery is requested at Assisted and the merge branch is only
reached at Autonomous.

**Words to avoid:** *"it fixes your code", "it ships changes", "hands-free", "self-healing",
"autonomous improvement" (unqualified), "it maintains your agent".*

**Say instead:** *"it proposes a change you merge"*, and if the Autonomous level is genuinely in scope,
name it as an Enterprise level with its own approval and its own kill switch.

### 3.2 It does not fix a codebase

> **It changes the configuration of an agent — the model, the prompt, the context policy, the memory
> strategy, the tools, the harness, the loop, the graph — and it changes it as a reviewable diff.**

It is not a refactoring tool, not a bug-fixer, and not a code-quality product. A customer who hears
*"fixes your codebase"* is buying something else, and they find out in week two.

### 3.3 It is not a background service that improves things over time

Runs are **explicit**. There is no scheduler in this phase, and when one arrives it will **stop at
proposals** — a scheduled run never delivers, at any automation level, because delivery requires a
per-proposal approval and an approval requires a person. Do not describe this as *"it keeps your agent
tuned"*.

---

## 4. 🔴 The two boundaries to state OUT LOUD, first (task 10.3)

Both of these sound like weaknesses and are strengths. A customer who hears them from you evaluates
them. A customer who discovers them evaluates *you*.

### 4.1 The platform needs write access on the console path

> **To open a pull request from the console, the platform holds a write credential for that repository.
> It is per-repository, least-privilege, revocable by you, and used only after a person approved a
> specific change.**

Say all five properties. They are what makes it survivable, and each one is a fact rather than a promise:

| Property | What makes it true |
|---|---|
| **Per-repository** | an installation names the repositories it covers, and "all repositories" is not expressible — an installation selecting none is refused rather than read as org-wide |
| **Least-privilege** | it may open and update pull requests and push the branches behind them. Nothing else. Widening it is a spec change, not a configuration choice |
| **Revocable by you** | you revoke it in your own forge settings; we need no involvement |
| **Immediate** | revocation stops pushes on the **next call**, not at the next token refresh — the coverage check runs before a token is even requested |
| **Separate from read** | the read connection that lets us assess your repository is a **different grant with an independent revocation**. Revoking write does not cost you your assessments; revoking read does not leave us able to push |

**If the customer's CI can open the pull request instead, it does.** That is the default everywhere
except the console, and on the CLI and CI paths the platform holds **no forge credential at all**.

### 4.2 A change can be withdrawn *after* you approve it

> **Every approved change is measured a second time after it is applied. If the second measurement does
> not agree with the first, the change is withdrawn before it reaches your repository — and you are
> shown both numbers.**

This sounds like a failure. It is the product working, and it is the single most persuasive thing to
demonstrate, because it is the one behaviour a competitor's demo will not have.

**How to say it:** *"the first measurement is a prediction on held-out data. The second is an
observation of the applied result. We let them disagree, because a check that can only confirm is
theatre — and when they disagree we tell you both numbers, because 'this looked like +8% and measured
+1% ± 4%' is information about your eval set as much as about the change."*

⚠️ **Do not present a withdrawal as an error, and do not apologise for one.** A rising withdrawal rate is
a finding about the customer's eval set — usually that it is noisier than they thought — and that is a
conversation worth having with them, not a defect to hide.

---

## 5. What to say when it produces nothing

There are **seven** distinct reasons a run can find nothing to propose, and the product names which one
rather than showing an empty screen. Two of them mean the customer is **done**, not broken:

| The screen says | It means | Their next step |
|---|---|---|
| No runs have been linked | we have nothing to attribute cost to | run an eval and `heros link` |
| The linked runs carry no per-node metrics | an older CLI recorded no breakdown | re-run on a current CLI |
| No source snapshot has been pushed | no node carries a pattern label | `heros push-source`, or connect the repository |
| This deployment publishes no model tiers | "cheaper" is not expressible here | an operator publishes a catalog |
| The linked run and the discovered graph are at different revisions | the numbers and the shape describe different code | push source at the measured revision |
| ✅ **Nothing dominates cost or latency** | **there is nothing to improve** | nothing — this is a healthy result |
| ✅ **A bottleneck was found and every operator declined it** | usually the node already runs the cheapest published model | nothing |

**Never say "no results".** The last two are wins and should be sold as wins.

---

## 6. Availability, stated plainly

⚠️ **On a hosted deployment today, the run produces a PLAN and cannot execute one.** The verification
gate runs the customer's eval harness, which by design executes on their machine and not on the
platform, so `POST /api/v1/improvement-runs` answers **503 naming exactly that**.

This is PRD §12 **stage 1** — plan only — and it is a rollout stage rather than a broken deployment. Say
so if it comes up; do not demo the run against a hosted environment and imply it is generally available.
The plan half is real, it is free, and it is worth showing on its own.

---

## 7. The one-slide version

> Ask in English. You see the plan — what will be looked at, what it may spend, when it stops — before
> anything runs. Changes that prove themselves on held-out data are offered one at a time, with the
> number and the diff. You approve them individually. Each one is measured again after it is applied,
> and a change that fails to reproduce is withdrawn before it reaches your repository, with both numbers
> shown. What survives becomes a pull request with the evidence attached.
>
> **We open pull requests. You merge them.** On the console path we hold a write credential for that
> repository — per-repository, least-privilege, yours to revoke, effective immediately, and separate
> from the credential that reads your source.
