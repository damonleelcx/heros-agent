# P32 Repo Intake — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-08-21)
- **Audience:** anyone who describes how this platform gets a customer's source — a deck, a demo, a
  scoping call, an SoW, a security questionnaire.
- **Rule:** this is the phase where the honest pitch and the exciting pitch differ MOST, and where the
  exciting one loses a deal at the security review rather than at the demo. Say the narrow thing.

## 1. The one sentence

> *"Connect one repository, read-only, revocable — or push a bundle and connect nothing at all."*

Every clause is a shipped capability, and the second half is not a consolation. It is the default, it
loses the customer nothing, and it is the answer for the buyer whose policy forbids third-party
repository grants — which is a buyer this product was built for.

Where it comes from: `docs/prd/P32-repo-intake.md` §6 and
[ADR-013](../adr/ADR-013-source-acquisition-posture.md). What stops it drifting:
`web/console/tests/connections.test.mjs` and `internal/sourceingest`'s fences.

## 2. 🔴 The boundary to state OUT LOUD, before anybody asks (§8.2)

**A connection is usable when the customer is not present.**

That is the whole of what a connection buys and the whole of what it costs, and it is the sentence a
technical buyer will eventually work out for themselves. Say it first:

> *"A connection is a standing read grant. Once you authorize it, scheduled and autonomous work reads
> that repository without asking you again — that is what it is for. So every read is recorded, with
> the revision, the reason, and whether a person was there. You can read that record at any time, and
> revoking deletes the grant, our copy of the credential, and every tree we derived from it."*

It is also **on the surface**: the consent screen carries it in its own block, in the caution palette,
before authorization can complete. A prospect meets it in the product rather than only in a deck. That
is deliberate — a disclosure a customer first hears from their own security team is a disclosure that
reads as something we hid.

Sales operations' standing note applies exactly here: **a differentiator when stated confidently, a
weakness when discovered later.** Every competitor's integration is an org-wide install. Ours is one
repository, and the question a technical buyer asks about an org-wide install is *"what else can it
see?"* — which we can answer with "nothing, and here is the refusal that makes that structural."

## 3. 🚫 What must never be said

| Do not say | Why it is false, and what it costs when a customer finds out |
|---|---|
| *"It watches your repository."* | **§8.3.** It reads a revision when asked. There is no webhook, no polling loop, and no subscription to your pushes — a customer who believes "watches" expects a finding within minutes of a merge, waits, and concludes the product is broken rather than idle. Say **reads when asked**. |
| *"Connect your GitHub org and we'll find everything."* | Organization-wide scope is **refused on the record** (ADR-013 Option B) and there is no field in the data model that can express it. The demo would have to be faked, and the fake is the kind a security reviewer catches. |
| *"It only needs read access, so there's no risk."* | Read access to source IS the asset for most of our buyers. The honest framing is the trade, not its absence: one repository, revocable, and every use recorded. |
| *"Connecting is how you get the good features."* | **§8.4 and FR12.** No feature is gated on a connection. A tenant who only pushes bundles reaches every surface. Saying otherwise creates an expectation the product refuses, and turns the default mode into a downgrade nobody chose. |
| *"We keep your code so the analysis stays fast."* | A cloned tree is deleted after **72 hours** (PRD §14 A4), and revoking deletes it immediately. Saying we keep it invites the follow-up "for how long", which has an answer — use the answer. |
| *"It's the same integration you already have for pull requests."* | It is a **second, separate** grant with its own scope and its own revocation. ADR-005's write installation and this read grant are deliberately not one credential, and a buyer who merges them in their head will be surprised by two revocations. |
| *"Local mode works against your self-hosted deployment."* | It works against the hosted service **only** (PRD §14 A1), the console states that before the flow starts, and a self-hosted endpoint is a separate decision nobody has taken. |

## 4. The three modes, and how to position them

They differ in exactly one thing — **what the platform can do when the customer is not there** — and
that is the axis to sell on, because it is the axis a buyer is actually deciding.

| Mode | What we hold | Standing capability | Say this |
|---|---|---|---|
| **Push a bundle** (default) | the revision they sent, until they delete it | **none** | *"You run a command, for one revision, and can stop. We hold what you pushed — not a credential that can read your repository again tomorrow."* |
| **Read on your machine** | nothing from the tree; the workflow's structure only | **none** | *"The repository is read on your machine and its contents are never transmitted. We show a code; you type it into a terminal that is already there."* |
| **Connect a repository** | a read grant, and a tree for 72 hours | **yes — say so** | *"One repository, read-only, revocable, and every read recorded. It is the only mode that works while you are asleep, which is the point and the cost."* |

🔴 **Lead with the first two.** Not out of modesty — because the buyer who is going to object to a
standing grant will object either way, and hearing the alternatives first turns that objection into a
choice rather than a blocker. A prospect who learns about bundles only after refusing a connection has
already decided we are an integration they cannot have.

## 5. What a security questionnaire will ask, and the true answers

| Question | Answer |
|---|---|
| What scope does the grant have? | One repository. GitHub: an App installation with that repository selected, `contents: read` + `metadata: read`. GitLab: a project access token with `read_repository`. Bitbucket: a repository access token with `repository:read`. |
| Can it write? | No. There is no scope on it that can push a ref, open a pull request, or change a setting — and the connect path refuses any authorization carrying one. |
| Can it reach our other repositories? | No, and it is refused structurally: an authorization whose resulting grant covers a repository the customer did not name is refused **at connect**, on every forge, and the stored record has no field that could express a wider scope. |
| Where is the credential? | In the deployment's secret store, reached through an interface with no accessor that returns it — it is handed to a closure and dropped. It never appears in a request body, a config file, a log line, or an audit record, and there is a fence over each of those four. |
| How long do you keep our code? | A cloned tree: 72 hours. A pushed bundle: until you delete it, because you chose to send it. The extracted working copy is released at the end of the operation in both cases. |
| What happens when we revoke? | The grant, our copy of the credential, and **every tree derived from it** are deleted, and a subsequent read reports no source rather than answering from what we already held. The console shows how many trees were deleted. |
| Can we see when you read it? | Yes. Every read is recorded with the revision, the reason, and whether a person asked for it — including the reads that **failed**, which is the record you want after rotating a token. |
| Which hosts do you connect out to? | `github.com`, `gitlab.com`, `bitbucket.org`, and only when a connection exists. They are named in the deployment manifests and checked against the code. |

## 6. 🔴 The demo script, and the one thing not to skip

1. Open **Source**. Three tabs. Say the sentence in §1.
2. Press **Connect a repository**. Name the repository.
3. **Stop on the consent screen and read the caution block out loud.** This is the moment the deal is
   either won on candour or lost later on discovery. It takes eleven seconds.
4. Show the read ledger — including a failed read, if there is one. "This is what you get for letting
   us read while you are not here."
5. Press **Revoke**. Read the confirmation. Show the count of deleted trees.
6. Then show the **Push a bundle** tab and say: *"and if none of that is acceptable to your security
   team, this is the default, and it costs you no feature."*

Step 6 is not a fallback. It is what makes step 3 credible.

## 7. What is NOT in this phase

- **Monorepo sub-path grants.** A connection covers the whole repository; a workflow may name a
  sub-path, which bounds what we READ. The grant stays repository-scoped because no forge issues a
  narrower one, and saying otherwise would be a claim about a credential's reach that the credential
  does not honour.
- **Attended-only connections.** A customer who wants "read only when I am there" cannot have it as a
  setting today. The honest answer is the off switch: revoke, and re-connect when you want a read.
  Recorded as PRD §14 A6, with the cheap version of the feature already designed.
- **Self-hosted local mode.** §14 A1. The console states it before the flow, not after.
- **Anything that writes.** Delivery is ADR-005 and P12, it is a separate installation, and this phase
  neither implies nor enables it.
