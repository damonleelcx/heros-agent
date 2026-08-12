import Link from "next/link";
import type { EvalSetView, EvalCaseView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { routes } from "@/lib/subjects";
import {
  PageFrame,
  Section,
  Chip,
  Row,
  Empty,
  Failure,
  Banner,
  DataTable,
  Stat,
  Stats,
} from "@/components/primitives";
import { integer, plural } from "@/lib/format";

export const dynamic = "force-dynamic";

/**
 * The eval set behind the board's denominator (P30 task 1.12).
 *
 * # Why this is a route and not a modal
 *
 * `n_cases` is doing load-bearing work on the board — every score is computed over it — and it was
 * unopenable. "Which cases?" is a question somebody asks in a review, links to, and returns to; an
 * overlay has no URL to send and disappears on the first navigation.
 *
 * # 🔴 What this page must never do
 *
 * Render an empty table when the platform holds no cases. On a hosted deployment the cases stay on the
 * customer's machine by wire contract, and an empty table under a headline of "8 cases" reads as a
 * broken eval. The state comes from the platform and names the limit; this page renders the state.
 */
export default async function EvalSetPage({ params }: { params: Promise<{ workflowId: string }> }) {
  const { workflowId } = await params;
  const id = decodeURIComponent(workflowId);
  const { outcome } = await load<EvalSetView>((paths) => paths.evalSet(id));
  return (
    <PageFrame
      eyebrow="Eval set"
      title={id}
      lede="The cases every score on this workflow's board is computed over — how many, of what kind, and how many of their oracles can decide anything."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="workflow" />
      ) : (
        <EvalSetBody view={outcome.data} workflowId={id} />
      )}
    </PageFrame>
  );
}

function EvalSetBody({ view, workflowId }: { view: EvalSetView; workflowId: string }) {
  const cases = view.cases ?? [];
  const families = view.families ?? [];
  const vacuous = view.vacuous_dimensions ?? [];
  const uncovered = view.uncovered_nodes ?? [];
  const reasons = view.indecisive_reasons ?? [];

  return (
    <>
      <Section
        title="This eval set"
        aside={
          <Link className="mono text-xs text-primary underline-offset-2 hover:underline" href={routes.board(workflowId)}>
            back to the board
          </Link>
        }
      >
        <Row>
          <Chip tone={view.state === "inconsistent" ? "bad" : "info"}>{view.state.replace(/_/g, " ")}</Chip>
        </Row>
        {/* 🔴 An inconsistent set is an ERROR, not a table with a caveat. Every score on the board is
            computed over `n_cases`, so a list that disagrees means one of the two numbers describes a
            different eval set — and rendering the shorter table under the larger number is exactly the
            way that disagreement becomes invisible. */}
        {view.state === "inconsistent" ? (
          <Banner tone="bad" title="The list and the denominator disagree.">
            <p>{view.sentence}</p>
          </Banner>
        ) : (
          <p className="text-sm leading-relaxed text-foreground/90">{view.sentence}</p>
        )}
        <Stats>
          <Stat label="Cases" value={integer(view.n_cases)} note="the denominator under every score" />
          <Stat
            label="Decisive oracles"
            value={integer(view.n_oracle)}
            note="cases whose oracle can actually return NO"
          />
          <Stat
            label="Indecisive"
            value={integer(view.n_indecisive)}
            note="an oracle that can never fail — looks measured, decides nothing"
          />
        </Stats>
      </Section>

      {families.length > 0 ? (
        <Section title="What is in it" aside="by reference label">
          <DataTable
            caption="How many cases carry each kind of reference"
            columns={[
              { key: "family", label: "Family" },
              { key: "cases", label: "Cases", numeric: true },
            ]}
          >
            <tbody>
              {families.map((f) => (
                <tr key={f.family}>
                  <td>{f.family}</td>
                  <td className="num">{integer(f.cases)}</td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        </Section>
      ) : null}

      <Section title="Cases" aside={`${integer(cases.length)} listed`}>
        {cases.length === 0 ? (
          <Empty title="The cases themselves are not held on this deployment.">
            {/* Not "you have no cases". The distinction is the whole point of the state above. */}
            <p>{view.sentence}</p>
            {(view.unattributed ?? []).length > 0 ? (
              <p>
                Not carried across:{" "}
                <span className="mono">{(view.unattributed ?? []).join(", ")}</span>.
              </p>
            ) : null}
          </Empty>
        ) : (
          <DataTable
            caption="Every case, its family, the oracle that decides it, and whether that oracle can fail"
            columns={[
              { key: "case", label: "Case" },
              { key: "family", label: "Family" },
              { key: "oracle", label: "Oracle" },
              { key: "decides", label: "Decides" },
            ]}
          >
            <tbody>
              {cases.map((c) => (
                <CaseRow key={c.case_id} row={c} />
              ))}
            </tbody>
          </DataTable>
        )}
        {reasons.length > 0 ? (
          <div className="flex flex-col gap-2">
            <p className="caption">Why an oracle was found indecisive:</p>
            <ul className="diagnostics">
              {reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        ) : null}
      </Section>

      {/* 🔴 BY NAME, never as a count (P30 task 1.13). "1 axis not measurable" gives a reader nothing to
          act on; "path coverage was not measurable" tells them their workflow's inter-node flow has not
          been observed, which is a different thing to go and fix. */}
      {vacuous.length > 0 ? (
        <Section title="Coverage that could not be measured" aside={`${vacuous.length} ${plural(vacuous.length, "axis", "axes")}`}>
          <Banner tone="warn" title="These axes had no obligations to measure at all.">
            <p>
              Not measurable is not the same as fully covered. Named here so the axis is identifiable:
            </p>
            <ul className="diagnostics">
              {vacuous.map((name) => (
                <li key={name} className="mono">
                  {name}
                </li>
              ))}
            </ul>
          </Banner>
        </Section>
      ) : null}

      {uncovered.length > 0 ? (
        <Section title="Graph nodes no case exercises" aside={`${integer(uncovered.length)}`}>
          <ul className="diagnostics">
            {uncovered.map((id) => (
              <li key={id} className="mono">
                {id}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}
    </>
  );
}

function CaseRow({ row }: { row: EvalCaseView }) {
  return (
    <tr>
      <td className="mono">{row.case_id}</td>
      {/* An empty family is UNATTRIBUTED, not "no family". An em-dash here would read as legitimately
          absent data, which is the exact confusion the state's `unattributed` list exists to prevent. */}
      <td>{row.family ? row.family : <span className="caption">not carried</span>}</td>
      <td className="mono">{row.oracle ? row.oracle : <span className="caption">not carried</span>}</td>
      <td>
        {row.indecisive ? (
          <Chip tone="bad" title="this oracle can never return NO">
            never fails
          </Chip>
        ) : (
          <Chip tone="ok">can fail</Chip>
        )}
      </td>
    </tr>
  );
}
