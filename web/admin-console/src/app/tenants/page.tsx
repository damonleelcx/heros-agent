import Link from "next/link";
import { requireIdentity } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, EmptyState, Pill } from "@/components/states";
import { DataTable, PageFrame, Section } from "@/components/primitives";
import type { TenantView } from "@/lib/types";

/**
 * The tenants surface: search/view, and the entry point to every per-tenant action.
 *
 * It reads the tenant list through the BFF, which resolves each tenant's "autonomous merges halted"
 * flag through the SAME admission gate the P6 loop consults — so a tenant this page shows as running
 * really is running, and one it shows as halted really cannot merge.
 *
 * The search term lives in the URL (FR33), so a filtered view is linkable, restorable, and survives
 * back/forward exactly. An operator pasting this URL into an incident channel hands over a view rather
 * than a description of one — and the recipient's own permissions still govern what renders.
 */
export default async function TenantsPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { q } = await searchParams;

  let tenants: TenantView[] = [];
  let degraded: string | null = null;
  try {
    const res = await adminFetch<{ tenants: TenantView[] }>(
      `/admin/api/tenants${q ? `?q=${encodeURIComponent(q)}` : ""}`,
      { sessionToken },
    );
    tenants = res.tenants ?? [];
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  const halted = tenants.filter((t) => t.autonomous_merges_halted).length;

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Subjects"
        title="Tenants"
        lede="Every tenant on the platform. Suspending a tenant halts its autonomous merges; the status here is resolved through the same gate the optimizer loop reads before every merge."
      >
        <Section title="Find a tenant">
          <form method="get" role="search" className="form-row">
            <span>
              <label htmlFor="q">Search by tenant or plan name</label>
              <input
                id="q"
                name="q"
                type="search"
                defaultValue={q ?? ""}
                autoComplete="off"
                placeholder="acme, Enterprise"
              />
            </span>
            <button type="submit" className="primary">
              Search
            </button>
          </form>
          <p className="hint">
            Or press <kbd>⌘K</kbd> and type a tenant&rsquo;s name — the palette finds subjects by name,
            so no identifier has to be recalled from memory.
          </p>
        </Section>

        <Section
          title={q ? `Tenants matching “${q}”` : "Tenants"}
          aside={
            <>
              <span>{tenants.length} shown</span>
              {halted > 0 ? <Pill tone="warn">{halted} halted</Pill> : null}
            </>
          }
          flush
        >
          {degraded ? (
            <div className="section__body">
              <DegradedState what="the tenant directory" detail={degraded} />
            </div>
          ) : tenants.length === 0 ? (
            <div className="section__body">
              <EmptyState what="tenants" hint={q ? "Try a different search." : undefined} />
            </div>
          ) : (
            <DataTable
              caption="Tenants, their plan, and whether their autonomous merges are currently halted."
              columns={[
                { label: "Tenant" },
                { label: "Plan" },
                { label: "Status" },
                { label: "Autonomous merges" },
                { label: "Config version" },
              ]}
            >
              {tenants.map((t) => (
                <tr key={t.tenant_id}>
                  <th scope="row">
                    <Link href={`/tenants/${encodeURIComponent(t.tenant_id)}`}>{t.tenant_id}</Link>
                  </th>
                  <td>{t.plan_name}</td>
                  <td>
                    {t.status === "suspended" ? (
                      <Pill tone="danger">Suspended</Pill>
                    ) : (
                      <Pill tone="ok">Active</Pill>
                    )}
                  </td>
                  <td>
                    {t.autonomous_merges_halted ? (
                      <Pill tone="warn">Halted</Pill>
                    ) : (
                      <Pill tone="neutral">Running</Pill>
                    )}
                    {t.halt_reason ? <div className="hint">{t.halt_reason}</div> : null}
                  </td>
                  <td className="mono">{t.plan_config_version}</td>
                </tr>
              ))}
            </DataTable>
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
