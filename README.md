# heros

A durable, long-running agent that improves and evaluates *other* agents.

## The two agents

| | |
|---|---|
| **Subject agent** | The customer's agent chain, in the customer's repository. Described by nine axes: `model, prompt, skills, context, tools, memory, harness, loop, graph`. It is the **object** of the work — heros reads it, proposes changes to it, and measures it. heros never runs it in production. |
| **Platform agent** | This system. A durable workflow that wakes, rebuilds its context from persisted state, does a bounded amount of work, persists, and exits. |

The governing rule: **a long-running agent is not a long-running LLM call.**

## Status

Rebuild in progress. See [docs/implementation-plan.md](docs/implementation-plan.md) for the phase DAG and
what is done versus pending — it is the single source of truth for progress.

## Quick start

```
make test
```

## Running it

```
make pg-up
go run ./cmd/herosd
```

Configuration is environment only — never a flag, never a committed file, because a credential in git
history is a credential that has leaked.

| | |
|---|---|
| `DEEPSEEK_API_KEY` | Required. Read from the environment or `.env.local`, which is git-ignored. |
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

| | |
|---|---|
| `POST /api/auth/password/forgot` | **3 per address**, then one every 20 minutes. |
| `POST /api/auth/login` | **10 wrong passwords per account**, then one a minute. A correct password costs nothing. |

Both answer `429` with a `Retry-After`, and both spend the budget **before** looking anything up — so a
real address and an invented one are limited on the same schedule. A limit that applied only where an
account exists would turn `429`-versus-`200` into an answer to "does this person have an account here",
which is the one question these endpoints are built to refuse.

They are keyed differently on purpose. A reset flood damages an **inbox**, and an inbox is one mailbox
however many organizations write to it, so that limit is keyed on the address alone. A sign-in guesses at
an **account**, and the same address in two organizations is two accounts — so that limit is keyed on the
organization and the address together.

A correct password is not charged, because a limit charged for every attempt is an account-lockout
weapon: fail to sign in as somebody often enough and they cannot sign in either. That does not make an
account unblockable — an attacker holding the budget at zero still gets the owner refused — but it
removes the accumulation, so the owner gets in after some retries rather than waiting for somebody to
intervene.

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

The rate limits above do not cover this: they are keyed on an account and an inbox, and an address with
**no account** still runs a full verification against a decoy hash so that a missing user costs what a
wrong password costs. Accepting an invitation and resetting a password also hash, and have no rate limit
in front of them at all.

An overloaded server answers `503` and never `401` — telling somebody their correct password is wrong
sends them to reset a password that was fine — and the shed attempt is refunded to their sign-in budget.

An invitation is the only way to join an organization, and it cannot create an owner — ownership is
transferred inside the console to somebody who already has an account, never by a link in an email that
travels through a mailbox the organization does not control.
