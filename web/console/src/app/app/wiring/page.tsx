import { redirect } from "next/navigation";

/**
 * `/app/wiring` moved to `/app/graph` in P34 (decisions.md D-34.4).
 *
 * 🔴 It REDIRECTS rather than 404s, permanently, and that is a decision rather than politeness. A
 * bookmark that stops working is indistinguishable from a feature that was withdrawn — and the reader
 * most likely to have saved a link to this page is the one who saved it while trying to understand a
 * refusal, which is exactly the reader a dead link fails hardest.
 *
 * 🔴 The wiring AXIS is not gone. `transform.AxisCoverage()` still reports `wiring` — reorder, merge,
 * prune, parallelize over `Order` (P15) — as an axis distinct from `graph` (P34: concurrency,
 * conditional routing, fan-in merge). What changed is that both are the SHAPE of a workflow, so they
 * share one page instead of one of them being invisible. `/app/graph` labels both and gives each its
 * own coverage, because collapsing them into a single badge would claim the console can apply a
 * concurrency change on the strength of being able to apply a transposition.
 */
export default function WiringPage() {
  redirect("/app/graph");
}
