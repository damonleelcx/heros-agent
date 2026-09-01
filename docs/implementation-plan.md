# Implementation plan — phase DAG

**Status of this document:** live. It is the single source of truth for what is built and what is not.
Every phase must close on its own — it is not done until its own tests pass without the next phase.

## Legend

`[x]` done and tested · `[~]` partially done, gap named · `[ ]` not started

## Phase DAG

```
P0 scaffold ──┬─> P1 intent set ─────────┐
              │                          │
              └─> P2 durable kernel ──┬──┴─> P5 tier-B queries
                   (goal/task/store)  │
                                      ├─> P3 queue + leases ─> P4 worker loop ─┬─> P6 tier-A: assess
                                      │                                        ├─> P7 tier-C: effects
                                      └─> P8 bounds + refusals ────────────────┤   (+approval gates)
                                                                               ├─> P9 tier-A: evalset
                                                                               ├─> P10 tier-A: improve
                                                                               └─> P11 tier-A: compare
P12 timeline/observability ── cuts across P2..P11
P13 eval scenarios + recovery drills ── gates P14
P14 gradual autonomy rollout
```

## Phases

### [x] P0 · Scaffold
Module, README, architecture, this plan. Closes on: `make test` runs.

### [x] P1 · The closed intent set
Nineteen intents, three tiers, structural fences. Fifteen carried from the previous system plus four
resolved gaps: `skills` and `tools` were axes with no intent; `evalset` had a route but no intent; and
`prompt_model` conflated two axes into one intent.
Closes on: fences fail when an intent has no tier, no surface, or no question.

### [x] P2 · Durable kernel — goal, task DAG, store
`Goal` (objective, ceilings, completion criteria). `Task` DAG with typed dependency edges and a
`Ready()` computation. Store interface + in-memory implementation with checkpoints.
Closes on: a goal round-trips; a DAG reports the right ready set; a checkpoint restores.

### [x] P3 · Durable queue + leases
Lease-based claim so two workers cannot execute one task. Lease expiry returns the task to the queue.
Closes on: concurrent claims yield exactly one winner; an expired lease is reclaimable.

### [x] P8 · Bounds and typed refusals
Hard ceilings (iterations, tokens, tool calls, cost, wall-clock, spawn depth) and the closed refusal
set. Unbounded requests are refused, never defaulted.
Closes on: each ceiling trips; each refusal names a next action.

### [x] P2b · Postgres store
Baseline schema, embedded idempotent migrations, and a Postgres `Store`. The claim is ONE
`UPDATE … FOR UPDATE SKIP LOCKED` statement: a read-then-write claim has a window in which two workers
both see a task as free, and the window is small enough to reach production and rare enough to be
blamed on something else. Idempotency is enforced by a partial unique index rather than in application
code, which loses the race it exists to win.
Closes on: the conformance suite passes against a live Postgres, and the skip-is-not-a-pass fence
confirms the Postgres leg actually ran.

### [x] P4 · Worker loop + tool contracts
`RunOnce` performs exactly one bounded cycle: observe → plan → execute → verify → persist → continue.
The loop belongs to the CALLER, so "test recovery explicitly" is a matter of not calling RunOnce again —
which is what a crash is.

Tools are contracts, not functions: declared permissions, a required timeout, retry-safety, and a
SEPARATE verifier. A tool verifying itself asks the component that may have failed whether it failed.
An effect-bearing tool with no verifier is refused at registration.
Closes on: verification failure fails the task; an unconfirmable effect is never retried; the retry
ladder is bounded; cancellation releases the lease; the approval gate parks without holding one; a
crashed worker is recovered by another from persisted Postgres state.

**!!! Uses fakes at the tool boundary only.** No real model provider is wired yet, so the loop is proven
against a substituted external world. That is the correct seam to fake, but it means no token or cost
figure in this repo has yet come from a real call.

### [ ] P5 · Tier-B query surfaces
Eleven read-only intents over the store.

### [ ] P6 · Tier-A `assess`
Nine-axis assessment of a subject repository. Requires P4 + subject-repo discovery (not yet scoped).

### [~] P7 · Tier-C effects + approval gates
The GATE is built and tested (`GateEffectsOutsideThePlatform`, default-deny on anything touching the
customer's world). The four effect intents and the approval *surface* — where a human actually sees and
answers the request — are not.

### [ ] P9 · Tier-A `evalset`
Generated eval sets. Four generators: seed-from-real-traces, schema-driven, LLM-driven,
adversarial-perturbation.

### [x] P4b · Planner / executor split + replanning
One component owns the plan; workers own one task each and know nothing about the shape around them.
The plan exists as ROWS, so it can be shown to a person before it costs anything, rather than as control
flow that can only be read by running it.

`assess` fans out one task per axis and joins on a synthesis — so an unmeasurable axis is one blocked
row in a report that still has eight, rather than an assessment that did not happen. `improve` plans an
assessment and grows proposal chains from findings that actually exist. `evalset` gates quality between
generation and publication, because a generator scoring its own output marks its own homework.
`compare` waits for both runs.

Replanning is a DIFF, not a conversation: the plan changes because facts arrived, not because a model
was asked what it fancied next. Bounded three ways — task ceiling, spawn depth, and never re-adding an
existing id (which would reset attempt counters and let a failing task retry forever while every
individual round stayed inside its limits).
Closes on: every durable intent has a planner or the registry refuses to build; replanning is idempotent;
pull-request idempotency keys are stable across replans and clocks.

### [x] P6/P10 · `assess` and `improve` end to end
Proven against live Postgres, including a mid-run process death and resume.

### [x] P15 · A real model provider
DeepSeek, called for real. `internal/provider` is the boundary; `internal/provider/deepseek` is the
client; `internal/tools` is the first tool that actually calls a model.

Measured on 2026-08-31, `deepseek-v4-flash`, nine axes over a real Postgres: **8 model calls, 4,397
tokens, $0.0016, 24.4s.** The `graph` axis failed because topology cannot be read without executing the
customer's code, and the synthesis blocked behind it — both as designed.

Spend is a **micro-cent** ledger against a **cent** ceiling. A real call proved why: 300 tokens cost
0.0127 cents, and rounding up to the cent — the correct instinct, since under-reporting is the dangerous
direction — overstated it 79x.

DeepSeek prices **double during peak hours** (01:00–04:00 and 06:00–10:00 UTC, Mon–Fri), so the price is
selected per call from the call's own clock, defaulting to peak when uncertain.

### [x] P16 · Repository intake and discovery
The fixture is gone. `internal/intake` resolves what a person types — a local path or a GitHub link — to
a PINNED revision, and refuses when it cannot: without a revision, "did my change help?" compares against
something that has moved. A dirty working tree is recorded and surfaced rather than silently pinned.

`internal/discovery` reads the repository within four bounds (containment, skipped trees, file size, file
count) and extracts per-axis EVIDENCE: real spans of the customer's code with file and line. It finds
evidence and stops; a model judges it. The two halves fail differently — extraction fails the same way
every time and can be fixed, judgement fails by being wrong in a plausible sentence — so keeping them
apart means a false claim can be traced to which half produced it.

Verified against two real repositories, which corrected two things reasoning had not:
- **Test files dominate.** Nearly every call site and axis span landed in test code, because tests
  instantiate models and set temperatures more explicitly than production code does. Excluded and
  counted.
- **Alphabetical sampling is a biased sample presented as evidence.** Spans are now ranked by proximity
  to a call site before truncation, and the sample discloses how it was ranked.

`Corpus.LooksLikeAnAgent()` separates "this repository has nine weaknesses" from "this is not an agent".

### [x] P17 · The console, wired to the engine
`internal/router` turns a sentence into one of the nineteen intents, a named redirection, or an
abstention — deterministically, because a component that decides whether to spend money should not
itself cost money on every keystroke, and because it must be testable against a fixed holdout that a
model's answers would move under. 70 held-out questions, ≥80% recall per intent, 100% abstention
precision.

`internal/api` serves subject intake, routing, and a live SSE stream of run progress.
`cmd/herosd` serves the console and drives goals with `context.Background()` — a durable goal's lifetime
is not the browser request's, or a refresh would cancel an hour of work.

Three bugs the wiring exposed:
- **"remember" contains "member"**, so a memory question redirected to the members page. Redirect
  matching is word-boundary now, not substring.
- **Nine axes rescanned the corpus nine times** — 26 seconds to load a 2,541-file repository.
  `discovery.Index` scans once, and the reuse is a TYPE rather than a hidden cache, because a hidden
  cache on a value type is a lie about aliasing.
- **The scope in a sentence was thrown away.** "How to improve prompt?" planned a nine-axis run. The
  principle was already written in `goal.Axes` and was a comment rather than a code path.

### [x] P18 · Discovery performance
A 2,541-file repository took **17.2 seconds** to index and now takes **764ms** — 22.6x — with evidence
proven byte-identical before and after. End to end over HTTP: 26s at the start of this work, **1.1s** now.

The route there was three wrong guesses and one profile:
1. A gate regex made of the union of all thirty axis patterns: **no change at all.** A union of complex
   expressions costs what its parts cost — it is the same automaton.
2. A bitmask index so a gated line only runs patterns whose hints it contains: **no change.** The
   per-pattern work was never the bottleneck.
3. The profile said the *gate itself* was 3.2s of `regexp.(*machine)` plus 1.9s of GC pressure from its
   allocations. Go's regexp is a general automaton and pays general-automaton costs even when every
   branch is a constant string.

The fix is a two-byte prefix index over the hint literals (`internal/discovery/literals.go`): one pass,
no automaton, no allocation. Applied to the axis patterns and then to the call-site scan.

**The optimisation is fenced, not trusted.** A hint that is too narrow does not fail loudly — it silently
drops findings, and this package reports absence AS a finding, so a dropped signal becomes a confident
claim that a customer's agent has no memory strategy. `TestTheLiteralGateChangesNothing` and
`TestTheCallSiteGateChangesNothing` run every pattern with the gate on and off over a real
2,541-file repository and assert the results are identical.

### [x] P19 · Truncation, and the default nobody chose
Three axes truncated on every real repository and none on fixtures. It was never a budgeting problem.

The provider enables chain-of-thought **by default, at `high` effort**. Reasoning tokens are billed as
output and consume `MaxTokens` before the answer begins — measured on a real excerpt: **243 of 296
output tokens were reasoning, leaving 53 for the answer.** Raising the ceiling would have bought more
thinking, not more answer.

Thinking mode also silently disables `temperature`: the provider documents that it "will not trigger an
error but will also have no effect". Every call had been setting temperature 0 for determinism and
getting neither the determinism nor an error.

`provider.Reasoning` now has **no usable zero value** — `ValidateRequest` refuses an unset one, the same
way it refuses an unset ceiling. A forgotten field must not silently buy the expensive option.

Per-axis budgets are measured (50–120 output tokens for the fixed reply shape, budgeted at ~4x) and
**escalate with the attempt number**, because a retry that repeats an identical request is not a retry.
Two escalations, then a failure that says the budget was raised and still ran out — a different report
from "it truncated", and the one that says the prompt is wrong rather than the number.

Same repository, before and after: 12 model calls → **9**; 22,750 tokens → **9,244**; $0.0085 →
**$0.0045**; 2m8s → **15.3s**; 6 of 9 axes assessed → **9 of 9**.

### [x] P20 · The synthesis
The join that answers "what is weak here?". First fully green run: **10 of 10 tasks**, 9 of 9 axes,
7 actionable, $0.0015, 14.4s.

**The ordering, the counts and the unmeasured list are COMPUTED; only the connective sentence comes from
a model.** The two halves fail differently and only one is survivable: a miscomputed ranking is wrong the
same way every time and can be fixed, while a generated summary is wrong by being *plausible* — it will
state that an agent has no memory strategy when the memory axis was never assessed, in a well-formed
sentence, next to eight true ones.

So the model may connect the findings it was given and may not add one. `validate` rejects a synthesis
naming any axis that produced no finding, and the unmeasured axes are never shown to it in the first
place. Refusing costs one retry; publishing costs a customer acting on a weakness never observed in
their code.

A join needs its edges, so `toolcontract.Call` gained `Inputs` — the results of *declared* dependencies
only. A tool that could read any result would make the DAG's edges decorative: the graph would say what
waits for what while the data flowed wherever a tool reached.

### [x] P21 · Tier-C effects — the first writes to somebody's code
`author`, `prompt`, `model`, `deliver`. They run IN-TURN rather than as durable goals, because the
tiering says so and the tiering is right: a bounded change is one model call and a diff, and a queue
would add every failure mode of a distributed run to something that finishes in two seconds.

`internal/edit` is built to REFUSE. A refused edit costs one exchange; a wrong edit costs a corrupted
file discovered later and attributed to us. So every ambiguity resolves to a refusal: text that occurs
more than once, a replacement that re-indents (in Python indentation IS block structure — a statement
moved one level out is valid, parseable, and in a different scope), a path leaving the repository, a
no-op, or a file that changed between proposing and approving.

Line numbers are never identity — the anchor is the text. An unrelated edit above moves every line
below it, and an edit applied by line number rewrites whatever now sits there.

**Nothing is pushed.** Approval writes the change on a new branch and commits it, then hands back the
exact `git push` command. Pushing would need a credential with write access to somebody's repository,
held by this process, used while they are not present — the standing grant that repository connection is
deliberately out of scope for. One more step for them, one fewer credential for us.

Proven against a disposable repository: proposal → minimal diff (1 file, 1 insertion, 1 deletion) →
branch → commit; decline leaves the file byte-identical and HEAD unmoved; a second decision on the same
change is refused.

`deliver`'s QUESTION was corrected, not its tier. It read "how does an approved change reach my
repository?" — explanatory wording on an effect-bearing intent, which would eventually have been
implemented as whichever half the reader noticed.

### [x] P22 · Memory, wired
Four classes, four tables, because they differ in lifetime, in who may write, and in what a row must
carry to be trustworthy. One table forces one shape and one write path onto all four, and the concrete
failure is knowledge: an agent able to INSERT there launders its own speculation into fact, and the next
goal reads it as if somebody had established it.

- **Knowledge cannot be written, only PROMOTED**, citing episodes, and only observations or effects — a
  decision is the agent's own reasoning, and promoting one is the laundering step. The schema requires
  evidence; it is not a convention.
- **Preferences require a human author.** The Go type refuses system identities, the column refuses an
  empty one. An agent that infers "they seem to prefer aggressive refactors" has invented a mandate.
- **Compression never folds a failure or an effect**, and never deletes its source. What broke and what
  changed in the world are the two things a reader most needs from an old run.

The payoff is that **`run_history` is now a real answer** rather than a placeholder: "what happened in
that run?" is a SELECT over what a durable run wrote down, and it costs nothing. That is what persisting
everything was for.

Two bugs the wiring exposed:
- `MAX(seq)+1` inside an INSERT is **not** atomic — 16 concurrent writers produced 6 sequences and 10
  errors. Now a per-goal advisory lock, released on rollback or a dropped connection so a dying worker
  cannot wedge a goal's log.
- Run history answered about **the lexically-last goal**, not the newest — ids carry the prefix of
  whatever created them (`g-`, `live-`, `e2e-`), so a leftover test goal sorted last and the real run
  reported "no episodes" while holding nine. `store.LatestGoal` now asks the question that was meant.

### [x] P25 · Authentication and multi-tenancy
**Isolation is structural, not remembered.** Twelve of the fourteen store methods take a goal id and
nothing else, so before this a goal id was sufficient to read, mutate, claim or approve any customer's
work. `store.Root.For(tenant)` closes over the tenant and every query carries it; a handler is given a
scoped store and never holds the root, so it cannot construct a query for another tenant — not because
it is careful, but because it has nothing to be careless with.

`TestATenantCannotReachAnotherTenantsData` exercises **every** method against a goal owned by somebody
else, on both implementations. A sample would prove only that the sampled methods were fixed, and the
one that was missed is the one that gets used.

A cross-tenant row is **invisible**, not forbidden: returning "forbidden" would confirm the id exists and
turn a guessable identifier into an enumeration of everybody's data.

**The loaded repository was a single global field** — one customer's question was answered about
whichever repository another customer had opened last, with real file:line references, about code they
have never seen. A cross-tenant leak wearing the shape of a cache. It is per-tenant now.

Authentication is argon2id (RFC 9106 parameters, encoded per hash so the cost can be raised later),
sessions stored as SHA-256 so a leaked dump yields nothing usable, HttpOnly + SameSite=Lax cookies, and
a **default-deny** mux: adding a route makes it protected, exposing one takes an edit to a list called
`public`. A missing user and a wrong password are indistinguishable, in message and in timing.

Bootstrap **refuses to start** when no user exists and no credentials are given. A built-in default
password is a published credential.

### [x] P26 · Roles, invitations, password reset, address confirmation
**Authority is a table, not a rank.** Four roles — `owner`, `admin`, `member`, `viewer` — against seven
capabilities, every pair written out including the falses. The obvious implementation is an ordered rank
and a `role >= admin` comparison; it is shorter and it is wrong the first time somebody inserts a role in
the middle, because the ordering silently becomes load-bearing for decisions nobody wrote down.
`TestTheCapabilityTableIsExhaustive` fails the build if a capability is added without a decision for
every role, and `Can` fails closed on anything it does not recognise.

**Authorization is declared beside the route and applied at registration.** `apiRoutes` is one table
carrying method, path, public-or-not, and the capability needed; the mux, the default-deny middleware and
the fences are all derived from it. A handler cannot be reached except through the wrapper its row asked
for, and the previous hand-written list of routes in the fence — a mirror that could fall behind what the
server serves — is gone.

**The role is read from the user row on every request, never stamped onto the session.** Authority cached
in a credential can only be withdrawn by destroying the credential, so somebody demoted this morning
would keep this morning's access for up to a fortnight.
`TestDemotionTakesEffectOnTheNextRequest` uses the same cookie either side of the change.

**Two rules close the escalation path an admin would otherwise have.** An admin cannot grant `owner`, and
cannot act on one — you may act on somebody only if you could have granted the role they hold. Without
the second, an admin demotes the owner and then holds the organization. An organization can never be
left without an owner: the owner set is row-locked before any operation that could shrink it, because
"there must always be an owner" is an invariant across rows and two owners demoting each other at the
same moment would each see the other still in place.

**An invitation is the only way somebody joins**, it cannot mint an owner (refused in the store, enforced
by a `CHECK` constraint), and the accepting party sends nothing but a token and a password — no role, no
address, so there is no field to forget to validate. Accepting **proves the address**, because the token
arrived in mail sent to it.

**A password reset destroys every session for that account.** The commonest reason to reset is that
somebody else may have the password; sessions that survive mean the reset changes the lock and leaves the
intruder inside. The reset issues no session of its own — a forwarded link must not become a login.

**`POST /api/auth/password/forgot` answers identically for a known and an unknown address**, including in
time: the mail is sent on a background goroutine so the response cannot be timed. For a product that
reads customers' private source code, the customer list is itself worth having.

**Mail is a seam with three implementations and no silent third state.** A half-configured relay stops
the process at startup — this product has run for days with a mailer reporting itself healthy and
delivering nothing. Links are built from `HEROS_PUBLIC_URL` and never from the request's `Host` header,
which an attacker requesting a reset chooses. `HEROS_MAIL_MODE=log` prints links to the log for
development and says so loudly at every boot; `off` refuses to send rather than discarding.

Three separate token tables rather than one with a `purpose` column: a lookup that forgets to filter on
purpose turns a confirmation link — sent liberally, to unproven addresses, worth nothing — into a
password reset, which is a complete account takeover.

**Two defects the fences found.** `NewServer()` left `Approvals` nil, so `POST /api/decide` was
reachable, authenticated, authorised, and then panicked on any server not assembled by `main`.
`ValidRole` trimmed whitespace and `Can` did not, so `" owner"` passed validation and then held nothing.

**And one the console work surfaced.** `web/console.html` and `web/static/index.html` were byte-identical
tracked duplicates with nothing keeping them in sync, and `-web` defaulted to `web`, which holds no
`index.html` — so the daemon's default flags served a **directory listing**. Nothing was red: the process
started, the port answered, `/` returned 200. It simply was not the product. The duplicate is deleted, the
default is `web/static`, and `TestTheDefaultConsoleDirectoryHasAConsoleInIt` asserts the constant the flag
declaration actually uses rather than a copy of it.

### [x] P27 · Rate limiting the password-reset and sign-in endpoints
**The limit is spent before the lookup, and the limiter is never told what the lookup found.** That
ordering is the entire safety of adding a limit here. `password/forgot` was built to answer identically
for a real address and an invented one — same body, same status, same timing — because it takes an
address, needs no credential, and can be called at any rate. A limiter is the classic way that property
is quietly reopened: count what was *done* (a mail sent, a token issued) and only real addresses can ever
be limited, so 429-versus-200 answers exactly the question the constant reply refuses.
`TestTheResetLimitIsIdenticalForAKnownAndAnUnknownAddress` pushes both past the ceiling and compares
every reply; it was observed red against the natural, wrong implementation, which reported
`429` for the real address and `200` for the invented one.

**Keyed per address, because what is being protected is an inbox.** Not per caller: the victim of a
reset flood is the person whose mailbox fills, not whoever sent the requests. Per-address also means
flooding one address cannot lock the rest of the deployment out of its own recovery path, which a global
or per-endpoint limit would.

The key is `auth.EmailKey` — trimmed and lowercased, the form the SQL compares by. A limit keyed on the
raw string is bypassed by pressing shift, so the ceiling would be "three per capitalisation".
`TestEmailKeyMatchesHowTheDatabaseCompares` asks the real database, through the real login path, whether
it considers each spelling the same person, and requires `EmailKey` to have said the same — because two
statements of one rule drift, and this drift is exploitable in both directions.

**A token bucket, not a fixed window.** A window lets six through across a boundary, which for a
mail-sending endpoint is six messages in two seconds — the burst the limit exists to prevent, arriving at
a moment nobody chose. Tokens are fractional: truncating them means a caller arriving every nineteen
minutes regains nothing, ever.

**Login is limited too, and its key and its accounting are both different — because a different thing is
being protected.** The reset limit is keyed on the ADDRESS, since what a reset flood damages is an inbox,
and an inbox is one mailbox however many organizations write to it. A sign-in guesses at an ACCOUNT, and
the same address in two organizations is two accounts with two passwords — keyed on the address alone,
guessing at one customer's user would spend a different customer's user's budget, which is one tenant
degrading another through an endpoint neither controls. So login is keyed on tenant **and** address.

🔴 **A correct password costs nothing.** Charged for every attempt, a per-account login limit is an
account-lockout weapon: fail to sign in as somebody often enough and they cannot sign in either — and
with the reset endpoint also limited per address, both ways into the account close at once for the price
of a few dozen requests an hour. Spend-then-restore removes the ratchet. It does not make an account
unblockable, and the claim is worth stating exactly: an attacker holding the bucket at zero still gets
the owner refused, because the refusal happens before the password is looked at. What it removes is the
accumulation — every refilled token is a fresh chance, the owner needs to win one of those races, and the
attempt that wins costs nothing. Retries instead of a lockout.

It is spend-then-restore rather than look-then-charge-on-failure because the check and the spend must be
one atomic step: two concurrent attempts that merely LOOK at a bucket with one token left both proceed,
and a limit that leaks under concurrency leaks most under attack, which is the only time it matters.

Ten wrong passwords back to back, then one a minute — about 1,500 guesses a day against one account,
against a bound of roughly a million when the only limit is how fast the server runs argon2id.

**At the memory ceiling it refuses rather than evicting.** The map is keyed on a value the caller
supplies, so it must be bounded — and least-recently-used eviction is a bypass wearing the costume of a
memory bound: flood `capacity` invented addresses, the victim's bucket is evicted, and the flood resumes
from a full allowance. Refusing costs availability under attack; evicting costs the protection itself.
Fully-refilled buckets are swept first, since one is indistinguishable from a key never seen.

### [x] P28 · A ceiling on concurrent argon2id
**The cost that makes the hash good is the cost that makes it a weapon.** Every verification allocates
64 MiB and holds it for tens of milliseconds, so two hundred simultaneous requests ask for thirteen
gigabytes and the kernel ends the process — taking with it every unrelated request in flight, the
console, and the worker.

**The rate limits do not close this**, which is why it needed its own mechanism. They are keyed on an
account and on an inbox, so an attacker spreads across a thousand invented addresses and trips neither —
and an address with **no account still runs a full verification against a decoy hash**, deliberately, so
that a missing user costs what a wrong password costs. Every one of those is 64 MiB. So is every password
reset and every invitation accepted with a garbage token, both of which hash before they check anything.

**The gate is inside `HashPassword` and `VerifyPassword`, not in middleware.** Middleware would bound the
one path somebody remembered; the paths that hash — accepting an invitation, resetting a password,
creating a user — are precisely the ones with no rate limit in front of them.

**The ceiling is derived, not configured**: `GOMAXPROCS / Parallelism`, floored at two. argon2id runs
Parallelism threads per call, so that many already saturate the CPU and more adds only memory and
latency. It follows a container's CPU limit instead of being a constant that is wrong on a laptop and
wrong again on a large host. The daemon prints it as a memory budget at startup, because it is one.

⚠️ **The banner first said "peak" and was wrong by more than a factor of two.** Go returns freed memory
to the operating system lazily, so resident memory climbs to a plateau well above the live bound. A flood
of 120 concurrent sign-ins against a ceiling of 9 (576 MiB live) settled at 1.4 GB resident — and stayed
there across three more rounds, which is the part that matters: it is a plateau, not growth. The line now
says "live at once", because a number labelled "peak" is read as a promise about how much the container
needs.

**Both numbers are settable** (`HEROS_PASSWORD_CONCURRENCY`, `HEROS_PASSWORD_MAX_WAIT`) and validated at
startup, because they ARE the protection: a ceiling of 200 re-opens the exhaustion this closes, and a
wait of zero sheds every password check in the deployment while the server sits idle — a total outage
that confidently blames load it does not have. Nonsense is refused where an operator is watching; a value
that is merely surprising is applied with a warning naming what it costs. The distinction is whether this
code can know the answer: it cannot know how much memory the container has, so a high ceiling warns; it
knows for certain that a wait of zero is never intended, so that refuses.

**Requests queue for up to three seconds, then are shed.** Shedding beats dying: an out-of-memory kill
takes down the requests that were fine along with the ones that were not. A caller whose context is
already done is refused without taking a slot — and that is checked *before* the `select`, because
`select` chooses at random among ready cases and would otherwise hand a slot to a client that had already
disconnected about half the time.

🔴 **Overload is never reported as a wrong password.** The handler had one error path, so shedding would
have told people their correct password was wrong — and they would reset it, spending their reset budget
and sending mail over a server that was merely busy, with nothing in the logs but ordinary failed logins.
It answers 503, and the shed attempt is **refunded** to the login budget: the budget is for wrong
passwords, not for the server's bad afternoon.

🔴 **And the trap that made this more than a semaphore.** `Login` runs a decoy verification when no
account matches, and discarded its result — correctly, since checking a password nobody has is
meaningless. Shedding made that discard a leak: the real branch propagated `ErrBusy` as 503 while the
decoy branch swallowed it and answered 401, so **making the server busy was how you found out which
addresses were real**. `TestOverloadLooksTheSameForAKnownAndAnUnknownAddress` covers all three cases and
was observed red against the discard.

One interaction, deliberate and stated: a token is held for the duration of a verification, so more than
`LoginBurst` simultaneous sign-ins **for one account** answer 429 even with the right password. At most
ten verifications for one account are ever in flight, which is the intended reading; it needs eleven
sign-ins inside fifty milliseconds to notice.

### [x] P29 · Limits on redeeming an invitation and on confirmation mail
**Two endpoints, two shapes, and only one of them is really a rate-limiting problem.**

`email/resend` is the straightforward one: keyed on the ADDRESS, like the password-reset limit and for
the same reason — an inbox is what fills up. Not on the account, even though the endpoint is
authenticated and the two are the same thing today: if an address ever becomes changeable, keying on the
account would let somebody walk a full budget from one inbox to the next. Separate bucket from the reset
limit, so the mail this deployment will send to one address is the sum of the two — stated in a test,
because sharing one bucket would model the inbox more exactly and would also mean somebody who asked for
three password resets could not then confirm their address.

`invitation/accept` is the interesting one, because **a rate limit there cannot close the actual
exposure**. The only thing to key on is the token, and an attacker varies the token, so every request
arrives with a fresh budget. The limit is still worth having — it bounds hammering of one valid
invitation — but the real hole was elsewhere: the handler hashed the password BEFORE looking at the
token, so any garbage string cost a full argon2id, 64 MiB and tens of milliseconds, on an unauthenticated
endpoint. That is the cheapest possible way to occupy every hashing slot the server has and starve real
sign-ins.

🔴 So the store now checks the token first. 🚫 That lookup is **not** authoritative and must not become
so — the conditional UPDATE inside the transaction is still what decides, atomically, whether an
acceptance happens. The check buys only that known-bad input is rejected for the price of an indexed
lookup.

⚠️ **The fence for that could not fire, and had to be rebuilt.** It held the argon2id ceiling at one slot
and hammered it from a goroutine, expecting a garbage token to be shed if it reached argon2id — and it
passed against the exact ordering it existed to catch, because the hammering goroutine releases its slot
between calls and the request slipped through a gap. A fence built on "probably contended" reports
whatever the scheduler felt like. Replaced with a counter of argon2id computations actually started
(`auth.HashesRun`, useful in its own right for deciding whether the ceiling is the right one), which
makes the claim directly: this path ran zero. Observed red, with a control asserting a real token does
hash — or the test would pass against a handler that never hashed anything.

### [x] P30 · The same fix for redeeming a password-reset link
`password/reset` had exactly the shape `invitation/accept` had before P29 — unauthenticated, and hashing
the new password **before** looking at the token, so any garbage string cost a full argon2id (64 MiB and a
hashing slot) before anything checked it. Same fix, both halves: the store checks the token first, and the
endpoint carries a limit keyed on the token's hash.

🚫 The check is **not** authoritative, here as there: the conditional UPDATE that marks the token used is
still what decides, atomically, whether the reset happens. It only makes known-bad input cost an indexed
lookup. The helper is unexported and returns no row, so nothing outside the package can ask "is this reset
token real" without redeeming it, and nothing inside is tempted to act on the answer instead of the claim.

It also fixes the order the person experiences: somebody holding a dead link is told the link is dead,
rather than being told their new password is too short, fixing that, and failing again on the thing that
was actually wrong.

**`ResetLimit` is now `ForgotLimit`.** The name stopped working the moment a second limit appeared in the
same flow — one bounds requesting a link and is keyed on an address, the other bounds using one and is
keyed on a token, and two fields that could both reasonably be called "the reset limit" is how the wrong
one gets used.

⚠️ Two edits in this change silently did nothing before being caught: a `sed` rename using `\b`, which
BSD sed does not support, and a python edit whose anchor no longer matched. Both left the tree consistent
and the build green, which is exactly why they were easy to miss — the check that caught them was
grepping for what should no longer exist, not the compiler.

### [x] P31 · The last unlimited endpoint, and why it was the weakest case
`email/verify` was the one endpoint left without a limit, and the honest assessment — stated before it was
added and kept in the code — is that it needed one least. It runs no argon2id: a request costs one
transaction and an indexed lookup, so what a limit bounds is connection-pool churn against a single link
rather than anything expensive. Like every token-keyed limit here, it cannot bound a flood of invented
tokens, because each invented token is a fresh key.

**It was added for consistency, and consistency is a real argument.** Every path that redeems a one-time
token now behaves the same way — same key (the token's SHA-256), same numbers, same refusal — so nobody
has to remember which one is the exception, and the exception is what gets used.
`TestEveryTokenEndpointIsLimited` walks the three and asserts it, rather than trusting that each handler
remembered.

🚫 And what was NOT written: a refund branch for `ErrBusy`, which the endpoints either side of it carry.
Nothing on this path runs argon2id, so there is no gate to be shed by and that error cannot arrive. A
refund added for symmetry would be a line nobody could ever make execute.

**`VerifyLimit` is now `ResendLimit`**, the same rename `ResetLimit` → `ForgotLimit` needed and for the
same reason: one limit bounds sending a link and one bounds using it, and a name either could wear is a
name the wrong one gets used under. The rename was done with a checked regex and verified by grepping for
what should no longer exist — the lesson from P30, where a `sed` using `\b` changed nothing at all and
left the build green.

## !!! Not started, and deliberately so

### [x] P23 · `evalset` and `compare`
**heros never executes the customer's code** — stated in `internal/tools/boundary.go` because it was
implicit until it forced a decision. Running a customer's agent means arbitrary code, with their
credentials, against their providers, on our infrastructure, while they are absent.

That costs the product something real and it is written down rather than hidden: `evalset` GENERATES a
set and hands it over (`heros-evalset.json`, in their repo, so they own it); `compare` cannot run an
eval set twice and diff the scores, so it diffs ASSESSMENTS — an honestly weaker claim, and the report
says so in the claim itself.

The `compare` plan used to be `run-baseline` / `run-candidate` of kind `run_eval_set`. That kind is gone,
and `TestNoPlanAsksToRunTheCustomersAgent` keeps it gone — a plan encoding a capability the system has
decided not to have gets implemented by whoever reaches it first.

Proven end to end on a disposable repository: **15 cases from 3 generators, `missing:
[seed_from_real_traces]` recorded in the artefact**, parked for approval, approved, published, goal
succeeded.

Four bugs the run exposed:
- **A failed generator killed the whole set.** Each strategy is blind to what the others find, so one
  failing must degrade rather than destroy. `task.Contributes` — dependencies that must be TERMINAL, not
  SUCCESSFUL — is the distinction the graph could not express.
- **`Claim` did not return the new column**, so the gate was handed zero results while three generators'
  cases sat in the database. A column added to a table has to be added to all three queries that build
  the struct.
- **Approval did not stick.** The policy ran on every claim, so an approved task parked again forever
  and each re-approval reported success. The policy answers "does this KIND need a person"; whether one
  has answered is a different fact and was nowhere.
- **Every Tier-A goal inherited an assess-shaped criterion.** An eval-set run has no axes, so it
  published the artefact and reported FAILED. A criterion borrowed from another intent is not a weaker
  measure, it is a measure of something else.

### [x] P12 · Timeline / observability
The kernel already RECORDED all of this — episodes, task states, checkpoints. What was missing was the
ability to ask, and a record nobody can read is not observability, it is storage.
`GET /api/goals/{id}/timeline` reads the episodes, the DAG and the goal state together, and the console
renders it.

🔴 **What-next leads, above the history.** The question somebody actually has is never "show me the log",
it is "this has been running for twenty minutes, what is it waiting for" — and if the answer is "you", it
is the only line on the page they can act on. Every unfinished task gets a sentence naming why it is not
running; a state with no case would render as "no reason" rather than "nobody wrote one".

🔴 **Ordered by the store's sequence, not by wall clock.** Seq is a total order assigned by the store;
timestamps are not, because workers write concurrently and clocks step backwards. Sorted by `At`, a
timeline puts an effect before the decision that caused it — rarely, unreproducibly, and only under the
concurrency that makes it matter.

🔴 **Truncation drops observations and decisions first, never failures and effects**, inheriting the rule
the memory package enforces for compression: the two things a reader most needs from an old run are what
broke and what it changed. Oldest-first truncation would undo that at the last step, dropping exactly the
early failure that explains everything after it. Whatever is dropped is counted and said out loud.

**Isolation:** the goal id comes from the URL, so ownership is checked against a tenant-scoped store
before anything is read, and another tenant's run is indistinguishable from one that never existed.
⚠️ `memory.Store` is still keyed by goal id with no tenant — the shape the tenancy work removed from the
goal store. `buildTimeline` takes an already-loaded goal rather than an id, which is a convention rather
than a wall; scoping that store properly is named below.

⚠️ **A defect found while writing it:** `DAG.Tasks` is a map and Go randomises map iteration, so the
first version of the what-next list came back in a different order on every call. On screen that reads as
the run churning. `TestWhatNextIsStableAcrossCalls` calls it sixty times; one call could never have
caught it.

### [x] P13 · Eval scenarios + recovery drills
Five drills in `internal/e2e/drills_test.go`, one per fault P13 names, injected through the store API
the product uses — a recovery path exercised only through a test-only back door is a recovery path that
was never exercised.

The mechanisms were already covered: the store proves `AnExpiredLeaseIsReclaimable` and
`AZombieWorkerCannotWriteItsResult`, the worker proves `ACrashedWorkerIsRecoveredByAnother`, and
`TheRunResumesAfterTheProcessDies` proves a killed process resumes without redoing finished work. What a
drill asks is different, and is the question an operator has: after the fault, is the RESULT still
correct — was anything lost, done twice, or reported untruthfully?

- **A killed worker hands over the same idempotency key.** A worker that opens a pull request and dies
  before recording it is retried from scratch, and the only thing between that and a second pull request
  in the customer's repository is that the retry presents a key the remote already knows.
- **Duplicate completions are refused**, and the second does not overwrite the first's result with an
  outcome that happened earlier.
- **Duplicate episodes are recorded, not merged** — a deliberate non-deduplication. A retried task
  genuinely ran twice, and collapsing that makes a three-attempt run look like a one-attempt run to
  whoever is reading the timeline after an incident.
- **A zombie's late write does not destroy a good result.** "The write is refused" is half the property;
  the half that matters is that the success another worker recorded is still there afterwards.
- **An unavailable provider fails loudly.** Every tool call errors; the run must end bounded, terminal,
  with reasons on the failed tasks, and must never report COMPLETE. This project has already shipped a
  run that announced success for doing almost nothing, and "the provider was down and the run finished
  successfully" is the worst sentence this system could produce, because nobody checks it.

Each drill was observed RED against the defect it names — regenerating the key per claim, dropping the
held-lease guard, and a provider that was not actually down.

⚠️ **Two ways the drills were nearly worthless, both caught and fixed.** The first version of the key
drill claimed whatever was ready, which is an ASSESS task carrying no idempotency key — so it compared
`""` with `""` and would have passed against a system that regenerated keys on every claim. It now drives
to the approval gate, takes the delivery task, and refuses to proceed if the key is empty. And every
drill skips without a database, so an unset DSN turned the whole recovery suite green having injected no
fault at all; `TestZZDrillsActuallyRan` counts, mirroring the store's Postgres-leg gate.

### [x] P14 · Gradual autonomy rollout
A per-organization LEVEL, and each level names which CLASSES of effect no longer need a person.
`supervised` (the default) gates everything; `assisted` lets a run change files in its own workspace and
still stops before anything reaches the customer's repository; `autonomous` gates nothing and is bounded
only by ceilings. An owner sets it — not an admin: every other capability an admin holds is about who may
act, and this one is about whether a person is asked before the product writes to somebody's repository.

🚫 **Autonomy never widens on its own.** The obvious "gradual" design lets it: after N proposals approved
unedited, stop asking for that class. It was considered and rejected. A system that grants itself
authority from its own record is exactly what should need a human, and a run of easy approvals is not
evidence about the hard change — the twenty diffs somebody waved through were the twenty that were
obviously fine, which is why they were waved through. "Gradual" describes an operator turning a dial as
confidence grows, not software deciding it has earned more room.

🔴 **Every failure gates.** The permissive branch is reached only when the setting was read successfully
AND is a level this build knows AND the kind has a declared class. A database that is down, a tenant that
does not exist, an unrecognised level, an unclassified effect, a policy wired with no source — each ends
in "wait for a person". `TestEveryFailureGates` walks all of them.

🔴 **Two effect classes, and every effect-bearing kind must have one.**
`TestEveryEffectBearingKindHasAClass` fails the build otherwise, because the alternative to a build
failure is a default — and a default is a decision in the permissive direction for whichever kind
somebody forgot. A future "push to production" landing in `workspace` would go out unapproved under the
level an organization chose for editing files in a checkout.

🔴 **An effect that proceeds with nobody asked leaves a record saying so, and naming the setting that
allowed it.** Otherwise "who approved this change to our repository?" has no answer at all — not "nobody,
because the organization is set to autonomous", but silence, which reads like an approval somebody has
forgotten giving. It is written as an EFFECT episode, not a decision, because decisions are compressible
and effects are not: a summariser must never be free to fold away the one line saying the world was
changed unattended.

**The default is the restrictive one**, so migration 0007 changed nobody's behaviour on the day it ran.
A default of `assisted` would have silently widened what every existing customer's runs may do, in a
schema change, with nobody choosing it.

🚫 **Scope: the durable run's gate only.** The in-turn Tier-C path (`/api/decide`) still always asks,
whatever the level — the person is right there, watching a diff they requested, and auto-applying in a
conversation is not autonomy, it is a surprise. Gradual autonomy is about long runs proceeding without
somebody sitting over them.

### [x] P17 · The console, wired to the engine
`internal/router` turns a sentence into one of the nineteen intents, a named redirection, or an
abstention — deterministically, because a component that decides whether to spend money should not
itself cost money on every keystroke, and because it must be testable against a fixed holdout that a
model's answers would move under. 70 held-out questions, ≥80% recall per intent, 100% abstention
precision.

`internal/api` serves subject intake, routing, and a live SSE stream of run progress.
`cmd/herosd` serves the console and drives goals with `context.Background()` — a durable goal's lifetime
is not the browser request's, or a refresh would cancel an hour of work.

Three bugs the wiring exposed:
- **"remember" contains "member"**, so a memory question redirected to the members page. Redirect
  matching is word-boundary now, not substring.
- **Nine axes rescanned the corpus nine times** — 26 seconds to load a 2,541-file repository.
  `discovery.Index` scans once, and the reuse is a TYPE rather than a hidden cache, because a hidden
  cache on a value type is a lie about aliasing.
- **The scope in a sentence was thrown away.** "How to improve prompt?" planned a nine-axis run. The
  principle was already written in `goal.Axes` and was a comment rather than a code path.

### [x] P18 · Discovery performance
A 2,541-file repository took **17.2 seconds** to index and now takes **764ms** — 22.6x — with evidence
proven byte-identical before and after. End to end over HTTP: 26s at the start of this work, **1.1s** now.

The route there was three wrong guesses and one profile:
1. A gate regex made of the union of all thirty axis patterns: **no change at all.** A union of complex
   expressions costs what its parts cost — it is the same automaton.
2. A bitmask index so a gated line only runs patterns whose hints it contains: **no change.** The
   per-pattern work was never the bottleneck.
3. The profile said the *gate itself* was 3.2s of `regexp.(*machine)` plus 1.9s of GC pressure from its
   allocations. Go's regexp is a general automaton and pays general-automaton costs even when every
   branch is a constant string.

The fix is a two-byte prefix index over the hint literals (`internal/discovery/literals.go`): one pass,
no automaton, no allocation. Applied to the axis patterns and then to the call-site scan.

**The optimisation is fenced, not trusted.** A hint that is too narrow does not fail loudly — it silently
drops findings, and this package reports absence AS a finding, so a dropped signal becomes a confident
claim that a customer's agent has no memory strategy. `TestTheLiteralGateChangesNothing` and
`TestTheCallSiteGateChangesNothing` run every pattern with the gate on and off over a real
2,541-file repository and assert the results are identical.

### [x] P19 · Truncation, and the default nobody chose
Three axes truncated on every real repository and none on fixtures. It was never a budgeting problem.

The provider enables chain-of-thought **by default, at `high` effort**. Reasoning tokens are billed as
output and consume `MaxTokens` before the answer begins — measured on a real excerpt: **243 of 296
output tokens were reasoning, leaving 53 for the answer.** Raising the ceiling would have bought more
thinking, not more answer.

Thinking mode also silently disables `temperature`: the provider documents that it "will not trigger an
error but will also have no effect". Every call had been setting temperature 0 for determinism and
getting neither the determinism nor an error.

`provider.Reasoning` now has **no usable zero value** — `ValidateRequest` refuses an unset one, the same
way it refuses an unset ceiling. A forgotten field must not silently buy the expensive option.

Per-axis budgets are measured (50–120 output tokens for the fixed reply shape, budgeted at ~4x) and
**escalate with the attempt number**, because a retry that repeats an identical request is not a retry.
Two escalations, then a failure that says the budget was raised and still ran out — a different report
from "it truncated", and the one that says the prompt is wrong rather than the number.

Same repository, before and after: 12 model calls → **9**; 22,750 tokens → **9,244**; $0.0085 →
**$0.0045**; 2m8s → **15.3s**; 6 of 9 axes assessed → **9 of 9**.

### [x] P20 · The synthesis
The join that answers "what is weak here?". First fully green run: **10 of 10 tasks**, 9 of 9 axes,
7 actionable, $0.0015, 14.4s.

**The ordering, the counts and the unmeasured list are COMPUTED; only the connective sentence comes from
a model.** The two halves fail differently and only one is survivable: a miscomputed ranking is wrong the
same way every time and can be fixed, while a generated summary is wrong by being *plausible* — it will
state that an agent has no memory strategy when the memory axis was never assessed, in a well-formed
sentence, next to eight true ones.

So the model may connect the findings it was given and may not add one. `validate` rejects a synthesis
naming any axis that produced no finding, and the unmeasured axes are never shown to it in the first
place. Refusing costs one retry; publishing costs a customer acting on a weakness never observed in
their code.

A join needs its edges, so `toolcontract.Call` gained `Inputs` — the results of *declared* dependencies
only. A tool that could read any result would make the DAG's edges decorative: the graph would say what
waits for what while the data flowed wherever a tool reached.

### [x] P21 · Tier-C effects — the first writes to somebody's code
`author`, `prompt`, `model`, `deliver`. They run IN-TURN rather than as durable goals, because the
tiering says so and the tiering is right: a bounded change is one model call and a diff, and a queue
would add every failure mode of a distributed run to something that finishes in two seconds.

`internal/edit` is built to REFUSE. A refused edit costs one exchange; a wrong edit costs a corrupted
file discovered later and attributed to us. So every ambiguity resolves to a refusal: text that occurs
more than once, a replacement that re-indents (in Python indentation IS block structure — a statement
moved one level out is valid, parseable, and in a different scope), a path leaving the repository, a
no-op, or a file that changed between proposing and approving.

Line numbers are never identity — the anchor is the text. An unrelated edit above moves every line
below it, and an edit applied by line number rewrites whatever now sits there.

**Nothing is pushed.** Approval writes the change on a new branch and commits it, then hands back the
exact `git push` command. Pushing would need a credential with write access to somebody's repository,
held by this process, used while they are not present — the standing grant that repository connection is
deliberately out of scope for. One more step for them, one fewer credential for us.

Proven against a disposable repository: proposal → minimal diff (1 file, 1 insertion, 1 deletion) →
branch → commit; decline leaves the file byte-identical and HEAD unmoved; a second decision on the same
change is refused.

`deliver`'s QUESTION was corrected, not its tier. It read "how does an approved change reach my
repository?" — explanatory wording on an effect-bearing intent, which would eventually have been
implemented as whichever half the reader noticed.

### [x] P22 · Memory, wired
Four classes, four tables, because they differ in lifetime, in who may write, and in what a row must
carry to be trustworthy. One table forces one shape and one write path onto all four, and the concrete
failure is knowledge: an agent able to INSERT there launders its own speculation into fact, and the next
goal reads it as if somebody had established it.

- **Knowledge cannot be written, only PROMOTED**, citing episodes, and only observations or effects — a
  decision is the agent's own reasoning, and promoting one is the laundering step. The schema requires
  evidence; it is not a convention.
- **Preferences require a human author.** The Go type refuses system identities, the column refuses an
  empty one. An agent that infers "they seem to prefer aggressive refactors" has invented a mandate.
- **Compression never folds a failure or an effect**, and never deletes its source. What broke and what
  changed in the world are the two things a reader most needs from an old run.

The payoff is that **`run_history` is now a real answer** rather than a placeholder: "what happened in
that run?" is a SELECT over what a durable run wrote down, and it costs nothing. That is what persisting
everything was for.

Two bugs the wiring exposed:
- `MAX(seq)+1` inside an INSERT is **not** atomic — 16 concurrent writers produced 6 sequences and 10
  errors. Now a per-goal advisory lock, released on rollback or a dropped connection so a dying worker
  cannot wedge a goal's log.
- Run history answered about **the lexically-last goal**, not the newest — ids carry the prefix of
  whatever created them (`g-`, `live-`, `e2e-`), so a leftover test goal sorted last and the real run
  reported "no episodes" while holding nine. `store.LatestGoal` now asks the question that was meant.

### [x] P25 · Authentication and multi-tenancy
**Isolation is structural, not remembered.** Twelve of the fourteen store methods take a goal id and
nothing else, so before this a goal id was sufficient to read, mutate, claim or approve any customer's
work. `store.Root.For(tenant)` closes over the tenant and every query carries it; a handler is given a
scoped store and never holds the root, so it cannot construct a query for another tenant — not because
it is careful, but because it has nothing to be careless with.

`TestATenantCannotReachAnotherTenantsData` exercises **every** method against a goal owned by somebody
else, on both implementations. A sample would prove only that the sampled methods were fixed, and the
one that was missed is the one that gets used.

A cross-tenant row is **invisible**, not forbidden: returning "forbidden" would confirm the id exists and
turn a guessable identifier into an enumeration of everybody's data.

**The loaded repository was a single global field** — one customer's question was answered about
whichever repository another customer had opened last, with real file:line references, about code they
have never seen. A cross-tenant leak wearing the shape of a cache. It is per-tenant now.

Authentication is argon2id (RFC 9106 parameters, encoded per hash so the cost can be raised later),
sessions stored as SHA-256 so a leaked dump yields nothing usable, HttpOnly + SameSite=Lax cookies, and
a **default-deny** mux: adding a route makes it protected, exposing one takes an edit to a list called
`public`. A missing user and a wrong password are indistinguishable, in message and in timing.

Bootstrap **refuses to start** when no user exists and no credentials are given. A built-in default
password is a published credential.

## !!! Not started, and deliberately so

- **Name resolution is exact for Python, conservative for JS/TS, absent for Go.** Python uses its own
  AST. JavaScript and TypeScript use a hand-written scanner measured at **0 false positives over 3,504
  simulated edits on 1,438 real files** — the differential rule is what makes that possible. Go would
  need a compile, which needs the customer's dependencies; the verdict says "no name check for this
  language" rather than implying one ran.
- **Discovery is heuristic, not a parser.** It finds candidate evidence by pattern, so a false positive
  is cheap (the model discards it) and a false negative is expensive (the axis reports absence). Python,
  TypeScript, JavaScript and Go only. Every report states that it shows what was found, not everything
  that exists.
- **Console / HTTP surface.** No API and no UI in this tree.
- **`memory.Store` is not tenant-scoped.** `Episodes(goalID)` returns whatever goal it is handed, for
  any customer — the same shape `store.Root.For(tenant)` structurally removed from the goal store. Both
  call sites today reach it only with a goal id already proven to belong to the caller, so nothing leaks;
  the guarantee is handler discipline rather than construction. Scoping it is a real refactor across the
  memory package, the worker and the API.
- **Authorization within a tenant is role-based only.** There are no per-object permissions, no
  per-repository access, and no audit log of who changed whose role. Every member of an organization
  sees every goal in it.
- **A confirmed address gates nothing.** It is recorded and shown. Mail is the least reliable component
  in this deployment, and making a session, a run or an approval depend on it turns a mail outage into a
  lockout — including for the one account that could fix the mail.
  `TestAnUnconfirmedAddressBlocksNothing` asserts the non-property, so gating something on it later is a
  decision made in the open rather than a default nobody chose.
- **Nothing is limited per CALLER.** Guessing at a thousand accounts once each is not limited by anything,
  because every limit here is keyed on the thing being protected — an inbox, an account — rather than on
  whoever is asking. A per-IP limit needs to know which proxies to trust, and this product has no such
  configuration; getting it wrong behind a load balancer gives every customer in the deployment one
  shared bucket, which is an outage rather than a limit. Left undone deliberately, not overlooked.
- **Shedding is per process, like the limits.** With several replicas the peak is the ceiling times the
  replica count, which is the number to size a container against.
- **The limiter is per process.** With more than one replica each holds its own buckets, so the
  effective ceiling is the configured one times the number of replicas. Stated rather than solved: the
  alternative is a write to shared storage on every unauthenticated request, keyed on a string the caller
  chooses, which is a worse thing to expose than a limit that is N times looser than it says.
