<img src="web/static/avatar.jpg" alt="Heros" width="120" align="right">

# heros

A durable, long-running agent that improves and evaluates *other* agents.

You talk to her in a console. She reads the agent in your repository, answers questions about it from
what she has actually parsed, and — once you say yes — opens pull requests against it.

## The two agents

| | |
|---|---|
| **Subject agent** | The customer's agent chain, in the customer's repository. Described by nine axes: `model, prompt, skills, context, tools, memory, harness, loop, graph`. It is the **object** of the work — heros reads it, proposes changes to it, and measures it. heros never runs it in production. |
| **Platform agent** | This system. A durable workflow that wakes, rebuilds its context from persisted state, does a bounded amount of work, persists, and exits. |

The governing rule: **a long-running agent is not a long-running LLM call.**

## Status

Rebuild in progress. See [docs/implementation-plan.md](docs/implementation-plan.md) for the phase DAG and
what is done versus pending — it is the single source of truth for progress.

## How a sentence becomes work

This is the centre of the product and the part with the most safety in it, so it is worth reading before
anything else. Everything below happens on one `POST /api/ask` — or on `POST /api/ask/stream`,
which is the same turn delivered as it is written. Same pipeline, same capability, same transcript;
only the transport differs, and the console falls back to `/api/ask` if streaming fails.

```
  you type a sentence
          │
  ┌───────▼──────────────────────────────────────────┐
  │ 1. the floor          deterministic, no model    │   "keep going until it is perfect"  → refused
  │    internal/router    cannot be argued with      │   "change my password"              → redirected
  └───────┬──────────────────────────────────────────┘
          │  not the floor's business
  ┌───────▼──────────────────────────────────────────┐
  │ 2. the agent          internal/converse          │   says / asks back / picks ONE capability
  │    sees the thread + what is actually loaded     │   may be persuaded — so it decides nothing
  └───────┬──────────────────────────────────────────┘   that step 1 owns
          │
  ┌───────▼──────────────────────────────────────────┐
  │ 3. the gate           spends money or writes?    │   8 of 19 → a confirmation card, and NOTHING
  │                       Tier is the discriminator  │   runs until a person says yes
  └───────┬──────────────────────────────────────────┘   11 of 19 → answered straight from the index
          │
  ┌───────▼──────────────────────────────────────────┐
  │ 4. the fallback       whenever step 2 failed     │   rate limit, timeout, unparseable reply
  │    internal/router    keyword scoring            │   → the console degrades and says so
  └──────────────────────────────────────────────────┘
```

**Why the floor is first, and deterministic.** Connecting a repository creates a standing read grant — a
credential used when you are not present — and its disclosure has to be displayed *before* the grant
exists. A model cannot be the only thing between a sentence and that. The same argument covers billing,
passwords and membership, which are pages rather than agent goals.

The floor is deliberately **narrow**: it matches named topics as word sequences, so `"connect a
repository for me"` is redirected and `"connect my other repo"` is not. That is not a hole, and widening
it would make one — a redirect beats every other outcome, so a false redirect is unrecoverable within
the turn, and a rule loose enough to catch every phrasing would also catch "how does my agent connect to
the model?". The broad phrasings are handled by the agent, which can *talk* about them and cannot *act*
on them, for the reason below.

**Why the agent is safe anyway.** Not because the prompt asks it nicely. Because its **action surface is
closed**: it may say anything and may only *do* one of the nineteen capabilities in `internal/intent`,
none of which connects a repository or touches an account. `Tier` is the single discriminator for every
spend ceiling and approval gate in the system, so an action outside that set is an action with nothing to
hang a ceiling on. There is no such action.

**Why it always asks before spending.** Eight capabilities either start a run that costs money or write
to your repository. Each one shows you what it understood — in its own words, not the capability's label
— and creates nothing until you agree. `"look at my repository and tell me what is weak"` is a category
you cannot check; `"you want me to look at how this agent is prompted"` is something you can catch as
wrong.

**Why an outage is not an outage.** Understanding is a network call now. Every failure falls back to the
keyword router, which is the behaviour the console had before any of this existed — and the transcript
records `keyword-fallback` on those turns, because two replies that read alike may have come from two
completely different mechanisms.

⚠️ **Answering is no longer free.** It used to be, and both the code and the console said so. Reading
your question costs about $0.0003; producing the answer still costs nothing, because it is read from what
was already parsed. A turn is bounded at four model calls and $0.0015 — see `converse.DefaultBounds`,
where the number is measured rather than guessed.

## Quick start

```
make pg-up                 # Postgres on :55700, which the tests and the daemon both expect
make test                  # both legs: in-memory and the real database
```

`HEROS_TEST_DATABASE_URL` is set by the Makefile. Without it the Postgres leg **skips**, and
`TestZZPostgresLegActuallyRan` fails rather than letting a skip read as a pass.

## Running it

```
make pg-up
HEROS_MAIL_MODE=log \
HEROS_PUBLIC_URL=http://127.0.0.1:8080 \
HEROS_BOOTSTRAP_EMAIL=you@example.com \
HEROS_BOOTSTRAP_PASSWORD='a-long-enough-password' \
go run ./cmd/herosd
```

Then open <http://127.0.0.1:8080/app/> and paste a path or a GitHub link into the box.

| command | what it is for |
|---|---|
| `cmd/herosd` | The console and the durable-goal driver. The only thing a deployment runs. |
| `cmd/discover` | Runs discovery over a repository and prints the nine axes. No model, no database. |
| `cmd/livecheck` | Drives a real goal against the real provider, for checking a provider change end to end. |
| `cmd/thinkcheck` | Probes the provider's reasoning and truncation behaviour. |

Configuration is environment only — never a flag, never a committed file, because a credential in git
history is a credential that has leaked.

| | |
|---|---|
| `QWEN_API_KEY` | Required. Read from the environment or `.env.local`, which is git-ignored. Regional: a Beijing key is rejected by the Singapore host. |
| `HEROS_DATABASE_URL` | Postgres DSN. Defaults to the container `make pg-up` starts. |
| `HEROS_BOOTSTRAP_EMAIL`, `HEROS_BOOTSTRAP_PASSWORD` | Used **once**, to create the first organization and its owner. If no user exists and these are unset, the process refuses to start: a built-in default password is a published credential. |

### Mail

Invitations, password resets and address confirmations need a relay. There is no default — a deployment
says which of the three it wants, because the difference between them is whether reset links end up in a
log file.

| | |
|---|---|
| `HEROS_SMTP_HOST`, `HEROS_SMTP_PORT`, `HEROS_SMTP_USERNAME`, `HEROS_SMTP_PASSWORD`, `HEROS_MAIL_FROM` | A real relay. **All or none** — a half-configured relay accepts connections and delivers nothing, which is indistinguishable from working until a customer cannot reset their password, so the process refuses to start on a partial set. Port defaults to 587; TLS is required and a relay that does not offer STARTTLS is an error rather than a fallback to cleartext. |
| `HEROS_MAIL_MODE=log` | Development. Writes each mail, links included, to the server log — and warns at every boot, because anybody who can read that log can take over any account. |
| `HEROS_MAIL_MODE=off` | Refuses to send. Invitations and resets fail with an explanation rather than being silently discarded. |
| `HEROS_PUBLIC_URL` | The origin customers reach, e.g. `https://console.example.com`. Required whenever mail can be sent. **Never taken from the request's `Host` header:** "I forgot my password" is a request an attacker can make, and they choose its headers — `Host: evil.example` would produce a real token for the victim's real account in a mail from the real product, pointing at the attacker. |

### Roles

| | |
|---|---|
| `owner` | Runs the organization. Only an owner can appoint another owner. An organization always has at least one. |
| `admin` | Invites people and changes roles — but cannot grant `owner`, and cannot act on one, so an admin cannot demote the owner above them and take over. |
| `member` | Loads repositories, starts runs, approves the changes those runs propose. |
| `viewer` | Reads goals and their history. Cannot start runs, which cost money, or approve changes, which write to the repository. |

### Rate limits

| | | |
|---|---|---|
| `POST /api/auth/password/forgot` | per address | **3**, then one every 20 minutes |
| `POST /api/auth/login` | per account (organization + address) | **10 wrong passwords**, then one a minute. A correct password costs nothing. |
| `POST /api/auth/invitation/accept` | per invitation | **5**, then one a minute |
| `POST /api/auth/password/reset` | per reset link | **5**, then one a minute |
| `POST /api/auth/email/verify` | per confirmation link | **5**, then one a minute |
| `POST /api/auth/email/resend` | per address | **3**, then one every 20 minutes |

All answer `429` with a `Retry-After`. The first two spend the budget **before** looking anything up — so
a real address and an invented one are limited on the same schedule. A limit that applied only where an
account exists would turn `429`-versus-`200` into an answer to "does this person have an account here",
which is the one question those endpoints are built to refuse.

Confirmation mail and password resets have **separate** budgets, so the most this deployment will send to
one address is the sum: six an hour.

They are keyed differently on purpose. A reset flood damages an **inbox**, and an inbox is one mailbox
however many organizations write to it, so that limit is keyed on the address alone. A sign-in guesses at
an **account**, and the same address in two organizations is two accounts — so that limit is keyed on the
organization and the address together.

A correct password is not charged, because a limit charged for every attempt is an account-lockout
weapon: fail to sign in as somebody often enough and they cannot sign in either. That does not make an
account unblockable — an attacker holding the budget at zero still gets the owner refused — but it
removes the accumulation, so the owner gets in after some retries rather than waiting for somebody to
intervene.

⚠️ **The token limits bound abuse of one live link, not a flood of invented tokens** — each invented token
is a fresh key with a fresh budget, so no limit keyed on the token can close that. What closes it is that
`invitation/accept` and `password/reset` check the token before hashing a password, so a garbage string
costs an indexed lookup instead of 64 MiB and a hashing slot. (`email/verify` hashes nothing either way;
its limit is there so every link endpoint behaves alike, not because that path was expensive.)

Both limits are held in memory: with several replicas each keeps its own buckets, so the real ceiling is
that number times the replica count. Nothing is limited per caller — see the implementation plan for why
a per-IP limit is deliberately absent.

### Password hashing is capped

argon2id costs 64 MiB and tens of milliseconds **per call**, by design — which is also what makes it a
way to exhaust the server. The number that may run at once is capped at `GOMAXPROCS / 2`, floored at two,
and the daemon prints it at startup. Beyond the cap, requests queue for up to three seconds and are then
shed with `503`.

⚠️ **Size the container above `ceiling × 64 MiB`, not at it.** That figure is the memory *live at once*;
Go returns freed memory to the operating system lazily, so resident memory climbs to a plateau
considerably higher and stays there. Measured here: a ceiling of 9 (576 MiB live) settles at about 1.4 GB
resident and does not grow under continued load.

Both numbers can be set, though almost no deployment should need to — the default follows the CPU count,
including a container's CPU limit.

| | |
|---|---|
| `HEROS_PASSWORD_CONCURRENCY` | How many hashes may run at once. Default `GOMAXPROCS / 2`, floored at 2. A value well above that warns, naming the memory it implies and the fact that it buys no extra throughput. Zero or less is refused. |
| `HEROS_PASSWORD_MAX_WAIT` | How long a request queues before being shed. Default `3s`; needs a unit (`3s`, `1500ms`, `1m`). Zero or less is **refused at startup** — it would shed every sign-in, invitation and password reset in the deployment, reporting that the server is busy while it sat idle. |

The rate limits above do not cover this: they are keyed on an account and an inbox, and an address with
**no account** still runs a full verification against a decoy hash so that a missing user costs what a
wrong password costs. Accepting an invitation and resetting a password also hash, and have no rate limit
in front of them at all.

An overloaded server answers `503` and never `401` — telling somebody their correct password is wrong
sends them to reset a password that was fine — and the shed attempt is refunded to their sign-in budget.

### Autonomy

How much of a run proceeds without a person, set per organization by an **owner**.

| | |
|---|---|
| `supervised` | Every change waits for a person, including edits inside the workspace. **The default.** |
| `assisted` | Edits inside the workspace go ahead; anything reaching your repository waits. |
| `autonomous` | Nothing waits — runs are bounded only by their ceilings. |

Every failure gates: an unreadable setting, an unknown level, or an effect this build has no class for
all end in "wait for a person". An effect that proceeds with nobody asked is recorded as an effect
episode naming the setting that allowed it, so "who approved this?" always has an answer — even when the
answer is "nobody, and here is the setting that said so".

It governs the **durable run's** approval gate. A Tier-C change proposed in conversation still always
asks, whatever the level: the person is right there looking at the diff.

An invitation is the only way to join an organization, and it cannot create an owner — ownership is
transferred inside the console to somebody who already has an account, never by a link in an email that
travels through a mailbox the organization does not control.

## The HTTP surface

Every route is declared in one table, `internal/api/routes.go`, together with the capability it needs.
The check is applied at **registration**, so a handler cannot be reached except through the wrapper its
row asked for — "somebody forgot the auth check" is not a state this mux can be in.

| | needs | |
|---|---|---|
| `POST /api/ask` | `RunGoals` | One sentence in, one reply out. The pipeline at the top of this file. |
| `POST /api/ask/stream` | `RunGoals` | The same turn as SSE: `delta` events as the reply is written, then an authoritative `final`. The console prefers this and falls back to `/api/ask`. |
| `POST /api/confirm` | `RunGoals` | Say yes (or no) to a capability the agent chose. Same capability as asking: somebody who may not ask must not be able to answer a question put to somebody who could. |
| `GET /api/conversation` | `ReadGoals` | Replays the thread. Optional `?conversation_id=`; without one it resumes the most recent. |
| `POST /api/subject` · `GET /api/subject` | `LoadSubject` · `ReadGoals` | Point her at a repository; read what she found. |
| `GET /api/history` | `ReadGoals` | The organization's runs, rebuilt from the goal record. |
| `GET /api/goals/{id}/events` · `/timeline` | `ReadGoals` | What is happening now; what happened and why. |
| `POST /api/decide` | `ApproveChange` | Approve a Tier-C diff. The sharpest route in the product — it writes to your repository. |
| `/api/auth/*`, `/api/members/*`, `/api/invitations/*`, `/api/autonomy` | various | Identity and organization. See the rate-limit and role tables above. |

🔴 `/api/conversation` and `/api/history` are **not** redundant. A sentence is final the moment it is
said, so it replays verbatim from the transcript. A run keeps changing after the sentence that started
it, so its card is rebuilt from the goal record — never from a copy frozen into the transcript, which
would show a finished run as pending forever.

## Where state lives

| what | where | lifetime |
|---|---|---|
| Goals, task DAGs, leases, checkpoints | `goals`, `tasks`, … via `internal/store` | Durable. Survives every restart; a run outlives the request that started it. |
| Episodes, summaries, knowledge, preferences | `internal/memory` | Durable, four tables, four different write rules — see `db/migrations/postgres/0002_memory.sql`. |
| The conversation | `conversation_turns` via `internal/memory` | Durable. Added in `0010`; before it, `/api/ask` was stateless and "and what about tools?" could not work. |
| The loaded repository | `tenants.subject_ref` + a rebuilt index | The **reference** is durable; the clone, corpus and index are rebuilt from it. Storing the corpus would mean this database accumulating copies of customers' source. |
| Pending confirmations and undecided diffs | in process | Deliberately lost on restart. A consent prompt nobody answered must not survive to be answered by accident later. |

Everything durable is **tenant-scoped by type**, not by remembering to filter: a handler is handed a
store already bound to the caller's organization and never holds the unscoped one, so it has nothing to
be careless with. `TestATenantCannotReachAnotherTenantsData` and its siblings prove it method by method,
on a real database.

## Project structure

```
cmd/herosd            the console and the durable-goal driver
internal/
  api                 HTTP: the route table, /api/ask's pipeline, the confirmation gate
  converse            the conversational agent — prompt, action protocol, per-turn ceiling
  router              the deterministic floor, and the fallback when the agent cannot answer
  intent              the nineteen capabilities and their tiers. the single discriminator
  discovery           reads a repository into nine axes of evidence, with file:line spans
  intake              resolves "github.com/acme/bot" or "../bot" to a pinned revision
  goal / task / store durable runs: admission, the DAG, leases, checkpoints
  planner             turns a goal into a task DAG
  worker              claims a task, runs its tool, records what happened
  tools               the things that actually call a model: assess, propose, verify, evalset
  toolcontract        a tool cannot be registered without whatever makes it safe to operate
  memory              episodes, summaries, knowledge, preferences, conversation turns
  bounds              ceilings and refusals — the vocabulary for "no, and here is why"
  auth / tenancy      identity, roles, capabilities, per-organization isolation
  provider            the boundary to a language model (Qwen today)
db/migrations         embedded SQL. every statement idempotent; the whole chain runs every boot
web/static/app        the console — one page, no build step
```

## Testing

```
make test          # everything, both legs
make race          # the same under -race, which is where sequence assignment gets interesting
```

Two rules this repository actually enforces, rather than merely intends:

- **A skip is not a pass.** With `HEROS_TEST_DATABASE_URL` set, `TestZZPostgresLegActuallyRan` fails if
  no Postgres subtest ran. The most dangerous green build is the one where the thing you cared about did
  not execute.
- **Both implementations, one set of guarantees.** Every store guarantee is written once and run against
  the in-memory and Postgres implementations. They diverge silently otherwise, always in the one nobody
  exercised — which is production. This is not theoretical: the conversation table's advisory lock
  passed in memory and failed on the first real call, because Postgres text cannot hold a NUL byte.

Fixed bugs are recorded in `workflow/CI/bugfix/`, and the code that fixes them carries a comment saying
what was wrong and why, plus the name of the test that goes red if it comes back. A repaired behaviour
with no note in the source is one the next refactor deletes as redundant.
