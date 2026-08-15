import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, NotMountedState, Pill } from "@/components/states";
import { DataTable, Num, PageFrame, Section } from "@/components/primitives";
import { ActionForm } from "@/components/actionForm";
import { setAgentCap, setAgentPlacement } from "@/lib/actions";
import type { AgentSpendView, AgentSpendRow, AdminIdentity } from "@/lib/types";

/**
 * The analysis agent's spend, its caps, and every tenant's placement (P30 tasks 6.5–6.7).
 *
 * # 🔴 `unpriced` is a WORD, never a zero
 *
 * A model with no published price produces a real token count and NO cost. Rendering a zero there
 * reports a spend nobody incurred — the most reassuring possible lie about a bill — so this page prints
 * "unpriced" and counts how many tenants are in that state, because a total that silently omits them is
 * a total a reader will take as complete.
 *
 * # 🔴 `defaulted` and `disabled` are DIFFERENT
 *
 * Q2 made `disabled` the default placement precisely so that enabling is deliberate. A tenant nobody
 * has considered and a tenant somebody looked at and switched off are the same VALUE and completely
 * different FACTS: one is an unopened question, the other is a decision. The platform carries
 * `placement_source` so this page can say which, and an operator reviewing the fleet can tell how much
 * of it has actually been reviewed.
 *
 * # Why every number here says it is an estimate
 *
 * Each is a token count multiplied through a price REFERENCE. It is not an invoice, the platform sets
 * `estimated: true` on the wire, and this page cannot decide to leave the label off.
 */
export default async function AgentSpendPage() {
  const { identity, sessionToken } = await requireIdentity();

  if (!hasCapability(identity, "agent.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Optimization" title="Analysis Spend">
          <DeniedState
            capability="agent.read"
            description="Read the platform analysis agent's definition, rehearsal and spend"
            heldBy={holdersOf(identity, "agent.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let view: AgentSpendView | null = null;
  let failure: { kind: string; message: string } | null = null;
  try {
    view = await adminFetch<AgentSpendView>("/admin/api/agent/spend", { sessionToken });
  } catch (error) {
    failure =
      error instanceof AdminApiError
        ? { kind: error.kind, message: error.message }
        : { kind: "degraded", message: String(error) };
  }

  const rows = view?.rows ?? [];

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Optimization"
        title="Analysis Spend"
        lede={
          <>
            What the analysis agent has spent, per tenant, and where it is allowed to run. Every cost
            below is an <strong>estimate</strong> derived from a token count and a price reference — it
            is not an invoice.
          </>
        }
      >
        {failure ? (
          failure.kind === "not_mounted" ? (
            <NotMountedState what="the analysis agent" detail={failure.message} />
          ) : (
            <DegradedState what="the analysis agent" detail={failure.message} />
          )
        ) : !view ? (
          <DegradedState what="the analysis agent" />
        ) : (
          <>
            <Section title="Caps" flush>
              <p>
                {view.fleet_cap_tokens > 0 ? (
                  <>
                    Fleet-wide ceiling: <Num value={view.fleet_cap_tokens} /> tokens. Reaching it stops
                    analysis <strong>before</strong> the provider call, and emits an event.
                  </>
                ) : (
                  /* 🔴 A REAL AND DANGEROUS STATE, said plainly rather than rendered as a blank cell.
                     No fleet cap means an analysis storm has no ceiling at all. */
                  <>
                    <Pill tone="warn">no fleet cap</Pill> Nothing bounds fleet-wide analysis spend. A
                    cap is checked before the provider call, so setting one is the difference between a
                    bounded cost and an unbounded one.
                  </>
                )}
              </p>
              {view.can_admin ? (
                <>
                  <p>
                    Editing a cap or a placement takes a <strong>reason</strong> and is recorded in the
                    audit chain. Setting a placement to <code>platform</code> makes this platform read
                    that tenant&rsquo;s source under a platform-held credential.
                  </p>
                  <p>
                    A cap of <strong>0 removes the ceiling</strong> rather than setting one of zero. It
                    is the only way to remove a cap, so it is offered — but a blank field is refused
                    rather than read as a zero, because &ldquo;I left it empty&rdquo; and &ldquo;make
                    this unbounded&rdquo; must not be the same submission.
                  </p>
                  {/* 🔴 The fleet control and the per-tenant control are ADJACENT and deliberately
                      unequal: the fleet one is the console's heaviest treatment, the per-tenant one is
                      the amber tier. Same argument as the kill switch — two spend ceilings that look
                      alike are two ceilings an operator can confuse under pressure, and only one of
                      them is fleet-wide. */}
                  <ActionForm
                    title="Set the FLEET-WIDE cap"
                    hint="The ceiling on analysis spend across every tenant. Checked before the provider call, so it bounds a cost rather than reporting one."
                    submitLabel="Set the fleet cap"
                    global
                    actionName="agent.cap"
                    targetLabel="fleet"
                    action={setAgentCap}
                  >
                    {/* The scope is a hidden field the ACTION reads, never a variable in a closure:
                        the platform spells "the fleet" as an empty tenant id, and this is what keeps a
                        blank tenant on the control below from meaning fleet-wide. */}
                    <input type="hidden" name="scope" value="fleet" />
                    <label htmlFor="fleet-cap-tokens">Ceiling in tokens (0 removes it)</label>
                    <input
                      id="fleet-cap-tokens"
                      name="tokens"
                      type="number"
                      min={0}
                      step={1}
                      autoComplete="off"
                      required
                    />
                  </ActionForm>
                  <ActionForm
                    title="Set one tenant's cap"
                    hint="A ceiling for a single tenant. The fleet cap still applies to it — the tighter of the two is what stops an analysis."
                    submitLabel="Set this tenant's cap"
                    danger
                    actionName="agent.cap"
                    action={setAgentCap}
                  >
                    <label htmlFor="tenant-cap-id">Tenant id</label>
                    <p className="hint">As it appears in the table below.</p>
                    <input id="tenant-cap-id" name="tenant_id" type="text" autoComplete="off" required />
                    <label htmlFor="tenant-cap-tokens">Ceiling in tokens (0 removes it)</label>
                    <input
                      id="tenant-cap-tokens"
                      name="tokens"
                      type="number"
                      min={0}
                      step={1}
                      autoComplete="off"
                      required
                    />
                  </ActionForm>
                </>
              ) : (
                <p>
                  You hold <code>agent.read</code> and not <code>agent.admin</code>, so the caps and
                  placements below are shown and not editable.
                </p>
              )}
            </Section>

            <Section
              title="Per tenant"
              aside={
                view.unpriced_tenants > 0
                  ? `${view.unpriced_tenants} unpriced — the total below is incomplete, not small`
                  : undefined
              }
              flush
            >
              {rows.length === 0 ? (
                <EmptyState
                  what="analysis spend"
                  hint="No tenant has run an analysis. With the default placement of `disabled`, that is the expected state until somebody enables one — it is not a fault."
                />
              ) : (
                <DataTable
                  caption="Each tenant's analysis spend, its cap, and where analysis is allowed to run for it."
                  columns={[
                    { label: "Tenant" },
                    { label: "Placement" },
                    { label: "Inferences" },
                    { label: "Tokens in" },
                    { label: "Tokens out" },
                    { label: "Estimated cost" },
                    { label: "Cap" },
                  ]}
                >
                  {rows.map((r) => (
                    <SpendRow key={r.tenant_id} row={r} />
                  ))}
                </DataTable>
              )}
            </Section>

            <PlacementSection view={view} identity={identity} />
          </>
        )}
      </PageFrame>
    </OperatorShell>
  );
}

/**
 * The placement editor — `#placements`, which the command palette has named all along.
 *
 * 🔴 The anchor is not decoration. `surfaces.ts` registers "Set a tenant's analysis placement" at
 * `/agent/spend#placements`, so an operator could find that command by name, press it, and land on a
 * page that rendered placements READ-ONLY with no such control anywhere on it. The palette was
 * advertising a capability the console did not have.
 *
 * # Why the options come from the platform
 *
 * `view.placements` is the closed set as the owning Go package declares it. Typing `platform`,
 * `customer` and `disabled` into this file would make the editor the fourth copy of that set, and the
 * failure would be silent in the worst direction: a placement added to the platform would simply not
 * appear here, on the one surface that exists to set it, and nothing would look broken.
 *
 * # Why an empty first option
 *
 * A `<select>` always has something selected, and whatever is selected first is a decision the console
 * made for the operator. FR37 is explicit that no field arrives pre-filled, and this field decides
 * whether a platform reads a customer's source — so it opens on nothing and `required` refuses the
 * submission until somebody chooses.
 */
function PlacementSection({ view, identity }: { view: AgentSpendView; identity: AdminIdentity }) {
  const placements = view.placements ?? [];
  return (
    <Section id="placements" title="Where analysis runs" flush>
      <p>
        A placement is per tenant and it is a <strong>decision</strong>, not a switch:{" "}
        <code>platform</code> has this platform read that tenant&rsquo;s source under a platform-held
        credential, <code>customer</code> has the tenant analyse on its own machine under its own
        credential and submit the result, and <code>disabled</code> runs nothing anywhere. The default
        is <code>disabled</code>, which is why the table above distinguishes a tenant somebody switched
        off from one nobody has looked at.
      </p>
      {!view.can_admin ? (
        <DeniedState
          capability="agent.admin"
          description="Set a tenant's analysis placement"
          heldBy={holdersOf(identity, "agent.admin")}
        />
      ) : placements.length === 0 ? (
        /* 🔴 Said, not hidden, and NOT replaced by a list typed here. This deployment's platform sent
           no placement set — an older build, or a surface that stopped carrying it — and the honest
           answer is that the console cannot offer a vocabulary it was not given. Falling back to three
           values written into this file is how the copy this control exists to avoid gets made. */
        <DegradedState
          what="the placement vocabulary"
          detail="The platform sent no placement set with this view, so the options cannot be listed. This console does not carry its own copy of them — a list typed here would go stale against the platform without anything looking wrong."
        />
      ) : (
        <ActionForm
          title="Set a tenant's placement"
          hint="Takes effect on the next analysis. Moving a tenant to `disabled` also marks its stored inferences stale — they are kept, not deleted, so nothing keeps rendering agent-authored facts as current."
          submitLabel="Set placement"
          danger
          actionName="agent.placement"
          action={setAgentPlacement}
        >
          <label htmlFor="placement-tenant">Tenant id</label>
          <p className="hint">As it appears in the table above.</p>
          <input id="placement-tenant" name="tenant_id" type="text" autoComplete="off" required />
          <label htmlFor="placement-value">Placement</label>
          <select id="placement-value" name="placement" defaultValue="" required>
            <option value="" disabled>
              Choose a placement
            </option>
            {placements.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </ActionForm>
      )}
    </Section>
  );
}

function SpendRow({ row }: { row: AgentSpendRow }) {
  return (
    <tr>
      <th scope="row">{row.tenant_id}</th>
      <td>
        <Pill tone={row.placement === "disabled" ? "neutral" : "accent"}>{row.placement}</Pill>{" "}
        {/* 🔴 The distinction the whole column exists for. `defaulted` means nobody has looked at this
            tenant; `explicit` means somebody decided. They are the same VALUE and different FACTS, and
            an operator reviewing the fleet needs to know how much of it has been reviewed. */}
        {row.placement_source === "defaulted" ? (
          <em>defaulted — nobody has set this</em>
        ) : (
          <em>set deliberately</em>
        )}
      </td>
      <td>
        <Num value={row.inferences} />
      </td>
      <td>
        <Num value={row.tokens_in} />
      </td>
      <td>
        <Num value={row.tokens_out} />
      </td>
      {/* 🔴 THREE states, not two, and the third was found by looking at the rendered page: a tenant
          that has run NOTHING was reporting "this model has no published price", which attributes an
          absence of spend to a pricing gap that may not exist. Nothing ran; there is nothing to price.

          🔴 And the number is rendered with `Num kind="quantity"`. The default `count` formatter is for
          WHOLE numbers and rounded 4.87 to "5" — an error of nearly three percent on a money figure,
          shown on the page somebody reads to decide whether a cap is needed. Also found by looking. */}
      <td>
        {row.inferences === 0 ? (
          <em>no analysis has run for this tenant</em>
        ) : row.priced ? (
          <Num value={row.estimated_cost} kind="quantity" />
        ) : (
          <em>unpriced — this model has no published price, so no cost can be estimated</em>
        )}
      </td>
      <td>
        {row.cap_tokens > 0 ? (
          <Num value={row.cap_tokens} />
        ) : (
          <em>none — the fleet cap still applies</em>
        )}
      </td>
    </tr>
  );
}
