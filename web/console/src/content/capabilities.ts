/**
 * capabilities.ts is the CAPABILITY MANIFEST — the only source of claims the public surface may make
 * (task 7b.1, FR33, R15).
 *
 * # 🔴 The rule, and why it is a machine
 *
 * A marketing page is the one file in a repository that no test reads and no engineer re-checks after
 * the first week. It is therefore the page most likely to describe the roadmap in the present tense —
 * and that drift is not caught internally. It is caught by a customer, after the sale, as a support
 * ticket or a refund.
 *
 * So every claim the public page renders resolves through an entry here, and
 * `scripts/scan-claims.mjs` **fails the build** on a claim that is unlisted or not `shipped: true`
 * with a named owning phase. The sales-operations rule *only promise what has been delivered* becomes
 * a gate rather than a habit.
 *
 * # What an entry is, and what it is not
 *
 * An entry is a statement about **something the platform does**, tied to the phase that delivers it
 * and the code that proves it. It is not a feature name, a benefit, or a positioning line — those are
 * written on the page, around the claim.
 *
 * `evidence` names where a reader inside the team can go to check. That field is the difference
 * between a manifest and a second marketing document: it makes the claim falsifiable by someone who
 * doubts it.
 *
 * # Adding an entry
 *
 * When a phase ships, add its capability here with `shipped: true` and the evidence. Until then, the
 * page physically cannot claim it — which is the entire point, and it will be inconvenient exactly
 * once, in the week somebody wants to announce something early.
 */

export type Capability = {
  id: string;
  /** The claim, in the present tense, as the page may state it. */
  claim: string;
  /** The owning phase. A claim with no owner is a claim with no one to keep it true. */
  phase: string;
  /** Shipped means the capability is in the product today — not designed, not planned, not in review. */
  shipped: boolean;
  /** Where to check. A claim nobody can verify is a claim nobody can correct. */
  evidence: string;
  /** The boundary: what this capability deliberately does NOT do. Rendered beside the claim. */
  boundary: string;
};

export const CAPABILITIES: Capability[] = [
  {
    id: "discover",
    claim: "Finds every LLM call site in a repository and builds a workflow graph from it",
    phase: "P1 — Discovery",
    shipped: true,
    evidence: "internal/discovery, cmd/discover; the graph read model is internal/patternclassifier.GraphView",
    boundary:
      "It reads source statically. Edges that only appear at runtime arrive with tracing, and the graph says which is which rather than implying it knows both.",
  },
  {
    id: "classify",
    claim: "Names the agentic patterns a workflow implements, and says why it could not name the rest",
    phase: "P3.5 — Pattern classifier",
    shipped: true,
    evidence: "internal/patternclassifier; the unclassified regions are a first-class part of the read model",
    boundary:
      "An unlabelled region means no structural signature matched and no model returned anything in the taxonomy. It does not mean the workflow implements no patterns, and the product says so on the screen.",
  },
  {
    id: "variant",
    claim: "Changes one call site at a time and produces a reviewable diff before anything runs",
    phase: "P2 — Config and runtime",
    shipped: true,
    evidence: "internal/api/p2.go, internal/transform, internal/worktree; ADR-001 makes the pull request the output",
    boundary:
      "A change that only parses is marked syntax-checked and is reviewed by a human at every automation level. A change that does not build is never run.",
  },
  {
    id: "sandbox",
    claim: "Runs a variant sandboxed and records every node's input, output and cost",
    phase: "P2 / P2.5 — Runtime and telemetry",
    shipped: true,
    evidence: "internal/sandbox, internal/executor, internal/telemetry.RunMonitor",
    boundary:
      "A node whose output violates its typed I/O contract halts the run rather than passing the value downstream. That is a stop, and the product reports it as one.",
  },
  {
    id: "evaluate",
    claim: "Scores variants over multiple seeds and reports every score with its confidence interval",
    phase: "P4 — Eval harness",
    shipped: true,
    evidence: "internal/evalstats, internal/scoring, internal/evalboard/view.go",
    boundary:
      "When two intervals overlap the result is a tie, and the product renders it as a tie. It will not tell you which is better when the measurement cannot say.",
  },
  {
    id: "gates",
    claim: "Refuses a variant that fails a gate, instead of trading the failure against a good score",
    phase: "P4 — Eval harness",
    shipped: true,
    evidence: "internal/scoring gate sets; disqualified variants are a separate list in the read model",
    boundary:
      "A disqualified variant is excluded from the ranked order — not ranked last. The two are different claims and the board keeps them apart.",
  },
  {
    id: "coverage",
    claim: "Reports what the eval set does not cover, and keeps the gap in the denominator",
    phase: "P4 — Eval harness",
    shipped: true,
    evidence: "internal/evalgen CoverageReport; the residual obligations are a first-class field",
    boundary:
      "Dropping an uncovered obligation would raise the percentage by deleting the evidence of failure. The product does not do that, so a coverage figure here is lower than one that would.",
  },
  {
    id: "judge-calibration",
    claim: "Reports a model judge's agreement with human labels wherever its metric appears",
    phase: "P4 — Eval harness",
    shipped: true,
    evidence: "internal/evalharness.JudgeStanding; the console flags an uncalibrated judge on every metric it produced",
    boundary:
      "An uncalibrated judge's number is shown, and shown as an opinion. The product does not silently exclude it, and it does not silently trust it.",
  },
  {
    id: "attribution",
    claim: "Explains which node a failure came from, and separates what it believes from what it measured",
    phase: "P4.5 — Attribution and diagnosis",
    shipped: true,
    evidence: "internal/attribution, internal/scorecard, internal/api/p45.go",
    boundary:
      "A diagnosis is a hypothesis with a confidence. An ablation is a measurement with an interval. The scorecard never presents the first as the second, and it applies no change.",
  },
  {
    id: "proposals",
    claim: "Proposes a change only when a held-out run has verified it helps",
    phase: "P5.5 — Proposals and verification",
    shipped: true,
    evidence: "internal/proposal, internal/verification, internal/api/p55.go; withheld proposals are a separate list",
    boundary:
      "A proposal that fails its gate is withheld and labelled, not quietly dropped and not shown as a recommendation.",
  },
  {
    id: "pull-request",
    claim: "Delivers a change as a pull request a person reviews and merges",
    phase: "P12 — Forge delivery",
    shipped: true,
    evidence:
      "internal/forgedelivery, internal/deliveryrecord, internal/api/p12.go; handleP55OpenPR refuses unless the verdict passed and the workflow is at Assisted automation",
    boundary:
      "The platform opens the pull request. It does not merge it. Autonomous merging exists as a governed, budgeted, audited option — it is not what happens by default.",
  },
  {
    id: "ci-mediated",
    claim: "Opens that pull request from your own CI, so the platform never needs write access to your repository",
    phase: "P12 — Forge delivery",
    shipped: true,
    evidence:
      "internal/forgedelivery: CI-mediated delivery is the default path; the delivery record is reconciled from the forge, not from a token the platform holds",
    boundary:
      "A hosted Git App is the opt-in alternative for teams that would rather the platform open the PR. It is off by default, and nothing here holds a repository token until you turn it on.",
  },
  {
    id: "console",
    claim: "Puts all of it in a browser without an API key ever reaching it",
    phase: "P9 — Web console",
    shipped: true,
    evidence: "web/console; the credential is held server-side and scripts/scan-bundle.mjs fails the build if it reaches a shipped chunk",
    boundary:
      "The console renders what the platform computed. It derives no score, no ranking and no confidence of its own, so it cannot disagree with the system of record.",
  },
  {
    id: "deploy",
    claim: "Stands the whole platform up from one digest-pinned image set, on a single host or a Kubernetes cluster",
    phase: "P19 — Deployment and delivery",
    shipped: true,
    evidence:
      "deploy/ (Docker Compose + Kustomize overlays), deploy/images.env, internal/deploy; make deploy-lint fails the build if the two substrates diverge or an image is unpinned",
    boundary:
      "One deployment serves one tenant boundary. Isolation between customers is deployment-level, not shared multi-tenancy — the operator console's cross-tenant views govern one operator's own deployments, not a shared install.",
  },
  {
    id: "billing-evidence",
    claim: "Bills verified savings only against merged pull requests, with the evidence attached",
    phase: "P7 — Billing and metering",
    shipped: true,
    evidence: "internal/metering, internal/billing; gainshare lines carry their verified-delta refs and merge commits",
    boundary:
      "Gainshare requires recorded, revocable consent. Without it, it is not charged.",
  },
  // ── P34 · the axis split (ADR-014) ──────────────────────────────────────────────────────────
  {
    id: "axis-split",
    claim:
      "Separates what a node's control loop does from what that node is allowed to do, so a policy and a choice are reviewed apart",
    phase: "P34 — Harness / loop / graph",
    shipped: true,
    evidence:
      "variantspec.DimLoop and DimHarness; registry.KindLoop and registry.StrategyEnvelope; docs/adr/ADR-014",
    boundary:
      "It configures and verifies your graph; it does not orchestrate anything. Your workflow runs on your infrastructure, in code delivered to your repository as a reviewable diff.",
  },
  {
    id: "envelope-ceilings",
    claim:
      "Refuses a configuration whose loop asks for more turns or more spend than its execution envelope allows, naming both numbers",
    phase: "P34 — Harness / loop / graph",
    shipped: true,
    evidence:
      "variantspec.checkTurnCeiling / checkHostServices, refused at resolve; sandbox.SandboxConcurrencyCeiling enforces the concurrency limit a second time at execution",
    boundary:
      "The ceiling is checked before anything is generated and again where it executes. The sandbox that enforces it at run time is yours; what this platform guarantees is that a configuration exceeding your declared envelope never becomes a change you can apply.",
  },
  // ── P35 · the improvement run ───────────────────────────────────────────────────────────────
  {
    id: "improvement-run",
    claim:
      "Turns a question into a bounded plan — what it will look at, what it may spend, and when it stops — and shows you the plan before anything runs",
    phase: "P35 — The improvement run",
    shipped: true,
    evidence:
      "improvementrun.Translate and Plan; POST /api/v1/improvement-plans spends nothing and is a separate call from the run; a question that cannot be bounded is refused with one of six named causes",
    boundary:
      "A question that cannot be turned into a plan with a budget and a stopping condition is refused rather than run with bounds nobody chose. On a hosted deployment today the run produces a plan and cannot execute one: the verification gate runs your eval harness, which by design runs on your machine.",
  },
  {
    id: "verified-proposal",
    claim:
      "Offers each change on its own, with the delta it measured on held-out data, its confidence interval, and the number of cases behind it",
    phase: "P35 — The improvement run",
    shipped: true,
    evidence:
      "improvementrun.NewVerifiedProposal refuses a candidate the P5.5 gate did not pass and one whose delta names no seed or case count; a high scorer that fails a gate is not offered, however high its score",
    boundary:
      "Each change is approved individually. There is no control that accepts several at once, and that is deliberate: a bundle approval is one click that means several things, and the first item is the one that gets read.",
  },
  {
    id: "withdrawn-after-approval",
    claim:
      "Measures an approved change a second time after applying it, and withdraws it before it reaches your repository when the second measurement disagrees — showing you both numbers",
    phase: "P35 — The improvement run",
    shipped: true,
    evidence:
      "improvementrun.Reconcile; the comparison is evalstats.Interval.Overlaps, the platform's own tie predicate, and a provider that moved between the two measurements is reported as such rather than blamed on the change",
    boundary:
      "This reads like a failure and is the product working. A check that can only confirm is theatre; the second measurement is allowed to disagree, and when it does you get both numbers rather than a verdict.",
  },
  {
    id: "console-write-credential",
    claim:
      "Opens the pull request itself on the console path, using a credential scoped to that one repository",
    phase: "P35 — The improvement run",
    shipped: true,
    evidence:
      "forgedelivery.DefaultModeFor (program ruling R3, amending ADR-005 for one surface); the installation names the repositories it covers and org-wide is not expressible; revocation stops pushes on the next call rather than at the next token refresh",
    boundary:
      "It is per-repository, least-privilege, revocable by you at any time and effective immediately, used only after a person approved a specific change, and it is a separate grant from the one that reads your source — revoking either leaves the other intact. On the CLI and CI paths the platform holds no forge credential at all.",
  },
  // 🚫 THERE IS DELIBERATELY NO ENTRY CLAIMING THE PLATFORM MERGES.
  //
  // P35 opens pull requests. Auto-merge is P6's Autonomous level and is Enterprise-only, and below it
  // the platform never merges whatever the forge would permit. A `merges-your-changes` id would be a
  // sentence this build cannot honour on any plan a console customer is on — and the public surface is
  // exactly where that sentence would be quoted back.
  //
  // 🚫 AND NONE CLAIMING SCHEDULED OR UNATTENDED RUNS. There is no scheduler in this phase, and the
  // decision recorded for the one that arrives is that a scheduled run stops at PROPOSALS. Listing it
  // here would let the page describe the roadmap in the present tense, which is the drift this whole
  // manifest exists to catch.

  // 🚫 THERE IS DELIBERATELY NO ENTRY FOR TOPOLOGY MATERIALIZATION.
  //
  // P34 ships concurrency, conditional routing and merge as things a spec can DECLARE — resolved,
  // hashed, validated against the customer's typed I/O contracts, comparable, proposable. No language
  // has a rewriter that writes any of them into source, so the public surface may not say the platform
  // applies them, and `scan-claims.mjs` is what makes that structural rather than a matter of somebody
  // remembering during a launch week.
  //
  // The day a topology rewriter lands, this is where its claim goes — with its boundary beside it.
];

/** shipped returns the manifest entries the public surface may claim. */
export function shipped(): Capability[] {
  return CAPABILITIES.filter((capability) => capability.shipped);
}

/** capability looks an entry up by id. Throws rather than rendering nothing, so a typo is loud. */
export function capability(id: string): Capability {
  const found = CAPABILITIES.find((entry) => entry.id === id);
  if (!found) {
    throw new Error(`no capability "${id}" in the manifest — the public surface may not claim it`);
  }
  if (!found.shipped) {
    throw new Error(`capability "${id}" is in the manifest but is not shipped — the public surface may not claim it`);
  }
  return found;
}
