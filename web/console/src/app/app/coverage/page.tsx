import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Row, Banner } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { CoverageLegend, CoverageMatrix, CoverageDetail } from "@/components/coverage";
import { fetchCoverage } from "./data";

/**
 * The coverage surface (P13 13d) — "what applies where, and whose move is the rest".
 *
 * # Why this surface exists
 *
 * Before it, the answer to "will the platform apply this to my node?" was scattered across five tables
 * in three Go packages, and — worse — a language with no materializer had NO ROW ANYWHERE. Absence
 * rendered, on every screen that consumed it, as *not applicable*: a claim about the reader's code,
 * when the truth was a claim about our backlog.
 *
 * So this page shows a TOTAL table. Every axis, every registered language, every form the axis binds
 * against, each with a state. There is no blank cell, and no cell that means "we did not say".
 *
 * # Why the three refusals are three different things on screen
 *
 * The reader's next action differs completely between them, and only one of the three is a wait:
 *
 *   your call site  → they edit code. A materializer would refuse it too.
 *   not in source   → nobody edits anything. There is no "when".
 *   not yet — ours  → they wait, and the thing being waited for is NAMED.
 *
 * Rendering those as one greyed-out control is the failure this page was built to end.
 *
 * # What is derived here: nothing
 *
 * Status, cause class and the missing artifact all come from `transform.AxisCoverage()` through the
 * BFF, rendered as received. The only computation on this side is which cells belong to which square of
 * the matrix, which is grouping rather than judgement.
 */
export const dynamic = "force-dynamic";

export default async function CoveragePage() {
  const session = await requireSession();
  const view = await fetchCoverage(session.tenantId);

  if (!view) {
    return (
      <PageFrame eyebrow="Coverage" title="What applies where" wide>
        <Banner tone="warn" title="Coverage is unavailable">
          The coverage table could not be read from the platform. This page states what the engine can
          apply, so showing a partial answer would be worse than showing none.
        </Banner>
      </PageFrame>
    );
  }

  const cells = view.cells ?? [];
  const applies = cells.filter((c) => c.status === "materializes").length;

  const tabs: TabItem[] = [
    {
      id: "matrix",
      label: "Matrix",
      content: (
        <Section title="Every axis, every language" aside={<span className="mono">{view.version}</span>}>
          <CoverageMatrix view={view} />
          <p className="text-xs text-muted-foreground">
            A square shows how many of that axis&rsquo;s forms apply in that language — providers and SDK
            generations for a binding, registry rows for a model, policies for context. Partial coverage
            reads as the refusal and is never rounded up: a square most readers hit a refusal in is not a
            square that &ldquo;applies&rdquo;.
          </p>
        </Section>
      ),
    },
    {
      id: "states",
      label: "The four states",
      content: (
        <Section title="Whose move is it">
          <CoverageLegend view={view} />
          <p className="text-xs text-muted-foreground">
            Only one of the three refusals is a wait. Telling a reader whose source cannot carry a change
            to wait for us costs them a quarter; telling a reader whose language we have not built to go
            and edit their code costs them an afternoon and a support ticket.
          </p>
        </Section>
      ),
    },
    {
      id: "detail",
      label: "Every form",
      content: (
        <Section title="Form by form">
          <CoverageDetail cells={cells} />
        </Section>
      ),
    },
  ];

  return (
    <PageFrame
      eyebrow="Coverage"
      title="What applies where"
      wide
      lede={
        <>
          The platform states, per language and per form, what it can write into your source — and where
          it cannot, which of three different things is missing. This answer is identical on every plan.
        </>
      }
      actions={
        <Row>
          <Chip tone="ok">{applies} cells apply</Chip>
          <Chip>{cells.length - applies} refuse by name</Chip>
          <Chip title="The content version of the table this build refuses from">{view.version}</Chip>
        </Row>
      }
    >
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
