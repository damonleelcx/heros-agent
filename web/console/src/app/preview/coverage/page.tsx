import { PageFrame, Section, Chip, Row } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { CoverageLegend, CoverageMatrix, CoverageDetail, CoverageBoundary } from "@/components/coverage";
import { PREVIEW_COVERAGE } from "./fixture";

/**
 * A self-contained preview of the coverage surface, seeded with the engine's own table so the
 * presentation can be checked in a browser without a live platform backend. It uses only the root
 * layout (no session, no BFF), which is why it lives outside /app.
 *
 * All four states have to be SEEN rather than asserted on. A test can prove two class names differ; it
 * cannot prove a reader can tell the states apart at a glance, and that is the property this surface
 * lives or dies by — the whole capability exists because three different answers were being rendered as
 * one greyed-out control.
 */
export const dynamic = "force-dynamic";

export default function CoveragePreview() {
  const view = PREVIEW_COVERAGE;
  const cells = view.cells ?? [];
  const applies = cells.filter((c) => c.status === "materializes").length;

  return (
    <PageFrame
      eyebrow="Preview · coverage"
      title="What applies where"
      wide
      lede="The platform states, per language and per form, what it can write into your source — and where it cannot, which of three different things is missing. This answer is identical on every plan."
      actions={
        <Row>
          <Chip tone="ok">{applies} cells apply</Chip>
          <Chip>{cells.length - applies} refuse by name</Chip>
          <Chip>{view.version}</Chip>
        </Row>
      }
    >
      <Tabs
        tabs={[
          {
            id: "matrix",
            label: "Matrix",
            content: (
              <Section title="Every axis, every language">
                <CoverageMatrix view={view} />
              </Section>
            ),
          },
          {
            id: "states",
            label: "The four states",
            content: (
              <Section title="Whose move is it">
                <CoverageLegend view={view} />
              </Section>
            ),
          },
          {
            id: "boundary",
            label: "Before the picker",
            content: (
              <Section title="What a node is told before anything is offered">
                <CoverageBoundary axis="skills" language="rust" cells={cells} />
                <CoverageBoundary axis="context" language="python" cells={cells}>
                  <p className="text-sm text-muted-foreground">…the policy picker would render here.</p>
                </CoverageBoundary>
              </Section>
            ),
          },
          {
            id: "detail",
            label: "Every form",
            content: (
              <Section title="Form by form">
                <CoverageDetail cells={cells.filter((c) => c.axis === "skills")} />
              </Section>
            ),
          },
        ]}
      />
    </PageFrame>
  );
}
