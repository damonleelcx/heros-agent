# P26 — Design

Product rationale: [`../../../docs/prd/P26-operator-console-refresh.md`](../../../docs/prd/P26-operator-console-refresh.md)
§8. This file carries the decisions, the numbers and the gap audit; the PRD carries the narrative.

## Context

### The gap, audited rather than assumed

Verified against the tree on 2026-07-30. Every "no operator surface" row below was established by
reading `internal/api/p8.go`, `internal/adminops/`, and
`web/admin-console/src/lib/surfaces.ts` — not inferred from the phase list.

| Subsystem (package) | Phase | Operator surface today | Verified how |
|---|---|---|---|
| `runlink`, `linkingest`, `clilink`, `metering` | P11 | **none** | no import in `adminops`/`p8.go`; no coverage field in `billing.go` or the billing page |
| `forgedelivery`, `deliveryrecord` | P12 | **none** | no import in `adminops`/`p8.go` |
| `changedelivery` | P13 | **none** | no import in `adminops`/`p8.go` |
| `distribution` (channels, trust, manifests) | P20 | **none** | no import in `adminops`/`p8.go` |
| six axes + coverage source | P13–P18 | **none** | no axis or coverage route among the 31 admin routes |
| prompt registry | P10 | models only | `/admin/api/registry` covers models and price refs |
| deployments | P19 | job fleet only | `/fleet` reads the P4/P6 queue and worker fleet |
| payments | P21 | pre-PSP billing | `billing.go` predates any payment provider |
| operator identity | P22 | verifier real, **IdP is a fixture** | `internal/adminidentity/fixture.go` |
| legal + consent | P23 | **none** | no legal or consent route |
| observability integrations | P24 | **none** (new) | this phase is the first to render them |

What *is* present and unchanged by this phase: 9 nav destinations, 11 palette-only action destinations,
31 admin API routes, 15 deny-by-default capabilities with a matrix test iterating `Capabilities`, the
audit chain, the impersonation control, and `internal/adminops/{rollout,mergeaudit,observability}.go`.

### The behavioural gap

`impersonate.read` is reason-required, time-bounded, read-scoped and fully audited — the marks of a
control designed to be **rare**. It is currently rare-by-policy and routine-by-necessity, because it is
the only tool that can show a tenant's axis coverage, delivery state or refusal causes. That makes the
platform's most privileged read the workaround for its missing aggregates.

## Decisions

### D1 — The ledger and its fence ship before any page

A checked-in ledger maps every capability in `openspec/specs/` to an operator surface or an explicit
`no-operator-surface` decision with a reason and the deciding phase. A build fence fails on a capability
in neither column. Both directions are asserted against `surfaces.ts`.

Fourteen phases of drift happened with nothing failing. The precedent for the fix is already in the tree
twice: P9 derives every public capability claim from a checked-in manifest so a claim outrunning a
capability fails the build, and P23 does the same for documentation accuracy. And the lesson is recorded
explicitly in the frontend scope guard — a manual, and then an agent, horizontal scan still missed the
fifth occurrence, which proves the rule must be machine-enforced.

*Rejected:* a PR-template checklist (that is effectively what we had; fourteen phases is the measured
failure rate); a coverage dashboard that fails nothing (a number nobody is blocked by is a number nobody
reads); **requiring** an operator surface per capability — rejected as *wrong*, not merely expensive:
many capabilities genuinely need none, and forcing one produces pages that exist to satisfy a fence,
which is worse than a gap because it *looks* like coverage; fencing on `docs/prd/` instead of
`openspec/specs/` (PRDs include proposals, so the build would fail for unbuilt things).

**Three ledger states, and no fourth:**

| State | Meaning | Requires |
|---|---|---|
| `surface: <href>` | An operator surface exercises this capability | a `surfaces.ts` destination that resolves |
| `no-operator-surface` | Deliberately none | a reason **and** the deciding phase |
| `not-yet-readable` | Wanted, not derivable from an existing store | **the collection that would make it readable**, named |

`not-yet-readable` is the typed-refusal shape applied to oversight: it converts a vague wish into a
specified task for a later phase, and it is why this change needs no new table (D7).

### D2 — Every new surface is read-only; the one candidate write is a costed fork

A gap analysis generates write controls: retry a delivery, unpublish a release, halt a channel. Each is
plausible, and each expands the platform's most dangerous surface during a phase whose purpose is
visibility. So every surface here is read-only, and the one control with a real argument — halting an
install channel so a bad release stops reaching strangers — is written up in the PRD's open questions
with both paths costed and a recommendation, and is not built.

The console's discipline is audit-then-effect with a recorded reason and a second confirmation, and its
trend ledger refuses, without qualification, anything that performs a privileged action on a user's
behalf. A refresh is where a new destructive control gets the least scrutiny, because it arrives inside a
page whose purpose is a table.

*Rejected here, deferred rather than refused:* the channel halt (genuinely defensible after the P20 key
rotation — it deserves its own design conversation). *Rejected on principle:* an operator "retry a
delivery" — delivery is downstream of verification and never a path around it, and the platform holds no
forge credential by default, so an operator retry would require one.

**Effect: this phase's blast radius is bounded by construction.** It can render a wrong number, which is
a bug. It cannot take an action, which would be an incident.

### D3 — Read the one coverage source; compute nothing

The console reads the same coverage source the transform's refusal, preflight, the CLI and the customer
console read, and renders it as received. Parity asserted in both directions: it may not offer a cell the
engine refuses, and may not omit a cell the engine materializes.

The `language-coverage` contract states this directly — one coverage source, read by everything that
states coverage, asserted in both directions — and the console's read-model rule already requires that
scores, intervals, ties, ranks, gate outcomes and coverage percentages are computed server-side and
rendered as received, because a client-side recomputation is a second source of truth for a statistical
claim.

*Rejected:* a fleet roll-up computed in the BFF (the BFF is a pass-through, not a brain — no merging,
re-ranking, reformatting or status translation, and a roll-up is all four); caching the matrix for latency
(a cached refusal the engine has since stopped refusing is precisely the offered-cell-the-engine-refuses
failure the parity assertion exists to catch).

### D4 — *Not applicable* is never rendered from an absent row

An absent coverage row renders as **unknown**, naming what is missing. *Not applicable* is rendered only
from a present row whose named cause says so.

The contract names this exact hazard: an absent row rendering as *not applicable* is a claim about the
customer's code, and it is the one thing coverage data must never say by accident. It is also the more
dangerous direction for us — it converts our backlog into the customer's problem, invisibly, and the
customer has no way to discover the substitution.

*Rejected:* a dash with an explanatory tooltip (the tooltip is not read; the dash is); suppressing rows
with no data (now the reader cannot tell the row exists — the absence becomes invisible rather than
merely ambiguous).

**Three renderings, from three states:** *applies* · *refused, with a named stable cause* · *unknown,
with the named missing input*.

### D5 — Correct the existing surfaces' honesty before adding new ones

Three known-wrong figures ship on the operator console today: SUM-derived figures with no link coverage
(against a rule stated in `project.md`), cross-tenant aggregates with no demonstrated `unverified`
exclusion, and an audit surface implying coverage of every merge while mirroring one of two merge paths.
All three corrections land in wave one, before any new page.

The argument is already made in this repository by the bundle scanner, about a different measurement: it
refuses to report a number it cannot measure honestly, because a scan that reports a wrong number is
worse than one that reports none — **the wrong number gets acted on.** A billing operator issuing a
credit against a SUM figure with 31% link coverage is that sentence with money attached.

*Rejected:* new surfaces first with corrections in a follow-up (a phase whose stated purpose is operator
honesty cannot add four pages while leaving three known-dishonest figures on shipped ones); a footnote
naming the caveat (a footnote beside a figure reads as reassurance, not as a caveat).

### D6 — Measure the phase by impersonation displaced, not by pages shipped

The headline metric is the change in impersonation sessions whose recorded reason is a routine lookup an
aggregate now answers — computed from the existing impersonation audit records, before and after.

A refresh phase's natural metric is "the surfaces exist", which four pages nobody opens satisfies.
Impersonation volume is a real data-protection measurement, the data already exists in the audit chain,
and it lets the phase **fail visibly**: if the number does not move, the surfaces answered the wrong
questions, and that is worth knowing.

*Rejected:* counting new surfaces; surveying operators (a preference is not evidence).

### D7 — No new table; where a read is not derivable, say so

Every read derives from an existing store. Where it does not, the ledger records `not-yet-readable` and
names the collection that would make it readable.

Creating a table is a one-way door requiring at least two non-table alternatives to be presented first,
and "build it now for future use" is refused. More importantly, the alternative to a table here is not a
gap — it is an **honest gap with a named cause**, directly actionable by the next phase.

*Rejected:* a per-tenant deployment-state table (it would be populated by a signal that does not exist, so
it would be a place to store guesses); inferring a version from the last-seen API contract version (an
inferred version rendered as a version is a wrong number that gets acted on during an incident).

**Worked example of the honest form:**
`deployed-version: not-yet-readable — requires a deployment heartbeat carrying the release identifier`.

### D8 — New capabilities partition; no existing role widens

New surfaces get new capabilities rather than being folded into existing ones, and no existing role gains
a capability because a new page made it convenient. The deny-by-default matrix test keeps iterating
`adminrbac.Capabilities`, so a capability added without a considered grant is a failing test.

The console's existing model already partitions capability so no single persona can both move money and
change a model registry. Axis refusal distributions and signing-key state are two genuinely new
personas' concerns — an axis owner and a release engineer — and folding them into `crosstenant.read` or
`registry.admin` would widen two roles at once.

**Proposed (see PRD open questions 3 and 4 — these need a decision, not an assumption):**

| Capability | Governs | Why not an existing one |
|---|---|---|
| `delivery.read` | Delivery records, rollout state | `crosstenant.read` also grants spend and usage; a support operator needs delivery without those |
| `release.read` | Channels, keys, verification, smoke | No existing role is a natural home for signing-key state |
| `axis.read` | Axis adoption, refusals, coverage | Lets an axis owner see refusals without seeing usage or spend |

### D9 — Three states, everywhere, in the type

Every state this phase renders is a named enum in Go, not a boolean:

| Subject | The three states | Why two would hide something |
|---|---|---|
| Merge | `merged` / `closed_unmerged` / `unknown` | A merge is **observed**, never inferred from a PR closing |
| Artefact verification | `verified` / `failed` / `not_yet_verified` | *Not yet checked* is not *failed* |
| Post-publish smoke | `passed` / `failed` / `queued_until_timeout` | A **retired runner label queues until timeout rather than failing**; rendering that as *failed* sends an engineer to debug a build that never ran (measured in P20) |
| Integration health | `absent` / `configured` / `degraded` | *Not configured* is a decision; *degraded* is a fault |
| Coverage cell | `applies` / `refused(cause)` / `unknown(missing)` | See D4 |
| Link coverage | a percentage / `unknown` | A figure with unknown coverage is **not rendered at all** |

This is the same rule the console already applies to 404 versus 503 versus transport failure, extended to
the states this phase introduces.

## Interfaces

```go
// internal/adminops — new read models beside the existing ones. Read-only; no writer in this phase.

type MergeState string   // merged | closed_unmerged | unknown
type VerifyState string  // verified | failed | not_yet_verified
type SmokeState string   // passed | failed | queued_until_timeout
type IntegrationState string // absent | configured | degraded

// DeliveryRow is one P12 delivery record as an operator reads it.
type DeliveryRow struct {
    TenantID       string
    ConfigHash     string
    SourceRevision string
    Target         string
    Merge          MergeState
    RolloutStage   string  // the P13 change-delivery stage
    Undeliverable  string  // typed cause, empty when deliverable
}

// ReleaseRow is one published artefact and its trust state. NO key material — identifier and
// fingerprint only, and no operation on this surface produces key material.
type ReleaseRow struct {
    Version        string
    Channel        string
    Platform       string
    SigningKeyID   string   // identifier
    KeyFingerprint string   // fingerprint
    KeyRetiredAt   string   // empty when the key is active
    KeyRetiredWhy  string
    Verification   VerifyState
    Smoke          SmokeState
}

// AxisRow is one axis's fleet picture. Counts, never scores — only P4 ranks, and only a P5.5
// verified delta is a claim, so this must not be rendered with the grammar of a ranked result.
type AxisRow struct {
    Axis     string
    Status   string            // EXISTS | PARTIAL | ABSENT — the axis's own declaration
    Tenants  int
    Nodes    int
    Refusals map[string]int    // keyed by STABLE typed cause identifier, never prose
}

// DerivedFigure pairs a SUM-derived figure with its link coverage. The pairing is in the TYPE so a
// figure cannot be rendered without its coverage: Coverage == nil means the figure is not rendered.
type DerivedFigure struct {
    Value    string
    Coverage *float64 // nil ⇒ unknown ⇒ the figure is not rendered
    Source   string   // e.g. "P5.5 verified-delta ledger" for a gainshare figure
}
```

```
operator-surface-ledger.md  (checked in; the fence parses it)

| capability            | state              | detail                                              |
|-----------------------|--------------------|-----------------------------------------------------|
| delivery-record       | surface            | /delivery                                           |
| forge-delivery        | surface            | /delivery                                           |
| run-linking           | surface            | /billing  (link coverage beside every figure)        |
| language-coverage     | surface            | /axes                                               |
| cli                   | no-operator-surface| customer-side, offline, no platform state to oversee (P26) |
| <payments>            | not-yet-readable   | requires the P21 webhook + dunning read model        |
| <deployed version>    | not-yet-readable   | requires a deployment heartbeat carrying the release id |
```

## Risks

| Risk | Mitigation |
|---|---|
| The refresh closes today's gaps and drift resumes | The fence ships in wave one, before any page; both directions asserted; a capability in neither column fails the build |
| The ledger becomes a rubber stamp of `no-operator-surface` | The decision requires a reason and the deciding phase, and is reviewable and attributable. A fence cannot force judgement — it can force the judgement to be recorded |
| A wrong number gets acted on | Coverage beside every derived figure; unknown coverage renders as unknown; the pairing is in the type, so a figure without coverage cannot be rendered |
| The console becomes a second opinion about coverage | Read the one source; parity asserted against the real engine in both directions; the BFF stays a pass-through |
| An absent coverage row reads as *not applicable* | Absent renders as *unknown* naming the missing input; *not applicable* only from a present row whose cause says so |
| A write control slips in | Read-only by construction; the one candidate is a costed fork with a recommendation |
| A new surface widens a role | Capabilities partition (D8); the deny-by-default matrix test iterates every capability |
| Key material reaches a surface | Identifier and fingerprint only; no generation, no export; asserted. A signing key has already leaked once in this project by being emitted into a session transcript |
| An aggregate hides a single-tenant defect | Every aggregate offers its drill-down; a count is not rendered with the grammar of a rank |
| P21/P22 surfaces render empty pages as working ones | `not-yet-readable` with the missing input named; an empty state is never rendered as a zero |
| Impersonation volume does not move | It is the headline metric (D6), measured from existing audit records — so the phase can fail visibly |
