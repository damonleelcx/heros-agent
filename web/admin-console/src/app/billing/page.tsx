import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, Pill } from "@/components/states";
import { DataTable, Drawer, Num, PageFrame, Section } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { CoverageStatement, Derived } from "@/components/derived";
import { quantity, timestamp } from "@/lib/format";
import { issueCredit, issueRefund } from "@/lib/actions";
import type { BillingOversight } from "@/lib/types";

/**
 * The billing oversight surface (Billing-Ops, not Support).
 *
 * It shows invoices, dunning, reconciliation and gainshare evidence. Quantities and provider
 * references are rendered; no amount is ever shown, because the amount lives with the PCI-compliant
 * provider and the console never holds one (FR28). A gainshare charge whose evidence does not hold is
 * surfaced as an EXCEPTION, not shown as valid. Corrections are additive and audited.
 *
 * The tenant is a URL parameter (FR33): an oversight view is a link, which is how a billing question
 * gets handed between people without anyone re-typing an identifier.
 */
export default async function BillingPage({
  searchParams,
}: {
  searchParams: Promise<{ tenant?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { tenant } = await searchParams;

  if (!hasCapability(identity, "billing.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Money" title="Billing oversight">
          <DeniedState
            capability="billing.read"
            description="View billing oversight"
            heldBy={holdersOf(identity, "billing.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  const canCorrect = hasCapability(identity, "billing.correct");

  let view: BillingOversight | null = null;
  let degraded: string | null = null;
  if (tenant) {
    try {
      view = await adminFetch<BillingOversight>(`/admin/api/billing/${encodeURIComponent(tenant)}`, {
        sessionToken,
      });
    } catch (error) {
      degraded = error instanceof AdminApiError ? error.message : String(error);
    }
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Money"
        title="Billing oversight"
        lede="Invoices, dunning, reconciliation and gainshare evidence for one tenant. Corrections are additive and audited — the original charge and its records stay intact."
      >
        <Section title="Choose a tenant" aside={view ? `period ${view.period}` : undefined}>
          <form method="get" role="search" className="form-row">
            <span>
              <label htmlFor="tenant">Tenant id</label>
              <input
                id="tenant"
                name="tenant"
                type="search"
                defaultValue={tenant ?? ""}
                autoComplete="off"
                required
              />
            </span>
            <button type="submit" className="primary">
              Open oversight
            </button>
          </form>
        </Section>

        {!tenant ? (
          <EmptyState what="tenant selected" hint="Enter a tenant id above, or find one with ⌘K." />
        ) : degraded ? (
          <DegradedState what={`billing for ${tenant}`} detail={degraded} />
        ) : !view ? (
          <DegradedState what="the billing service" />
        ) : (
          <>
            {/* 🔴 Link coverage FIRST, and beside the figures it qualifies — not in a footnote and not
                behind a link. A figure whose coverage is unknown is not rendered at all; the surface
                says coverage is unknown in its place. See components/derived.tsx. */}
            <Section
              title="What these figures count"
              aside={
                view.link_coverage.known ? (
                  view.link_coverage.complete ? (
                    <Pill tone="ok">complete coverage</Pill>
                  ) : (
                    <Pill tone="neutral">partial coverage</Pill>
                  )
                ) : (
                  <Pill tone="warn">coverage unknown</Pill>
                )
              }
            >
              <CoverageStatement coverage={view.link_coverage} />
              <div className="derived-row">
                <Derived
                  label="Metered spend under management"
                  figure={view.metered_sum}
                  coverage={view.link_coverage}
                />
                <Derived
                  label="Verified savings (gainshare basis)"
                  figure={view.gainshare_savings}
                  coverage={view.link_coverage}
                />
              </div>
            </Section>

            <Section
              title="Reconciliation"
              aside={
                view.reconciliation_degraded ? (
                  <Pill tone="danger">unknown</Pill>
                ) : view.reconciliation_matched ? (
                  <Pill tone="ok">matched</Pill>
                ) : (
                  <Pill tone="danger">drift</Pill>
                )
              }
            >
              {view.reconciliation_degraded ? (
                <DegradedState what="the billing provider" detail={view.reconciliation_detail} />
              ) : view.reconciliation_matched ? (
                <p className="state__body">
                  The platform&rsquo;s records agree with the provider for this period.
                </p>
              ) : (
                <div className="state state--degraded" role="alert">
                  <p className="state__title">Drift detected</p>
                  <ul>
                    {(view.drift ?? []).map((d, i) => (
                      <li key={i}>
                        {d.metric}: {d.detail} (platform {quantity(d.platform_quantity)} vs provider{" "}
                        {quantity(d.provider_quantity)})
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </Section>

            <Section
              title="Gainshare evidence"
              aside={
                view.exceptions > 0 ? (
                  <Pill tone="danger">{view.exceptions} exceptions</Pill>
                ) : (
                  <Pill tone="ok">clean</Pill>
                )
              }
              flush={Boolean(view.gainshare && view.gainshare.length > 0)}
            >
              {!view.gainshare || view.gainshare.length === 0 ? (
                <EmptyState what="gainshare charges this period" />
              ) : (
                <DataTable
                  caption="Each gainshare charge and the verified, merged evidence behind it."
                  columns={[
                    { label: "Charge" },
                    { label: "Verified deltas" },
                    { label: "Merge commits" },
                    { label: "Status" },
                  ]}
                >
                  {view.gainshare.map((g) => (
                    <tr key={g.event_id}>
                      <th scope="row" className="mono">
                        {g.event_id}
                      </th>
                      <td className="mono">{(g.verified_delta_refs ?? []).join(", ") || "—"}</td>
                      <td className="mono">{(g.merge_commits ?? []).join(", ") || "—"}</td>
                      <td>
                        {g.exception ? (
                          <>
                            <Pill tone="danger">exception</Pill>
                            <div className="hint">{g.exception}</div>
                          </>
                        ) : (
                          <Pill tone="ok">verified &amp; merged</Pill>
                        )}
                      </td>
                    </tr>
                  ))}
                </DataTable>
              )}
            </Section>

            {/* The plan trail, ABOVE the invoices, because it answers the first question an operator
                has about a tenant — which plan, since when, and on the strength of what. It is not
                period-scoped: a plan change carries no period, so the period-filtered invoice list
                below can never contain one. */}
            <Section
              title="Plan history"
              aside={`${(view.plan_history ?? []).length} changes, all periods`}
              flush={(view.plan_history ?? []).length > 0}
            >
              {(view.plan_history ?? []).length === 0 ? (
                <EmptyState
                  what="plan changes for this tenant"
                  hint="This tenant has never moved between plans."
                />
              ) : (
                <DataTable
                  caption="Every audited plan change, newest first. Entitlement moves are recorded, never deleted."
                  columns={[
                    { label: "Event" },
                    { label: "What changed" },
                    { label: "Status" },
                    { label: "Caused by" },
                    { label: "When" },
                  ]}
                >
                  {(view.plan_history ?? []).map((line) => (
                    <tr key={line.event_id}>
                      <th scope="row" className="mono">
                        {line.event_id}
                      </th>
                      <td>{line.reason || "—"}</td>
                      <td>
                        <Pill tone="neutral">{line.status}</Pill>
                      </td>
                      <td className="mono">{line.caused_by || "—"}</td>
                      <td>{timestamp(line.created_at)}</td>
                    </tr>
                  ))}
                </DataTable>
              )}
            </Section>

            <Section
              title="Invoices"
              aside={`${view.invoices.length} events, period ${view.period}`}
              flush={view.invoices.length > 0}
            >
              {view.invoices.length === 0 ? (
                <EmptyState what="billing events this period" />
              ) : (
                <DataTable
                  caption="Billing events for the period. Quantities, never amounts."
                  columns={[
                    { label: "Event" },
                    { label: "Type" },
                    { label: "Status" },
                    { label: "Quantity", numeric: true },
                    { label: "Counts" },
                    { label: "When" },
                    ...(canCorrect ? [{ label: "Correct" }] : []),
                  ]}
                >
                  {view.invoices.map((line) => (
                    <tr key={line.event_id}>
                      <th scope="row" className="mono">
                        {line.event_id}
                      </th>
                      <td>{line.type}</td>
                      <td>
                        {line.status === "pending" ? (
                          <Pill tone="warn">unsettled</Pill>
                        ) : (
                          <Pill tone="neutral">{line.status}</Pill>
                        )}
                      </td>
                      <td className="num">
                        <Num value={line.quantity} kind="quantity" />
                      </td>
                      <td>
                        {line.sum_derived ? (
                          <Pill tone="neutral">SUM · linked runs</Pill>
                        ) : (
                          <span className="hint">not SUM-derived</span>
                        )}
                      </td>
                      <td>{timestamp(line.created_at)}</td>
                      {canCorrect ? (
                        <td>
                          {/* 🔴 The KIND says what a charge is for; the TYPE says what kind of ledger
                              row it is. This condition read `type` against two KIND values —
                              "subscription" and "metered" — which no `type` ever takes, so the
                              credit/refund drawer never appeared against an ordinary charge and the
                              console's one money-correcting control was unreachable for the rows it
                              exists for. Found while adding link coverage to this table (P26). */}
                          {line.kind === "subscription" ||
                          line.kind === "metered" ||
                          line.type === "gainshare_charge" ? (
                            <Drawer summary="Credit / refund">
                              <ActionForm
                                title={`Credit ${line.event_id}`}
                                hint="Additive correction. The original charge stays intact."
                                submitLabel="Issue credit"
                                actionName="billing.credit"
                                targetLabel={line.event_id}
                                action={issueCredit.bind(null, tenant)}
                              >
                                <input type="hidden" name="against_event_id" value={line.event_id} />
                              </ActionForm>
                              <ActionForm
                                title={`Refund ${line.event_id}`}
                                hint="Additive money-back correction."
                                submitLabel="Issue refund"
                                danger
                                actionName="billing.refund"
                                targetLabel={line.event_id}
                                action={issueRefund.bind(null, tenant)}
                              >
                                <input type="hidden" name="against_event_id" value={line.event_id} />
                              </ActionForm>
                            </Drawer>
                          ) : (
                            <span className="hint">not a charge</span>
                          )}
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </DataTable>
              )}
            </Section>
          </>
        )}
      </PageFrame>
    </OperatorShell>
  );
}
