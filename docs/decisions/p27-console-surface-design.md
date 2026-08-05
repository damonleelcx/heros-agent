# P27 · The console surface: the words, the controls, and the path in

- **Status:** Accepted (2026-08-05)
- **Audience:** anyone who adds a control, a state or a sentence to the organization surfaces — and
  anyone reviewing one.
- **Companion:** [`docs/sales/P27-account-copy.md`](../sales/P27-account-copy.md) is what may be SAID to
  a customer. This is what the product itself says and shows.

P27 introduces the four most confusable words in the product and the first surface where three roles see
three different things. Both are the kind of thing that is decided once, correctly, or decided forty
times, inconsistently. This document is the once.

---

## 1. The noun dictionary — three layers, kept apart

Every row names one thing at three depths. The rule is that **none of them substitutes for another**, and
a review that finds one in the wrong layer is a review that found a defect.

| The customer reads | The schema says | The code calls it | 🚫 Never |
|---|---|---|---|
| **organization** | `tenant` | `tenancy.Tenant` | "tenant" in anything a customer reads. Our multi-tenancy is our problem, not their identity. The **operator** console may say it — its reader is us. |
| **member** | `membership` | `tenancy.Membership` | "seat". A removed member is still a membership row; a seat is a paid-for slot. |
| **invitation** | `invitation` | `tenancy.Invitation` | "pending member". It is not a membership until somebody signs in. |
| **seat** | *(derived)* | `seats_current` / `seats_billed` | an unqualified count. See §4. |
| **plan** | `account.active_plan_id` | `plancfg.PlanConfig` | "subscription" — that is the provider's object, not ours. |
| **API key** | `api_credential` | `tenancy.Credential` | "token", "secret". Those are what it contains. |
| *(never shown)* | `account` | `account.Account` | **"account" for a login.** See below. |
| *(never shown)* | — | `auth.Principal` | "user". A machine credential is a principal and is nobody. |

**"Create an account" is the single most tempting wrong sentence in this phase**, because it is what
every other product says. Here `account` names the *billing* record. Using it for sign-up would make the
one screen that talks about money ambiguous, so sign-up says **"Name your organization"** and the word
account appears only where a plan and a bill do.

---

## 2. The control-visibility matrix

The table is [`CONTROL_MATRIX`](../../web/console/src/lib/organizationCopy.ts), not this document — code
is where it can be read by the thing that renders. This is the reasoning.

| Control | owner | admin | member | machine credential |
|---|:--:|:--:|:--:|:--:|
| See the member list | ✅ | ✅ | ✅ | ❌ |
| Invite somebody | ✅ | ✅ | ❌ | ❌ |
| Change a member's role | ✅ | ✅ | ❌ | ❌ |
| Change an **owner's** role | ✅ | ❌ | ❌ | ❌ |
| Promote somebody to **owner** | ✅ | ❌ | ❌ | ❌ |
| Remove a member | ✅ | ✅ | ❌ | ❌ |
| Remove an **owner** | ✅ | ❌ | ❌ | ❌ |
| Create / revoke API keys | ✅ | ✅ | ❌ | ❌ |
| Close the account | ✅ | ❌ | ❌ | ❌ |

**Three rules produce every row.** An owner does everything, because ownership and the plan are financial
authority. An admin manages people but never owners and never money. A member sees who is here and
changes nothing. A machine credential is not a person and the surface says so rather than showing it a
read-only view it has no business in.

### 🔴 Absent, disabled, or refused — the three are not interchangeable

- **ABSENT** when the viewer's ROLE forbids it. An admin never sees "promote to owner". A control that is
  visible, pressable and always refused is a *silent dead write*: the person presses it, the platform
  says no, and what they learn is that the product is broken rather than that the action is not theirs.
- **DISABLED, carrying its reason** when a ROW's state forbids it. The only owner cannot be removed —
  that is a fact about that row with a remedy the viewer can act on (make somebody else an owner), so the
  control stays and the reason is on it.
- **REFUSED by the platform**, always, for both. The matrix decides what to *ask*. Hiding is not
  enforcement, and every denial above has a matching refusal in `internal/api/accounts.go` — checked by
  `tests/control-matrix.test.mjs`, which fails if a denial has no refusal behind it.

### The viewer's role comes from MEMBERSHIP, never from the credential

A credential's role is what it was minted with; a membership's role is what the person is **now**. An
owner demoted this morning must not still see owner controls because their key remembers otherwise.

---

## 3. State → copy

Both tables live in [`organizationCopy.ts`](../../web/console/src/lib/organizationCopy.ts). They are
centralised for the reason a translation pass wants a column — and for a better one: **a state whose
words are written inline is a state somebody adds without noticing it needs its own.**

**The runs list has three empty states, and collapsing any pair is the failure the page exists to
avoid.** "You have no runs yet" shown to somebody whose history predates ownership recording reads as
data loss and is not one.

**The member list has six.** `invited` and `expired` are separate rows because *"waiting for them"* and
*"send it again"* are different next actions; showing the first for the second is how an invitation sits
dead in an inbox for a fortnight.

---

## 4. Seats: two numbers, never one word

There is deliberately **no string anywhere that says only "seats"**.

- **Seats in use** — active members right now. What the next invitation is checked against.
- **Seats included** — what the plan allows. An operator can raise it for one organization.
- The **period's billed peak** is a third number and is not on this surface at all; it belongs beside a
  bill, not beside a member list.

The first two move in opposite directions on the same day — removing somebody frees a seat immediately
and does not reduce what the period is billed. A reader given one unlabelled cannot tell which they got.

⚠️ **What a seat includes is not settled** (PRD Open Question 3), and the surface says so: *"Every active
member counts as one seat. What a seat includes for billing is still being settled, so this number is
what is enforced, not a quote."* Until it is ratified, no surface and no conversation states what a seat
includes.

---

## 5. Refusals

Nine codes, nine sentences, in `REFUSAL_COPY`. **The console branches on the CODE and never on the
prose** — branching on a sentence puts the decision in two places and lets a copy edit change behaviour.

Two rules the copy follows:

1. **A refusal that involves a limit names both numbers.** *"5 in use against 5 included — upgrade, or
   remove a member first."* A limit the reader cannot check is a limit they distrust, and the number is
   already on the server that refused.
2. **An unknown code falls back to the platform's own sentence**, never to an invented cause. `refusalCopy`
   carries the upstream message through; a message manufactured in the console would replace the one
   thing the server actually knew.

---

## 6. The path in

```
        ┌── invited ──────────────────────────────────────────────┐
        │  /join/<id>          "the link fills the form in;       │
        │   (no session)        signing in is what joins you"     │
        │      ↓                                                   │
        │  /signin?next=/app/join/<id>                             │
        │      ↓  (session issued; the person is resolved)         │
        │  /app/join/<id>      one button, a POST, never on render │
        └──────────────────────────────────────────────────────────┘

        ┌── first person at a new customer ────────────────────────┐
        │  /signup             posture read FIRST — an install     │
        │                      that refuses never shows the form   │
        │      ↓  one question: what is it called?                 │
        │  /app                free plan, no card, working         │
        └──────────────────────────────────────────────────────────┘
```

**One question, not a form.** The organization's name is the only field, and it is asked rather than
derived: an email domain gives "Gmail" for every independent developer and the wrong legal entity for
half of everyone else — and an editable wrong name is a name most people never edit.

**The posture is checked before the form renders.** A form that collects a name and then refuses teaches
somebody the product is broken; a page that never offered one says *"ask whoever runs this install"*,
which is a next action. Unknown resolves to **off** — a console that cannot ask must not offer.

**Acceptance is a POST behind a press, never an effect on render.** A GET that changes state is a link a
mail scanner follows, and an invitation spent by a corporate link-preview bot is an invitation nobody can
use.

⚠️ **An invitation needs a sign-in seam that knows an ADDRESS.** `oidc` and `saml` supply one;
`configured` and `dev` do not. On those deployments an emailed invitation cannot be matched and refuses
with `invitation_identity_mismatch`. Loosening the match to anything the person supplies is exactly what
turns the link back into a credential, so the limitation is stated on the page rather than designed away.

---

## 7. Design review — the checklist, answered

| Block item | Verdict |
|---|---|
| A raw id where a human-readable name belongs | ⚠️ **Partially.** The member list falls back to `user_id` when a person has no email — which happens on the `configured` seam. It is the honest value (there is no name to show), and it is ugly. Recorded rather than papered over. |
| An interface name that differs from the code name and is not in the dictionary | ✅ §1 |
| A one-off action rendered as a permanent menu item | ✅ Members sits with Account and Billing — a thing you *are*, not a subject you open |
| A batch operation with no impact preview | ✅ Removal previews both halves before it will confirm |
| A back-end invariant translated into extra user steps | ✅ Last-owner is stated on the row, not discovered by trying |
| A control that is shown but never effective | ✅ §2, fenced by `tests/control-matrix.test.mjs` |
| An empty state serving three meanings | ✅ §3 |
| A seat number with no label | ✅ §4, and there is no string that could produce one |
| Acceptance criteria that cannot be judged true or false | ✅ PRD §13 is observable facts with triggers |
| A capability not yet built, sold as current | ✅ The sales copy gates every claim on its tasks, and refuses the seat number entirely |

**One Warn accepted with its reason:** the members page shows an internal `user_id` for a person the
sign-in seam gave no address. The alternatives are worse — a fabricated display name, or an empty cell
that reads as a rendering bug. When the seam knows an address, the address is shown.
