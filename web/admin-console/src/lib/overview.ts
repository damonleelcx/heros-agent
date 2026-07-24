import "server-only";
import { adminFetch, AdminApiError } from "./adminApi";
import type {
  AuditEntry,
  AuditView,
  FleetHealth,
  ImpersonationView,
  KillSwitchPage,
  ReadModel,
  TenantView,
} from "./types";

/**
 * overview.ts composes the operating picture (FR34).
 *
 * # It is a read model, not a pipeline
 *
 * Every figure here already exists behind an endpoint the console was already calling: the kill-switch
 * state, the queue, the tenant directory, the active impersonations, the P2.5 anomaly aggregate, the
 * audit tail. This function fans out to those and assembles the answer to the two questions an
 * operator opens the console with — **is anything halted, and is anything wrong** — so the answer is
 * not four navigations away. It stands up nothing new (design.md Decision 8).
 *
 * # Why each panel fails independently
 *
 * The panels are gathered with `allSettled`, not `all`. A single unreachable subsystem must degrade
 * ITS panel and leave the rest of the picture standing — the alternative is an operator who cannot see
 * that the fleet is halted because the billing aggregate timed out. A panel that could not be read
 * says so; it never renders as "nothing wrong" (FR26).
 *
 * # Why every panel carries a permission verdict
 *
 * A capability the role does not grant renders as a denial naming who holds it, not as an empty panel
 * (FR22). "Support cannot see the queue" and "the queue is empty" are different facts.
 */

export type PanelState = "ok" | "denied" | "degraded";

export type OverviewPanel<T> = {
  state: PanelState;
  detail?: string;
  data?: T;
};

export type OperatingPicture = {
  /** The instant this picture was assembled, in RFC 3339. Every live figure is read against it. */
  as_of: string;
  killswitch: OverviewPanel<{
    global_armed: boolean;
    tenant_armed: Array<{ scope: string; reason?: string; set_by?: string }>;
  }>;
  fleet: OverviewPanel<FleetHealth>;
  tenants: OverviewPanel<{ total: number; suspended: number; halted: number }>;
  impersonations: OverviewPanel<ImpersonationView[]>;
  anomalies: OverviewPanel<ReadModel>;
  recent: OverviewPanel<AuditEntry[]>;
};

function panelFrom<T>(result: PromiseSettledResult<T>): OverviewPanel<T> {
  if (result.status === "fulfilled") return { state: "ok", data: result.value };
  const error = result.reason;
  if (error instanceof AdminApiError && error.kind === "denied") {
    return { state: "denied", detail: error.message };
  }
  return { state: "degraded", detail: error instanceof Error ? error.message : String(error) };
}

/** DENIED is the panel a role simply cannot see. It is distinct from a panel that failed to load. */
const DENIED: OverviewPanel<never> = { state: "denied", detail: "Your role does not grant this." };

export async function loadOperatingPicture(
  sessionToken: string,
  capabilities: string[],
): Promise<OperatingPicture> {
  const can = (c: string) => capabilities.includes(c);

  const [killswitch, fleet, tenants, impersonations, anomalies, recent] = await Promise.allSettled([
    can("killswitch.operate")
      ? adminFetch<KillSwitchPage>("/admin/api/killswitch", { sessionToken })
      : Promise.reject(new AdminApiError("denied", 403, "killswitch.operate is not granted to your role")),
    can("job.read")
      ? adminFetch<FleetHealth>("/admin/api/fleet", { sessionToken })
      : Promise.reject(new AdminApiError("denied", 403, "job.read is not granted to your role")),
    can("tenant.read")
      ? adminFetch<{ tenants: TenantView[] }>("/admin/api/tenants", { sessionToken })
      : Promise.reject(new AdminApiError("denied", 403, "tenant.read is not granted to your role")),
    can("impersonate.read")
      ? adminFetch<{ sessions: ImpersonationView[] }>("/admin/api/impersonation", { sessionToken })
      : Promise.reject(new AdminApiError("denied", 403, "impersonate.read is not granted to your role")),
    can("crosstenant.read")
      ? adminFetch<ReadModel>("/admin/api/crosstenant/anomalies", { sessionToken })
      : Promise.reject(new AdminApiError("denied", 403, "crosstenant.read is not granted to your role")),
    can("audit.read")
      ? adminFetch<AuditView>("/admin/api/audit?limit=8", { sessionToken })
      : Promise.reject(new AdminApiError("denied", 403, "audit.read is not granted to your role")),
  ]);

  const ksPanel = panelFrom(killswitch);
  const tenantPanel = panelFrom(tenants);
  const impPanel = panelFrom(impersonations);
  const auditPanel = panelFrom(recent);

  return {
    as_of: new Date().toISOString(),
    killswitch: can("killswitch.operate")
      ? {
          state: ksPanel.data?.degraded ? "degraded" : ksPanel.state,
          detail: ksPanel.data?.detail ?? ksPanel.detail,
          data: ksPanel.data
            ? {
                global_armed: ksPanel.data.global?.armed ?? false,
                tenant_armed: (ksPanel.data.states ?? [])
                  .filter((s) => s.armed && s.scope !== "global")
                  .map((s) => ({ scope: s.scope, reason: s.reason, set_by: s.set_by })),
              }
            : undefined,
        }
      : DENIED,
    fleet: can("job.read") ? panelFrom(fleet) : DENIED,
    tenants: can("tenant.read")
      ? {
          state: tenantPanel.state,
          detail: tenantPanel.detail,
          data: tenantPanel.data
            ? {
                total: tenantPanel.data.tenants.length,
                suspended: tenantPanel.data.tenants.filter((t) => t.status === "suspended").length,
                halted: tenantPanel.data.tenants.filter((t) => t.autonomous_merges_halted).length,
              }
            : undefined,
        }
      : DENIED,
    impersonations: can("impersonate.read")
      ? { state: impPanel.state, detail: impPanel.detail, data: impPanel.data?.sessions ?? [] }
      : DENIED,
    anomalies: can("crosstenant.read") ? panelFrom(anomalies) : DENIED,
    recent: can("audit.read")
      ? { state: auditPanel.state, detail: auditPanel.detail, data: auditPanel.data?.entries ?? [] }
      : DENIED,
  };
}
