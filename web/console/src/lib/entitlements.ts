/**
 * entitlements.ts maps each CONSOLE capability to the plan that unlocks it (§10.1, FR15).
 *
 * # 🔴 Two rules, and the second is the one that decays
 *
 * **Plans by name only. No price value, ever.** Prices live in the platform's plan configuration and
 * are read at billing time. A price committed to a repository outlives the moment it was true, and it
 * ships — `npm run scan:bundle` fails the build over one.
 *
 * **This table does not decide anything.** It is a rendering hint: it says which plan a reader should
 * be told about when the platform has NOT granted a capability. Whether the tenant holds it is read
 * from the P7 entitlement rows the platform itself enforces with, and the console asks that question
 * rather than answering it. If this file ever starts deciding availability, the screen and the gate
 * become two sources of truth — and the day they disagree, the console has told a customer they have
 * something they will then be refused.
 *
 * # Why a gated capability is SHOWN rather than hidden
 *
 * A hidden feature reads as a missing feature. The reader concludes the product does not do it, or
 * that it is broken, and either way the next step is a support ticket. A capability shown as gated,
 * with its plan named, produces an upgrade conversation instead — and, more importantly, tells the
 * truth about what the product does.
 *
 * The `feature` key is the platform's own feature identifier, so a row here resolves against the
 * entitlement rows without a translation table in between. `null` means "no entitlement gates this" —
 * every plan has it, including Free.
 */

export type ConsoleCapability = {
  id: string;
  label: string;
  description: string;
  /** The platform feature this maps to, or null when nothing gates it. */
  feature: string | null;
  /** The plan a reader should be told about when it is not held. Named, never priced. */
  unlockedBy: "Free" | "Team" | "Business" | "Enterprise";
  /** Where an automation level is also required, the level. */
  automationLevel?: "advisory" | "assisted" | "autonomous";
};

export const CAPABILITIES: ConsoleCapability[] = [
  {
    id: "graph",
    label: "See a workflow's classified graph",
    description: "Nodes, edges, and the patterns the classifier could and could not name — with why.",
    feature: null,
    unlockedBy: "Free",
  },
  {
    id: "run",
    label: "Inspect and watch a run",
    description: "Per-node input and output, streaming live while it executes.",
    feature: null,
    unlockedBy: "Free",
  },
  {
    id: "configure",
    label: "Configure a variant and run it",
    description: "Override one call site, review the diff it produced, and run it sandboxed.",
    feature: null,
    unlockedBy: "Free",
  },
  {
    // 🔴 §11b.5 — the studio is mapped `feature: null` on purpose, and the purpose is honesty.
    //
    // The platform (P10) enforces NO entitlement on the studio: `internal/api/p10.go` and
    // `internal/api/p10matrix.go` gate on authentication alone, and the P10 spec records no plan
    // boundary. Mapping it to `dashboard` or any other plan would make this table CLAIM a gate the
    // platform will not honour — and the day a Free tenant is told "Studio needs Team" and then served
    // the studio anyway is the day the screen and the gate disagree, which is the one failure this
    // whole file exists to prevent (see the header). So the mapping tells the truth: exploration in the
    // studio is ungated.
    //
    // The commercial line is not lost by this — it moves one step downstream and is already priced.
    // Everything here is EXPLORATORY: a bound cell is "in force, unverified", never "proven best". The
    // gated act is SHIPPING a verified change, which is the `open-pr` / `proposals` path below
    // (Business, at assisted automation). If P10 ever introduces its own entitlement, this row maps to
    // that feature — not before, because a gate that isn't enforced isn't a gate.
    id: "studio",
    label: "Explore prompts and models in the Studio",
    description:
      "Browse and diff prompt versions, publish a new immutable version, and try a model per node on the matrix. Every result is exploratory — a bound cell is in force, not proven; shipping a verified change is the gated open-PR path below.",
    feature: null,
    unlockedBy: "Free",
  },
  {
    id: "dashboard",
    label: "The console itself",
    description: "A browser surface over the platform, with a session instead of an API key.",
    feature: "dashboard",
    unlockedBy: "Team",
  },
  {
    id: "board",
    label: "Compare variants on an eval board",
    description: "Scores with confidence intervals, gate outcomes, the cost/quality frontier and coverage.",
    feature: "dashboard",
    unlockedBy: "Team",
  },
  {
    id: "proposals",
    label: "Review what the platform proposes",
    description: "Each proposal with its rationale, its verified delta, and the full diff a human merges.",
    feature: "assisted_pr",
    unlockedBy: "Business",
    automationLevel: "assisted",
  },
  {
    id: "open-pr",
    label: "Open a pull request from a verified proposal",
    description: "Puts a verified change in front of a reviewer in the repository. The platform never merges it.",
    feature: "assisted_pr",
    unlockedBy: "Business",
    automationLevel: "assisted",
  },
  {
    id: "auto-merge",
    label: "Let the optimizer merge autonomously",
    description: "Governed by budget and automation level, with every merge on the tamper-evident record.",
    feature: "auto_merge",
    unlockedBy: "Enterprise",
    automationLevel: "autonomous",
  },
];

/**
 * PLAN_ORDER is the ladder, for copy that needs to say "or higher".
 *
 * Names only. There is deliberately no price, no per-seat figure, and no percentage here.
 */
export const PLAN_ORDER = ["Free", "Team", "Business", "Enterprise"] as const;
