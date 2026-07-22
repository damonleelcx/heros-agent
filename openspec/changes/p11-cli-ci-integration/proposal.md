## Why

The CLI is delivery surface #1, the only surface available on **every plan including Free**, and the
strategic reason this project is Apache-2.0 — *"so the discovery/CLI layer can be adopted freely and
become the ecosystem's ingestion standard."* It is also 163 lines that do one of the three things the
README advertises, specified in two lines across the whole repository.

[`cmd/discover/main.go`](../../../cmd/discover/main.go) takes `--repo --config --out --report
--repo-url --commit --workflow-id` and emits a Workflow IR plus a discovery report. That is the entire
CLI. The codemod ([`internal/transform`](../../../internal/transform/)) and the eval harness
([`internal/evalharness`](../../../internal/evalharness/)) have **no command-line entry point**, and CI
integration does not exist at all — `.github/workflows/ci.yml` is this repository's own CI, not a
product surface. The only behavioral requirement anywhere is in P7's entitlements spec ("CLI and
discovery SHALL be available to all plans including Free"), which is a billing rule that happens to
mention the CLI.

**The commercial consequence is sharper than the feature gap.** P7 derives the billable value metric
from telemetry:

> `p7/specs/metering/spec.md` — *"SUM SHALL be derived from the P2.5 cost events, not collected by a
> second pipeline."*

Correct — but the CLI is supposed to run eval **in the customer's build environment with the
customer's own provider keys**, so those cost events are emitted *there*. Nothing carries them to the
platform. **SUM is zero, the subscription meter has no input, and the dashboard has nothing to show.**
One missing link breaks the product surface and the revenue model at the same time.

Sending anything is a trust decision, and for this product the default *is* the strategy. The CLI runs
over proprietary source with the customer's own keys; a developer tool that phones home by default
while reading a private repository is a trust event, and the blast radius of getting redaction wrong
once is the company. So the boundary is opt-in, disclosed before it is crossed, and conservative by
**construction** rather than by review — the payload is built from an allowlist, never produced by
stripping secrets out of a rich object, because a denylist fails **silently** the first time someone
adds a field and forgets the stripper.

And CI has requirements of its own that are easy to get backwards. A step must distinguish *"the
quality gate you configured failed"* from *"the tool broke"* from *"your config is invalid"* — three
conditions with three different remedies, and a CI job that fails for an unclear reason gets disabled.
Above all, **our availability must not be able to break a customer's build**: failing someone's
pipeline because our service blinked is a stability cost we impose on them for our own convenience,
which the project's priority ordering does not permit.

## What Changes

- **New capability `cli`.** The command set the product advertises — **`discover`**, **`apply`**
  (Variant Spec → reviewable diff), **`eval`** (multi-seed run + score), plus `login`, `link`, and
  `status`. All of `discover` / `apply` / `eval` complete **with no platform account and no network
  access**: the free tier has to be genuinely free to be an ingestion standard, and an offline
  guarantee is also the simplest possible answer in a security review. **Provider credentials are read
  from the customer's environment and used only for calls their own machine makes** — they are never
  transmitted to the platform under any configuration. **Machine-consumable output goes to stdout in a
  stable, versioned format; human narration goes to stderr**, so CI consumes one while a developer
  reads the other. **Exit codes are a contract** with distinct, documented values for success, a
  configured gate failing, an operational error, and an invalid configuration. The CLI declares a
  platform-contract version and, on mismatch, **names the required version and refuses to produce
  results under mismatched semantics** rather than silently computing something different.
  Configuration resolves in a documented order and `status` reports each effective value **and where it
  came from**. Every command is non-interactive by default and needs no TTY.
- **New capability `run-linking`.** The egress boundary. Transmitting run data requires an **explicit
  command and an authenticated identity**; no other command transmits anything. A **dry-run renders the
  exact payload without sending it** — "we only send metrics" is a claim, and a command that prints the
  bytes is evidence, which is what a security reviewer actually needs. The payload is **constructed from
  an explicit allowlist**: cost / latency / token metrics, IR **structure** (node ids, edges, model
  refs, pattern labels), `config_hash` and `source_revision`, eval scores and intervals, and run
  metadata. **Prompt text, source code, file contents, generated diffs, environment values and provider
  credentials are never transmitted** — on any path, including error reporting and debug mode, so a
  verbose flag cannot widen the boundary. Linking is **idempotent**, so a retried CI step cannot
  double-count a meter that becomes an invoice, and linked events enter the **existing P2.5 substrate**
  with the standard tag set rather than a second pipeline — P7 already forbids a parallel counter for
  the billable metric. **SUM is derived only from linked runs and the platform never infers,
  extrapolates, or estimates unlinked spend**; instead **link coverage** is a first-class read model
  displayed wherever a spend figure derived from linked runs appears, because once metering is partial
  by design the completeness of the figure is part of the figure. A successful link prints a URL that
  opens that run in the dashboard, and a **failed link never invalidates the local result** — the run
  happened, the numbers are real, and our inability to receive them is our problem.
- **New capability `ci-integration`.** A **published, versioned** action / reusable workflow rather than
  a README snippet, because a snippet copied into two hundred pipelines cannot be fixed. It posts a
  **check** with the run's outcome and uploads the IR and run report as **artifacts**. It **does not
  fail the build when the platform is unreachable, degraded, or slow** — it reports and continues, with
  a bounded timeout so a hung platform cannot stall a pipeline either — and it **does fail the build
  when a customer-configured quality gate fails**, because a check that never fails is decoration.
  Credentials come from the CI secret mechanism and are never echoed into logs, check output, **or
  uploaded artifacts** (artifacts persist and are the easy one to forget). The integration is usable
  **without linking** — a customer may run everything locally and publish nothing — and it exposes the
  hook [P12](../p12-forge-delivery/)'s CI-mediated delivery uses without defining that contract itself.
- **Not changed here.** No second telemetry pipeline, no client-side scoring (the CLI runs the **P4
  harness**, not a local approximation, so the CLI and the platform cannot produce two answers to one
  question), no TUI or IDE plugin, no production runner, and **no opt-out telemetry channel of any
  kind**. P7's metering and entitlement specs are consistent with this and are not edited: SUM still
  derives from P2.5 cost events, and CLI+discovery remain available on every plan.

## Impact

- **Affected capabilities:** `cli` (new), `run-linking` (new), `ci-integration` (new). Consumed, not
  modified: `discovery-engine` (P1), `config-layer`/`runtime` (P2), `metrics-observability` (P2.5),
  `eval-harness`/`scoring` (P4), `metering`/`entitlements` (P7), `web-console` (P9).
- **Affected code/systems:** `cmd/` gains the full command set (today only `cmd/discover`); a new
  release/signing pipeline for customer-installed binaries; a published CI action; and an authenticated
  ingest path into the existing P2.5 substrate. No new store — `LinkCoverage` is a read model.
- **Dependencies:** requires **P1** (IR), **P2** (codemod + worktree/build), **P2.5** (event shapes and
  substrate), **P4** (harness + scoring), **P7** (tenant identity, metering), **P9** (where linked runs
  and coverage are displayed).
- **Unblocks:** **SUM metering has an input at all** — the subscription revenue model becomes
  computable; **[P12](../p12-forge-delivery/)'s CI-mediated delivery**, which runs inside this CI
  integration and is the default delivery mode under
  [ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md); and the free-adoption
  strategy the license was chosen for.
- **Breaking:** none. `cmd/discover`'s existing flags and outputs are preserved; the full command set is
  additive.
- **Sequencing:** **11a** (CLI + the egress boundary) is a complete phase on its own and carries no CI
  surface. **11b** (CI integration) follows and is what P12 builds on.
