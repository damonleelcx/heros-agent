/**
 * envelope.ts mirrors the platform's execution-envelope contract
 * (`registry.EnvelopeHarness{}.ParamsSchema()`, `internal/registry/harness_envelope.go`) and the two
 * places each field is enforced.
 *
 * # Why a mirror and not a fetch
 *
 * The envelope's SHAPE is a property of the build, not of a tenant — no entitlement, plan or role can
 * change which fields exist or which are required. Mirroring it here lets the surface render its full
 * explanation without a live platform, which is what the browser-checkable preview needs. The VALUES a
 * tenant has chosen are a different thing entirely and are not here.
 *
 * 🔴 A mirror with no gate is a second source of truth, and its failure is silent: the page keeps
 * rendering, confidently, a contract that has moved. `tests/envelope.test.mjs` reads the engine's own
 * Go source and asserts this file agrees with it, field for field and required-flag for required-flag.
 * That test is the gate, and it is the reason this comment is not just a promise.
 */

export type EnvelopeField = {
  /** The wire name in the sealed params. Stable forever; a rename orphans stored hashes. */
  name: string;
  /** The human layer, free to be reworded because it is never hashed. */
  label: string;
  /**
   * required is whether the registry REFUSES an envelope that omits it.
   *
   * 🔴 The three required fields are each a blast-radius statement, and the honest default for one of
   * those is that there isn't one: an omitted ceiling reads as "unbounded" to a person and has to be
   * read as SOME number by the code, and those two readings differing is how a policy stops being one.
   */
  required: boolean;
  /** What this field bounds, in the terms an operator setting it thinks in. */
  bounds: string;
  /**
   * enforcedAt names WHERE the value bites. It is a first-class field rather than prose because the
   * answer differs per field, and a reader who assumes "refuses at resolve" for all of them will
   * believe the concurrency limit can be bypassed by not resolving — which is exactly the assumption
   * the sandbox's own limit exists to defeat.
   */
  enforcedAt: string;
};

/** The registry's own upper bound on any turn ceiling — `registry.MaxTurnsCeiling`. */
export const TURN_CEILING_MAX = 16;

/** The sandbox's own concurrency ceiling — `sandbox.SandboxConcurrencyCeiling`. */
export const SANDBOX_CONCURRENCY_CEILING = 8;

/** The closed set of sandbox postures — `registry.SandboxPostures()`. */
export const SANDBOX_POSTURES = ["no-network", "provider-egress-only", "unrestricted-egress"] as const;

/** The closed set of host services an envelope may grant — `registry.HostServiceNames()`. */
export const HOST_SERVICES = ["critic", "planner", "tool-executor"] as const;

export const ENVELOPE_FIELDS: EnvelopeField[] = [
  {
    name: "sandbox_posture",
    label: "Where it may reach",
    required: true,
    bounds: `One of ${SANDBOX_POSTURES.join(", ")}. There is no safe default for what a node is allowed to talk to, so the registry refuses an envelope that does not say.`,
    enforcedAt: "the sandbox, at execution",
  },
  {
    name: "turn_ceiling",
    label: "The most turns any loop may take",
    required: true,
    bounds: `1–${TURN_CEILING_MAX}. A policy, imposed. The loop chooses a value at or below it; a loop asking for more is refused when the configuration resolves, naming BOTH numbers.`,
    enforcedAt: "resolve — before any diff, worktree, build or provider call exists",
  },
  {
    name: "spend_ceiling_usd",
    label: "The most it may spend in one run",
    required: true,
    bounds:
      "Checked BEFORE each provider call, not after — checking afterwards enforces a ceiling by having already exceeded it. Exhaustion is a named stopping condition, not an error: the run produced a real, partial answer under a known configuration.",
    enforcedAt: "the runtime, before each call",
  },
  {
    name: "host_services",
    label: "Which second actors it grants",
    required: false,
    bounds: `Any of ${HOST_SERVICES.join(", ")}. A loop needing one the envelope does not grant is refused at resolve — naming the loop and the missing service — and is never degraded to a strategy that needs none.`,
    enforcedAt: "resolve",
  },
  {
    name: "concurrency_limit",
    label: "How many of its steps may overlap",
    required: false,
    bounds: `1–32. Concurrency multiplies a run's PEAK resource use by the group's width, which is what makes this a blast-radius bound rather than a scheduling hint.`,
    enforcedAt: `BOTH: refused at resolve, and capped by the sandbox at execution (at most ${SANDBOX_CONCURRENCY_CEILING} per group, whatever the spec says)`,
  },
  {
    name: "retry_budget",
    label: "How many failed turns may be retried",
    required: false,
    bounds:
      "0–8. It is here rather than on the loop because retries multiply turns, so an unbounded budget would defeat the turn ceiling from the side.",
    enforcedAt: "the runtime",
  },
  {
    name: "timeout_seconds",
    label: "The wall-clock bound on one run",
    required: false,
    bounds: "1–3600.",
    enforcedAt: "the sandbox, at execution",
  },
  {
    name: "guardrail_ref",
    label: "The guardrail it answers to",
    required: false,
    bounds: "A reference. Which guardrail applied is part of what makes a result reproducible.",
    enforcedAt: "the runtime",
  },
  {
    name: "approval_gate_ref",
    label: "The approval gate it answers to",
    required: false,
    bounds: "A reference.",
    enforcedAt: "the runtime",
  },
];

/**
 * THE BOUNDARY, mirrored from `transform.CoverageFor("harness")`.
 *
 * 🔴 The envelope refuses in EVERY language, permanently, and this is the one axis where that is not a
 * gap anybody will close. An envelope is a fact about how a node is DEPLOYED — none of it is written at
 * a call site in any language — so there is no rewriter that could ever emit one.
 *
 * 🚫 Refused is NOT unenforced, and the page says so in the same breath. A reader who took the badge to
 * mean "ignored" would draw exactly the wrong conclusion about their own blast radius.
 */
export const ENVELOPE_BOUNDARY = {
  materializesAnywhere: false,
  cause: "not-expressible-at-a-call-site",
  permanent: true,
  missingArtifact: "",
  reason:
    "An execution envelope is a property of how a node is deployed, so there is nothing to write into your source in any language. That is not the same as unenforced: it is checked when the configuration resolves, and again by the sandbox at execution.",
} as const;
