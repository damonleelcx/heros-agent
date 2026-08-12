import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState, NotMountedState, Pill } from "@/components/states";
import { DataTable, Num, PageFrame, Section } from "@/components/primitives";
import type { AgentSpendView, AgentSpendRow } from "@/lib/types";

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
                <p>
                  Editing a cap or a placement takes a <strong>reason</strong> and is recorded in the
                  audit chain. Setting a placement to <code>platform</code> makes this platform read
                  that tenant&rsquo;s source under a platform-held credential.
                </p>
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
          </>
        )}
      </PageFrame>
    </OperatorShell>
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
