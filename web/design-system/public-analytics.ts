/**
 * public-analytics.ts loads the two public-surface tags — and nothing else, on nothing else.
 *
 * # Where this runs, in one sentence
 *
 * The PUBLIC prefix of the customer console, after first paint, only under the category the visitor
 * granted. There is no code path that reaches a tenant or an operator route: `installPublicAnalytics`
 * refuses a surface id that is not `public.*`, and even if it did not, the policy those prefixes serve
 * names neither origin, because `product_analytics` and `session_replay` are absent from their classes'
 * permitted categories.
 *
 * # Why the tags are injected by first-party code rather than written into the HTML
 *
 * A `<script src="https://…">` in the document would need a host on `script-src`, and adding one would
 * make `'strict-dynamic'` decorative for every prefix at once. Injected from a module that itself
 * arrived through the nonced bootstrap, the tag is reached BY trust propagation — which is exactly what
 * `'strict-dynamic'` is for, and it keeps `script-src` naming no host anywhere.
 *
 * It also makes "after first paint" and "only under its granted category" properties of code rather
 * than of a template: a tag in the HTML loads before anybody has answered anything.
 *
 * # Why the configuration is asserted rather than documented
 *
 * Every line in `GA4_CONFIG` below turns something OFF that is on by default. A comment saying "we do
 * not do remarketing" is worth nothing — the test reads this object.
 */

import { isAnalyticsEvent, type AnalyticsEvent, type SectionId } from "./analytics-events.ts";
import type { SurfaceId } from "./third-party-policy.ts";

/** The measurement id and the project id. Public identifiers by design — neither authorises a read. */
export type PublicAnalyticsConfig = {
  /** GA4 measurement id. Empty means ABSENT: no tag, no listener, no request. */
  measurementId: string;
  /** Clarity project id. Empty means absent, independently of the measurement id. */
  clarityProjectId: string;
  surface: SurfaceId;
  release: string;
  edition: string;
  granted: { productAnalytics: boolean; sessionReplay: boolean };
};

/**
 * GA4_CONFIG is every setting this product turns off, stated as data.
 *
 * `anonymize_ip` on; ad-personalisation signals off; no cross-site identifier; no advertising
 * identifier; no remarketing audience; no conversion pixel. `client_storage: "none"` is the one that
 * does the most work: it stops GA4 writing a client id into a cookie at all, so a visitor who granted
 * usage analytics is counted without being given a persistent identity.
 */
export const GA4_CONFIG = {
  anonymize_ip: true,
  allow_google_signals: false,
  allow_ad_personalization_signals: false,
  restricted_data_processing: true,
  client_storage: "none",
  send_page_view: false,
  // Not a Google setting — a marker the fence reads, so "no conversion pixel" is a property of this
  // object rather than of nobody having added one.
  conversion_linker: false,
} as const;

/**
 * CLARITY_CONFIG is masking ON BY DEFAULT.
 *
 * `mask: "all"` is not the vendor default; the vendor default masks a subset. The rule here is that a
 * recording starts fully masked and an UNMASKING is a per-element decision with a recorded reason — see
 * `CLARITY_UNMASKED`, which is empty and is asserted to be empty until somebody writes a reason.
 */
export const CLARITY_CONFIG = {
  mask: "all",
  maskTextInputs: true,
  cookies: false,
} as const;

/**
 * CLARITY_UNMASKED is the per-element opt-out list. EMPTY, and each entry needs a reason.
 *
 * A selector denylist over a surface that gains pages every phase fails silently the first time
 * somebody adds a page — which is why the tenant surface refuses replay outright rather than masking
 * it. On the PUBLIC surface, where there is no customer content to leak, the same discipline applies in
 * the opposite direction: nothing is unmasked unless somebody wrote down why.
 */
export const CLARITY_UNMASKED: readonly { selector: string; reason: string }[] = [];

type Gtag = (...args: unknown[]) => void;

declare global {
  interface Window {
    dataLayer?: unknown[];
    gtag?: Gtag;
    clarity?: (...args: unknown[]) => void;
  }
}

/** loadScript injects a tag AFTER first paint and never blocks render. */
function loadScript(src: string): void {
  const el = document.createElement("script");
  el.async = true;
  el.src = src;
  document.head.appendChild(el);
}

/**
 * afterFirstPaint defers to the frame after the first one, then to an idle moment.
 *
 * Two steps rather than one. `requestAnimationFrame` alone still runs inside the first frame's work on
 * a slow device, and `requestIdleCallback` alone may never fire on a page that stays busy. Together
 * they mean the tag has no way to be on the critical path, and the public page's own requirement — that
 * it renders with the platform API stopped — is untouched because neither tag is awaited by anything.
 */
function afterFirstPaint(run: () => void): void {
  const idle = (window as unknown as { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => void })
    .requestIdleCallback;
  requestAnimationFrame(() => {
    if (idle) idle(run, { timeout: 3000 });
    else setTimeout(run, 0);
  });
}

/**
 * installPublicAnalytics loads the granted tags. Returns the list of integrations it started, so a
 * caller (and a test) can see what happened rather than inferring it.
 */
/**
 * plannedIntegrations is the DECISION — which tags this configuration would load — separated from the
 * loading so it can be tested without a DOM.
 *
 * Separating them is not tidiness. The decision is the security-relevant half ("does a tenant surface
 * ever load a recorder"), and an earlier version could only be exercised in a browser, which meant the
 * unit suite asserted it by reading the source. A rule you can only check by reading is the kind this
 * phase exists to replace.
 *
 * 🔴 The surface check is the structural refusal in CODE, beside the one in the POLICY. Both halves
 * exist because one of them is a checklist.
 */
export function plannedIntegrations(config: PublicAnalyticsConfig): string[] {
  if (!config.surface.startsWith("public.")) return [];
  const planned: string[] = [];
  if (config.granted.productAnalytics && config.measurementId) planned.push("product_analytics");
  if (config.granted.sessionReplay && config.clarityProjectId) planned.push("session_replay");
  return planned;
}

export function installPublicAnalytics(config: PublicAnalyticsConfig, onReady?: () => void): string[] {
  const planned = plannedIntegrations(config);
  if (typeof window === "undefined" || planned.length === 0) return [];

  const started: string[] = [];

  if (planned.includes("product_analytics")) {
    afterFirstPaint(() => {
      window.dataLayer = window.dataLayer || [];
      /*
       * 🔴 A `function` with `arguments`, NOT an arrow with a rest parameter.
       *
       * This looks like a style choice and is not. GA4's library reads `dataLayer` entries and expects
       * each one to be an `arguments` OBJECT, which is what the vendor's own snippet pushes; a real
       * Array is silently ignored. An earlier version here used `(...args) => dataLayer.push(args)`,
       * which type-checks, runs, throws nothing, and produces a tag that loads 167 KB and reports
       * nothing at all.
       *
       * It was found by the acceptance run measuring ZERO bytes to the measurement endpoint while the
       * tag host had plainly transferred 167 KB — a gap no unit test would have shown, because every
       * function involved did exactly what it said.
       */
      function gtag(this: unknown) {
        // eslint-disable-next-line prefer-rest-params
        window.dataLayer?.push(arguments);
      }
      const push = gtag as unknown as Gtag;
      window.gtag = push;
      push("js", new Date());
      push("config", config.measurementId, GA4_CONFIG);
      loadScript(`https://www.googletagmanager.com/gtag/js?id=${encodeURIComponent(config.measurementId)}`);
      // 🔴 The funnel runs HERE, not at the call site.
      //
      // An earlier version called `observeFunnel` immediately after `installPublicAnalytics` returned,
      // which reads correctly and does nothing: `window.gtag` is assigned inside this deferred callback,
      // so every `track` call before it ran was a silent no-op. The acceptance run found it by measuring
      // ZERO bytes to the measurement endpoint while the tag itself had plainly loaded.
      onReady?.();
    });
    started.push("product_analytics");
  }

  if (planned.includes("session_replay")) {
    afterFirstPaint(() => {
      const queue: unknown[] = [];
      window.clarity =
        window.clarity ||
        ((...args: unknown[]) => {
          queue.push(args);
        });
      window.clarity("set", "mask", CLARITY_CONFIG.mask);
      window.clarity("consent");
      loadScript(`https://www.clarity.ms/tag/${encodeURIComponent(config.clarityProjectId)}`);
    });
    started.push("session_replay");
  }

  return started;
}

/**
 * observeFunnel reports the page view and the sections a reader actually reached.
 *
 * # Why an IntersectionObserver and not a scroll handler
 *
 * A scroll handler fires hundreds of times and would have to be throttled, and the throttle is where
 * the bug lives. An observer fires once per element, when it is genuinely in view, and it is cheap
 * enough to run on a surface whose defining property is that it renders with the platform stopped.
 *
 * # Why it observes ids rather than positions
 *
 * `section_reached` names a section from the closed `SECTIONS` set. Reporting a scroll DEPTH instead
 * would be a number derived from the page's length, which changes every time somebody edits it — so the
 * series would be uncomparable across releases while looking perfectly comparable.
 */
export function observeFunnel(sections: readonly SectionId[]): () => void {
  if (typeof window === "undefined" || typeof IntersectionObserver === "undefined") return () => {};
  track("page_viewed");

  const seen = new Set<string>();
  const observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const id = entry.target.id;
        if (seen.has(id)) continue;
        seen.add(id);
        // The id is checked against the closed set before it is reported. An element whose id somebody
        // changed reports NOTHING rather than reporting the new id — which is the difference between a
        // closed set and a set that is closed until it is not.
        if ((sections as readonly string[]).includes(id)) track("section_reached", { section: id as SectionId });
      }
    },
    { threshold: 0.4 },
  );
  for (const id of sections) {
    const el = document.getElementById(id);
    if (el) observer.observe(el);
  }
  return () => observer.disconnect();
}

/**
 * track reports a funnel event.
 *
 * The name is typed to the enum, and `scripts/scan-events.mjs` fails the build on a call whose argument
 * is not a member — because a type is checked at build time and a string is not, and the failure this
 * guards against is a template literal that types fine and carries free text.
 */
export function track(event: AnalyticsEvent, params?: { section?: SectionId; plan_name?: string }): void {
  if (typeof window === "undefined" || !window.gtag) return;
  if (!isAnalyticsEvent(event)) return;
  // Constructed, not spread: an object spread here would carry whatever a caller happened to pass.
  const payload: Record<string, string> = {};
  if (params?.section) payload.section = params.section;
  if (params?.plan_name) payload.plan_name = params.plan_name;
  window.gtag("event", event, payload);
}
