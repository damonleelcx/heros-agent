import { Fragment } from "react";
import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, Pill } from "@/components/states";
import { DataTable, Drawer, Num, PageFrame, Section } from "@/components/primitives";
import { timestamp } from "@/lib/format";
import type { AuditView } from "@/lib/types";

/**
 * The audit-log viewer.
 *
 * It shows the chain's integrity verdict at the top of the page, ALWAYS, recomputed on every read.
 * A viewer that listed rows without saying whether the chain they came from is intact would keep
 * looking normal after somebody rewrote history — which is exactly what the hash chain exists to make
 * impossible. Reading the log is itself a cross-tenant read, so it is permission-gated and Support is
 * denied with an escalation path.
 *
 * # Why the filters live in the URL (FR33)
 *
 * A receipt links here with `?seq=` so an operator can go from "what did I just do" to the entry that
 * recorded it in one click (FR36). An investigation filters by actor or action and pastes the URL to a
 * colleague. Both need the view's state to BE the URL — and neither may put anything sensitive in it,
 * so what travels is a sequence number and a capability name, never a session or a subject's data.
 */
export default async function AuditPage({
  searchParams,
}: {
  searchParams: Promise<{ seq?: string; actor?: string; action?: string; result?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const filters = await searchParams;

  if (!hasCapability(identity, "audit.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="The record" title="Audit log">
          <DeniedState
            capability="audit.read"
            description="Read the audit log"
            heldBy={holdersOf(identity, "audit.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let view: AuditView | null = null;
  let degraded: string | null = null;
  try {
    view = await adminFetch<AuditView>("/admin/api/audit?limit=100", { sessionToken });
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  // Filtering happens here rather than at the API because the audit read model is a fixed-size page
  // of the newest entries: narrowing it client-side never hides an entry the operator was entitled to
  // see, and it keeps the filter a pure view concern. The unfiltered total stays on screen so a
  // narrowed view can never be mistaken for a short log.
  const seq = filters.seq ? Number(filters.seq) : undefined;
  const all = view?.entries ?? [];
  const entries = all.filter(
    (e) =>
      (seq === undefined || e.seq === seq) &&
      (!filters.actor || e.actor_admin_id === filters.actor) &&
      (!filters.action || e.action === filters.action) &&
      (!filters.result || e.result === filters.result),
  );
  const filtered = entries.length !== all.length;

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="The record"
        title="Audit log"
        lede="Append-only and hash-chained. Every admin action and every AUTONOMOUS merge is here, and no role — not even Superadmin — can alter or remove an entry without breaking the chain. It does not record every merge the platform is involved in — see what this chain covers, below."
      >
        {view ? (
          view.verification.intact ? (
            <Section title="Chain verification" aside={<Pill tone="ok">Intact</Pill>}>
              <p className="state__body">
                <Num value={view.verification.checked} /> entries verified end to end. Each entry&rsquo;s
                hash covers the one before it, so an alteration anywhere breaks every hash after it.
              </p>
            </Section>
          ) : (
            <div className="state state--degraded" role="alert">
              <p className="state__title">Chain broken — tampering detected</p>
              <p className="state__body">
                The audit chain does not verify. First break at sequence {view.verification.break_at}:{" "}
                {view.verification.detail}
              </p>
            </div>
          )
        ) : null}

        {/* 🔴 What the chain covers, and what it does not. Before P26 the surface implied it recorded
            every merge; it records the ones the P6 loop performs itself. P12 deliveries merge in the
            CUSTOMER'S CI, under a credential the platform does not hold, so their absence here is not
            evidence that they did not happen — which is the false conclusion an auditor would
            otherwise draw from a true page. */}
        {view ? (
          <Section title="What this chain covers" aside={<Pill tone="neutral">merge paths</Pill>}>
            <p className="state__body">{view.merge_coverage.statement}</p>
            <DataTable
              caption="Each merge path, whether the hash chain records it, and where it is read."
              columns={[{ label: "Merge path" }, { label: "In this chain" }, { label: "How" }, { label: "Read at" }]}
            >
              {view.merge_coverage.covered.map((p) => (
                <tr key={p.id}>
                  <th scope="row">{p.name}</th>
                  <td>
                    <Pill tone="ok">recorded</Pill>
                  </td>
                  <td>{p.mechanism}</td>
                  <td>this log</td>
                </tr>
              ))}
              {view.merge_coverage.not_covered.map((p) => (
                <tr key={p.id}>
                  <th scope="row">{p.name}</th>
                  <td>
                    <Pill tone="neutral">not recorded</Pill>
                  </td>
                  <td>{p.mechanism}</td>
                  <td>{p.readable_at ? <a href={p.readable_at}>{p.readable_at}</a> : "—"}</td>
                </tr>
              ))}
            </DataTable>
          </Section>
        ) : null}

        <Section title="Filter">
          <form method="get" className="form-row">
            <span>
              <label htmlFor="seq">Sequence</label>
              <input id="seq" name="seq" type="search" defaultValue={filters.seq ?? ""} autoComplete="off" />
            </span>
            <span>
              <label htmlFor="actor">Actor</label>
              <input id="actor" name="actor" type="search" defaultValue={filters.actor ?? ""} autoComplete="off" />
            </span>
            <span>
              <label htmlFor="action">Action</label>
              <input id="action" name="action" type="search" defaultValue={filters.action ?? ""} autoComplete="off" />
            </span>
            <span>
              <label htmlFor="result">Result</label>
              <input id="result" name="result" type="search" defaultValue={filters.result ?? ""} autoComplete="off" />
            </span>
            <button type="submit" className="primary">
              Apply
            </button>
            <a className="palette-trigger" href="/audit">
              Clear
            </a>
          </form>
        </Section>

        <Section
          title="Entries"
          aside={
            <>
              {filtered ? <Pill tone="accent">Filtered</Pill> : null}
              <span>
                {entries.length} shown · {view?.total ?? 0} in the chain
              </span>
            </>
          }
          flush
        >
          {degraded ? (
            <div className="section__body">
              <DegradedState what="the audit store" detail={degraded} />
            </div>
          ) : entries.length === 0 ? (
            <div className="section__body">
              <EmptyState
                what="audit entries"
                hint={filtered ? "No entry in this page of the chain matches the filter." : undefined}
              />
            </div>
          ) : (
            <DataTable
              caption="The most recent audit entries, newest first. Each entry's evidence — the motivating diagnosis, the verified delta, and the merge commit for an autonomous merge — is one disclosure away."
              columns={[
                { label: "Seq", numeric: true },
                { label: "When" },
                { label: "Actor" },
                { label: "Action" },
                { label: "Target" },
                { label: "Result" },
                { label: "Reason" },
                { label: "Evidence" },
              ]}
            >
              {entries.map((e) => (
                <tr key={e.seq} id={`entry-${e.seq}`}>
                  <td className="num">{e.seq}</td>
                  <td>{timestamp(e.created_at)}</td>
                  <td className="mono">{e.actor_admin_id}</td>
                  <td className="mono">{e.action}</td>
                  <td className="mono">{e.target}</td>
                  <td>
                    {e.result === "denied" ? (
                      <Pill tone="warn">denied</Pill>
                    ) : e.result === "failed" ? (
                      <Pill tone="danger">failed</Pill>
                    ) : (
                      <Pill tone="neutral">{e.result}</Pill>
                    )}
                  </td>
                  <td>{e.reason ?? "—"}</td>
                  <td>
                    {/*
                      FR16 requires an autonomous merge to be reconstructable from the record: its
                      motivating diagnosis, its verified delta, and its merge commit. The backend
                      records all three — this is where an auditor actually reads them. Without it the
                      chain holds the evidence and the screen does not show it, which satisfies the
                      requirement on paper and fails the person the requirement exists for.

                      Progressive disclosure: the row reads at a glance, the evidence is one
                      disclosure away, and it stays collapsed so it cannot push the table around.
                    */}
                    {e.evidence && Object.keys(e.evidence).length > 0 ? (
                      <Drawer summary="Evidence">
                        <dl className="receipt__grid">
                          {Object.entries(e.evidence)
                            .sort(([a], [b]) => a.localeCompare(b))
                            .map(([key, value]) => (
                              <Fragment key={key}>
                                <dt>{key.replace(/_/g, " ")}</dt>
                                <dd className="mono">{value}</dd>
                              </Fragment>
                            ))}
                          <dt>entry hash</dt>
                          <dd className="mono">{e.entry_hash}</dd>
                          <dt>prev hash</dt>
                          <dd className="mono">{e.prev_hash}</dd>
                        </dl>
                      </Drawer>
                    ) : (
                      <span className="hint">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </DataTable>
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
