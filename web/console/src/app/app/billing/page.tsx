import Link from "next/link";
import type { LineView, PaymentView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { routes } from "@/lib/routes";
import { usd2, score } from "@/lib/format";
import { PlanActions } from "@/components/billingActions";
import { SUMTrend, UsageAgainstAllowance } from "@/components/billingCharts";
import { LinkCoverage } from "@/components/linkCoverage";
import {
  PageFrame,
  Section,
  Chip,
  Row,
  Empty,
  Failure,
  DataTable,
  Banner,
  Stat,
  Stats,
} from "@/components/primitives";

/**
 * Billing.
 *
 * # What this page is, and why it is not the Account page
 *
 * Account answers *what am I on and what does it entitle*. This answers *what am I being charged, why,
 * and can you charge me* — and it is the only surface with actions that move money. Keeping them apart
 * means the page a customer opens when a payment fails is the page with the fix on it, rather than a
 * capability table with a banner at the bottom.
 *
 * # 🔴 Every figure comes from the API. There is no price here.
 *
 * The plan is a NAME. The spend is a quantity in the unit the server names. Each invoice line carries
 * the BASIS that justified it and an opaque amount handle — the platform holds no amount, so the page
 * cannot show one, and the payment-UI fence fails the build on a priced literal. What a customer owes
 * is the payment provider's arithmetic, shown on the payment provider's invoice, which the line links
 * to by reference.
 *
 * # The unhappy states are first-class, not an afterthought
 *
 * Four of them, each a different fact with a different next action:
 *
 *   billing unavailable   we could not reach the provider — come back, nothing is wrong with your account
 *   past due / failed     the provider says a payment did not go through — here is how to restore it
 *   empty                 nothing recorded this period yet — a real state, not a failure
 *   no payment method     you have not attached one — here is where
 *
 * Rendering any of them as any other is the mistake this section exists to prevent: an unreachable
 * provider drawn as an empty invoice tells a customer they owe nothing, confidently, and wrongly.
 */
export const dynamic = "force-dynamic";

export default async function BillingPage({
  searchParams,
}: {
  searchParams: Promise<{ checkout?: string; period?: string }>;
}) {
  const params = await searchParams;
  const { outcome } = await load<PaymentView>((paths) => paths.payment(params.period), ["billing", "payment_method"]);

  return (
    <PageFrame
      eyebrow="Billing"
      title="What you are being charged, and why"
      lede="Your plan by name, this period's usage, every invoice line with the record that justified it, and the payment method on file. Your card is entered on the payment provider's own page and never reaches this platform."
      wide
    >
      {/*
        The return from checkout. It is a CLAIM about a process still finishing — the subscription
        becomes active when the provider's event arrives — so it says that rather than announcing
        success the page cannot yet see.
      */}
      <CheckoutReturn state={params.checkout} />

      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="billing" />
      ) : (
        <Body view={outcome.data} />
      )}
    </PageFrame>
  );
}

function CheckoutReturn({ state }: { state?: string }) {
  if (state === "done") {
    return (
      <Banner tone="info" title="Your payment method was submitted">
        <p>
          The payment provider has your card. Your subscription becomes active when the provider confirms
          it — usually within a few seconds. Reload this page to see the change.
        </p>
        <p className="caption">
          Nothing on this page is guessed from the checkout you just completed: it shows what the provider
          has told the platform, so it is briefly behind rather than briefly wrong.
        </p>
      </Banner>
    );
  }
  if (state === "canceled") {
    return (
      <Banner tone="info" title="Checkout was cancelled">
        <p>No payment method was added and nothing was charged. Your plan is unchanged.</p>
      </Banner>
    );
  }
  return null;
}

function Body({ view }: { view: PaymentView }) {
  const billing = view.billing;
  const state = billing.state;
  const lines = billing.invoice?.lines ?? [];
  const unhappy = state.past_due || state.payment_failed;

  // Billing unavailable is checked FIRST and rendered INSTEAD of the invoice. Showing an empty invoice
  // beside "we could not reach billing" would leave the reader to work out which of the two to believe.
  if (view.unavailable) {
    return (
      <>
        <Banner tone="warn" title="Billing is temporarily unavailable">
          <p>{view.unavailable.detail}</p>
          <p>
            {view.unavailable.retryable
              ? "This is a provider outage, not a problem with your account. Nothing has been charged twice, and no usage has been lost — the platform records what you use and reports it when the provider is reachable again."
              : "This needs a change before it can work; retrying will not clear it. Whoever operates this platform can see the reason on the readiness endpoint."}
          </p>
        </Banner>
        <Section title="Your plan" aside="the last state the platform recorded">
          <Row>
            <Chip variant="plan">{billing.plan_name}</Chip>
            <Chip title="the period this view covers">{billing.period}</Chip>
          </Row>
          <p className="hint">
            Your product is unaffected. Everything except the payment surface keeps working while billing
            is unreachable.
          </p>
        </Section>
      </>
    );
  }

  return (
    <>
      {unhappy ? <DunningBanner view={view} /> : null}

      <Section
        title="Your plan"
        aside={<span className="mono">config {billing.plan_config_version}</span>}
      >
        <Row>
          <Chip variant="plan">{billing.plan_name}</Chip>
          <Chip title="the period this view covers">{billing.period}</Chip>
          {state.subscription_status ? (
            // The provider's own word, carried verbatim. The platform never recomputes dunning, and it
            // never translates the provider's vocabulary into its own.
            <Chip tone={unhappy ? "warn" : "ok"} title="the payment provider's own subscription status">
              {state.subscription_status}
            </Chip>
          ) : null}
        </Row>
        <PlanActions
          plans={view.plans}
          collectionAvailable={view.collection_available}
          hasPaymentMethod={view.payment_method.present}
        />
        <p className="caption">
          A plan change takes effect for what you can do at the moment you make it. The amount is prorated
          by the payment provider on its own schedule — the platform does not compute it, and a downgrade
          does not delete anything you have already used or been billed for.
        </p>
      </Section>

      <Section title="Payment method" aside="held by the payment provider, never by this platform">
        <PaymentMethod view={view} />
      </Section>

      <Section title="This period" aside={billing.sum_unit}>
        <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:gap-10">
          <Stats>
            <Stat
              label="Spend under management"
              value={usd2(billing.sum)}
              unit={billing.sum_unit}
              note="derived from LINKED runs only — the console computes no total of its own, and an unlinked run is never estimated"
            />
          </Stats>
          <div className="w-full lg:max-w-md">
            <LinkCoverage coverage={billing.link_coverage} />
          </div>
        </div>
        {billing.empty ? (
          <Empty title="Nothing has been recorded for this period yet.">
            <p>
              This is a real state, not a failure to load. A period with no linked runs has no spend, and
              a plan with no metered usage has nothing to meter.
            </p>
          </Empty>
        ) : (
          <div className="flex flex-col gap-8">
            <SUMTrend points={billing.sum_trend ?? null} unit={billing.sum_unit} />
            <UsageAgainstAllowance meters={billing.meters ?? null} />
          </div>
        )}
      </Section>

      <VerifiedSavings view={view} />

      <Section
        title="Invoice breakdown"
        aside={billing.invoice?.status ? `provider status: ${billing.invoice.status}` : "no invoice yet"}
      >
        {lines.length === 0 ? (
          <Empty title="No invoice lines for this period.">
            <p>
              Lines appear as charges are raised. This is a real state — a period that has not been
              billed yet — and not the provider being unreachable, which this page says separately.
            </p>
          </Empty>
        ) : (
          <>
            <DataTable
              caption="Every line on this period's invoice, what kind of charge it is, and the platform record that justified it"
              columns={[
                { key: "kind", label: "Kind" },
                { key: "basis", label: "Why this line exists" },
                { key: "quantity", label: "Quantity", numeric: true },
                { key: "ref", label: "Provider reference" },
              ]}
            >
              <tbody>
                {lines.map((line) => (
                  <InvoiceRow key={`${line.kind}:${line.charge_ref ?? line.basis}`} line={line} />
                ))}
              </tbody>
            </DataTable>
            <p className="caption">
              There is no amount on this page. The platform stores provider handles, never money, so what
              each line came to is on the payment provider&rsquo;s own invoice — which is also the only
              place it can be authoritative.
            </p>
          </>
        )}
      </Section>
    </>
  );
}

/**
 * VerifiedSavings shows what gainshare billed — and, the part that makes the claim checkable, what it
 * DID NOT.
 *
 * 🔴 The excluded rows are not a nicety. The platform's gainshare claim is that it bills only for
 * savings it verified and a human merged, and the only way a customer can believe that is to see the
 * savings it declined to bill and how large they were. On a real repository the largest verified saving
 * is often the un-merged one; showing that it billed nothing is worth more than any sentence about
 * integrity.
 */
function VerifiedSavings({ view }: { view: PaymentView }) {
  const savings = view.billing.savings;
  if (!savings || !savings.consent_available) return null;

  const billed = savings.billed ?? [];
  const excluded = savings.excluded ?? [];
  if (billed.length === 0 && excluded.length === 0) return null;

  return (
    <Section title="Verified savings" aside="billed only when verified AND merged">
      {savings.none_verified ? (
        <p className="hint">
          Nothing was verified and merged this period, so gainshare billed nothing. That is a real
          state — not a measured zero, and not a figure that failed to load.
        </p>
      ) : null}

      <DataTable
        caption="Every saving considered this period, whether it was billed, and the evidence or the reason"
        columns={[
          { key: "ref", label: "Saving" },
          { key: "billed", label: "Billed" },
          { key: "amount", label: `Savings (${savings.unit})`, numeric: true },
          { key: "why", label: "Evidence, or why not" },
        ]}
      >
        <tbody>
          {billed.map((row) => (
            <tr key={row.ref}>
              <td className="mono text-sm">{row.ref}</td>
              <td>
                <Chip tone="ok">billed</Chip>
              </td>
              <td className="num mono">{score(row.savings)}</td>
              <td>
                <span className="caption mono">merge {row.merge_commit}</span>
                {row.method ? (
                  <p className="caption mt-1">
                    held out on {row.method.holdout_cases} case(s) over {row.method.seeds} seed(s)
                  </p>
                ) : null}
              </td>
            </tr>
          ))}
          {excluded.map((row) => (
            <tr key={row.ref}>
              <td className="mono text-sm">{row.ref}</td>
              <td>
                <Chip tone="unknown">not billed</Chip>
              </td>
              <td className="num mono">{score(row.would_have_been)}</td>
              <td>
                <span className="caption">{row.reason}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </DataTable>
      <p className="caption">
        The &ldquo;not billed&rdquo; column is the point. A saving that was measured but never merged is
        a saving that is not in effect, so it bills nothing however large it is — and this table shows
        you how large it was.
      </p>
    </Section>
  );
}

/**
 * DunningBanner is the past-due / payment-failed state (task 6.4).
 *
 * 🔴 Both halves are required: a NAMED REASON, in the provider's words, and a RESTORE PATH. A dunning
 * banner with no next step is an alarm — it tells a customer something is wrong and leaves them to
 * guess what to do, which is how a recoverable card decline becomes a churned account.
 *
 * Every word of the reason comes from the MIRRORED provider state. The console computes no payment
 * status: if it did, there would be two opinions about whether this customer is past due, and the one
 * on screen would be the one that is wrong.
 */
function DunningBanner({ view }: { view: PaymentView }) {
  const method = view.payment_method;
  const state = view.billing.state;
  const reason =
    method.reason ??
    state.guidance ??
    "The payment provider reports that a payment on this account did not go through.";

  return (
    <Banner tone="warn" title="A payment did not go through">
      <p>{reason}</p>
      <p>
        <span className="font-medium text-foreground">To restore it: </span>
        {method.restore_path ??
          "update the payment method below. The provider retries automatically on its own schedule; a working card ends the retries."}
      </p>
      <p className="caption">
        invoice {state.invoice_status ?? "unknown"} · subscription {state.subscription_status ?? "unknown"}{" "}
        — the payment provider&rsquo;s own words, mirrored here rather than recomputed.
      </p>
      <p className="caption">
        Your plan has not been changed. Access follows the provider&rsquo;s retry schedule, and if it ends
        without a payment the plan moves to the free tier at the period boundary — an audited change that
        deletes nothing and reverses when you pay.
      </p>
    </Banner>
  );
}

function PaymentMethod({ view }: { view: PaymentView }) {
  const method = view.payment_method;

  if (!view.collection_available) {
    return (
      <p className="hint">
        This deployment&rsquo;s billing provider does not collect payment methods. Whoever operates this
        platform administers your plan directly.
      </p>
    );
  }

  if (!method.present) {
    return (
      <Empty title="No payment method on file.">
        <p>
          Nothing is owed until you subscribe to a paid plan. When you do, the card is entered on the
          payment provider&rsquo;s own page — this platform receives a reference to it and never the card
          itself.
        </p>
      </Empty>
    );
  }

  return (
    <Row>
      <Chip title="the card the payment provider holds for this account">
        {method.brand ? `${method.brand} ` : "Card "}
        {method.last4 ? `•••• ${method.last4}` : "on file"}
      </Chip>
      {method.status ? (
        <Chip tone={method.status === "ok" ? "ok" : "warn"} title="the payment provider's own status">
          {method.status}
        </Chip>
      ) : null}
      <span className="caption">
        The platform holds a reference to this card, not the card. See{" "}
        <Link className="text-primary underline underline-offset-2" href={routes.account()}>
          your plan and entitlements
        </Link>{" "}
        for what it pays for.
      </span>
    </Row>
  );
}

function InvoiceRow({ line }: { line: LineView }) {
  return (
    <tr>
      <td>
        <Chip tone={line.kind === "credit" || line.kind === "refund" ? "ok" : undefined}>{line.kind}</Chip>
      </td>
      <td>
        <span className="mono text-sm">{line.basis}</span>
        {line.corrects ? <p className="caption mt-1">corrects {line.corrects}</p> : null}
        {/*
          A gainshare line carries its EVIDENCE — the verified deltas and the merges that shipped them.
          A gainshare line without it is a defect, not a rendering choice: the whole claim is that the
          platform bills only for savings it verified and a human merged.
        */}
        {line.evidence && line.evidence.length > 0 ? (
          <ul className="mt-1.5 flex flex-col gap-1">
            {line.evidence.map((e) => (
              <li className="caption" key={`${e.kind}:${e.ref}`}>
                {e.link ? (
                  <Link className="text-primary underline underline-offset-2" href={e.link}>
                    {e.label}
                  </Link>
                ) : (
                  e.label
                )}
                <span className="mono"> · {e.kind}</span>
              </li>
            ))}
          </ul>
        ) : null}
      </td>
      <td className="num mono">
        {score(line.quantity)}
        {line.unit ? <span className="caption"> {line.unit}</span> : null}
      </td>
      <td>
        <span className="mono text-sm">{line.charge_ref ?? line.amount_ref ?? "—"}</span>
      </td>
    </tr>
  );
}
