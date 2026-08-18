import Link from "next/link";

import { PageFrame, Section } from "@/components/primitives";
import { AxisApplied, AxisRefusal } from "@/components/axisRefusal";

/**
 * A self-contained preview of the P15 wiring outcome cards (task 9.4).
 *
 * # Why a preview page exists at all
 *
 * The declined-change card is the wiring axis's PRIMARY user-facing state: until a call-site rewriter
 * lands for every shape, most rearrangements end here. A state that common cannot be checked only by
 * asserting that a string appears in a file — the assertions in `inventory.test.mjs` (P15-2…P15-4) prove
 * the component says the right words, and say nothing about whether the card renders, wraps, or fits.
 * A green build is entirely compatible with a page that renders nothing.
 *
 * So the fixtures get a URL. It uses only the root layout — no session, no BFF — which is why it lives
 * outside `/app`: a surface that needs a live platform to look at is a surface nobody looks at.
 *
 * # Why these four shapes
 *
 * Three are the wiring axis's own operators (reorder, merge, rewired edge). The fourth is an axis this
 * console carries NO note for, and it is the load-bearing one: the failure it guards against is a
 * refusal being swallowed for being unrecognised, which would leave a reader with a submission that
 * simply vanished. Rendering it here is how that stays checkable.
 *
 * The applied case is included alongside them for the same reason the `/app/wiring` surface leads with
 * it: four refusals in a row read as a broken feature however carefully each one is worded.
 *
 * `?tab=` selects the fixture, so each state is directly linkable — a state reachable only by clicking
 * is a state that never reaches a PR description, a bug report, or a screenshot pipeline.
 *
 * # What is shared with the submit path
 *
 * `AxisRefusal` and `AxisApplied` are imported from `@/components/axisRefusal` — the same components
 * `configurator.tsx` renders on a dimension-named 400. This page seeds them; it does not reimplement
 * them. A copy would drift the first time the card changed, and then this page would be a picture of a
 * surface rather than the surface.
 */
export const dynamic = "force-dynamic";

type Fixture = {
  id: string;
  label: string;
  axis: string;
  nodeId: string;
  submitted: string;
  before: string;
  after: string;
  /** Present on the applied fixture only; a refusal carries no diff, by construction. */
  message?: string;
  diff?: string;
};

/**
 * The engine's real sentences, carried verbatim from `internal/transform/rewrite.go` (`refuseWiring`).
 * A paraphrase would be checking a page that does not exist.
 */
const FIXTURES: Fixture[] = [
  {
    id: "applied",
    label: "An applied reorder",
    axis: "wiring",
    nodeId: "twoCalls",
    submitted:
      "Two calls that sit next to each other in one function, share no name, and could legitimately run in either order. The transform engine could materialise this one, because exchanging two adjacent statements is a move whose result it can prove harmless.",
    before: "first → second",
    after: "second → first",
    diff: `--- a/wiring.go
+++ b/wiring.go
@@ -7,8 +7,8 @@
 // first is a measurable choice — exactly what the wiring axis proposes and what 15c can now apply.
 func twoCalls(client *anthropic.Client) {
 	prepare(client)
-	first := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeSonnet4})
+	second := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeOpus4_6})
-	second := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeOpus4_6})
+	first := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeSonnet4})
 	report(first, second)
 }
`,
  },
  {
    id: "reorder",
    label: "A declined reorder",
    axis: "wiring",
    nodeId: "summarize",
    submitted:
      "The same shape as the applied case — two nodes proposed in the other order — but these two calls are not adjacent statements in one block: something else sits between them. A transposition exchanges neighbours; anything else is a move, and a move is not something this engine can prove harmless.",
    before: "plan → draft → summarize",
    after: "plan → summarize → draft",
    message:
      'this spec asks for a wiring change (node order differs at position 1: the source runs "draft" there, the spec asks for "summarize" (source [plan draft summarize], spec [plan summarize draft])), but no call-site rewriter materializes a node rearrangement as source — moving, fusing, or deleting a call is control-flow surgery, not the value replacement this engine performs. It is REFUSED rather than applied as a no-op: a no-op would let this spec\'s config_hash, which already records the new graph, be scored against source that was never rewired — a false measurement, not a missing feature',
  },
  {
    id: "merge",
    label: "A merge",
    axis: "wiring",
    nodeId: "summarize",
    submitted:
      "An adjacent pair is fused into one call: one node survives and absorbs the other, whose edges are rewired through the survivor. Only an adjacent pair, and only two — a merge a reviewer cannot read in one screen is a merge nobody can check.",
    before: "plan → draft → summarize",
    after: "plan → draft   (summarize absorbed)",
    message:
      "this spec asks for a wiring change (the source wires 3 node(s) [plan draft summarize] but the spec orders 2 [plan draft]), but no call-site rewriter materializes a node rearrangement as source — moving, fusing, or deleting a call is control-flow surgery, not the value replacement this engine performs. It is REFUSED rather than applied as a no-op: a no-op would let this spec's config_hash, which already records the new graph, be scored against source that was never rewired — a false measurement, not a missing feature",
  },
  {
    id: "edge",
    label: "A rewired edge",
    axis: "wiring",
    nodeId: "plan",
    submitted:
      "Same nodes, same order, one edge added. A graph is its order AND its edges, so this is a rewire too — and it produces a different configuration with a different hash.",
    before: "plan → draft → summarize",
    after: "plan → draft → summarize,  plan → summarize",
    message:
      "this spec asks for a wiring change (the spec adds the edge plan -data-> summarize, which the source does not wire), but no call-site rewriter materializes a node rearrangement as source — moving, fusing, or deleting a call is control-flow surgery, not the value replacement this engine performs. It is REFUSED rather than applied as a no-op: a no-op would let this spec's config_hash, which already records the new graph, be scored against source that was never rewired — a false measurement, not a missing feature",
  },
  {
    id: "unknown",
    label: "An un-annotated axis",
    axis: "provenance",
    nodeId: "router",
    submitted:
      "A refusal on an axis this console carries no note for. It still renders, and the platform's own sentence carries it: swallowing an unrecognised refusal would leave the reader with a submission that vanished.",
    before: "—",
    after: "—",
    message:
      "this dimension has no call-site rewriter for the matched registry row, so the override is refused rather than dropped from the diff",
  },
];

function safeTab(tab: string | undefined): Fixture {
  return FIXTURES.find((f) => f.id === tab) ?? FIXTURES[0];
}

export default async function P15PreviewPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string }>;
}) {
  const chosen = safeTab((await searchParams).tab);
  return (
    <PageFrame
      eyebrow="Preview"
      title="Wiring outcome cards"
      lede="The P15 submit-path cards, seeded with representative fixtures. One shape is applied to your source and carries a diff; everything else is declined by name and carries the platform's own sentence instead."
      wide
    >
      {/*
        The fixture picker is LINKS, not a tablist: choosing WHICH fixture is navigation, and a URL is
        what makes a state shareable. There is no second tablist to nest inside, which is the other half
        of why this is not <Tabs>.
      */}
      <nav className="flex flex-wrap items-center gap-2" aria-label="Preview fixture">
        {FIXTURES.map((f) => (
          <Link
            key={f.id}
            className={
              f.id === chosen.id
                ? "rounded-lg border border-primary/40 bg-primary/10 px-3 py-1.5 font-mono text-xs text-primary"
                : "rounded-lg border border-border px-3 py-1.5 font-mono text-xs text-muted-foreground transition-colors hover:text-foreground"
            }
            href={`/preview/wiring?tab=${f.id}`}
          >
            {f.label}
          </Link>
        ))}
      </nav>

      <Section title="What was submitted">
        <p className="text-sm leading-relaxed text-muted-foreground">{chosen.submitted}</p>
        {/* Two labelled lines rather than two chips: a chip does not wrap, and these graphs run off a
            375px viewport — where the end of the graph is the part that CHANGED. */}
        <dl className="flex flex-col gap-2">
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-24">the source wires</dt>
            <dd className="mono break-words text-sm text-muted-foreground">{chosen.before}</dd>
          </div>
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-24">the spec asks for</dt>
            <dd className="mono break-words text-sm text-foreground">{chosen.after}</dd>
          </div>
        </dl>
      </Section>

      <Section title="What the console shows">
        {chosen.diff ? (
          <AxisApplied
            axis={chosen.axis}
            nodeId={chosen.nodeId}
            filename="wiring.go"
            what="The two statements were exchanged where they stand. Nothing was moved into or out of a block, no binding was rewritten, and no call was fused or deleted."
            invariant={
              <>
                <strong>The file&apos;s lines were reordered and nothing else changed.</strong> Same line
                count, same lines — so this change cannot have altered what any line says, only when it
                runs. That is checked before the diff is offered, not asserted here.
              </>
            }
            diff={chosen.diff}
          />
        ) : (
          <AxisRefusal axis={chosen.axis} nodeId={chosen.nodeId} message={chosen.message ?? ""} />
        )}
      </Section>

      <p className="hint">
        These are worked examples, not a tenant&apos;s data — the applied one carries the engine&apos;s
        real diff, the declined ones its real wording. Your own changes appear where they happen: on
        Configure, at submit.
      </p>
    </PageFrame>
  );
}
