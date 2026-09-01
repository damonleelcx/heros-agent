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

`POST /api/auth/password/forgot` allows **3 requests per address**, then one every 20 minutes, and
answers `429` with a `Retry-After`. The bucket is spent before the address is looked up, so a real
address and an invented one are limited on the same schedule — a limit that applied only to addresses
with accounts would turn `429`-versus-`200` into an answer to "does this person have an account here",
which is the one question this endpoint is built to refuse.

The limit is per address, because what it protects is somebody's inbox, and it is held in memory: with
several replicas each keeps its own buckets, so the real ceiling is that number times the replica count.

An invitation is the only way to join an organization, and it cannot create an owner — ownership is
transferred inside the console to somebody who already has an account, never by a link in an email that
travels through a mailbox the organization does not control.
