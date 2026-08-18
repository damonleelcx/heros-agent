## Why

Everything the platform knows about a customer's agentic system starts with somebody handing
`discovery.Run` a checked-out tree. Today there are two ways to do that and both require the CLI: push a
bundle, or be a developer with a local clone. The GEHA program's premise is a person who has installed
nothing, so intake is the phase that decides whether the program is reachable at all.

`internal/sourceingest` already has the seam — `Source` is one interface, `BundleSource` is one
implementation, and [`source.go:32-34`](../../internal/sourceingest/source.go) names the missing one:
*"A GitSource that clones from a registered remote can implement it later without touching the discovery
orchestrator."* It also states, at length, why it was not built first: a clone needs a credential *"held
by the platform, usable at any time, for any revision, without the customer present. That is a standing
capability. A bundle is an act."*

[ADR-013](../../../docs/adr/ADR-013-source-acquisition-posture.md) reviews that argument rather than routing
around it, and concludes that the cost is a property of the grant's **scope and lifetime**, not of
cloning. A grant that is one repository, read-only, revocable and logged per use is a materially different
object from the broad form the original argument was written against. So the bundle stays the default and
the clone becomes an opt-in upgrade.

## What Changes

- **ADDED** `GitSource` behind the existing `sourceingest.Source` seam — GitHub, GitLab and Bitbucket —
  producing the same source snapshot the bundle produces, indistinguishable to every consumer.
- **ADDED** the repository **connection**: per-repository, read-only, revocable from the console, disclosed
  before authorization, and recorded per use with `(tenant, repository, revision, actor, reason)`.
- **ADDED** revocation that cascades — the grant **and** every derived tree — so a revoked connection
  cannot be answered from cache.
- **ADDED** the local mode as a **pairing flow** with the existing `heroslocallink` bridge: a local
  repository is read in place and its contents are never transmitted.
- **ADDED** four distinct clone failure causes (credential rejected / repository not found / revision not
  found / network) and an explicit prohibition on falling back to an older snapshot.
- **ADDED** retention and ingest observability: expired snapshots removed on a schedule whose last run is
  readable from a health endpoint; ingest outcomes broken out per forge and per cause, never only as an
  aggregate.
- **ADDED** a **forge-read** credential kind in `internal/broker`'s `Secrets` source, which is
  provider-scoped today.
- **MODIFIED** nothing about `BundleSource`. Mode 1 keeps every capability, and a feature that works only
  under a connection is a defect rather than a tier.
- **NOT CHANGED** the egress allowlist posture (forge hosts are added to it, explicitly), ADR-005's
  delivery default, and the wire contract in `internal/runlink`.

## Impact

- **Affected capabilities:** `source-acquisition` (new)
- **Affected code/systems:** `internal/sourceingest` (new implementation behind the existing interface),
  `internal/broker` (new credential kind), `internal/launch` (one constructor call), `internal/api`
  (connection routes), `web/console` (connection list, consent screen, revoke control), P19 deploy
  (retention scheduler, forge egress)
- **Dependencies:** upstream — P29 (the seam), P11 (the local bridge), P19 (scheduling), ADR-013.
  Unblocks — [P33](../../../docs/prd/P33-surface-assessment.md), which has nothing to assess without source.
- **Known gap carried, not closed:** `heros link` is pinned to `https://heros-agent.space` and nothing
  overrides it, so the local mode against a self-hosted deployment is blocked on a boundary decision.
  PRD §14 Q1.
- **Documents only in this program.** Every task is unchecked; no Go, TSX or migration ships here.
