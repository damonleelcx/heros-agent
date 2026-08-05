# P27 Accounts, Members & Seats — what we sell, what we say, and where the boundary is

- **Status:** 2026-08-05 — **§2 is licensed except the seat row.** Every task named in the *Licensed by*
  column is closed and its evidence is in [`tasks.md`](../../openspec/changes/p27-account-system/tasks.md).
  Two boundaries survive the phase and are not editorial softeners:
  - 🔴 **No seat number, anywhere a customer buys from.** PRD Open Question 3 is still open — see rule 2.
    The product measures and displays the count correctly; what nobody can state is what a seat *includes*.
  - ⚠️ **The end-to-end commercial walk has not been run on the hosted deployment** (task 11.7).
    `heros-agent.space` currently serves a pre-P27 build. Every claim below is proved a layer down against
    a live database; what has not been observed is the whole sequence on a real host with real payment
    collection. Do not describe the *flow* from personal experience until somebody has walked it.
- **Audience:** anyone who writes a sentence about organizations, members, seats or account persistence that a
  customer, a prospect, a procurement reviewer or a security reviewer reads — the console, the pricing page, a
  datasheet, an RFP answer, a support reply.
- **Rule:** this is the phase where an over-claim is *checked by the buyer themselves, in normal use.* A
  customer who is told "remove a member and their access ends" will remove a member and then try the key. A
  customer told "5 seats included" will add a sixth person. Every sentence below is written so that the test
  they run is a good day.

## 1. The five rules, before any copy

1. **Sell what is built. Nothing here is built.** This document exists *before* the phase so that the wrong
   sentences do not enter a deck during implementation. Every row in §2 carries the tasks that license it; a
   claim whose tasks are open is a claim we do not make.
2. **🔴 Do not quote a seat number until the seat is defined.** Whether a CLI-only member — somebody who holds
   a personal API key and never opens the console — occupies a seat is **undecided** (PRD Open Question 3). Our
   own commercial guidance points one way (*developers calling through a key are typically not billed per
   seat*) and the plan fixtures point the other. Until it is ratified, "5 seats included" is a sentence whose
   meaning we cannot state, and a customer who asks *"does my CI service account count?"* gets a shrug from
   the person who is supposed to know.
3. **State revocation as a next-request effect, and state what it does not cover.** The honest sentence has
   two halves and both are load-bearing: *their next request is denied — no grace period* **and** *organization
   API keys they created keep working until you revoke them.* Saying only the first half is how an offboarding
   checklist gets signed while a CI key is still deploying.
4. **P27 changes no price and no plan.** A phase that makes seats countable is not a phase that repriced them.
   If a pricing change is wanted, it is a separate decision with a separate owner, and it does not ride in on
   this one.
5. **"Persist" is not a feature; say what actually became possible.** "Your data persists" is the kind of
   sentence that invites the reply *"…it did not before?"*. Lead with the capability — organizations, members,
   a runs history, self-serve sign-up — and let persistence be the thing that makes them work.

## 2. What we sell

**Every row is gated**, and the gate is the task list rather than anybody's judgement. All the tasks below
are closed — so these are now sayable, with one exception marked in the table.

| Claim | What it means | Licensed by |
|---|---|---|
| **Organizations and members** | A customer is an organization with named members, roles (`owner` / `admin` / `member`) and invitations. Not a line in a config file we maintain for them. | tasks 4.1, 4.3, 4.4 |
| **Self-serve sign-up** | Sign in with your identity provider, name your organization, and you are working — on the free tier, with no card. No email thread, no deploy on our side. | tasks 4.1, 4.2 |
| **Your run history** | Runs belong to your organization and are listed newest first. Coming back next week does not require the run id from your shell history. | tasks 5.1, 5.2, 7.4 |
| **Remove a member and their access ends at their next request** | Their console sessions and their personal API keys stop working — no restart, no grace window. **Always paired with the second half:** organization API keys they created are listed for you and are *not* revoked. | tasks 4.5, 4.6, 3.3, 7.6 |
| **Your data is yours and nobody else's** | A run belonging to another organization is not merely hidden — the API answers as though it does not exist. Scope travels inside the credential; no header, path or body value can widen it. | tasks 5.3, 5.4, 11.2 |
| **Your session survives our releases** | We deploy without signing your people out. | tasks 7.1, 10.3 |
| **Seats you can check** 🔴 **NOT YET SAYABLE** | The seat count is measured from your member list, and a refusal names your plan's allowance and your current count — both numbers, on the screen where you can act on them. **The mechanism ships; the sentence does not.** Describe the behaviour if asked, quote no number, and do not say what a seat includes. | tasks 6.1, 6.2, 8.4 — closed — **and rule 2, which is not** |
| **Upgrading is a payment, not a provisioning ticket** | Click upgrade, pay, and the plan applies. Card data goes to the payment provider, never to us — unchanged from P21. | tasks 6.4, 6.5 |

### The one sentence to lead with

> *Sign in with your own identity provider, name your organization, and start. Add your team, see what they
> ran, pay when you outgrow the free tier — and when somebody leaves, one click ends their access.*

## 3. What we do NOT commit to — say this out loud, early

Each of these is a real question a buyer asks. Answering *"not yet, and here is exactly what happens
instead"* in the first conversation costs a follow-up. Answering it after the signature costs the account.

| They ask | The honest answer |
|---|---|
| *"Does it sync with our directory (SCIM)?"* | **No.** You invite people and remove people here. There is no directory sync, and there is no push channel from your IdP to us. |
| *"If I disable someone in Okta, are they out of your console immediately?"* | **Two different things, and we say both.** They can start no new session — the next sign-in fails at your IdP. A session they already hold ends when it expires, bounded by the console session TTL. We have no back-channel from your directory. *(Unchanged from P22; P27 does not close this window and does not claim to.)* |
| *"Can I define custom roles / per-project permissions?"* | **No.** Three roles: owner, admin, member. A permission system is a later phase, not a configuration option we are hiding. |
| *"Can I move a run to another organization?"* | **No, by design.** Ownership is written once. A transfer would move billed usage between customers. |
| *"Will you delete my data when I close the account?"* | **Closing stops billing and suspends the organization. It does not erase history.** Erasure is a separate, audited request. Do not say "deleted" about closure. |
| *"How long do you keep my runs?"* | **We have no retention enforcement yet.** The plan has a retention limit and nothing enforces it. Say so; do not quote the number on the plan sheet as though it were a policy in force. |
| *"Can I bring my own password login?"* | **No.** We run no password database and never have. Your identity provider proves who someone is. *(This is a differentiator, and it is also simply true.)* |
| *"Does everyone in my company automatically join our organization?"* | **Undecided (PRD Open Question 2).** Today the answer depends on your deployment's mapping strategy. Do not promise silent domain-join. |

## 4. The three questions a reviewer always asks about this phase

**"Can organization A read organization B's runs?"**
No, and the mechanism is worth stating rather than asserting: the tenant is not a value the request carries —
it is inside the credential the platform verifies. There is no header, parameter or body field that names an
organization on a read path. A subject belonging to another organization answers `404`, in the same shape as
one that does not exist, so the API is not an existence oracle either. There is a test that runs the two
probes as two different organizations, and it was written that way deliberately, because the version that runs
both as the same organization passes without proving anything.

**"When exactly does removal take effect?"**
At the removed person's **next request**. The session and credential stores are read on every request, and a
revocation is never cached — a positive verification may be cached briefly for latency; a revocation may not,
because that would turn "next request" into "within a minute" and change the sentence we are asking you to
rely on. What removal does **not** touch: organization-scoped machine credentials, which are listed for you by
name on the confirmation screen before you remove anyone.

**"What happens to our data if we stop paying?"**
The organization is suspended and metered accrual stops. History is retained; nothing is erased by closure.
Erasure is a separate request with its own audit trail. Say all three parts — a customer who hears "we keep
it" without "you can ask us to erase it" hears a retention problem, and a customer who hears "we delete it"
without "closure is not deletion" hears a promise we do not keep.

## 5. Banned phrases — enforced at build time, in the same mechanism P21's billing copy uses

| 🚫 Never say | ✅ Say instead | Why |
|---|---|---|
| "instantly revoked everywhere" | "denied at their next request; organization keys they created keep working until you revoke them" | The first half is defensible, the word *everywhere* is not |
| "fully deleted" / "wiped" about closing an account | "closing suspends the organization and stops billing; erasure is a separate, audited request" | Closure is not erasure and the difference is a regulatory one |
| "5 seats included" (or any seat number) — **until rule 2 is resolved** | *(nothing — do not quote a seat number)* | We cannot state what a seat includes, so we cannot state how many are included |
| "syncs with your directory" | "you invite and remove people here; there is no directory sync" | We have no SCIM and no push channel |
| "your data persists" | "your runs are listed under your organization" | The first sentence invites the question we do not want asked |
| "unlimited history" / any retention promise | *(nothing — retention is unenforced)* | Quoting a plan's retention number as a policy in force is quoting something nothing implements |
| "enterprise-grade permissions" | "three roles: owner, admin, member" | Three roles is a fine answer; dressing it up invites the follow-up that finds out |

## 6. How §5 is enforced

The list in §5 is not a convention. It is a build gate: `web/console/scripts/scan-claims.mjs` fails the
console build on any of those phrases, in the same mechanism that already carries P21's billing bans, and
`tests/public-surface.test.mjs` drills every rule against a probe file so none of them is a rule nobody has
watched fire.

Two things the gate does deliberately, both of which are the difference between a fence people keep and one
they route around:

- **Only affirmative claims are banned.** A bare *"directory sync"* was on the list for about a minute and
  flagged the sign-in page's own copy — *"no directory sync, no per-seat user model"* — which is exactly the
  sentence we want written. The negation that names an absence stays sayable.
- **A seat number is banned on the PUBLIC surface and legal inside the product.** `/app/settings/members`
  must render a measured count with its label; that is the whole point of the phase, since the number finally
  comes from the member list instead of a usage record nothing ever wrote. The same digits on a pricing page
  are a different sentence — a claim about what a seat *includes* — and that is what rule 2 forbids. Banning
  both would have deleted the honest half.

What the gate does **not** cover, stated rather than implied: tone, emphasis, and what a sentence omits. It
matches phrases. A deck, an RFP answer and a support reply are outside it entirely — §5 is still a rule
somebody has to apply there, and this document is where they read it.

## 7. Where the feedback goes

A question asked twice by two prospects is a requirement, not a support burden. Route it:

- **A seat question** → PRD Open Question 3. It is blocking, and every week it stays open is a week nobody can
  quote a plan sheet accurately.
- **A domain-join question** → PRD Open Question 2, which is a product decision with a security dimension.
- **A retention question** → PRD Open Question 4. Expect this within a quarter of shipping a runs list, because
  a visible history is what makes people ask how long it lasts.
- **A SCIM question** → the identity-provisioning follow-up. Count them; the count is the argument for
  scheduling it.
