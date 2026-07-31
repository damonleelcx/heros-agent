import type { BillingView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import {
  PageFrame,
  Section,
  Chip,
  Empty,
  Failure,
  DataTable,
  Banner,
  Value,
  Stat,
  Stats,
  Row,
} from "@/components/primitives";
import { usd2, score, integer } from "@/lib/format";
import { CAPABILITIES } from "@/lib/entitlements";
import { LinkCoverage } from "@/components/linkCoverage";
import { AcceptanceHistory, type AcceptanceRow, type PendingRow } from "@/components/legalAcceptance";
import { platformFetch } from "@/lib/platformApi";
import { legalAcceptances } from "@/lib/legalPaths";

export const dynamic = "force-dynamic";

type LegalAcceptanceView = {
  accepted?: AcceptanceRow[];
  pending?: PendingRow[];
  pending_unknown?: boolean;
};

export default async function AccountPage() {
  const { outcome, session } = await load<BillingView>((paths) => paths.billing(), [
    "plan_name",
    "period",
    "sum_unit",
    "state",
  ]);

  /*
   * The acceptance history is fetched SEPARATELY from billing, and its failure is not billing's failure
   * (task 10.1). A tenant whose consent surface is not mounted still gets their plan and their spend —
   * and the agreements section says it could not be read, rather than the whole page rendering a
   * failure for a section that is not the reason they came.
   *
   * 🔴 The path carries no tenant. The platform reads tenant and principal from the authenticated
   * session on its own side, so there is no parameter here that a caller could widen.
   */
  const consent = await platformFetch<LegalAcceptanceView>(legalAcceptances(), {
    tenantId: session.tenantId,
  });
  return (
    <PageFrame
      eyebrow="Account"
      title={session.tenantId}
      lede="What this tenant is on, what it entitles, and what this period has cost."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="account" />
      ) : (
        <Body billing={outcome.data} />
      )}

      <AcceptanceHistory
        accepted={consent.ok ? (consent.data.accepted ?? []) : []}
        pending={consent.ok ? (consent.data.pending ?? []) : []}
        // A failed read and a manifest outage are both "we do not know what is outstanding". Neither may
        // render as "nothing is outstanding" — that would silently clear the gate.
        unknown={!consent.ok || Boolean(consent.data.pending_unknown)}
      />
    </PageFrame>
  );
}

function Body({ billing }: { billing: BillingView }) {
  const entitlements = billing.entitlements ?? [];
  const meters = billing.meters ?? [];
  const byFeature = new Map(entitlements.map((row) => [row.feature, row]));

  return (
    <>
      <Section title="Plan" aside={<span className="mono">config {billing.plan_config_version}</span>}>
        <Row>
          <Chip variant="plan">{billing.plan_name}</Chip>
          <Chip title="the period this view covers">{billing.period}</Chip>
        </Row>
        {billing.empty ? (
          <p className="hint">
            Nothing has been recorded for this period yet. That is a real state, not a failure to load.
          </p>
        ) : null}
      </Section>

      {/*
        🔴 FR15 in table form: a capability the plan does not include is LISTED, named, and paired with
        what unlocks it. Hiding it would leave a reader unable to tell a product that cannot do
        something from a plan that does not include it — and the second has an answer.
      */}
      <Section title="What the console can do on this plan" aside="named, never hidden">
        <DataTable
          caption="Each console capability, whether this plan includes it, and what the platform says unlocks it"
          columns={[
            { key: "cap", label: "Capability" },
            { key: "state", label: "On this plan" },
            { key: "why", label: "What the platform says" },
          ]}
        >
          <tbody>
            {CAPABILITIES.map((capability) => {
              const row = capability.feature === null ? undefined : byFeature.get(capability.feature);
              const available = capability.feature === null || row?.included === true;
              return (
                <tr key={capability.id}>
                  <td>
                    <Value flags={available ? [] : ["gated"]} showQualifiers={false}>
                      <span className="text-sm">{capability.label}</span>
                    </Value>
                    <p className="caption mt-1">{capability.description}</p>
                  </td>
                  <td>{available ? <Chip tone="ok">included</Chip> : <Chip tone="unknown">not included</Chip>}</td>
                  <td>
                    {available ? (
                      <span className="caption">not gated</span>
                    ) : (
                      <span className="qualifier">
                        <span className="qualifier__badge">gated</span>
                        <span className="qualifier__copy">
                          {row?.upgrade_plan_name
                            ? `${row.upgrade_plan_name} plan`
                            : `${capability.unlockedBy} plan (the platform returned no entitlement row for this feature)`}
                          {capability.automationLevel ? `, at ${capability.automationLevel} automation` : ""}
                          {row?.reason ? ` — ${row.reason}` : ""}
                        </span>
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </DataTable>
      </Section>

      <Section title="Entitlements the platform enforces" aside="the same rows the gate reads">
        {entitlements.length === 0 ? (
          <Empty title="The platform returned no entitlement rows for this plan." />
        ) : (
          <DataTable
            caption="Every feature the plan resolves, and whether this tenant holds it"
            columns={[
              { key: "feature", label: "Feature" },
              { key: "included", label: "Included" },
              { key: "unlocks", label: "Unlocked by" },
            ]}
          >
            <tbody>
              {entitlements.map((row) => (
                <tr key={row.feature}>
                  <td>
                    <span className="text-sm">{row.label}</span>
                    <p className="caption mono mt-1">{row.feature}</p>
                  </td>
                  <td>{row.included ? <Chip tone="ok">yes</Chip> : <Chip tone="unknown">no</Chip>}</td>
                  <td>
                    {row.included ? (
                      <span className="caption">already included</span>
                    ) : (
                      <span className="caption">
                        {row.upgrade_plan_name ?? row.upgrade_plan ?? "a higher plan"}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>

      <Section title="This period" aside={billing.sum_unit}>
        <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:gap-10">
          <Stats>
            <Stat
              label="Spend this period"
              value={usd2(billing.sum)}
              unit={billing.sum_unit}
              note="SUM reflects LINKED runs only — the console computes no total of its own, and unlinked runs are never estimated"
            />
          </Stats>
          {/*
            🔴 FR17: the completeness of the figure is part of the figure. Link coverage sits beside SUM,
            not in a footnote, and distinguishes complete from unknown so a partial figure is never read
            as the whole.
          */}
          <div className="w-full lg:max-w-md">
            <LinkCoverage coverage={billing.link_coverage} />
          </div>
        </div>
        {meters.length === 0 ? (
          <Empty title="Nothing metered in this period yet." />
        ) : (
          <DataTable
            caption="Every metered quantity for this period, against the plan's allowance"
            columns={[
              { key: "metric", label: "Metric" },
              { key: "value", label: "Used", numeric: true },
              { key: "allowed", label: "Allowance", numeric: true },
            ]}
          >
            <tbody>
              {meters.map((meter) => (
                <tr key={meter.metric}>
                  <td>
                    <span className="text-sm">{meter.label}</span>
                    <p className="caption mono mt-1">
                      {meter.metric} · {meter.unit}
                    </p>
                  </td>
                  <td className="num">
                    <Value flags={meter.over ? ["low-confidence"] : []} showQualifiers={false}>
                      <span className="mono">{score(meter.value)}</span>
                    </Value>
                  </td>
                  <td className="num mono">{meter.unlimited ? "unlimited" : integer(meter.allowed)}</td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>

      {billing.state.past_due || billing.state.payment_failed ? (
        <Banner tone="warn" title="Billing needs attention">
          <p>{billing.state.guidance ?? "The billing provider reports a problem with this account."}</p>
          <p className="caption">
            invoice {billing.state.invoice_status ?? "unknown"} · subscription{" "}
            {billing.state.subscription_status ?? "unknown"}
          </p>
        </Banner>
      ) : null}
    </>
  );
}
