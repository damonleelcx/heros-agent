/**
 * surfaces.ts is the ONE map from a capability to the place in the console that exercises it.
 *
 * Both the navigation and the command palette read it, so the two can never disagree about what an
 * operator can reach — and both filter it through the SAME permission map the backend enforces
 * (FR22, FR32). A capability the role does not grant is absent from the nav and absent from the
 * palette; it is never offered and then refused.
 *
 * # Why `danger` is here and not decided by the component
 *
 * The console does not form its own opinion about blast radius — the backend classifies every
 * capability (`read_only`, `irreversible`, `typed_target`) and the console renders that classification.
 * What this file adds is only WHERE the confirmation lives, so the palette can navigate to it.
 *
 * # Why every entry is a destination, never a command
 *
 * A palette entry is a link. Selecting "Arm the global kill switch" opens that control's confirmation
 * — with its reason field and its scope acknowledgement empty and intact — and performs nothing
 * (FR37). Velocity belongs to the read path; the write path keeps every deliberate step it had.
 */

/** Surface is one destination in the console. */
export type Surface = {
  /** The capability that must be granted for this destination to appear. */
  capability: string;
  href: string;
  label: string;
  /** hint is the one-line description shown beneath the label in the palette. */
  hint: string;
  /** nav marks the destinations that appear in the primary navigation. */
  nav?: boolean;
  /** danger marks a destination whose target is a destructive control, so the palette can say so. */
  danger?: boolean;
};

/**
 * SURFACES is ordered as the navigation is ordered: the operating picture first, then the subjects an
 * operator works on, then the controls, then the record.
 */
export const SURFACES: Surface[] = [
  {
    capability: "tenant.read",
    href: "/",
    label: "Overview",
    hint: "The live operating picture — what is halted, what is wrong",
    nav: true,
  },
  {
    capability: "tenant.read",
    href: "/tenants",
    label: "Tenants",
    hint: "Search and view tenants, their plan and their merge state",
    nav: true,
  },
  {
    capability: "billing.read",
    href: "/billing",
    label: "Billing",
    hint: "Invoices, dunning, reconciliation and gainshare evidence",
    nav: true,
  },
  {
    capability: "registry.admin",
    href: "/registry",
    label: "Model Registry",
    hint: "Models and the price references used to derive SUM",
    nav: true,
  },
  {
    capability: "job.read",
    href: "/fleet",
    label: "Jobs & Fleet",
    hint: "The P4/P6 queue and worker-fleet health",
    nav: true,
  },
  {
    capability: "killswitch.operate",
    href: "/killswitch",
    label: "Kill Switch",
    hint: "The platform brake on autonomous merges",
    nav: true,
  },
  {
    capability: "delivery.read",
    href: "/delivery",
    label: "Delivery",
    hint: "P12 deliveries, their OBSERVED merge state, and the change-delivery rollout picture",
    nav: true,
  },
  {
    capability: "release.read",
    href: "/releases",
    label: "Releases",
    hint: "Install channels, artefact verification, post-publish smoke and signing-key state",
    nav: true,
  },
  {
    capability: "axis.read",
    href: "/axes",
    label: "Axes",
    hint: "Per-axis status, refusals by typed cause, and the coverage matrix from the one source",
    nav: true,
  },
  {
    capability: "crosstenant.read",
    href: "/crosstenant",
    label: "Cross-Tenant",
    hint: "Fleet-wide aggregates — every view is logged",
    nav: true,
  },
  {
    capability: "audit.read",
    href: "/audit",
    label: "Audit Log",
    hint: "Append-only, hash-chained record of every action and every merge",
    nav: true,
  },
  {
    capability: "audit.read",
    href: "/oversight",
    label: "Oversight",
    hint: "Operator sessions and their factor, legal acceptance, and whether reporting is working",
    nav: true,
  },
  {
    capability: "gdpr.execute",
    href: "/compliance",
    label: "Compliance",
    hint: "Data-subject erasure: actionable and verifiable",
    nav: true,
  },

  // ── Actions. These are NOT in the nav: they are destinations inside a surface, reachable by
  // name from the palette so an operator does not have to remember which page holds which control.
  // Each one opens a confirmation. None of them performs anything (FR37).
  {
    capability: "killswitch.operate",
    href: "/killswitch#global-kill-switch",
    label: "Arm the global kill switch",
    hint: "Halts EVERY tenant's autonomous merges — opens the fleet-wide confirmation",
    danger: true,
  },
  {
    capability: "killswitch.operate",
    href: "/killswitch#per-tenant-kill-switch",
    label: "Halt one tenant's merges",
    hint: "Per-tenant kill switch — opens its confirmation",
    danger: true,
  },
  {
    capability: "job.cancel",
    href: "/fleet#jobs",
    label: "Cancel a job",
    hint: "Find the run on the queue and open its cancel confirmation",
    danger: true,
  },
  {
    capability: "registry.admin",
    href: "/registry#add-model",
    label: "Add a model",
    hint: "Register a model with its provider and price reference",
  },
  {
    capability: "billing.correct",
    href: "/billing",
    label: "Issue a credit or refund",
    hint: "Additive, audited correction against an invoiced event",
  },
  {
    capability: "entitlement.override",
    href: "/tenants",
    label: "Override a tenant's plan",
    hint: "Plans-as-config — takes effect with no deploy",
  },
  {
    capability: "tenant.suspend",
    href: "/tenants",
    label: "Suspend or reactivate a tenant",
    hint: "Suspension halts that tenant's autonomous merges",
    danger: true,
  },
  {
    capability: "impersonate.read",
    href: "/tenants",
    label: "Start an impersonation session",
    hint: "Reason-required, read-scoped, time-bounded, fully audited",
  },
  {
    capability: "gdpr.execute",
    href: "/compliance#erasure",
    label: "Execute a data-subject erasure",
    hint: "Irreversible — opens the typed-target confirmation",
    danger: true,
  },
];

/** navFor returns the primary-navigation destinations this operator's role grants. */
export function navFor(capabilities: string[]): Surface[] {
  return SURFACES.filter((s) => s.nav && capabilities.includes(s.capability));
}

/** commandsFor returns every destination this operator's role grants, for the palette. */
export function commandsFor(capabilities: string[]): Surface[] {
  return SURFACES.filter((s) => capabilities.includes(s.capability));
}
