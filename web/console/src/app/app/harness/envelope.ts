/**
 * envelope.ts holds the two numbers and the boundary the harness SURFACE renders (P34; trimmed by P37).
 *
 * # 🔴 What used to be here, and the fence that was promised and never written
 *
 * `ENVELOPE_FIELDS` mirrored `registry.EnvelopeHarness{}.ParamsSchema()` — nine fields, each with what
 * it bounds and where it is enforced — and this comment claimed:
 *
 *   *"`tests/envelope.test.mjs` reads the engine's own Go source and asserts this file agrees with it,
 *   field for field and required-flag for required-flag. That test is the gate, and it is the reason
 *   this comment is not just a promise."*
 *
 * **That test did not exist.** The mirror was ungated from the day it shipped, and nothing went red for
 * the whole time — which is exactly the silent failure the paragraph described, arriving through the
 * paragraph itself.
 *
 * P37 moved the table to `/docs/concepts/execution-envelope` (block-inventory H3) and WROTE the gate:
 * `tests/envelope.test.mjs` now asserts the DOCUMENT against the Go schema, field for field and
 * required-flag for required-flag. The transcription moved and its fence moved with it.
 *
 * What remains here is what the surface itself renders: the two ceilings it quotes in prose, and the
 * boundary — which is a fact about the BUILD, unchanged by any entitlement, plan or role.
 */

/** The registry's own upper bound on any turn ceiling — `registry.MaxTurnsCeiling`. */
export const TURN_CEILING_MAX = 16;

/** The sandbox's own concurrency ceiling — `sandbox.SandboxConcurrencyCeiling`. */
export const SANDBOX_CONCURRENCY_CEILING = 8;

/** The closed set of sandbox postures — `registry.SandboxPostures()`. */
export const SANDBOX_POSTURES = ["no-network", "provider-egress-only", "unrestricted-egress"] as const;

/** The closed set of host services an envelope may grant — `registry.HostServiceNames()`. */
export const HOST_SERVICES = ["critic", "planner", "tool-executor"] as const;

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
