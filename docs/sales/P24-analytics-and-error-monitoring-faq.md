# P24 — the four questions a buyer actually asks

**Audience:** anybody in front of a prospect or a security reviewer. **Rule for this page:** every answer
below is what SHIPPED, and where something did not ship the answer says so. A sales answer that is
narrower than the truth costs a deal; one that is wider than the truth costs a customer, later, in the
room where they are reading a contract back to you.

---

## 1. "Do you record my screen?"

**On the pages you use to do work, no — and it is not a setting.**

Session recording runs on the **public marketing site only**, and only for a visitor who turns it on.
It is **refused** on every signed-in page and on our internal operator console, structurally: the
category is absent from those surfaces' policy, so no plan, role, entitlement, flag, variable or
parameter reaches it. Turning it on there would mean editing a table and failing three tests in two
applications.

The reason is worth giving, because it is more convincing than the promise: a signed-in page renders
prompt text, generated diffs, node identifiers, model configuration and run output. A recording of it
would be a legible copy of the exact material our egress boundary exists to keep inside your
deployment. Masking a surface that gains pages every quarter fails the first time somebody adds a page —
so we did not mask it, we refused it.

On the public site, recording is masked by default: **all** text and every form input, with no
per-element exceptions currently taken.

---

## 2. "Can I turn it off?"

**Everything optional is already off.** There is nothing to turn off — there is something to turn on.

Four categories, and three of them start unanswered and behave as declined: *Usage analytics*, *Session
recording*, *Error diagnostics*. Nothing loads and nothing is stored before you choose.

- **Declining costs you nothing.** Every page, every control and every route works identically. That is
  asserted by a test that compares the set of destinations, headings and controls on a declined page
  against a granted one.
- **Accept and decline are the same size, the same colour and the same class**, and decline comes first
  in the tab order.
- **A refusal is remembered.** You are not asked again — not on the next page, not on the next visit —
  until the document naming who receives data changes materially.
- **Withdrawal is on every page**, takes effect on your next navigation, and needs no sign-out.

---

## 3. "Does your on-prem install phone home?"

**No, and the package proves it rather than promising it.**

Every deployment in existence today is run by a customer or by an operator on their behalf. On those
substrates — Compose, Kubernetes, and the air-gapped package — all three integrations are **absent**:
no measurement id, no project id, no reporting endpoint, and no empty slot for one either. A deployment
manifest does not mention the switches at all, because an empty slot is one `--set` from being filled in
a file somebody edits without reading.

The air-gapped package is the one where the claim would otherwise be uncheckable — a customer with no
egress cannot observe something trying to leave. So the claim is produced by the same run that produces
the tarball: the package build **fails** if any staged artefact references an external origin or carries
a reporting identity. It is a property of the artefact, established while the artefact is being cut,
not a README somebody updates.

**The CLI carries nothing either**, and that is structural: its whole dependency graph is banned from
linking an HTTP client, which no crash reporter can live without.

---

## 4. "Can I get error reports from my own install?"

**No. That is a self-hosted collector, and we have not built it.**

Error reporting today points at our own deployment or at nothing. There is no configuration that makes
your install report to your own endpoint, and there is no partial version of it — no "point the DSN
wherever you like", because the browser reporter refuses a destination that is not on the checked-in
allowlist, which is what makes our published policy an honest statement of where data goes rather than a
suggestion.

What you **do** get today, from the surfaces you already run:

- a `trace_id` on every internal-error response, which resolves the same request in the span store and
  in the structured log — one string, not three piles;
- a three-state reporting entry on the readiness endpoint (`absent` / `configured` / `degraded`), so a
  monitor can tell "we chose not to configure this" from "it is configured and failing";
- the platform's own telemetry substrate, which is complete, unsampled and joined to runs — and which is
  where every frequency question is answered anyway, because an incident inbox is a defect inbox and not
  a rate source.

If a self-hosted collector matters to a deal, say it is not built and take the requirement back. Do not
say "roadmap" unless it is on one.

---

## What to hand a security reviewer

Three documents, in this order. All three are generated or fenced, so none of them can drift from what
runs:

| Document | What it answers |
|---|---|
| `/legal/sub-processors` | Who receives what, on which surfaces, gated on what, processing where. Published and versioned; a material change re-asks for consent |
| `docs/decisions/p24-error-event-allowlist.md` | The complete set of fields an error report can contain — **generated from the code that builds the report**, with a build gate on drift |
| `docs/decisions/p24-operator-acceptable-use.md` | Why our own operator console has no consent banner, and what it does and does not collect |

The sentence that usually ends the conversation: **an error report is built from a thirteen-field list,
not filtered down to one.** A field somebody adds to an internal error is *absent* by default — visible
to us as a missing feature — rather than *sent* by default and discovered by you.
