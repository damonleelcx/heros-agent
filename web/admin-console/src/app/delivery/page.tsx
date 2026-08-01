import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, NotMountedState, Pill } from "@/components/states";
import { DataTable, Num, PageFrame, Section, Stat, StatRow } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import type { DeliveryView, MergeState } from "@/lib/types";

/**
 * The delivery oversight surface (P26 wave 26b) — READ-ONLY.
 *
 * # What it is for
 *
 * "Did this tenant's change reach their repository, and if not, why?" was answerable only by opening a
 * reason-required, time-bounded, audited impersonation session into the customer's own console. That
 * made the platform's most privileged read the routine tool for a question an aggregate answers, which
 * is a data-protection cost with no product need behind it.
 *
 * # 🔴 A merge is OBSERVED, never inferred
 *
 * A pull request that closed may have been merged, squashed, rebased, or abandoned, and only one of
 * those is a delivery. So the outcome has three values and the third — *state unknown* — is rendered
 * as itself rather than replaced by the most likely one.
 *
 * # 🔴 Nothing here acts
 *
 * No control on this page opens, closes, retries or merges a delivery. Delivery is downstream of
 * verification and never a path around it, and in the default mode the platform holds NO forge
 * credential — an operator "retry" would have to create one. The page shows a problem it cannot act
 * on, which is this phase's deliberate boundary rather than an omission.
 */

/** MERGE_TONE maps the three outcomes onto the console's status vocabulary.
 *
 * `unknown` is NEUTRAL, not a hazard: not having observed a merge yet is an ordinary state of an open
 * pull request, and painting it with the hazard palette would spend the colour the kill switch needs
 * on the most common row on the page (FR31). */
const MERGE_TONE: Record<MergeState, "ok" | "neutral"> = {
  merged: "ok",
  closed_unmerged: "neutral",
  unknown: "neutral",
};

const MERGE_LABEL: Record<MergeState, string> = {
  merged: "merged (observed)",
  closed_unmerged: "closed unmerged",
  unknown: "state unknown",
};

export default async function DeliveryPage({
  searchParams,
}: {
  searchParams: Promise<{ tenant?: string; merge?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { tenant, merge } = await searchParams;

  if (!hasCapability(identity, "delivery.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Delivery" title="Delivery oversight">
          <DeniedState
            capability="delivery.read"
            description="Read delivery records and the change-delivery rollout picture"
            heldBy={holdersOf(identity, "delivery.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  const path = tenant ? `/admin/api/delivery/${encodeURIComponent(tenant)}` : "/admin/api/delivery";
  let view: DeliveryView | null = null;
  let failure: { kind: string; message: string } | null = null;
  try {
    view = await adminFetch<DeliveryView>(path, { sessionToken });
  } catch (error) {
    failure =
      error instanceof AdminApiError
        ? { kind: error.kind, message: error.message }
        : { kind: "degraded", message: String(error) };
  }

  // The drill-down is a URL, so a delivery question is a link an operator can paste into a ticket.
  const rows = (view?.rows ?? []).filter((r) => !merge || merge === "all" || r.merge === merge);

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Delivery"
        title={tenant ? `Delivery — ${tenant}` : "Delivery oversight"}
        lede={
          <>
            P12 delivery records and the ADR-010 change-delivery picture. A merge is shown as{" "}
            <strong>observed</strong> — never inferred from a pull request closing. This surface reads
            and does nothing: no control here opens, closes, retries or merges a delivery.
          </>
        }
      >
        <Section title="Scope" aside={view?.source}>
          <form method="get" role="search" className="form-row">
            <span>
              <label htmlFor="tenant">Tenant id</label>
              <input
                id="tenant"
                name="tenant"
                type="search"
                defaultValue={tenant ?? ""}
                autoComplete="off"
                placeholder="leave empty for the fleet"
              />
            </span>
            <button type="submit" className="primary">
              Open
            </button>
          </form>
        </Section>

        {failure ? (
          failure.kind === "not_mounted" ? (
            <NotMountedState what="delivery oversight" detail={failure.message} />
          ) : failure.kind === "not_found" ? (
            <EmptyState what="tenant with that identifier" hint="Check what you pasted." />
          ) : (
            <DegradedState what="the delivery record" detail={failure.message} />
          )
        ) : !view ? (
          <DegradedState what="the delivery record" />
        ) : view.degraded ? (
          <DegradedState what="the delivery record" detail={view.detail} />
        ) : (
          <Tabs
            tabs={[
              {
                id: "deliveries",
                label: "Deliveries",
                content: (
                  <>
                    <Section title="Fleet counts" aside="every count drills down">
                      <StatRow>
                        {view.counts.map((c) => (
                          <Stat
                            key={c.label}
                            label={c.label.replace(/_/g, " ")}
                            value={
                              /* 🔴 An empty aggregate is stated, never rendered as `0` — a reader takes
                                 a zero for a measured value. */
                              c.value === 0 ? (
                                <span className="derived__withheld">no records</span>
                              ) : (
                                <a href={c.drill_down}>
                                  <Num value={c.value} />
                                </a>
                              )
                            }
                            small={c.value === 0}
                          />
                        ))}
                      </StatRow>
                    </Section>

                    <Section
                      title="Delivery records"
                      aside={merge && merge !== "all" ? `filtered: ${merge}` : undefined}
                      flush={rows.length > 0}
                    >
                      {rows.length === 0 ? (
                        <EmptyState
                          what="delivery records for this scope"
                          hint="A delivery appears here once a pull request has been opened for it."
                        />
                      ) : (
                        <DataTable
                          caption="Each delivery, the credential path that opened it, and its OBSERVED merge outcome."
                          columns={[
                            { label: "Delivery" },
                            { label: "Tenant" },
                            { label: "Target" },
                            { label: "Mode" },
                            { label: "Lifecycle" },
                            { label: "Merge outcome" },
                            { label: "Merge commit" },
                          ]}
                        >
                          {rows.map((r) => (
                            <tr key={`${r.tenant_id}-${r.delivery_id}`}>
                              <th scope="row" className="mono">
                                {r.delivery_id.slice(0, 12)}
                              </th>
                              <td className="mono">{r.tenant_id}</td>
                              <td className="mono">{r.target}</td>
                              <td>{r.mode}</td>
                              <td>{r.state}</td>
                              <td>
                                <Pill tone={MERGE_TONE[r.merge]}>{MERGE_LABEL[r.merge]}</Pill>
                              </td>
                              <td className="mono">
                                {r.merge_commit ? (
                                  r.audit_target ? (
                                    <a href={`/audit?target=${encodeURIComponent(r.audit_target)}`}>
                                      {r.merge_commit.slice(0, 12)}
                                    </a>
                                  ) : (
                                    r.merge_commit.slice(0, 12)
                                  )
                                ) : (
                                  "—"
                                )}
                              </td>
                            </tr>
                          ))}
                        </DataTable>
                      )}
                    </Section>
                  </>
                ),
              },
              {
                id: "undeliverable",
                label: "Undeliverable",
                content: (
                  <Section
                    title="Undeliverable changes, by typed cause"
                    aside={`${view.undeliverable_total} cells`}
                    flush={view.undeliverable.length > 0}
                  >
                    {/* 🔴 Never a single total. The three causes are answered by three different
                        people, and one combined number tells all three of them the same useless
                        thing. They are listed in EVALUATION order — nobody, you, the platform — so a
                        permanent boundary is read before a backlog item. */}
                    {view.undeliverable.length === 0 ? (
                      <EmptyState what="undeliverable change cells" />
                    ) : (
                      <DataTable
                        caption="Each typed cause, who can close it, and what would close it."
                        columns={[
                          { label: "Cause" },
                          { label: "Whose move" },
                          { label: "Cells", numeric: true },
                          { label: "What it means" },
                          { label: "Would be closed by" },
                        ]}
                      >
                        {view.undeliverable.map((c) => (
                          <tr key={c.cause}>
                            <th scope="row" className="mono">
                              {c.cause}
                            </th>
                            <td>{c.owner}</td>
                            <td className="num">
                              <Num value={c.count} />
                            </td>
                            <td>{c.label}</td>
                            <td>
                              {/* A permanent boundary names NO artifact. Attaching one would turn
                                  "this cannot be built" into "this has not been built yet". */}
                              {c.permanent ? (
                                <Pill tone="neutral">a boundary — nothing would</Pill>
                              ) : (
                                <span className="mono">{c.missing_artifact || "—"}</span>
                              )}
                            </td>
                          </tr>
                        ))}
                      </DataTable>
                    )}
                  </Section>
                ),
              },
              {
                id: "rollout",
                label: "Rollout picture",
                content: (
                  <Section title="What each route carries" aside="ADR-010" flush>
                    <DataTable
                      caption="The change-delivery table: for each change kind, what the source and runtime routes do."
                      columns={[
                        { label: "Axis" },
                        { label: "Change" },
                        { label: "Route" },
                        { label: "Status" },
                        { label: "Cause" },
                        { label: "Whose move" },
                      ]}
                    >
                      {view.rollout_stages.map((r) => (
                        <tr key={`${r.axis}-${r.change}-${r.route}`}>
                          <th scope="row">{r.axis}</th>
                          <td className="mono">{r.change}</td>
                          <td>{r.route}</td>
                          <td>
                            <Pill tone={r.status === "delivers" ? "ok" : "neutral"}>{r.status}</Pill>
                          </td>
                          <td className="mono">{r.cause || "—"}</td>
                          <td>{r.owner || "—"}</td>
                        </tr>
                      ))}
                    </DataTable>
                  </Section>
                ),
              },
              {
                id: "chain",
                label: "Audit coverage",
                content: (
                  <Section title="What the audit chain records about merges">
                    <p className="state__body">{view.merge_coverage.statement}</p>
                    <DataTable
                      caption="Each merge path and whether the hash chain records it."
                      columns={[{ label: "Merge path" }, { label: "In the chain" }, { label: "Read at" }]}
                    >
                      {view.merge_coverage.covered.map((p) => (
                        <tr key={p.id}>
                          <th scope="row">{p.name}</th>
                          <td>
                            <Pill tone="ok">recorded</Pill>
                          </td>
                          <td>
                            <a href="/audit">/audit</a>
                          </td>
                        </tr>
                      ))}
                      {view.merge_coverage.not_covered.map((p) => (
                        <tr key={p.id}>
                          <th scope="row">{p.name}</th>
                          <td>
                            <Pill tone="neutral">not recorded</Pill>
                          </td>
                          <td>{p.readable_at ?? "—"}</td>
                        </tr>
                      ))}
                    </DataTable>
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
