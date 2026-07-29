import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Row, Banner } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { AxisApplied, AxisRefusal } from "@/components/axisRefusal";
import { WiringBoundaries } from "./boundaries";
import { WiringEditor } from "./editor";

/**
 * The node-wiring surface (P15, updated for wave 15c).
 *
 * # Why this surface exists inside the console rather than only in a document
 *
 * The wiring axis has TWO outcomes and they look nothing alike. One shape — an exchange of two
 * adjacent, independent statements — is applied, and produces a diff a reviewer reads like any other.
 * Everything else is declined by name, because materialising it would mean moving, fusing, or deleting
 * a call, and applying such a change as a no-op would let a `config_hash` that already records the
 * rearranged graph be scored against code that was never rearranged.
 *
 * A user meeting either outcome for the first time on the configure screen has one question, and it is
 * not a question a release note answers: *is this thing broken, or is this the answer?* This surface
 * exists to answer it in both directions — by showing the applied case WITH its diff, so "declined" is
 * legible as a boundary rather than as a failure.
 *
 * So the axis gets a surface that says what it proposes, what it declines, and why declining is the
 * honest half — with the DECLINED-CHANGE CARD the configure screen actually renders, not a picture of
 * one. The card is the shared `AxisRefusal` component, so what is read here is what ships there.
 *
 * # What is live and what is a worked example
 *
 * Nothing on this page is this tenant's data, and it says so where a reader will see it. The four
 * refusal panels are worked examples carrying the transform engine's own sentences verbatim
 * (`internal/transform/rewrite.go`, `refuseWiring`) — a paraphrase would be checking a page that does
 * not exist. The tenant's real declined changes appear where they happen: on Configure, at submit.
 *
 * # Why tabs
 *
 * Viewport-first (NFR17): five sections stacked would put four of them below the fold, and the four
 * that matter are the ones a reader compares against each other. One tablist, no nesting.
 */
export const dynamic = "force-dynamic";

type Example = {
  id: string;
  label: string;
  axis: string;
  nodeId: string;
  submitted: string;
  before: string;
  after: string;
  message: string;
};

/**
 * The four shapes a declined change takes. Three are the wiring axis's own operators; the fourth is
 * the fallback, which is here because an axis the console cannot annotate must still render — a
 * refusal swallowed for being unrecognised is the worst of both.
 */
const EXAMPLES: Example[] = [
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
    label: "Another axis",
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

// APPLIED_DIFF is the real artifact — the unified diff the engine produced for a transposition of two
// adjacent statements in the Go fixture (`internal/transform/testdata/target/wiring.go.txt`), copied
// verbatim. A hand-written illustration would drift from what ships the first time the emitter changed,
// and this page's whole claim is "what you read here is what the engine emits".
const APPLIED_DIFF = `--- a/wiring.go
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
`;

const OPERATORS = [
  {
    name: "Merge",
    what: "Fuses an adjacent pair into one call: one survives, the other's edges are rewired through it.",
    bound: "Adjacent pairs only, never across a gap and never three at once.",
  },
  {
    name: "Reorder",
    what: "Exchanges two neighbours that exchange no data.",
    bound: "A data dependency is a fact about the code, never an ordering choice.",
  },
  {
    name: "Parallelize",
    what: "Drops a sequencing edge between two nodes that share no data, so they may run at once.",
    bound: "Expressed as the absence of an edge — there is no separate flag to disagree with it.",
  },
  {
    name: "Prune",
    what: "Removes a node nothing downstream reads and rewires its neighbours to each other.",
    bound: "Proposed, never applied on the strength of looking redundant.",
  },
];

function AppliedTab() {
  return (
    <div className="flex flex-col gap-6">
      {/*
        🔴 P15 20.14 — BOTH boundaries, stated before a reader reaches for a node. They are two different
        facts with two different next steps, and an editor that accepts the drag and refuses afterwards
        manufactures rearrangements that can never ship.
      */}
      <WiringBoundaries />
      <Section title="What was submitted">
        <p className="text-sm leading-relaxed text-muted-foreground">
          Two calls that sit next to each other in one function, share no name, and could legitimately
          run in either order. The proposal engine asked for the other order; the transform engine could
          materialise it, because exchanging two adjacent statements is a move whose result it can prove
          harmless.
        </p>
        <dl className="flex flex-col gap-2">
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-24">the source wires</dt>
            <dd className="mono break-words text-sm text-muted-foreground">first → second</dd>
          </div>
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-24">the spec asks for</dt>
            <dd className="mono break-words text-sm text-foreground">second → first</dd>
          </div>
        </dl>
      </Section>
      <Section title="What the console shows">
        <AxisApplied
          axis="wiring"
          nodeId="twoCalls"
          filename="wiring.go"
          what="The two statements were exchanged where they stand. Nothing was moved into or out of a block, no binding was rewritten, and no call was fused or deleted."
          invariant={
            <>
              <strong>The file&apos;s lines were reordered and nothing else changed.</strong> Same line
              count, same lines — so this change cannot have altered what any line says, only when it
              runs. That is checked before the diff is offered, not asserted here.
            </>
          }
          diff={APPLIED_DIFF}
        />
      </Section>
    </div>
  );
}

function AxisTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What the wiring axis proposes">
        <ul className="flex list-none flex-col gap-3 p-0">
          {OPERATORS.map((op) => (
            <li key={op.name} className="flex flex-col gap-1 border-t border-border/50 pt-3 first:border-t-0 first:pt-0">
              <Row>
                <Chip tone="info">{op.name}</Chip>
                <span className="text-sm text-foreground">{op.what}</span>
              </Row>
              <p className="hint">{op.bound}</p>
            </li>
          ))}
        </ul>
        <p className="hint">
          Each is a <strong>proposal</strong>. A change that removes a call is not a win because it
          removed a call — a candidate reaches you only after it scores better or cheaper on held-out
          cases, and one that reads redundant but scores worse is withheld with its reason.
        </p>
      </Section>

      <Section title="What is delivered, and what is not">
        <Banner tone="info" title="One shape is applied; the rest is declined by name">
          <p>
            A rearranged graph is a real, different configuration: node order and edges are part of a
            configuration&apos;s identity, so a reorder, a merge, or a prune produces its own hash and is
            scored like any other change.
          </p>
          <p>
            <strong>Applied:</strong> an exchange of two <em>adjacent, independent</em> statements — the
            one move whose result can be proven harmless, because the file that comes out has the same
            lines as the file that went in, reordered.
          </p>
          <p>
            <strong>Declined by name:</strong> a merge, a prune, a rewired edge, a move across other
            code, and any language whose statements this platform does not parse. Those need a rewriter
            that moves, fuses, or deletes a call, and applying one as a no-op would let a configuration
            claiming a different graph be scored against code that still has the old one — a number that
            means nothing and looks exactly like one that does.
          </p>
        </Banner>
      </Section>
    </div>
  );
}

function ExampleTab({ example }: { example: Example }) {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What was submitted">
        <p className="text-sm leading-relaxed text-muted-foreground">{example.submitted}</p>
        {/* Two labelled lines rather than two chips: a chip does not wrap, and these graphs are long
            enough to run off a 375px viewport — where the app shell clips them, so the end of the graph
            (the part that CHANGED) would be the part nobody could read. */}
        <dl className="flex flex-col gap-2">
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-24">the source wires</dt>
            <dd className="mono break-words text-sm text-muted-foreground">{example.before}</dd>
          </div>
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-24">the spec asks for</dt>
            <dd className="mono break-words text-sm text-foreground">{example.after}</dd>
          </div>
        </dl>
      </Section>
      <Section title="What the console shows">
        <AxisRefusal axis={example.axis} nodeId={example.nodeId} message={example.message} />
      </Section>
    </div>
  );
}

export default async function WiringPage() {
  await requireSession();
  const tabs: TabItem[] = [
    { id: "axis", label: "The axis", content: <AxisTab /> },
    // 🔴 The EDITOR tab (P15 15d). It comes second, right after the axis: a reader who is going to
    // rearrange a graph should meet the verdicts before the worked examples of what gets declined.
    { id: "editor", label: "Rearrange the graph", content: <WiringEditor /> },
    // The APPLIED case comes first among the outcomes, deliberately: a reader who opens this surface
    // and sees four refusals in a row learns the wrong thing about the axis.
    { id: "applied", label: "An applied reorder", content: <AppliedTab /> },
    ...EXAMPLES.map((e) => ({ id: e.id, label: e.label, content: <ExampleTab example={e} /> })),
  ];
  return (
    <PageFrame
      eyebrow="Wiring"
      title="Node wiring"
      lede="How this platform changes the SHAPE of a workflow — which calls run, in what order, and which of them are one call. One shape is applied to your source; everything else is declined by name, and both are shown here."
      wide
    >
      <p className="hint">
        The outcome panels are worked examples — the applied one carries the engine&apos;s real diff, the
        declined ones its real wording. None of it is this tenant&apos;s data; your own changes appear
        where they happen: on Configure, at submit.
      </p>
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
