import { requireSession } from "@/lib/session";
import { PageFrame, Section } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { WiringBoundaries } from "./boundaries";
import { WiringEditor } from "./editor";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { loadProjection } from "@/lib/projection";
import { loadAxisValues, valueFor } from "@/lib/axisRead";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";
import { AxisFrame } from "@/components/axisFrame";
import { CurrentValue, ReadOn } from "@/components/editorKit";
import { AXIS_DOC } from "@/lib/axisSubject";

/**
 * The GRAPH surface — the shape of a workflow (P15 wiring + P34 topology).
 *
 * # 🔴 P37 — bound to the reader's own node; the explanation moved
 *
 * The four wiring operations, the three topology forms, the applied transposition and the four declined
 * examples are at `/docs/concepts/graph-and-wiring`, one row each in `block-inventory.md` §G. Nothing was
 * deleted, and the gesture editor was NOT deleted either — it moved into a tab that says it is the
 * platform's fixture, because a rearrangement is a change BETWEEN nodes and has no node-bound form to
 * convert to.
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

/**
 * The four shapes a declined change takes. Three are the wiring axis's own operators; the fourth is
 * the fallback, which is here because an axis the console cannot annotate must still render — a
 * refusal swallowed for being unrecognised is the worst of both.
 */

// APPLIED_DIFF is the real artifact — the unified diff the engine produced for a transposition of two
// adjacent statements in the Go fixture (`internal/transform/testdata/target/wiring.go.txt`), copied
// verbatim. A hand-written illustration would drift from what ships the first time the emitter changed,
// and this page's whole claim is "what you read here is what the engine emits".





/**
 * TOPOLOGY_FORMS mirrors `transform.GraphForms()` and the coverage the engine derives for each.
 *
 * 🔴 Every one of them REFUSES in every language today, and the page leads with that rather than
 * offering a control that cannot produce a diff (P34 §7.3). The three states are deliberately three:
 * what the form declares, what it is refused for, and — the one FR18 insists on — WHICH missing thing
 * would close it. "We do not support this", "your call site cannot carry this" and "nobody can carry
 * this in any language" are three different sentences to three different people.
 */


export default async function GraphPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  await requireSession();
  const outcome = await resolveSubject(subjectFromSearchParams(await searchParams));
  const projection = await loadProjection();
  const values = await loadAxisValues(
    outcome.state === "resolved" ? outcome.subject.workflow_id : undefined,
  );

  return (
    <PageFrame
      eyebrow="Graph"
      title="The shape of a workflow"
      lede="Which calls run, in what order, which may overlap, which are conditional, and how results combine where they converge. One shape reaches your source; the rest are declined by name."
      wide
    >
      <AxisFrame axis="graph" outcome={outcome} returnTo="/app/graph">
        {(subject) => {
          const wiring = valueFor(values, subject.node_id, "wiring");
          const topology = valueFor(values, subject.node_id, "graph");
          const tabs: TabItem[] = [
            {
              id: "this-node",
              label: "This node",
              content: (
                <div className="flex flex-col gap-6">
                  {/*
                    🔴 The BOUNDARIES first (FR15). Two facts with two owners and two next steps, and
                    collapsing them into "reordering is unavailable here" sends the second reader to wait
                    for the first reader's fix.
                  */}
                  <WiringBoundaries />
                  <Section title="What this node does on each axis today">
                    <CurrentValue
                      {...(wiring
                        ? {
                            state: wiring.state,
                            current: wiring.current,
                            detail: wiring.detail,
                            missingInput: wiring.missing_input,
                            because: wiring.because,
                          }
                        : {
                            state: "not_measured" as const,
                            missingInput: "unresolved_in_ir",
                            because: "this node was not present in the structure this workflow reported",
                          })}
                    />
                    <CurrentValue
                      {...(topology
                        ? {
                            state: topology.state,
                            current: topology.current,
                            detail: topology.detail,
                            missingInput: topology.missing_input,
                            because: topology.because,
                          }
                        : {
                            state: "not_measured" as const,
                            missingInput: "frontend_emits_no_edges",
                            because: "topology is a property BETWEEN nodes, so a per-node read cannot answer it",
                          })}
                    />
                    <ReadOn href={AXIS_DOC.graph}>
                      The four wiring operations, the three topology forms, and what reaches your source
                    </ReadOn>
                  </Section>
                </div>
              ),
            },
            {
              // 🔴 The gesture editor is KEPT — `ui-redesign-feature-and-visual-consistency`: a redesign
              // may not lose a feature. What changed is its POSITION and its LABEL. FR4 forbids a fixture
              // in the position the reader's own data occupies; it does not forbid a demonstration in a
              // tab of its own that says what it is. A rearrangement is a change BETWEEN nodes, so it has
              // no node-bound form to convert to, and deleting it would trade a working demonstration for
              // nothing.
              id: "editor",
              label: "Rearrange the graph (the platform's fixture)",
              content: <WiringEditor />,
            },
            {
              id: "your-nodes",
              label: "Your nodes (wiring)",
              content: <AxisProjectionPanel axis="wiring" outcome={projection} />,
            },
            {
              // 🔴 A SECOND panel, not a merged one. `wiring` and `graph` are two axes with two coverage
              // tables and two denominators; one panel over both would report a single fraction that is
              // true of neither.
              id: "your-nodes-graph",
              label: "Your nodes (topology)",
              content: <AxisProjectionPanel axis="graph" outcome={projection} />,
            },
          ];
          return <Tabs tabs={tabs} />;
        }}
      </AxisFrame>
    </PageFrame>
  );
}
