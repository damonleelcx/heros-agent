import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, NotFoundState, Pill, UnusableState } from "@/components/states";
import { DataTable, Num, PageFrame, Section, Stat, StatRow } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { RecordVisit } from "@/components/recordVisit";
import { timestamp } from "@/lib/format";
import {
  suspendTenant,
  reactivateTenant,
  setQuota,
  overrideEntitlement,
  startImpersonation,
} from "@/lib/actions";
import type { PlanOption, TenantView } from "@/lib/types";

/**
 * The tenant detail surface: view one tenant and run every per-tenant action against it.
 *
 * Each action form is rendered ONLY if the operator's role grants the capability (read from the same
 * permission map the backend enforces), and its friction is the server's classification — a reason for
 * a reversible action, and nothing here is irreversible so nothing types the target. If the role lacks
 * a capability, the surface shows a denial that names who holds it, never a control that would be
 * refused.
 *
 * Visiting a tenant records it as a recent subject for the command palette (FR32), so returning to it
 * later is two keystrokes rather than a search. That is a READ-path convenience: it changes nothing
 * about what the controls below require.
 */
export default async function TenantDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const { identity, sessionToken } = await requireIdentity();

  let tenant: TenantView | null = null;
  /*
   * The failure is kept AS ITS KIND rather than flattened to a message.
   *
   * A mistyped tenant id used to leave through `notFound()`, which renders the framework's own
   * unstyled 404 — no chrome, no acting principal, no palette, and no statement of which console the
   * operator is even on. Every other failure became "degraded", which told them the platform was
   * broken when they had simply pasted the wrong thing. Both are now rendered in place, inside the
   * shell, as the answer they actually are.
   */
  let failure: { kind: "not_found" | "unusable" | "degraded"; detail?: string } | null = null;
  try {
    tenant = await adminFetch<TenantView>(`/admin/api/tenants/${encodeURIComponent(id)}`, {
      sessionToken,
    });
  } catch (error) {
    const kind = error instanceof AdminApiError ? error.kind : "degraded";
    failure = {
      kind: kind === "not_found" || kind === "unusable" ? kind : "degraded",
      detail: error instanceof Error ? error.message : String(error),
    };
  }

  let plans: PlanOption[] = [];
  if (tenant && hasCapability(identity, "entitlement.override")) {
    try {
      const res = await adminFetch<{ plans: PlanOption[] }>("/admin/api/plans", { sessionToken });
      plans = res.plans ?? [];
    } catch {
      plans = [];
    }
  }

  const target = `tenant:${id}`;
  const overrides = Object.entries(tenant?.quota_overrides ?? {});

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame eyebrow="Tenant" title={id} lede={tenant ? `${tenant.plan_name} plan` : undefined}>
        {tenant ? (
          <RecordVisit
            subject={{
              id,
              label: id,
              hint: `${tenant.plan_name} · ${tenant.status}`,
              href: `/tenants/${encodeURIComponent(id)}`,
              kind: "tenant",
            }}
          />
        ) : null}

        {failure || !tenant ? (
          failure?.kind === "not_found" ? (
            <NotFoundState what="tenant" identifier={id} />
          ) : failure?.kind === "unusable" ? (
            <UnusableState what={`tenant ${id}`} detail={failure.detail} />
          ) : (
            <DegradedState what={`tenant ${id}`} detail={failure?.detail} />
          )
        ) : (
          <>
            <Section title="State">
              <StatRow>
                <Stat
                  label="Status"
                  small
                  value={
                    tenant.status === "suspended" ? (
                      <Pill tone="danger">Suspended</Pill>
                    ) : (
                      <Pill tone="ok">Active</Pill>
                    )
                  }
                  meta={
                    tenant.suspended_at
                      ? `Since ${timestamp(tenant.suspended_at)}`
                      : (tenant.suspension_reason ?? undefined)
                  }
                />
                <Stat
                  label="Autonomous merges"
                  small
                  tone={tenant.autonomous_merges_halted ? "warn" : undefined}
                  value={
                    tenant.autonomous_merges_halted ? (
                      <Pill tone="warn">Halted</Pill>
                    ) : (
                      <Pill tone="neutral">Running</Pill>
                    )
                  }
                  meta={tenant.halt_reason ?? undefined}
                />
                <Stat label="Plan" small value={tenant.plan_name} meta={tenant.plan_id} />
                <Stat
                  label="Config version"
                  small
                  value={<span className="mono">{tenant.plan_config_version}</span>}
                />
                <Stat
                  label="Gainshare consent"
                  small
                  value={tenant.gainshare_consent ? "Given" : "Not given"}
                />
              </StatRow>
            </Section>

            <Section title="Quota overrides" aside={`${overrides.length} set`} flush={overrides.length > 0}>
              {overrides.length === 0 ? (
                <p className="hint">None — this tenant resolves the plan&rsquo;s allowances.</p>
              ) : (
                <DataTable
                  caption="Allowances overridden for this tenant, replacing the plan's own."
                  columns={[{ label: "Limit" }, { label: "Allowance", numeric: true }]}
                >
                  {overrides.map(([limit, value]) => (
                    <tr key={limit}>
                      <th scope="row" className="mono">
                        {limit}
                      </th>
                      <td className="num">
                        <Num value={value} kind="quantity" />
                      </td>
                    </tr>
                  ))}
                </DataTable>
              )}
            </Section>

            <h2>Actions</h2>
            <div className="grid-2">
              {/* Suspend / reactivate */}
              <Section title="Lifecycle">
                {hasCapability(identity, "tenant.suspend") ? (
                  tenant.status === "suspended" ? (
                    <ActionForm
                      title="Reactivate tenant"
                      hint="Restores the tenant to active and resumes its autonomous merges."
                      submitLabel="Reactivate"
                      actionName="tenant.reactivate"
                      targetLabel={id}
                      action={reactivateTenant.bind(null, id)}
                    />
                  ) : (
                    <ActionForm
                      title="Suspend tenant"
                      hint="Halts this tenant's autonomous merges immediately. Reversible by reactivation."
                      submitLabel="Suspend"
                      danger
                      actionName="tenant.suspend"
                      targetLabel={id}
                      action={suspendTenant.bind(null, id)}
                    />
                  )
                ) : (
                  <DeniedState
                    capability="tenant.suspend"
                    description="Suspend or reactivate a tenant"
                    heldBy={holdersOf(identity, "tenant.suspend")}
                  />
                )}
              </Section>

              {/* Quota */}
              <Section title="Quota override">
                {hasCapability(identity, "tenant.quota") ? (
                  <ActionForm
                    title="Set quota override"
                    hint="Overrides one plan allowance for this tenant. Leave the value blank to clear the override and return to the plan's allowance."
                    submitLabel="Apply quota"
                    actionName="tenant.quota"
                    targetLabel={id}
                    action={setQuota.bind(null, id)}
                  >
                    <label htmlFor="limit">Limit</label>
                    <select id="limit" name="limit" defaultValue="seats">
                      <option value="seats">Seats</option>
                      <option value="retention_days">Retention days</option>
                      <option value="sum_band">SUM band (quantity)</option>
                      <option value="eval_compute">Eval compute</option>
                    </select>
                    <label htmlFor="value">New allowance (quantity — blank to clear)</label>
                    <input id="value" name="value" type="number" min={0} step="any" autoComplete="off" />
                  </ActionForm>
                ) : (
                  <DeniedState
                    capability="tenant.quota"
                    description="Adjust a tenant's quota"
                    heldBy={holdersOf(identity, "tenant.quota")}
                  />
                )}
              </Section>

              {/* Entitlement override */}
              <Section title="Entitlement override">
                {hasCapability(identity, "entitlement.override") ? (
                  <ActionForm
                    title="Override plan"
                    hint="Moves the tenant to a different published plan. Effective immediately, with no deploy. No price is entered here — plans are named and prices are references in configuration."
                    submitLabel="Apply override"
                    danger
                    actionName="entitlement.override"
                    targetLabel={id}
                    action={overrideEntitlement.bind(null, id)}
                  >
                    <label htmlFor="plan_ref">Move to plan</label>
                    <select id="plan_ref" name="plan_ref" defaultValue={tenant.plan_id}>
                      {plans.map((p) => (
                        <option key={p.plan_id} value={p.plan_id}>
                          {p.plan_name} — {p.features.join(", ")}
                        </option>
                      ))}
                    </select>
                  </ActionForm>
                ) : (
                  <DeniedState
                    capability="entitlement.override"
                    description="Override a tenant's plan or entitlement"
                    heldBy={holdersOf(identity, "entitlement.override")}
                  />
                )}
              </Section>

              {/* Impersonation */}
              <Section title="Impersonation">
                {hasCapability(identity, "impersonate.read") ? (
                  <ActionForm
                    title="Impersonate (read-only)"
                    hint="Opens a bounded, read-only window onto this tenant. Every action is logged as impersonation, and the session expires automatically."
                    submitLabel="Start read-only impersonation"
                    actionName="impersonate.start"
                    targetLabel={id}
                    action={startImpersonation}
                  >
                    <input type="hidden" name="tenant_id" value={id} />
                    <label htmlFor="ttl_seconds">Duration (minutes)</label>
                    <select id="ttl_seconds" name="ttl_seconds" defaultValue="1800">
                      <option value="900">15 minutes</option>
                      <option value="1800">30 minutes</option>
                      <option value="3600">60 minutes</option>
                    </select>
                  </ActionForm>
                ) : (
                  <DeniedState
                    capability="impersonate.read"
                    description="Start a read-scoped impersonation session"
                    heldBy={holdersOf(identity, "impersonate.read")}
                  />
                )}
              </Section>
            </div>
            <p className="hint">
              Target identifier for confirmations: <code>{target}</code>
            </p>
          </>
        )}
      </PageFrame>
    </OperatorShell>
  );
}
