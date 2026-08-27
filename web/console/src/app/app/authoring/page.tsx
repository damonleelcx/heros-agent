import { loadProjection } from "@/lib/projection";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { PageFrame, Section, Banner } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { AxisFrame } from "@/components/axisFrame";
import { CurrentValue, ReadOn } from "@/components/editorKit";
import { AuthoredChanges } from "./changes";
import { loadAxisValues, valueFor } from "@/lib/axisRead";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";
import { AXIS_DOC } from "@/lib/axisSubject";

/**
 * `/app/authoring` — the shared contract, and the reader's own authored changes (P13 §11, re-bound by P37).
 *
 * # Why this surface still exists after P37
 *
 * Its reason is unchanged and it is a good one: the rules governing an authored change are the SAME on
 * every axis — one spine, origin-blind refusals, three preflight verdicts, unverified-until-measured, an
 * exact undo. Spreading them across seven pages would restate them seven times, and the day one
 * restatement drifts a reader learns the rules differ per axis, which they do not.
 *
 * What changed is WHERE the restatement lives. Those five rules are the same for every reader, so by the
 * moving rule they are documentation: they are at `/docs/concepts/authored-changes`, and this surface
 * links to them once per section.
 *
 * # 🔴 What replaced them here
 *
 * The reader's OWN authored changes, and their own node's state. The page used to render two fixture
 * changes (`ac_4f19c2ab7d3e5610bb42`, `ac_77b0e4c1aa9f3d2265be`) and three fixture verdicts in exactly
 * the position their own changes now occupy — the shape FR4 forbids.
 *
 * # 🔴 What did not move
 *
 * The `unverified` stamp, and the sentence that an unverified change contributes nothing to any figure
 * and is never auto-merged. That is a claim about the reader's own change and it is the one this surface
 * exists to keep making.
 */
export const metadata = { title: "Author a change" };
export const dynamic = "force-dynamic";

export default async function AuthoringPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const outcome = await resolveSubject(subjectFromSearchParams(await searchParams));
  const projection = await loadProjection();
  const values = await loadAxisValues(
    outcome.state === "resolved" ? outcome.subject.workflow_id : undefined,
  );

  return (
    <PageFrame
      eyebrow="Author a change"
      title="Make the change yourself"
      lede="Set a model, a prompt, a policy or a strategy directly. Every change goes through the gates the platform applies to its own proposals, and stays unverified until an evaluation has run."
      wide
    >
      {/*
        🔴 The AXIS half is inside the frame; the reader's own AUTHORED CHANGES are not. Same reason as
        `/app/delivery`: a change somebody authored exists whether or not they have reported a workflow's
        structure, and hiding it behind a subject would take it from the reader who has least. FR4 is
        about the position a FIXTURE may not occupy, not about the rest of the page.
      */}
      <Tabs
        tabs={[
          {
            id: "changes",
            label: "This node",
            content: (
              <div className="flex flex-col gap-6">
                      {/*
                        🔴 Protected (FR16). An authored change may be applied without a verification run;
                        what the platform will not do is call it a result. This is a claim about the
                        reader's own change, so the moving rule keeps it here.
                      */}
                      <Banner tone="info" title="Applied is not verified">
                        <p>
                          It is your repository, so the platform will not refuse to emit your edit. What it
                          will not do is call it a result: an unverified change stays outside the
                          verified-delta ledger, contributes nothing to any improvement or savings figure,
                          and is never merged automatically at any level.
                        </p>
                <ReadOn href={AXIS_DOC.prompt}>
                  One spine, three verdicts, validation at save, and the exact undo
                </ReadOn>
              </Banner>

              <AxisFrame axis="prompt" outcome={outcome} returnTo="/app/authoring">
                {(subject) => {
                  const prompt = valueFor(values, subject.node_id, "prompt");
                  return (
                    <Section title="What this node's prompt is today">
                      <CurrentValue
                        {...(prompt
                          ? {
                              state: prompt.state,
                              current: prompt.current,
                              detail: prompt.detail,
                              missingInput: prompt.missing_input,
                              because: prompt.because,
                            }
                          : {
                              state: "not_measured" as const,
                              missingInput: "not_visible_in_static_ir",
                              because:
                                "a prompt's origin is a template reference resolved at the call site, and the reported structure carries no field for it",
                            })}
                      />
                    </Section>
                  );
                }}
              </AxisFrame>

              <AuthoredChanges />
            </div>
          ),
        },
        {
          id: "your-nodes",
          label: "Your nodes",
          content: <AxisProjectionPanel axis="prompt" outcome={projection} />,
        },
      ]}
      />
    </PageFrame>
  );
}
