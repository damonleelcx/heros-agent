import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Row, Banner, DataTable } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { AxisApplied, AxisRefusal } from "@/components/axisRefusal";
import { WiringBoundaries } from "./boundaries";
import { WiringEditor } from "./editor";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { loadProjection } from "@/lib/projection";

/**
 * The GRAPH surface — the shape of a workflow (P15 wiring + P34 topology).
 *
 * # This page was `/app/wiring`, and it now carries TWO axes
 *
 * P34 split one axis into three and gave the third a home here. `/app/wiring` redirects; the wiring
 * axis did not go anywhere. Both axes are about the shape BETWEEN nodes, so they share a page — and
 * both are labelled, with their own coverage, because they are different capabilities:
 *
 *	wiring (P15)   reorder · merge · prune · parallelize, over the node ORDER. One shape applies.
 *	graph  (P34)   concurrent groups · conditional edges · fan-in merge. None applies yet, in any
 *	               language, and every cell says which of the frontend, the analysis or the language
 *	               support is missing.
 *
 * 🚫 They are NOT collapsed into one badge. Doing so would claim the console can apply a concurrency
 * change on the strength of being able to apply a transposition of two adjacent statements.
 *
 * ⚠️ One word means two things on this page, and the page says so rather than leaving a reader to
 * discover it: wiring's `Parallelize` DROPS a sequencing edge between nodes that share no data, while
 * P34's `concurrent group` DECLARES that nodes may overlap. And P15's `Merge` FUSES two nodes into one,
 * while P34's `merge` says how a fan-in's results COMBINE — opposite operations sharing a word.
 *
 * Every item from the old page has a destination in
 * `openspec/changes/p34-harness-loop-graph-split/frontend-inventory.md`; nothing was dropped.
 *
 * # The node-wiring surface (P15, updated for wave 15c)
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

/**
 * TOPOLOGY_FORMS mirrors `transform.GraphForms()` and the coverage the engine derives for each.
 *
 * 🔴 Every one of them REFUSES in every language today, and the page leads with that rather than
 * offering a control that cannot produce a diff (P34 §7.3). The three states are deliberately three:
 * what the form declares, what it is refused for, and — the one FR18 insists on — WHICH missing thing
 * would close it. "We do not support this", "your call site cannot carry this" and "nobody can carry
 * this in any language" are three different sentences to three different people.
 */
const TOPOLOGY_FORMS = [
  {
    form: "concurrent group",
    declares:
      "Two or more nodes may overlap. The ordering still lists every one of them, so a replay visits them in a defined sequence even when the live run overlapped them.",
    missing: "a topology rewriter for your language, or — where the frontend is syntactic — a typed analysis that emits edges at all",
  },
  {
    form: "conditional edge",
    declares:
      "An edge taken only when its predicate holds. The predicate is an expression binding and follows the same rule a prompt slot's does: it must name a value already in scope at the producing call site, validated when the configuration resolves, never inferred.",
    missing: "a topology rewriter for your language",
  },
  {
    form: "merge",
    declares:
      "How a fan-in's results combine, and what happens when one of its nodes fails. Both are required and neither has a default — first-result-wins, concatenate and last-writer are all decisions about YOUR program.",
    missing: "a topology rewriter for your language",
  },
];

function TopologyTab() {
  return (
    <div className="flex flex-col gap-6">
      {/*
        🔴 P34 §7.3 — an axis unavailable in this build renders read-only WITH ITS REASON, and the reason
        leads. A hidden axis is indistinguishable from one that does not exist, and a picker that cannot
        produce a diff is worse than either: it reads as a bug.
      */}
      <Section title="What the graph axis declares">
        <Banner tone="warn" title="You can declare all three today. None of them is written into your source yet — in any language.">
          <p>
            A topology declaration <strong>resolves</strong>, is <strong>validated against your typed
            I/O contracts</strong>, and is part of the configuration&apos;s hash. What does not exist is
            the codemod: no language has a rewriter that writes a concurrent group, a conditional edge
            or a merge into source.
          </p>
          <p>
            So it is <strong>refused by name</strong> rather than applied as a no-op. A no-op would let a
            hash that already records the new topology be scored against source that still runs the old
            one — a false measurement, not a missing feature. Your declaration is not dropped; the
            refusal names the axis, the node and the form.
          </p>
        </Banner>
        <DataTable
          caption="Each topology form, what it declares, and what would close the gap"
          columns={[
            { key: "form", label: "Form" },
            { key: "status", label: "At a call site" },
            { key: "declares", label: "What it declares" },
            { key: "missing", label: "What is missing" },
          ]}
        >
          <tbody>
            {TOPOLOGY_FORMS.map((f) => (
              <tr key={f.form}>
                <td className="mono text-sm align-top">{f.form}</td>
                <td className="align-top">
                  <Chip tone="halt" title="what this platform does with the form">
                    refused in every language
                  </Chip>
                </td>
                <td className="text-sm align-top text-muted-foreground">{f.declares}</td>
                <td className="text-sm text-muted-foreground">{f.missing}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </Section>

      <Section title="Two words that mean two things on this page">
        <Banner tone="info" title="Wiring and topology share vocabulary and do not share meaning">
          <p>
            <strong>Parallelize</strong> (wiring) <em>drops a sequencing edge</em> between two nodes that
            share no data, so nothing orders them any more. <strong>A concurrent group</strong> (graph){" "}
            <em>declares that nodes may overlap</em>, over an ordering that still lists both.
          </p>
          <p>
            <strong>Merge</strong> (wiring) <em>fuses two nodes into one</em> — the node set shrinks, and
            the claim is that one of the calls was unnecessary. <strong>A merge declaration</strong>{" "}
            (graph) says <em>how a fan-in&apos;s results combine</em> — every call still happens.
          </p>
          <p className="hint">
            They are opposite operations sharing a word. This page states it rather than leaving you to
            find out from a refusal.
          </p>
        </Banner>
      </Section>

      <Section title="Why a fan-in has to declare its merge">
        <Banner tone="warn" title="Refused when the spec is validated, never defaulted">
          <p>
            When two nodes converge on a third, something has to say how their results combine. The
            available defaults — first result wins, concatenate, last writer — are all semantic choices
            about <strong>your</strong> program, and none of them is more obviously right than the
            others. A default here is the platform deciding what your code means.
          </p>
          <p>
            The same applies to failure: <span className="mono">fail-fast</span> and{" "}
            <span className="mono">collect-partial</span> are different products, so{" "}
            <span className="mono">on_node_failure</span> is required too.
          </p>
          <p>
            🔴 And <span className="mono">collect-partial</span> carries a consequence that is checked
            rather than documented: the merge may deliver fewer inputs than the group has nodes, so a
            downstream contract that <em>requires</em> a field only one node produces is refused. Without
            that check, the mode would be a promise the type system does not keep.
          </p>
        </Banner>
      </Section>
    </div>
  );
}

export default async function GraphPage() {
  // 🔴 P29 §5.10 — `coverage × your nodes`, BESIDE the worked examples and never instead of them.
  // The examples carry the engine's own verbatim sentences and are what makes a refusal legible;
  // the projection is this organization's own rows, under its own heading and its own denominator.
  const projection = await loadProjection();
  await requireSession();
  const tabs: TabItem[] = [
    // 🔴 TOPOLOGY LEADS. It is the axis this page gained, it is the one with no materializer anywhere,
    // and §7.3 says an unavailable axis must render read-only with its reason rather than be discovered
    // through a refusal. Putting it behind the wiring tabs would hide the newest and least-covered
    // capability behind the oldest and best-covered one.
    { id: "topology", label: "Topology", content: <TopologyTab /> },
    { id: "axis", label: "The wiring axis", content: <AxisTab /> },
    // 🔴 The EDITOR tab (P15 15d). It comes second, right after the axis: a reader who is going to
    // rearrange a graph should meet the verdicts before the worked examples of what gets declined.
    { id: "editor", label: "Rearrange the graph", content: <WiringEditor /> },
    // The APPLIED case comes first among the outcomes, deliberately: a reader who opens this surface
    // and sees four refusals in a row learns the wrong thing about the axis.
    { id: "applied", label: "An applied reorder", content: <AppliedTab /> },
    ...EXAMPLES.map((e) => ({ id: e.id, label: e.label, content: <ExampleTab example={e} /> })),
    {
      id: "your-nodes",
      label: "Your nodes",
      // 🔴 P29 §5.10 — a TAB rather than a panel below the tab strip. `Tabs` is `flex-1` and
      // owns the remaining viewport (NFR17, viewport-first), so a sibling after it collides
      // with the tab bar — observed in the browser on this very page before it was moved here.
      // The worked examples keep their own tabs; this is added BESIDE them, never instead.
      content: <AxisProjectionPanel axis="wiring" outcome={projection} />,
    },
    {
      id: "your-nodes-graph",
      label: "Your nodes (topology)",
      // 🔴 A SECOND panel, not a merged one. `wiring` and `graph` are two axes with two coverage tables
      // and two denominators; one panel over both would report a single fraction that is true of
      // neither, and the reader would have no way to tell which capability the number was about.
      content: <AxisProjectionPanel axis="graph" outcome={projection} />,
    },
  ];
  return (
    <PageFrame
      eyebrow="Graph"
      title="The shape of a workflow"
      lede="Which calls run, in what order, which of them may overlap, which are taken only under a condition, and how results combine where they converge. One shape is applied to your source; everything else is declined by name, and both are shown here."
      wide
    >
      <p className="hint">
        Two axes live here. <strong>Wiring</strong> (reorder, merge, prune, parallelize over the
        ordering) applies one shape to your source and declines the rest by name.{" "}
        <strong>Topology</strong> (concurrent groups, conditional edges, fan-in merge) resolves, hashes
        and validates today and is written into source in no language yet — the first tab says exactly
        what is missing. This page was <span className="mono">/app/wiring</span>; that link still works.
      </p>
      <p className="hint">
        The outcome panels are worked examples — the applied one carries the engine&apos;s real diff, the
        declined ones its real wording. None of it is this tenant&apos;s data; your own changes appear
        where they happen: on Configure, at submit.
      </p>
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
