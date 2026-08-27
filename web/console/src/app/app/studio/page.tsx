import { PageFrame } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { AxisFrame } from "@/components/axisFrame";
import { StudioMatrix } from "./matrix";
import { Studio, BoundModeShowcase } from "./studio";
import { StudioAuthoring } from "./authoring";
import { loadAxisValues, valueFor } from "@/lib/axisRead";
import { loadModelVocabulary } from "@/lib/axisVocabulary";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";

/**
 * Prompt & Model Studio — bound to the reader's own node (P10 + M-series, re-bound by P37).
 *
 * # What P37 changed here, and what it deliberately did not
 *
 * `/app/studio` was folded into full scope (block-inventory.md §1.6) because it was the console's
 * largest working route at 2,890 words AND it carried its own workflow picker — a second answer to the
 * question the shell now answers once.
 *
 * 🔴 **The matrix is not deleted.** `ui-redesign-feature-and-visual-consistency`: a redesign may not lose
 * a feature. Every column, every cell state and the cost figure are exactly where they were. What
 * changed is that its workflow picker is gone — replaced by the resolved subject — and its explanation
 * moved to `/docs/concepts/prompt-and-model-studio`, one row per block in the inventory.
 *
 * # 🔴 What did not move
 *
 * Nothing here is ranked. No score, no winner, no confidence interval, no promotion path — a bound cell
 * is *selected, unverified*, never *proven best*. That claim changes with the reader's data (it is a
 * statement about THEIR configuration) and it is the one sentence this surface cannot afford to lose.
 */
export const metadata = { title: "Prompt & Model Studio" };
export const dynamic = "force-dynamic";

export default async function StudioPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const outcome = await resolveSubject(subjectFromSearchParams(await searchParams));
  const values = await loadAxisValues(
    outcome.state === "resolved" ? outcome.subject.workflow_id : undefined,
  );
  const currentModel =
    outcome.state === "resolved" ? valueFor(values, outcome.subject.node_id, "model") : null;
  // The provider is the node's OWN, read from the IR. It decides which models are offered live and which
  // are shown disabled — never a default, because a wrong provider produces a diff that compiles.
  const models = await loadModelVocabulary(currentModel?.detail ?? "");

  return (
    <PageFrame
      eyebrow="Prompt & Model Studio"
      title="Pick a model and a prompt for this node"
      lede="Nothing here is ranked: no score, no winner, no promotion path. Every result is exploratory, and a bound cell is in force and unverified — never proven best."
      wide
    >
      <AxisFrame axis="model" outcome={outcome} returnTo="/app/studio">
        {(subject) => (
          <Tabs
            tabs={[
              {
                id: "author",
                label: "This node",
                content: (
                  <StudioAuthoring
                    subject={subject}
                    vocabulary={models.state === "ok" ? models.vocabulary : null}
                    applyMode="unknown"
                    current={
                      currentModel
                        ? {
                            state: currentModel.state,
                            current: currentModel.current,
                            detail: currentModel.detail,
                            missingInput: currentModel.missing_input,
                            because: currentModel.because,
                          }
                        : {
                            state: "not_measured",
                            missingInput: "unresolved_in_ir",
                            because:
                              "this node was not present in the structure this workflow reported, so there is no model binding here to read",
                          }
                    }
                  />
                ),
              },
              { id: "matrix", label: "Matrix", content: <StudioMatrix workflowId={subject.workflow_id} /> },
              { id: "library", label: "Prompt library", content: <Studio /> },
              { id: "bound", label: "Bound nodes", content: <BoundModeShowcase /> },
            ]}
          />
        )}
      </AxisFrame>
    </PageFrame>
  );
}
