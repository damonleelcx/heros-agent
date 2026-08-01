import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, NotMountedState, Pill } from "@/components/states";
import { DataTable, Num, PageFrame, Section } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import type { AxisView, CellState } from "@/lib/types";

/**
 * The axis oversight surface (P26 wave 26d) — READ-ONLY.
 *
 * # What it answers
 *
 * *Which materializer would unblock the most refused nodes across the fleet.* Six axes resolve into
 * `config_hash` and the console showed none of them, so the platform's own backlog question had no
 * data path and the last several such decisions were made without one.
 *
 * # 🔴 It renders the one coverage source AS RECEIVED
 *
 * Nothing on this page computes, caches, merges, re-ranks or reformats a coverage answer. A console
 * that did would be a second opinion about coverage, and coverage is a claim about a customer's code.
 * Parity is asserted in both directions against the real engine.
 *
 * # 🔴 An absent cell renders as UNKNOWN, never as *not applicable*
 *
 * *Not applicable* says "your call site cannot carry this". The truth may be "we have not built the
 * materializer", and that substitution converts our backlog into the customer's problem invisibly.
 * The only thing that produces a call-site claim here is a PRESENT cell whose stable cause says so.
 *
 * # 🔴 These are counts, not results
 *
 * Only the eval harness ranks, and only a P5.5 verified delta is a claim. The ranking below is
 * rendered as a table of counts with no score, no bar, no medal — `is_ranking` is false on the wire
 * and this page honours it.
 */

const CELL_LABEL: Record<CellState, string> = {
  applies: "applies",
  refused: "refused",
  unknown: "unknown",
};

/* Every state is NEUTRAL or OK. A refusal is not a hazard: it is the ordinary state of most cells in
 * a total coverage table, and painting it red would spend the colour the kill switch needs on the
 * commonest row on the page (FR31). */
const CELL_TONE: Record<CellState, "ok" | "neutral"> = {
  applies: "ok",
  refused: "neutral",
  unknown: "neutral",
};

export default async function AxesPage({
  searchParams,
}: {
  searchParams: Promise<{ axis?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { axis } = await searchParams;

  if (!hasCapability(identity, "axis.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Optimization" title="Axes">
          <DeniedState
            capability="axis.read"
            description="Read per-axis adoption, refusals and coverage"
            heldBy={holdersOf(identity, "axis.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let view: AxisView | null = null;
  let failure: { kind: string; message: string } | null = null;
  try {
    view = await adminFetch<AxisView>("/admin/api/axes", { sessionToken });
  } catch (error) {
    failure =
      error instanceof AdminApiError
        ? { kind: error.kind, message: error.message }
        : { kind: "degraded", message: String(error) };
  }

  const matrix = (view?.matrix ?? []).filter((c) => !axis || c.axis === axis);

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Optimization"
        title="Axes"
        lede={
          <>
            Per-axis status, fleet adoption, and refusals by <strong>typed cause</strong>. Coverage is
            read from the one coverage source and rendered as received — this page computes, caches and
            reformats nothing. A coverage gap is <strong>not a plan boundary</strong>: it is identical
            on every plan, and no tier unlocks a cell the engine refuses.
          </>
        }
      >
        {failure ? (
          failure.kind === "not_mounted" ? (
            <NotMountedState what="axis oversight" detail={failure.message} />
          ) : (
            <DegradedState what="the coverage source" detail={failure.message} />
          )
        ) : !view ? (
          <DegradedState what="the coverage source" />
        ) : (
          <Tabs
            tabs={[
              {
                id: "axes",
                label: "Per axis",
                content: (
                  <Section title="Each axis, as it declares itself" aside={view.coverage_version} flush>
                    <DataTable
                      caption="Each axis's own declared status, its fleet adoption, and its refusals by stable typed cause."
                      columns={[
                        { label: "Axis" },
                        { label: "Declared status" },
                        { label: "Tenants with an override" },
                        { label: "Nodes with an override" },
                        { label: "Refusals by cause" },
                      ]}
                    >
                      {view.axes.map((a) => (
                        <tr key={a.axis}>
                          <th scope="row">
                            <a href={`/axes?axis=${a.axis}`}>{a.axis}</a>
                          </th>
                          <td>
                            <Pill tone={a.status === "EXISTS" ? "ok" : "neutral"}>{a.status}</Pill>
                          </td>
                          {/* 🔴 null is UNKNOWN, not zero. "No tenant uses this axis" and "we did not
                              measure adoption" are opposite claims that look identical as a 0. */}
                          <td className="num">
                            {a.tenants === null ? (
                              <span className="hint">unknown — no adoption source</span>
                            ) : (
                              <Num value={a.tenants} />
                            )}
                          </td>
                          <td className="num">
                            {a.nodes === null ? (
                              <span className="hint">unknown — no adoption source</span>
                            ) : (
                              <Num value={a.nodes} />
                            )}
                          </td>
                          <td>
                            {/* Never a single combined total: the three causes are answered by three
                                different parties. */}
                            {Object.keys(a.refusals).length === 0 ? (
                              <span className="hint">no refusals</span>
                            ) : (
                              <ul>
                                {Object.entries(a.refusals).map(([cause, n]) => (
                                  <li key={cause}>
                                    <span className="mono">{cause}</span>: <Num value={n} />
                                  </li>
                                ))}
                              </ul>
                            )}
                          </td>
                        </tr>
                      ))}
                    </DataTable>
                  </Section>
                ),
              },
              {
                id: "ranking",
                label: "What would close the most",
                content: (
                  <Section
                    title="Candidate artefacts, by refusals closed"
                    aside="counts, not results"
                    flush={view.ranking.length > 0}
                  >
                    {/* 🔴 Rendered as a plain table of COUNTS. No bar, no score, no position medal:
                        only the eval harness ranks, and only a P5.5 verified delta is a claim. */}
                    <p className="hint">
                      Counts of refused coverage cells each artefact would close. These are counts, not
                      evaluation results — nothing here has been scored, ranked or verified.
                    </p>
                    {view.ranking.length === 0 ? (
                      <EmptyState what="candidate artefacts" />
                    ) : (
                      <DataTable
                        caption="Each artefact that would close refusals, and how many it would close."
                        columns={[
                          { label: "Artefact" },
                          { label: "Refusals it would close", numeric: true },
                          { label: "Axes" },
                          { label: "Languages" },
                        ]}
                      >
                        {view.ranking.map((r) => (
                          <tr key={r.artefact}>
                            <th scope="row" className="mono">
                              {r.artefact}
                            </th>
                            <td className="num">
                              <Num value={r.closes} />
                            </td>
                            <td>{r.axes.join(", ")}</td>
                            <td>{r.languages.join(", ")}</td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "matrix",
                label: "Coverage matrix",
                content: (
                  <Section
                    title={axis ? `Coverage — ${axis}` : "Coverage"}
                    aside={view.coverage_source}
                    flush
                  >
                    <DataTable
                      caption="Every axis × every registered language × every form, as the engine reports it."
                      columns={[
                        { label: "Axis" },
                        { label: "Language" },
                        { label: "Form" },
                        { label: "State" },
                        { label: "Cause" },
                        { label: "What would close it" },
                      ]}
                    >
                      {matrix.map((c) => (
                        <tr key={`${c.axis}-${c.language}-${c.form}`}>
                          <th scope="row">{c.axis}</th>
                          <td>{c.language}</td>
                          <td className="mono">{c.form}</td>
                          <td>
                            <Pill tone={CELL_TONE[c.state]}>{CELL_LABEL[c.state]}</Pill>
                          </td>
                          {/* The stable identifier, verbatim. Not translated, not prettified: the same
                              cause must render identically on every surface that shows it. */}
                          <td className="mono">{c.cause || "—"}</td>
                          <td>
                            {c.missing_input ? (
                              <span className="mono">{c.missing_input}</span>
                            ) : c.state === "refused" ? (
                              <span className="hint">nothing would — this is not unbuilt work</span>
                            ) : (
                              "—"
                            )}
                          </td>
                        </tr>
                      ))}
                    </DataTable>
                  </Section>
                ),
              },
              {
                id: "causes",
                label: "The three causes",
                content: (
                  <Section title="Who answers each refusal" flush>
                    <p className="hint">
                      Three causes, three parties. Keeping them distinguishable is the point: telling an
                      engineer to migrate a node when the change can never be written into source sends
                      them to do work that will not help.
                    </p>
                    <DataTable
                      caption="Each typed cause, whose move it is, and whether anything would close it."
                      columns={[
                        { label: "Cause" },
                        { label: "Whose move" },
                        { label: "Closable" },
                        { label: "What it means" },
                      ]}
                    >
                      {view.legend.map((l) => (
                        <tr key={l.cause}>
                          <th scope="row" className="mono">
                            {l.cause}
                          </th>
                          <td>{l.owner}</td>
                          <td>
                            {l.permanent ? (
                              <Pill tone="neutral">a boundary — nothing would</Pill>
                            ) : (
                              <Pill tone="neutral">an artefact would</Pill>
                            )}
                          </td>
                          <td>{l.meaning}</td>
                        </tr>
                      ))}
                    </DataTable>
                    <p className="state__body">
                      A gap here is <strong>not yet applied by the platform</strong>, identical on every
                      plan. No plan, tier, role, entitlement or setting would materialize a cell the
                      engine refuses.
                    </p>
                  </Section>
                ),
              },
            ]}
          />
        )}
      </PageFrame>
    </OperatorShell>
  );
}
