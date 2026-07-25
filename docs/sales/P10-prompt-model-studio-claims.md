# P10 Prompt & Model Studio — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-07-25)
- **Audience:** anyone who describes P10 to a customer — a deck, a demo, a scoping call, an SoW.
- **Rule:** the honest version of this feature is narrower than "runtime configuration" implies, and
  the honest pitch is **stronger** than the inflated one. Do not trade the second for the first.

## 11.1 The capability, with its boundary

**What P10 gives a customer:**

- Author and version prompts from the console — an authenticated write path over a content-addressed,
  structurally-immutable registry. Every edit is a new version; nothing is ever mutated or deleted.
- See a prompt's **version history**, **diff** any two versions (with the slot-set change shown
  separately from the wording), and get **impact analysis before publishing** — which nodes an edit
  would stop from transforming, and which could not be analyzed.
- **Bind** a prompt's variables explicitly (`literal`, `expr`, `env`, `input`), validated at
  spec-resolve with the node, dimension and slot named on any failure.
- **Preview** the exact string a run would send (byte-identical), **test-run** a prompt against a model
  (output, cost, latency, tokens), and **compare** two versions or one version across two models.
- With `bound` apply mode (opt-in per node): change the **model, its parameters, the prompt version,
  and `literal`/`env` bindings as DATA** — a reviewed edit to a binding document, no new codemod.

**The boundary — state it, do not let the customer infer it:**

> **Model and prompt version are data. The wiring between a prompt's holes and the program's values is
> code.**

Concretely, these require a **code change** (a reviewed diff, a build, a merge) — they are **not**
runtime-changeable, even in `bound` mode:

- the graph wiring (which node calls which),
- the skills bound to a node,
- the context policy,
- `expr` bindings (a call-site expression like `ticket`) and `input` bindings — they name a variable
  in the program's **lexical scope**, which cannot move into a data file without reflection Go does
  not offer.

A customer planning around **general runtime reconfiguration** discovers the truth during delivery.
Say the boundary up front. The console states it per node; your slides should too.

## 11.2 The demo script — never present a comparison as a result

🚫 **Do not** run a studio side-by-side and say "see, version B is better." A studio comparison shows
two outputs; it declares **no winner**, carries **no score**, and offers **no promotion path** — by
design. Presenting a two-sample eyeball as a finding is exactly the amateur loop the platform exists to
replace, and it manufactures false confidence at the moment a buyer is deciding.

✅ **The honest pitch, which demos better:**

> *Try a model and a prompt in **seconds** in the studio — discard the obviously-wrong cheaply. Then
> **prove** the promising one with a multi-seed evaluation, and **ship it as a verified pull request**
> with the delta attached.*

Three beats: **try it (studio) → prove it (P4 multi-seed eval) → ship it (P5.5 verified delta as a
PR)**. The studio is the cheap first filter, not the instrument of record. Keeping that line is what
makes the "prove it" claim credible.

## 11.3 Do not promise 10b's runtime layer before it ships

`bound` apply mode (Wave 10b) is opt-in and sequenced second, deliberately, so it can be cut. Until it
is delivered and enabled for a customer:

- **Every configuration change is still a reviewed diff** — a new codemod, a build, a merge. That is
  the whole product today and it is a good story: reproducible, reviewable, revertible.
- Do **not** promise "change your model at runtime with no deploy" until `bound` mode is delivered for
  that customer. When it is, promise exactly what it does — model, params, prompt, `literal`/`env` as
  data — and nothing wider.
- The runtime resolver is **fail-static and opt-in**: it never puts our platform on the customer's
  production boot path, and a platform outage never changes or halts their nodes. That is a feature to
  sell, not a caveat to hide — lead with it when the topic of "runtime config" comes up.

## The studio matrix (M-series) — a configuration surface, not a leaderboard

The studio's primary screen is a **node × model matrix**: agent nodes across the top, models down the
side, a prompt at each intersection. It *looks* like every eval tool's grid — so the discipline is to
say what it is **not**.

- **It ranks nothing.** No cell is scored, highlighted as best, or marked a winner. The only markings a
  cell carries are **in force** (bound into runtime) and **unverified/verified**. A tested cell shows
  that execution's cost, latency and tokens — never a comparison, never a "this one wins."
- 🚫 **Never present the matrix as a result or a ranking.** "Look, the grid shows model X is best" is
  the forbidden move — it is D8's amateur loop in a prettier layout, and it manufactures false
  confidence at the buying moment.
- **A bound cell is "selected, unverified," never "proven best."** Saving a cell injects it into runtime
  (bound mode), and it is marked **in force — unverified**. "In force" means *someone chose it*, not
  *it was proven better*. Only a P4 multi-seed evaluation ranks, and only a P5.5 verified delta is a
  claim.
- ✅ **The honest pitch, which the grid makes fast:** *try each cell in seconds — discard the
  obviously-wrong cheaply — then prove the promising one with a multi-seed evaluation and ship it as a
  verified pull request.* Try it → prove it → ship it. The matrix is the cheap first filter; the
  instrument of record is still P4 + P5.5.

## The console fits one screen (viewport-first)

Every console surface fits the viewport — the shell never page-scrolls, and a page's sections are
**in-page tabs** rather than a long vertical stack (P9 NFR17). The studio's matrix is the landing tab,
on screen the instant the page opens; the prompt library and bound-node views are one tab-click away,
not a long scroll down. For a demo this matters: you reach the primary surface immediately, and you
never scroll past a wall of banners to get to the thing you are showing. Measured at a standard
laptop viewport, `documentElement.scrollHeight ≤ innerHeight` for every `/app/*` view.

The **operator console** (internal ops) got the same treatment: a fixed-height shell, and the
tenant-detail's stacked sections split into **State & quotas** and **Actions** tabs. It matters more
here than in the customer console — the **kill-switch alarm** and the **acting-principal / impersonation
band** now never scroll out of view during an incident. Every admin view fits one screen (measured
0-overflow across overview, tenants, tenant detail, billing, fleet, and audit).
