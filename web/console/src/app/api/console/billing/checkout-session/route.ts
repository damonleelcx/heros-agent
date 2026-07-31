import { withSession, isResponse, forward } from "@/lib/bff";
import type { CheckoutView } from "@/lib/types.generated";

/**
 * P21 task 6.1 — mint a payment-provider Checkout session SERVER-SIDE.
 *
 * # Why this route exists at all, when the console renders everything else on the server
 *
 * The BFF's route set is closed and deliberately small (see `bff.ts`): a view that merely displays data
 * is rendered server-side and needs no route. This one is different because it is an ACTION taken from
 * a click, and the thing it returns is a short-lived pointer the browser must then follow.
 *
 * # 🔴 What the client sends, and what it gets back
 *
 * It sends a plan NAME. It does not send a price, a price reference, an amount, or a customer id — the
 * customer is the session's tenant, resolved by `scope.ts` from the cookie, and the price reference is
 * resolved from the published plan configuration on the platform. A client that could name a price
 * would be a client that had one.
 *
 * It gets back a URL (or an embedded element's client secret). It never gets the payment provider's API
 * key: that key lives on the platform, is resolved from the Secrets seam at the moment of use, and has
 * no path into this response — which is design Decision 6 expressed as a payload shape.
 *
 * # Why the return URLs are built HERE rather than accepted from the client
 *
 * A client-supplied return URL is an open redirect wearing a checkout costume: anyone who can get a
 * customer to click a crafted link lands them on an attacker's page carrying the appearance of having
 * just paid. The origin comes from the request the browser actually made, and the path is one of two
 * this console owns.
 */
export const dynamic = "force-dynamic";

/** The only paths a checkout may return to. A closed set, for the reason in the doc comment. */
const RETURN_PATHS = new Set(["/app/billing", "/app/account"]);

export async function POST(request: Request) {
  const context = withSession(request);
  if (isResponse(context)) return context;

  const body = (await request.json().catch(() => null)) as { plan_name?: unknown; return_path?: unknown } | null;
  const planName = typeof body?.plan_name === "string" ? body.plan_name : "";
  if (planName === "") {
    return Response.json(
      { error: "a checkout needs the name of the plan to subscribe to", kind: "bad_request" },
      { status: 400, headers: { "cache-control": "no-store" } },
    );
  }

  const requested = typeof body?.return_path === "string" ? body.return_path : "/app/billing";
  const path = RETURN_PATHS.has(requested) ? requested : "/app/billing";
  // 🔴 The origin the BROWSER is on, not the one this process happens to know itself by.
  //
  // `new URL(request.url).origin` is the server's own view, and Next resolves it to `localhost`
  // regardless of the host the request arrived on. A customer browsing `127.0.0.1` was therefore sent
  // back to `localhost` — a different host for cookie purposes, so no session — and behind a proxy it
  // would be worse: the return URL would be the internal origin, which is not reachable from a browser
  // at all. The forwarded headers are what a proxy sets and what a dev server passes through; the
  // request URL is the last resort rather than the first choice.
  const forwardedHost = request.headers.get("x-forwarded-host") ?? request.headers.get("host");
  const forwardedProto =
    request.headers.get("x-forwarded-proto") ?? new URL(request.url).protocol.replace(":", "");
  const origin = forwardedHost ? `${forwardedProto}://${forwardedHost}` : new URL(request.url).origin;

  return forward<CheckoutView>(context, context.paths.checkoutSession(), {
    method: "POST",
    body: {
      plan_name: planName,
      // `?checkout=done` / `?checkout=canceled` is how the page knows to say "your subscription is
      // being confirmed" rather than rendering the pre-checkout state a customer just left.
      success_url: `${origin}${path}?checkout=done`,
      cancel_url: `${origin}${path}?checkout=canceled`,
    },
  });
}
