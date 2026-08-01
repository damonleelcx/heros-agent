/**
 * third-party-policy.ts is the ONE checked-in artefact describing where third-party code and
 * third-party requests are permitted (P24 design D11).
 *
 * # Why it lives here and not in either console
 *
 * Both consoles ship their own `middleware.ts`, their own `next.config.mjs` and their own
 * `scan-bundle.mjs`. A rule copied into two files is a rule that will differ in two files, and this
 * repository has the scar tissue for exactly that failure class. So the origins, the consent
 * categories, the surface enum and the per-prefix rules are DATA, checked in once, beside the token
 * set that is already jointly owned. Both middlewares construct their Content-Security-Policy from
 * this file (see `csp.ts`); neither may name an origin of its own, and a build gate fails on a
 * hard-coded `https://` literal in either middleware or either Next configuration.
 *
 * # Why it is data and not a function
 *
 * The value of the CSP is that it is a READABLE statement of where data goes. A reader must be able to
 * answer "which third parties does this product talk to, on which surface, gated on what" by reading
 * one table — not by simulating a header builder. `csp.ts` holds the construction; this file holds
 * only the facts it is constructed from.
 *
 * # The direction of the allowlist
 *
 * Asserted in BOTH directions. A surface may not load an origin this file does not carry, and this
 * file may not carry an origin no surface loads: a stale entry is a permission nobody asked for.
 */

// ── Consent ──────────────────────────────────────────────────────────────────

/**
 * ConsentCategory is the closed set of things a visitor may separately agree to.
 *
 * Four rather than one, because a visitor who accepted usage counting has not accepted being filmed
 * (design D7). `essential` is the only category that is not gated: it covers the session cookie, the
 * theme preference and the consent record itself, none of which are optional for the product to work.
 */
export type ConsentCategory =
  | "essential"
  | "product_analytics"
  | "session_replay"
  | "error_diagnostics";

/** CONSENT_CATEGORIES is the enumeration, in the order the banner presents them. */
export const CONSENT_CATEGORIES: readonly ConsentCategory[] = [
  "essential",
  "product_analytics",
  "session_replay",
  "error_diagnostics",
] as const;

/** NON_ESSENTIAL_CATEGORIES default to denied and are never loaded before an explicit grant. */
export const NON_ESSENTIAL_CATEGORIES: readonly ConsentCategory[] = [
  "product_analytics",
  "session_replay",
  "error_diagnostics",
] as const;

// ── Origins ──────────────────────────────────────────────────────────────────

/**
 * CspDirective is the set of directives an allowlisted origin may appear under.
 *
 * `script-src` is deliberately absent and not expressible. Third-party scripts are reached through
 * `'strict-dynamic'` from the nonced loader, so the script directive gains NO host on any prefix —
 * which is what keeps `'strict-dynamic'` meaningful rather than decorative.
 *
 * 🔴 `"strict-dynamic"` is how a SCRIPT HOST is declared. It contributes nothing to the header — the
 * builder skips it — and it exists because of a real measurement: the acceptance run found the browser
 * transferring 167 KB from `www.googletagmanager.com`, an origin that legitimately appears in no
 * directive and which the both-directions check therefore reported as unlisted. Without this value the
 * only ways out were to put a host on `script-src` (which would make `'strict-dynamic'` decorative on
 * every prefix at once) or to exempt script hosts from the allowlist (which would mean the heaviest
 * third-party request on the page was the one nobody budgeted). Declaring them, with a budget, and with
 * no effect on the header, is the third option and the honest one.
 */
export type CspDirective = "connect-src" | "img-src" | "strict-dynamic";

/** AllowedOrigin is one third-party origin, with everything a reviewer needs to judge it. */
export type AllowedOrigin = {
  /**
   * The exact origin. No path.
   *
   * 🔴 A `*` is permitted in the leftmost label ONLY, and only under the conditions `wildcardReason`
   * documents. Everything else is an exact match — a wildcard is an allowlist that stopped being one.
   */
  origin: string;
  /**
   * Why this entry needs a wildcard subdomain, or absent for an exact origin.
   *
   * The design's rule is "exact origin, no wildcard", and it is the right rule. One vendor cannot meet
   * it: Microsoft Clarity ingests to a REGIONAL host under `clarity.ms` that is chosen at runtime, and
   * the vendor's own guidance is `https://*.clarity.ms`. The three ways out and why this is the one
   * taken:
   *
   *   - Enumerate the regional hosts. Fails closed and SILENTLY when a new region appears — recording
   *     stops, nothing goes red, and the first person to notice raises it to a wildcard anyway.
   *   - Refuse Clarity. Defensible, and it is what the tenant and operator surfaces do. On the public
   *     surface it would refuse a decision the product owner has already made.
   *   - Permit a wildcard, BOUNDED and STATED. A wildcard entry may appear only under `connect-src`,
   *     only on the `public` class, and only with this field filled in — asserted in
   *     `tests/third-party-fence.test.mjs`. The acceptance run reports the hosts actually contacted by
   *     name, so the wildcard's real footprint is observed rather than assumed.
   *
   * The narrowing that makes it survivable: `*.clarity.ms` cannot reach a tenant or operator prefix,
   * because `session_replay` is absent from those classes' permitted categories. The exception is one
   * vendor wide and one surface class wide.
   */
  wildcardReason?: string;
  /** Which product requires it. */
  integration: string;
  /** Which consent grant gates it. `essential` here would be a contradiction and is refused. */
  category: ConsentCategory;
  /** The directive it appears under. Never `script-src`. */
  directive: CspDirective;
  /** The acceptance run's per-origin transfer ceiling, in bytes on the wire. */
  budgetBytes: number;
  /** The surface classes whose policy may name it. */
  surfaces: readonly SurfaceClass[];
  /**
   * When the browser contacts this origin.
   *
   * `page-load` origins are fetched on load and are directly observable in the acceptance run — the
   * stale-entry check requires each of them to be seen. `on-event` origins are contacted only when
   * something happens (an error is reported), which no clean page load can produce. Distinguishing them
   * is what stops the both-directions assertion from being either vacuous or wrong: without it, an
   * ingest endpoint would be permanently reported as a stale entry, and the first person to hit that
   * would relax the check for everything.
   */
  contactedOn: "page-load" | "on-event";
  /** The one-line justification a reviewer reads. */
  why: string;
};

/**
 * ALLOWED_ORIGINS is the complete set of third-party origins any surface of this product may reach.
 *
 * 🔴 It was EMPTY through wave 24a, on purpose: every fence in this phase was built and demonstrated
 * red against an empty allowlist before any tool was installed, because installing the tool first is
 * how a fence ends up shaped around the tool rather than around the requirement.
 *
 * Entries arrive one wave at a time, and each names the wave that added it, so a reader can see not
 * only which origins are permitted but when and for what each one was let in.
 */
export const ALLOWED_ORIGINS: readonly AllowedOrigin[] = [
  {
    // Wave 24c. The ONE third-party origin permitted on a tenant or operator prefix.
    //
    // It is permitted there — where an analytics tag and a session recorder are structurally refused —
    // because the payload that reaches it carries no content BY CONSTRUCTION: the event is built field
    // by field from a thirteen-entry allowlist, the message body is dropped unless it is a member of a
    // closed error-code enum, there is no breadcrumb collection to filter, and the surface is an id
    // rather than a URL. That is a different kind of claim from "the recorder is configured to mask
    // things", and it is why this exception is exactly one origin wide.
    origin: "https://o4511833794019328.ingest.us.sentry.io",
    integration: "Sentry (error reporting)",
    category: "error_diagnostics",
    directive: "connect-src",
    // The design's ceiling for a browser error SDK. The reporter shipped here is FIRST-PARTY code
    // inside the already-measured bundle, so what this origin actually costs the page is an ingest
    // response of a few hundred bytes. The budget bounds the ORIGIN, not our own weight, and it stays
    // at the designed number so a future switch to a hosted SDK would have to argue for a raise.
    budgetBytes: 100 * 1024,
    surfaces: ["public", "tenant", "operator"],
    contactedOn: "on-event",
    why:
      "Wave 24c. Browser error reporting on all three web surfaces. The only third-party origin a " +
      "tenant or operator prefix may name, permitted because the event is constructed from an " +
      "allowlist rather than filtered, and reached under connect-src only - script-src gains no host.",
  },
  /*
   * 🔴 `www.google-analytics.com` and `region1.google-analytics.com` are NOT here, and their absence is
   * a measurement rather than an omission.
   *
   * Both were on this list, declared from GA4's documented ingest hosts. The acceptance run — a real
   * Chrome, five public routes, every category granted, 4.5 s per route — observed the tag host transfer
   * 167 KB, observed GA4 write its `_ga` cookies, and observed **zero bytes to either measurement
   * endpoint**. So the allowlist carried two permissions nothing exercised, which is precisely the
   * "stale entry is a permission nobody asked for" case the both-directions check exists to find. It
   * found them.
   *
   * They are removed rather than retained, because the alternative is to keep an entry the run cannot
   * confirm — and once one such entry is tolerated the check stops meaning anything for the others.
   *
   * ⚠️ **The risk this accepts, stated rather than buried.** If GA4 does send a hit to one of those
   * hosts in a configuration this run did not reproduce, the policy will refuse it and the funnel will
   * under-count. That failure is not silent: the browser logs a policy refusal, and the next acceptance
   * run with a measurement id reports it as `UNLISTED ORIGIN` naming the host and the bytes. The entry
   * then comes back with a measurement behind it, which is the only basis on which it should have been
   * there in the first place.
   */
  {
    // The GA4 TAG HOST. Declared with `directive: "strict-dynamic"`, so it appears in no directive and
    // is still budgeted and reviewable — see the CspDirective comment for why that third option exists.
    //
    // 🔴 Its budget is 200 KB and the design said 120 KB. That is a MEASUREMENT, not a preference: the
    // acceptance run weighed `gtag.js` at 167,463 bytes on the wire. Raising a ceiling to fit what you
    // just installed is the failure the trend ledger warns about by name, so this is recorded as a
    // number somebody has to look at rather than as a quietly widened one — and the run prints the
    // measured value on every execution, so the gap between 167 and 200 is visible rather than assumed.
    origin: "https://www.googletagmanager.com",
    integration: "Google Analytics 4",
    category: "product_analytics",
    directive: "strict-dynamic",
    budgetBytes: 200 * 1024,
    surfaces: ["public"],
    contactedOn: "page-load",
    why:
      "Wave 24e. The gtag.js tag host. Reached through 'strict-dynamic' from our own nonced loader, so " +
      "script-src gains no host on any prefix; declared here so its weight is budgeted and its presence " +
      "is reviewable.",
  },
  {
    // The Clarity TAG HOST, same treatment and same reason.
    origin: "https://www.clarity.ms",
    integration: "Microsoft Clarity",
    category: "session_replay",
    directive: "strict-dynamic",
    budgetBytes: 80 * 1024,
    surfaces: ["public"],
    contactedOn: "page-load",
    why: "Wave 24e. The Clarity tag host, reached through 'strict-dynamic'. Declared so it is budgeted.",
  },
  {
    // Wave 24e. Microsoft Clarity, PUBLIC PREFIX ONLY, gated on `session_replay`.
    //
    // 🔴 REFUSED on `/app/**` and on every operator route — structurally, not by configuration. See
    // SURFACE_CLASS_RULES: `session_replay` is absent from those classes' permitted categories, so this
    // entry could claim them and still never appear in their policy.
    origin: "https://*.clarity.ms",
    integration: "Microsoft Clarity",
    category: "session_replay",
    directive: "connect-src",
    budgetBytes: 80 * 1024,
    surfaces: ["public"],
    contactedOn: "page-load",
    wildcardReason:
      "Clarity ingests to a REGIONAL host under clarity.ms chosen at runtime; the vendor's own CSP " +
      "guidance is https://*.clarity.ms. Enumerating regions fails closed and silently when a new one " +
      "appears. Bounded: leftmost label only, connect-src only, public class only.",
    why:
      "Wave 24e. Session-replay ingest for the public surface, with masking on by default. A replay of " +
      "/app/studio would be a legible copy of most of the never-permitted list in " +
      "internal/runlink/allowlist.go, which is why this surface list is one entry long.",
  },
];

/**
 * THIRD_PARTY_TOTAL_BUDGET_BYTES is a ceiling on the SUM, kept alongside the per-origin budgets
 * rather than instead of them.
 *
 * Per-origin is the load-bearing one: a total-only budget lets one integration absorb another's
 * headroom silently, which is a decision made by nobody. The total exists so that three integrations
 * each inside their own budget still cannot add up to a public page that costs more than the product.
 */
export const THIRD_PARTY_TOTAL_BUDGET_BYTES = 300 * 1024;

// ── Prefixes ─────────────────────────────────────────────────────────────────

/**
 * SurfaceClass is what a route IS, which is what decides its policy — not which application serves it.
 *
 * The distinction matters because the operator console's `/api` and the customer console's `/api` are
 * different applications with the same rule, and the drift check compares them by class.
 */
export type SurfaceClass = "public" | "tenant" | "operator";

/** ThirdPartyRule is the whole of what a prefix says about third-party origins. */
export type ThirdPartyRule = "none" | "allowlisted";

/** SurfaceClassRule is the rule a class carries, with the argument that produced it. */
export type SurfaceClassRule = {
  thirdPartyOrigins: ThirdPartyRule;
  /** Categories whose origins may appear on this class at all, even when granted. */
  categories: readonly ConsentCategory[];
  why: string;
};

/**
 * SURFACE_CLASS_RULES is the policy per class.
 *
 * `tenant` and `operator` are `"none"` in the sense that matters — no analytics origin and no replay
 * origin, ever, by any grant, plan, role, flag or parameter. The single exception is the error
 * -reporting ingest origin under `connect-src`, which is why `error_diagnostics` appears in their
 * category lists: an error event on those surfaces is constructed from an allowlist and carries no
 * content by construction, whereas a session recording of `/app/studio` is a legible copy of most of
 * the never-permitted list in `internal/runlink/allowlist.go`.
 */
export const SURFACE_CLASS_RULES: Record<SurfaceClass, SurfaceClassRule> = {
  public: {
    thirdPartyOrigins: "allowlisted",
    categories: ["product_analytics", "session_replay", "error_diagnostics"],
    why:
      "The public surface renders no session and no tenant data. It is the only surface where a " +
      "measurable funnel is worth a third-party origin, and every origin it names is on the " +
      "allowlist and gated on its own consent category.",
  },
  tenant: {
    thirdPartyOrigins: "none",
    categories: ["error_diagnostics"],
    why:
      "A tenant surface renders prompt text, generated diffs, node identifiers, model configuration " +
      "and run output. No browser analytics tag and no session recorder, structurally. Console usage " +
      "is emitted server-side from the BFF with a surface id from a closed enum.",
  },
  operator: {
    thirdPartyOrigins: "none",
    categories: ["error_diagnostics"],
    why:
      "An operator surface renders cross-tenant aggregates, tenant names, active impersonation state " +
      "and audit rows. Same refusal as the tenant class, for a strictly larger blast radius. It is a " +
      "staff surface governed by the internal acceptable-use notice and presents no consent banner.",
  },
};

/** ConsoleId names an application. Two of them, and the artefact knows both. */
export type ConsoleId = "customer" | "operator";

/** PrefixPolicy binds a route prefix to a surface class. */
export type PrefixPolicy = {
  prefix: string;
  surface: SurfaceClass;
};

/**
 * CONSOLE_PREFIXES maps each application's route prefixes to a surface class.
 *
 * Ordered LONGEST PREFIX FIRST, and `"/"` is always last: the resolver takes the first match, so a
 * new tenant prefix added above `"/"` is governed, and a route nobody classified falls through to the
 * catch-all rather than to no rule at all. The customer console's catch-all is `public`, which is
 * correct because its unclassified routes ARE public; the operator console's catch-all is `operator`,
 * because every one of its routes is.
 */
export const CONSOLE_PREFIXES: Record<ConsoleId, readonly PrefixPolicy[]> = {
  customer: [
    { prefix: "/app", surface: "tenant" },
    { prefix: "/api", surface: "tenant" },
    { prefix: "/", surface: "public" },
  ],
  operator: [
    { prefix: "/api", surface: "operator" },
    { prefix: "/", surface: "operator" },
  ],
};

/**
 * SHARED_PREFIXES are the prefixes both applications serve, and therefore the ones a drift check can
 * compare. `/api` is the one that matters: both consoles have a BFF, and both BFFs must be governed
 * identically no matter which application happens to be under review.
 */
export const SHARED_PREFIXES: readonly string[] = ["/api"];

// ── The closed surface enum ──────────────────────────────────────────────────

/**
 * SURFACES is the complete set of reportable surface identifiers.
 *
 * 🔴 This is the reason no event in this phase carries a URL. A URL under `/app` carries variant, run,
 * node and tenant identifiers; a surface id carries which page it was. You can read this list and know
 * the complete set of things a third party can be told about where a user was, which you can never do
 * for a URL — a denylist over paths that gain segments every phase.
 *
 * A surface id is `<class>.<name>`. Adding a page means adding a row here; an event naming a surface
 * that is not on this list fails the build.
 */
export const SURFACES = [
  // public
  "public.home",
  "public.install",
  "public.docs",
  "public.legal",
  "public.signin",
  "public.preview",
  // tenant
  "tenant.overview",
  "tenant.account",
  "tenant.authoring",
  "tenant.billing",
  "tenant.configure",
  "tenant.context",
  "tenant.coverage",
  "tenant.delivery",
  "tenant.harness",
  "tenant.memory",
  "tenant.runs",
  "tenant.run",
  "tenant.run_live",
  "tenant.studio",
  "tenant.transforms",
  "tenant.transform",
  "tenant.variants",
  "tenant.scorecard",
  "tenant.wiring",
  "tenant.workflows",
  "tenant.workflow",
  "tenant.workflow_board",
  "tenant.workflow_graph",
  "tenant.workflow_proposals",
  "tenant.workflow_proposal",
  // operator
  "operator.overview",
  "operator.audit",
  "operator.axes",
  "operator.billing",
  "operator.compliance",
  "operator.crosstenant",
  "operator.delivery",
  "operator.fleet",
  "operator.killswitch",
  "operator.oversight",
  "operator.registry",
  "operator.releases",
  "operator.signin",
  "operator.tenants",
  "operator.tenant",
] as const;

/** SurfaceId is the type of a member of the closed enum. A string that is not one does not typecheck. */
export type SurfaceId = (typeof SURFACES)[number];

/** isSurfaceId is the runtime half of the same closed set, for values crossing a wire. */
export function isSurfaceId(value: string): value is SurfaceId {
  return (SURFACES as readonly string[]).includes(value);
}

/** surfaceClassOf reads the class out of a surface id, so the two cannot disagree. */
export function surfaceClassOf(id: SurfaceId): SurfaceClass {
  return id.slice(0, id.indexOf(".")) as SurfaceClass;
}

// ── Runtimes that may never reach a tenant or operator chunk ─────────────────

/**
 * OBSERVABILITY_RUNTIMES is the inverse of `scan-bundle.mjs`'s decorative-runtime list.
 *
 * The decorative list asks "did a 3D library ship". This one asks "did a recorder ship where a
 * recorder is refused". Named by a needle that appears in the runtime's own shipped code rather than
 * inferred from a package name, because an import can be renamed and a bundle cannot.
 *
 * `tenantForbidden` is what makes the two lists different in kind: an error-reporting runtime is
 * PERMITTED on a tenant chunk (design D5 — the browser SDK runs on all three surfaces), while an
 * analytics or replay runtime is refused there structurally (D2, D3).
 */
export type ObservabilityRuntime = {
  name: string;
  needle: string;
  kind: "analytics" | "session_replay" | "error_reporting";
  tenantForbidden: boolean;
};

export const OBSERVABILITY_RUNTIMES: readonly ObservabilityRuntime[] = [
  { name: "Google Analytics (gtag)", needle: "googletagmanager.com/gtag/js", kind: "analytics", tenantForbidden: true },
  { name: "Google Analytics (dataLayer bootstrap)", needle: "window.dataLayer=window.dataLayer||[]", kind: "analytics", tenantForbidden: true },
  { name: "Google Tag Manager", needle: "googletagmanager.com/gtm.js", kind: "analytics", tenantForbidden: true },
  { name: "Microsoft Clarity", needle: "clarity.ms/tag/", kind: "session_replay", tenantForbidden: true },
  { name: "a session-replay recorder (rrweb)", needle: "rrwebSnapshot", kind: "session_replay", tenantForbidden: true },
  { name: "Sentry's replay integration", needle: "replayIntegration", kind: "session_replay", tenantForbidden: true },
  { name: "Sentry's browser-tracing integration", needle: "browserTracingIntegration", kind: "error_reporting", tenantForbidden: true },
];
