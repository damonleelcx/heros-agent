# Information architecture — the shell, the navigation set, the canonical routes (P9 §1.4)

> **What this fixes.** Four pages, zero navigation, zero links between them, and every entry point a
> bare query parameter the user must already know. This document names the shell, the navigation set,
> and **the canonical route for every subject**, so every legacy entry point has a target (FR11) and
> no surface is reachable only by URL (FR9).

---

## 1. The two zones, and why the boundary is a route prefix

| Zone | Prefix | Session | Tenant data | Upstream call |
|---|---|:---:|:---:|:---:|
| **Public** | `/`, `/signin` | no | **no** | **no** |
| **Console** | `/app/**` | required | yes | yes |

The boundary is a **prefix**, not a per-page decision, because a fail-closed rule enforced page by page
fails the first time somebody adds a page. Everything under `/app` requires a session and redirects to
sign-in without one (FR2); nothing outside it reads tenant data or causes the BFF to use the
server-held credential (FR32).

---

## 2. The subjects

The console has exactly **six** subjects. Every route belongs to one of them, and every view names
exactly one (R13).

| Subject | Identified by | Where it comes from |
|---|---|---|
| **Workflow** | `workflow_id` | The unit of everything: it has a graph, a board, and proposals. |
| **Run** | `run_id` | One execution of one configuration. |
| **Transform** | `config_hash` + `source_revision` | The reviewable diff. Two-part key, because a config hash means nothing without the revision it was applied to. |
| **Variant** | `variant_id` | One configuration under evaluation; carries a scorecard. |
| **Proposal** | `proposal_id` (within a workflow) | A P5.5 recommendation with its verified delta. |
| **Account** | the session's tenant | Plan, entitlements, spend. There is exactly one, and it is never named in a URL — it comes from the session (NFR12). |

---

## 3. Canonical routes

```
PUBLIC
  /                                                  home — what the platform does, honestly
  /signin                                            session exchange

CONSOLE                                              (all require a session)
  /app                                               overview — this session's subjects, and what to do next
  /app/configure                                     author + submit a Variant Spec

  /app/workflows/{workflowId}                        workflow hub
  /app/workflows/{workflowId}/graph                  pattern-classified graph
  /app/workflows/{workflowId}/board                  eval board            (?profile= selects a weight profile)
  /app/workflows/{workflowId}/proposals              P5.5 recommendations
  /app/workflows/{workflowId}/proposals/{proposalId} one proposal: rationale, verified delta, full diff

  /app/runs/{runId}                                  run record + per-node I/O
  /app/runs/{runId}/live                             live monitor (SSE, polling fallback)

  /app/transforms/{configHash}/{sourceRevision}      transform + diff

  /app/variants/{variantId}/scorecard                P4.5 attribution scorecard

  /app/account                                       plan, entitlements, spend, automation level
```

**Every path segment that is not a literal is a subject identifier**, so a route is readable without a
key and a pasted link is self-describing. Nothing carries a tenant identifier: scope comes from the
session, and a tenant id in a URL is a scope-widening attempt (NFR12).

**Query parameters are for view state, never for subject identity.** `?profile=` selects a weight
profile on a board whose subject is already fixed by its path. That split is what makes R9's
shareability and R8's no-hand-typed-identifiers compatible: the subject is in the path (shareable),
and the way you *got* there was selection (not typing).

---

## 4. Legacy entry points → canonical routes (FR11)

| Legacy | Canonical | Note |
|---|---|---|
| `/p2?run={id}` | `/app/runs/{id}` | |
| `/p2?cfg={hash}&rev={revision}` | `/app/transforms/{hash}/{revision}` | Both parts required; one without the other is its own message, distinct from not-found (P2-12). |
| `/p2` (no parameters) | `/app/configure` | The authoring panel is the only one of P2's three that has no subject until you submit. |
| `/p25/monitor?run_id={id}` | `/app/runs/{id}/live` | |
| `/p35/graph?workflow_id={id}` | `/app/workflows/{id}/graph` | |
| `/p4/board?workflow={id}` | `/app/workflows/{id}/board` | |
| `/p4/board` (no parameter) | `/app` | ⛔ The `'wf-demo'` default is **not** ported (P4-0). No subject means selection, never someone else's board. |
| `internal/api/static/index.html` (orphan) | `/app/workflows/{id}/proposals` | Forward-ported to the P5.5 surface, in English (IDX-1…5). |

---

## 5. Splitting `/p2` into three routes without losing P2-11

`p2.html` is three panels on one page — configure, review the diff, watch the run — and one behavior
crosses all three: **P2-11**, where a successful submit fills the transform and run panels, auto-loads
both, and starts watching. Three canonical routes would ordinarily lose that.

It is preserved as follows, and the distinction matters:

- `/app/configure`'s subject is **the variant being submitted**. After a successful submit it renders
  the submit outcome — which is genuinely a property of the *submission*, not of the run: the
  400-only *nothing was persisted* message (P2-9), the `build-rejected` state (P2-10), the resolved-ref
  chips (P2-5) — and **begins watching the run in place** (P2-23…26), so nothing about P2-11 is lost.
- `/app/runs/{runId}` and `/app/transforms/{hash}/{revision}` are the **canonical, shareable** routes
  for those subjects, linked from the outcome. A link pasted into a pull request opens the subject,
  not the form that produced it.

`/app/runs/{runId}` also renders the transform that produced the run, because the run record already
carries `config_hash` and `source_revision`. That is **two client requests**, not a merged response:
Decision 3 forbids the *BFF* from merging two upstream calls, and says nothing about a view reading two
read models — which is what the legacy page already did.

---

## 6. The navigation set

**Primary navigation** (always present, in the shell):

| Item | Target | Present when |
|---|---|---|
| Overview | `/app` | always |
| Configure | `/app/configure` | always |
| Workflow → Graph / Board / Proposals | `/app/workflows/{id}/…` | always; the sub-items resolve against the **current subject** and offer selection when there is none |
| Runs | the current or last-visited run | always; offers selection when there is none |
| Account | `/app/account` | always |

**The current subject is carried across surfaces** (FR30): moving from graph to board to proposals
never re-asks which workflow, and a run opened from a board keeps its workflow context so the way back
is one step, not a new search.

**The command path** (`Cmd/Ctrl-K`) reaches every surface and every subject this session has visited.
It is the keyboard answer to the enumeration gap in
[`surface-or-drop.md`](surface-or-drop.md) §3: the console cannot list a tenant's workflows, but it
can always offer the ones you have already opened, and it must never ask you to retype one of them.

**Cross-surface links** (§7.5), each a link rather than a URL edit:

```
graph  ──▶ node ──▶ (its runs, when one is in view)
board  ──▶ row ──▶ variant scorecard ──▶ its run ──▶ its transform ──▶ its diff
run    ──▶ live monitor · its transform · its workflow
proposal ──▶ its diff · the diagnosis that produced it · the variant it proposes
```

---

## 7. What is deliberately **not** in the navigation

- **Anything cross-tenant, and anything operator.** P8's surfaces are a different application on a
  different origin with a different cookie jar. There is no link, no shared component that reads an
  admin identity, and no route here that an admin principal reaches *as an admin*.
- **A "dashboard" landing page of aggregate numbers.** `/app` is a list of **subjects and next
  actions**, not a wall of statistics: every number this platform produces carries a qualifier, and a
  summary tile is exactly the rendering that drops it (FR31, R14).
- **A settings area.** P9 adds no tenant configuration. Plan and automation level are *read* on
  `/app/account`; changing them is P7's surface.
