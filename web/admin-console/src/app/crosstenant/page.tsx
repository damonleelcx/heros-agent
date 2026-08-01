import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, EmptyState } from "@/components/states";
import { PageFrame, Section } from "@/components/primitives";
import { AggregateChart } from "@/components/chart";
import type { ReadModel } from "@/lib/types";

/**
 * The cross-tenant read-model surface.
 *
 * Every view here is permission-gated and LOGGED by the backend — opening this page against an
 * aggregate records who looked at whom. The charts carry an accessible tabular fallback (FR27), and a
 * suppressed aggregate (below the minimum-cohort floor) renders its suppression reason rather than an
 * empty result (FR26).
 *
 * The selected aggregate is a URL parameter, so a tab is a linkable view (FR33): "look at provider
 * spend" is a link an operator can paste, and back/forward move between aggregates exactly.
 */

const AGGREGATES: Array<{ key: string; label: string }> = [
  { key: "usage_sum", label: "Usage (SUM)" },
  { key: "cogs_provider_spend", label: "Provider Spend" },
  { key: "revenue_ops", label: "Revenue & Operations" },
  { key: "top_consumers", label: "Top Consumers" },
  { key: "anomalies", label: "Anomalies" },
  // Improvement, savings and quality — every figure in it excludes `unverified` authored changes at
  // the query, and the count of what was excluded is shown beside them. An invisible exclusion is
  // indistinguishable from an oversight.
  { key: "authored_improvement", label: "Authored Improvement" },
];

export default async function CrossTenantPage({
  searchParams,
}: {
  searchParams: Promise<{ aggregate?: string }>;
}) {
  const { identity, sessionToken } = await requireIdentity();
  const { aggregate } = await searchParams;
  const selected = AGGREGATES.find((a) => a.key === aggregate)?.key ?? "usage_sum";

  if (!hasCapability(identity, "crosstenant.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Fleet" title="Cross-tenant">
          <DeniedState
            capability="crosstenant.read"
            description="Read cross-tenant aggregates"
            heldBy={holdersOf(identity, "crosstenant.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  let model: ReadModel | null = null;
  let degraded: string | null = null;
  try {
    model = await adminFetch<ReadModel>(`/admin/api/crosstenant/${encodeURIComponent(selected)}`, {
      sessionToken,
    });
  } catch (error) {
    degraded = error instanceof AdminApiError ? error.message : String(error);
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Fleet"
        title="Cross-tenant read models"
        lede={
          <>
            Fleet-wide aggregates from the P2.5 substrate — mechanism, not raw tenant content. Every
            view is logged. Aggregates over fewer than {identity.minimum_cohort} tenants are suppressed
            to prevent re-identifying a small tenant.
          </>
        }
      >
        <nav className="nav" aria-label="Aggregates">
          {AGGREGATES.map((a) => (
            <a
              key={a.key}
              href={`/crosstenant?aggregate=${a.key}`}
              aria-current={a.key === selected ? "page" : undefined}
            >
              {a.label}
            </a>
          ))}
        </nav>

        <Section
          title={model?.display_name ?? selected}
          aside={model ? `${model.cohort} tenants · ${model.source}` : undefined}
        >
          {degraded ? (
            <DegradedState what="the cross-tenant substrate" detail={degraded} />
          ) : !model ? (
            <DegradedState what="the read model" />
          ) : model.degraded ? (
            <DegradedState what="the read model" detail={model.detail} />
          ) : model.suppressed ? (
            <div className="state state--empty" role="note">
              <p className="state__title">Suppressed to protect a small cohort</p>
              <p className="state__body">{model.suppression_reason}</p>
            </div>
          ) : !model.rows || model.rows.length === 0 ? (
            <EmptyState what="values for this period" />
          ) : (
            <>
              {/* The exclusion is stated WHERE THE FIGURES APPEAR, in the same view — an operator has
                  to be able to tell a measured improvement from an unverified estimate without
                  navigating anywhere. */}
              {model.note ? <p className="state__body">{model.note}</p> : null}
              <AggregateChart caption={`${model.display_name} for ${model.period}`} rows={model.rows} />
            </>
          )}
        </Section>
      </PageFrame>
    </OperatorShell>
  );
}
