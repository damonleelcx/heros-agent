import { PageFrame } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { StudioMatrix } from "./matrix";
import { Studio, BoundModeShowcase } from "./studio";

/**
 * Prompt & Model Studio (P10 + M-series), laid out viewport-first (P9 NFR17). Its three sections —
 * the node × model MATRIX (primary), the PROMPT LIBRARY (authoring/diff), and the BOUND NODES view —
 * are in-page TABS, not a vertical stack: the matrix is the landing tab and nothing is pushed below the
 * fold. Nothing here is ranked: no score, no winner, no promotion path; a bound cell is "selected,
 * unverified," never "proven best."
 */
export const metadata = { title: "Prompt & Model Studio" };

export default function StudioPage() {
  return (
    <PageFrame
      eyebrow="Prompt & Model Studio"
      title="Pick a model and a prompt per node"
      lede="Agent nodes across, models down. Every result is exploratory — no score, no winner, no promotion path; a bound cell is in force, unverified. Prove a configuration with a multi-seed evaluation and ship it as a verified pull request."
      wide
    >
      <Tabs
        tabs={[
          { id: "matrix", label: "Matrix", content: <StudioMatrix /> },
          { id: "library", label: "Prompt library", content: <Studio /> },
          { id: "bound", label: "Bound nodes", content: <BoundModeShowcase /> },
        ]}
      />
    </PageFrame>
  );
}
