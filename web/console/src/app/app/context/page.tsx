import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, DataTable } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { AxisFrame } from "@/components/axisFrame";
import { ReadOn } from "@/components/editorKit";
import { ContextEditor } from "./editor";
import { loadProjection } from "@/lib/projection";
import { loadAxisValues, coverageForNode, valueFor } from "@/lib/axisRead";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";
import { AXIS_DOC } from "@/lib/axisSubject";

/**
 * The context surface — an editor bound to the reader's own node (P37).
 *
 * # What this page stopped being
 *
 * It carried 1,995 words and the reader could change nothing. Its coverage table was transcribed from Go
 * by hand, its diff was a fixture, and its decline cards were the engine's real sentences about the
 * platform's demonstration nodes. None of that was wrong when it was written: the engine existed and the
 * reader's repository did not, so explaining the engine was the honest maximum.
 *
 * P32 removes that constraint. Every block that was the same for every reader has moved to
 * `/docs/concepts/context-policies` — enumerated, one row each, in
 * `openspec/changes/p37-source-bound-editors/block-inventory.md` §1.1. Nothing was deleted.
 *
 * # 🔴 What did NOT move, and must not
 *
 *   · the reader's own refusal cause, verbatim, rendered by `PreflightPanel` (FR13)
 *   · `not_measured` with its named missing input (FR14)
 *   · the drop tolerance, stated ABOVE the picker (FR15)
 *   · the `unverified` stamp (FR16)
 *
 * All four change with the reader's data, which is why the moving rule keeps them here. A layout rewrite
 * deletes them by accident because on the page they look like decoration; §6's fences drive each one.
 *
 * # 🔴 The coverage table is now a LIVE READ (FR17)
 *
 * `COVERAGE` was a hand-copied array kept honest by `TestContextCoverageTableMatchesEngine`. The fence
 * was correct and it guarded a TRANSCRIPTION; after this read there is no transcription to drift. That
 * is the only acceptable reason to remove a fence, and `tests/p37-context.test.mjs` says so where the
 * removal happens rather than leaving it to a commit message.
 */
export const dynamic = "force-dynamic";

export default async function ContextPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  await requireSession();
  // 🔴 FR18 — a `finding` links here naming a node, and that selection outranks everything else.
  const outcome = await resolveSubject(subjectFromSearchParams(await searchParams));
  const projection = await loadProjection();
  const values = await loadAxisValues(
    outcome.state === "resolved" ? outcome.subject.workflow_id : undefined,
  );

  return (
    <PageFrame
      eyebrow="Context"
      title="Context strategy"
      lede="What this node is given to read — which turns survive, what is summarised, what is retrieved."
      wide
    >
      <AxisFrame axis="context" outcome={outcome} returnTo="/app/context">
        {(subject) => {
          const current = valueFor(values, subject.node_id, "context");
          const coverage = coverageForNode(values, subject.node_id);
          const tabs: TabItem[] = [
            {
              id: "editor",
              label: "This node",
              content: (
                <ContextEditor
                  subject={subject}
                  coverage={coverage}
                  setVersion={values.state === "ok" ? values.coverage_version : "unknown"}
                  current={
                    current
                      ? {
                          state: current.state,
                          current: current.current,
                          detail: current.detail,
                          missingInput: current.missing_input,
                          because: current.because,
                        }
                      : {
                          state: "not_measured",
                          missingInput: "unresolved_in_ir",
                          because:
                            "this node was not present in the structure this workflow reported, so there is no policy here to read",
                        }
                  }
                />
              ),
            },
            {
              id: "coverage",
              label: "What reaches your source",
              content: <CoverageTab coverage={coverage} language={subject.language} />,
            },
            {
              id: "your-nodes",
              label: "Your nodes",
              content: <AxisProjectionPanel axis="context" outcome={projection} />,
            },
          ];
          return <Tabs tabs={tabs} />;
        }}
      </AxisFrame>
    </PageFrame>
  );
}

/**
 * CoverageTab is FR17's live read: what the engine does with each policy at THIS node's call site.
 *
 * 🔴 Per the node's own language, and NOTHING when that language has no rewriter. A coverage answer
 * attributed to the wrong language is a claim about the reader's code drawn from a guess, and it would be
 * wrong in the direction that wastes their afternoon.
 */
function CoverageTab({
  coverage,
  language,
}: {
  coverage: { policy: string; mode: string; reason?: string }[];
  language?: string;
}) {
  if (coverage.length === 0) {
    return (
      <Section title="What reaches your source">
        {/* 🔴 FR14 — absence is drawn, with the input that would resolve it. Never an empty region. */}
        <p className="hint">
          <strong className="font-medium">Not measured. </strong>
          No context rewriter has landed for{" "}
          <span className="mono">{language || "this node's language"}</span>, so nothing here can say what
          a policy would do at this call site.
        </p>
        <ReadOn href={AXIS_DOC.context}>Which policies reach source, and the two reasons one is declined</ReadOn>
      </Section>
    );
  }
  return (
    <Section title="What reaches your source" aside={language}>
      <DataTable
        caption="What each context policy does at this node's call site"
        columns={[
          { key: "policy", label: "Policy" },
          { key: "mode", label: "At this call site" },
          { key: "reason", label: "Why" },
        ]}
      >
        <tbody>
          {coverage.map((c) => (
            <tr key={c.policy}>
              <td className="mono text-sm align-top">{c.policy}</td>
              <td className="align-top">
                <Chip tone={c.mode === "applied" ? "ok" : c.mode === "declined" ? "warn" : "info"}>
                  {c.mode}
                </Chip>
              </td>
              <td className="text-sm text-muted-foreground">{c.reason}</td>
            </tr>
          ))}
        </tbody>
      </DataTable>
      <ReadOn href={AXIS_DOC.context}>The two different reasons a policy is declined</ReadOn>
    </Section>
  );
}
