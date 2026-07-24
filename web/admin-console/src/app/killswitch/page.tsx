import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, Pill } from "@/components/states";
import { DataTable, PageFrame, Section } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { timestamp } from "@/lib/format";
import { armKillSwitch, disarmKillSwitch } from "@/lib/actions";
import type { KillSwitchPage } from "@/lib/types";

/**
 * The kill-switch surface.
 *
 * The GLOBAL control is rendered visually distinct and higher-friction than the per-tenant one
 * (FR24): "halt this tenant" must never be mistaken for "halt the fleet". Disarming globally resumes
 * every tenant's autonomous merges, so when policy requires two-person authorization the form asks
 * for a second approver.
 *
 * When the state store is unreachable, the page renders DEGRADED — never "not armed" — because the
 * console must never show a fleet as running when it cannot tell (FR26).
 *
 * # Why the two controls have ids
 *
 * `#global-kill-switch` and `#per-tenant-kill-switch` are what the command palette navigates to
 * (FR32). Selecting "Arm the global kill switch" from the palette lands the operator on that
 * confirmation with its reason field empty and its friction intact. The palette gets them here fast;
 * it does not get them past anything (FR37).
 */
export default async function KillSwitchPage() {
  const { identity, sessionToken } = await requireIdentity();

  if (!hasCapability(identity, "killswitch.operate")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Controls" title="Kill switch">
          <DeniedState
            capability="killswitch.operate"
            description="Arm or disarm the autonomous-merge kill switch"
            heldBy={holdersOf(identity, "killswitch.operate")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let page: KillSwitchPage | null = null;
  let degraded: string | null = null;
  try {
    page = await adminFetch<KillSwitchPage>("/admin/api/killswitch", { sessionToken });
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  const twoPerson = page?.policy.global_disarm_requires_two_person ?? identity.kill_switch_two_person_disarm;
  const globalArmed = page?.global?.armed ?? false;
  const tenantArmed = (page?.states ?? []).filter((s) => s.armed && s.scope !== "global");

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Controls"
        title="Kill switch"
        lede="The platform brake on the autonomous optimizer. Arming halts further pull-request merges immediately, with no deploy. Reading the state fails closed to halt."
      >
        {degraded || (page && page.degraded) ? (
          <DegradedState what="the kill-switch state store" detail={degraded ?? page?.detail} />
        ) : null}

        {/* ── The GLOBAL control, deliberately distinct ── */}
        <Section
          id="global-kill-switch"
          title="Global kill switch"
          aside={
            globalArmed ? <Pill tone="danger">Armed — fleet halted</Pill> : <Pill tone="ok">Disarmed</Pill>
          }
        >
          {globalArmed ? (
            <ActionForm
              title="Disarm the GLOBAL kill switch"
              hint="This resumes autonomous merges for EVERY tenant. It is the riskiest direction."
              submitLabel="Resume the entire fleet"
              global
              actionName="killswitch.disarm"
              targetLabel="global"
              action={disarmKillSwitch}
            >
              <input type="hidden" name="scope" value="global" />
              {twoPerson ? (
                <>
                  <label htmlFor="second_approver">
                    Second approver (a different admin who holds this capability)
                  </label>
                  <p className="hint">
                    Resuming the fleet requires two-person authorization. Enter the admin id of your
                    co-approver.
                  </p>
                  <input id="second_approver" name="second_approver" type="text" autoComplete="off" required />
                </>
              ) : null}
            </ActionForm>
          ) : (
            <ActionForm
              title="Arm the GLOBAL kill switch"
              hint="Halts EVERY tenant's autonomous merges immediately. Use for a bad model, a provider incident, or a runaway search across the fleet."
              submitLabel="Halt the entire fleet"
              global
              actionName="killswitch.arm"
              targetLabel="global"
              action={armKillSwitch}
            >
              <input type="hidden" name="scope" value="global" />
            </ActionForm>
          )}
        </Section>

        {/* ── Per-tenant control, ordinary danger styling ── */}
        <Section
          id="per-tenant-kill-switch"
          title="Per-tenant kill switch"
          aside={tenantArmed.length > 0 ? <Pill tone="warn">{tenantArmed.length} armed</Pill> : undefined}
        >
          <p className="hint">Halts a single tenant. Other tenants continue to operate.</p>
          <ActionForm
            title="Arm per-tenant kill switch"
            hint="Enter the tenant id to halt only that tenant."
            submitLabel="Halt this tenant"
            danger
            actionName="killswitch.arm"
            action={armKillSwitch}
          >
            <label htmlFor="tenant_id">Tenant id</label>
            <input id="tenant_id" name="tenant_id" type="text" autoComplete="off" required />
          </ActionForm>
        </Section>

        <Section
          title="Current state"
          aside={page ? `${page.states.length} scopes` : undefined}
          flush={Boolean(page && page.states.length > 0)}
        >
          {page && page.states.length > 0 ? (
            <DataTable
              caption="Every kill-switch scope with a recorded state."
              columns={[
                { label: "Scope" },
                { label: "State" },
                { label: "Set by" },
                { label: "Reason" },
                { label: "When" },
              ]}
            >
              {page.states.map((s) => (
                <tr key={s.scope}>
                  <th scope="row" className="mono">
                    {s.scope}
                  </th>
                  <td>{s.armed ? <Pill tone="danger">Armed</Pill> : <Pill tone="ok">Disarmed</Pill>}</td>
                  <td className="mono">{s.set_by ?? "—"}</td>
                  <td>{s.reason ?? "—"}</td>
                  <td>{timestamp(s.set_at)}</td>
                </tr>
              ))}
            </DataTable>
          ) : degraded ? null : (
            <EmptyState what="armed scopes" hint="Nothing is halted right now." />
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
