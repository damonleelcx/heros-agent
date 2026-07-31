"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import type { PlanOptionView } from "@/lib/types.generated";
import { Banner, Chip } from "@/components/primitives";

/**
 * billingActions.tsx is the only client component on the billing page: subscribe, upgrade, downgrade,
 * and "update payment method".
 *
 * # What it does NOT hold
 *
 * 🔴 No Stripe key. No price. No card field. It sends a plan NAME to the BFF and, for checkout,
 * receives a short-lived URL the browser is then sent to — so the card goes browser→Stripe directly
 * (design Decision 2) and the only Stripe value that ever reaches this bundle is a session pointer that
 * expires. There is no code path here that could receive a card number, which is what makes "the
 * platform never stores a card" structural rather than a promise.
 *
 * # Why the plan name and not a plan id or a price reference
 *
 * The server resolves the name against the published plan configuration, which is the only place that
 * knows which price reference is current. A console that sent a price reference would be a console that
 * had one, and the next step from having one is displaying it — which is the money-in-git failure with
 * extra steps.
 *
 * # Why a full refresh rather than optimistic state
 *
 * A plan change moves an entitlement, an audit row, and a provider subscription. Painting the new plan
 * before the server confirms would show a customer a plan they may not have — and on the one path where
 * the provider refuses, the optimistic UI is the only thing that ever said it worked.
 */
export function PlanActions({
  plans,
  collectionAvailable,
  hasPaymentMethod,
}: {
  plans: PlanOptionView[] | null;
  collectionAvailable: boolean;
  hasPaymentMethod: boolean;
}) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  const options = plans ?? [];

  if (!collectionAvailable) {
    // The control is ABSENT rather than present-and-broken. A button that returns "this provider
    // cannot do that" is worse than no button: it invites a customer to try, and then blames them.
    return (
      <p className="hint">
        This deployment&rsquo;s billing provider does not collect payment methods, so plan changes are
        not self-serve here. Your plan is administered by whoever operates this platform.
      </p>
    );
  }

  async function post(path: string, body: unknown) {
    const response = await fetch(path, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    const payload = await response.json().catch(() => ({}) as Record<string, unknown>);
    if (!response.ok) {
      // The upstream message is rendered verbatim. A generic "something went wrong" manufactured here
      // would destroy the one sentence that says which plan name was not recognised, or which gate
      // refused.
      throw new Error(typeof payload.error === "string" ? payload.error : `The request failed (${response.status}).`);
    }
    return payload as Record<string, unknown>;
  }

  function checkout(planName: string) {
    setBusy(planName);
    setMessage(null);
    void (async () => {
      try {
        const result = await post("/api/console/billing/checkout-session", {
          plan_name: planName,
          return_path: "/app/billing",
        });
        const url = typeof result.url === "string" ? result.url : "";
        if (!url) {
          throw new Error("Checkout is configured for an embedded payment element, which this console does not render yet.");
        }
        // 🔴 The browser leaves for Stripe here. Everything after this line runs on Stripe's page.
        window.location.assign(url);
      } catch (error) {
        setBusy(null);
        setMessage({ tone: "bad", text: error instanceof Error ? error.message : String(error) });
      }
    })();
  }

  function changePlan(planName: string) {
    setBusy(planName);
    setMessage(null);
    void (async () => {
      try {
        const result = await post("/api/console/billing/plan", { plan_name: planName });
        if (result.checkout_required === true) {
          // "You have not paid yet" is a step, not a failure — so it becomes checkout rather than an
          // error message.
          checkout(planName);
          return;
        }
        setBusy(null);
        setMessage({
          tone: "ok",
          text:
            result.changed === true
              ? `You are on the ${planName} plan. Your entitlements changed now; the amount is prorated by the payment provider.`
              : `You were already on the ${planName} plan — nothing changed.`,
        });
        startTransition(() => router.refresh());
      } catch (error) {
        setBusy(null);
        setMessage({ tone: "bad", text: error instanceof Error ? error.message : String(error) });
      }
    })();
  }

  return (
    <div className="flex flex-col gap-4">
      <ul className="flex flex-wrap gap-2.5" aria-label="Plans you can move to">
        {options.map((plan) => (
          <li key={plan.plan_id}>
            <PlanButton
              plan={plan}
              busy={busy === plan.name || pending}
              disabled={busy !== null || pending}
              onSelect={() => (hasPaymentMethod ? changePlan(plan.name) : checkout(plan.name))}
            />
          </li>
        ))}
      </ul>

      <div className="flex flex-wrap items-center gap-3">
        <button
          className="button"
          type="button"
          disabled={busy !== null || pending}
          onClick={() => {
            const target = options.find((p) => p.current && p.subscribable) ?? options.find((p) => p.subscribable);
            if (target) checkout(target.name);
          }}
        >
          {hasPaymentMethod ? "Update payment method" : "Add a payment method"}
        </button>
        <span className="caption">
          Opens the payment provider&rsquo;s own page. Your card is entered there and never reaches this
          platform.
        </span>
      </div>

      {/*
        A live region, so the outcome is announced rather than only drawn. An action whose only feedback
        is a repaint two hundred pixels away is an action a screen-reader user cannot tell succeeded.
        It is always in the tree — a region that appears with its message is announced inconsistently.
      */}
      <div role="status" aria-live="polite">
        {message === null ? null : message.tone === "bad" ? (
          <Banner tone="bad" title="That plan change did not go through">
            <p>{message.text}</p>
            <p className="caption">
              Nothing was charged and your plan is unchanged. Trying again is safe — a repeated request
              under the same intent produces one subscription, not two.
            </p>
          </Banner>
        ) : (
          <p className="hint">{message.text}</p>
        )}
      </div>
    </div>
  );
}

function PlanButton({
  plan,
  busy,
  disabled,
  onSelect,
}: {
  plan: PlanOptionView;
  busy: boolean;
  disabled: boolean;
  onSelect: () => void;
}) {
  if (plan.current) {
    return (
      <span aria-current="true">
        <Chip variant="plan">{plan.name} — current plan</Chip>
      </span>
    );
  }
  if (!plan.subscribable) {
    // The free tier has no subscription price, so there is nothing to check out. Saying so beats a
    // button that fails.
    return (
      <Chip title="This plan has no subscription price, so there is nothing to check out">
        {plan.name} — no subscription
      </Chip>
    );
  }
  if (plan.unavailable) {
    // 🔴 Not set up with the payment provider yet (P21 Decision 9). The control is DISABLED and says
    // why. Offering it would send the customer to a checkout that fails on somebody else's
    // configuration — the worst version of this state, because it looks like they did something wrong.
    return (
      <Chip tone="warn" title="This plan is not yet configured with the payment provider, so checkout would fail">
        {plan.name} — not available yet
      </Chip>
    );
  }
  return (
    <button className="button" type="button" disabled={disabled} onClick={onSelect}>
      {busy ? "Working…" : `${label(plan.direction)} to ${plan.name}`}
    </button>
  );
}

/** label turns the server's direction into the verb a customer reads. The console never derives the
 * direction itself — it has no price and rank is configuration. */
function label(direction: string): string {
  switch (direction) {
    case "upgrade":
      return "Upgrade";
    case "downgrade":
      return "Downgrade";
    default:
      return "Subscribe";
  }
}
