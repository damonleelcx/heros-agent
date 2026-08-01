/**
 * csp.ts constructs a Content-Security-Policy header from `third-party-policy.ts`.
 *
 * # Why construction and not a literal
 *
 * Both consoles used to carry the same ten-line array of directive strings. Two copies of a security
 * header is two chances to widen one of them, and the widening is invisible in review because the diff
 * shows a plausible line being added to a file that already contains nine plausible lines. Building the
 * header from data means an origin is added in ONE place, by editing a table that states which
 * integration needs it and which consent grant gates it — and a `https://` literal in either
 * middleware fails the build (`scan-origins.mjs`).
 *
 * # Why the builder is shared but the policy is still drift-checked
 *
 * Sharing the builder makes the two consoles' headers agree on SHAPE by construction. It does not make
 * them agree on CLASSIFICATION: each console passes its own id, and its own prefix→class map lives in
 * the artefact. A mapping that called the operator console's `/api` public would produce a perfectly
 * well-formed header naming an analytics origin on an operator surface. That is what the drift check
 * in `tests/` compares, and it is why the check is not a tautology.
 *
 * # What is refused rather than parameterised
 *
 * `'unsafe-inline'` for scripts and `'unsafe-eval'` in a production build are not options this function
 * accepts. An integration that cannot run without either is refused, not accommodated — there is no
 * argument to pass and no branch to reach.
 */

import {
  ALLOWED_ORIGINS,
  CONSOLE_PREFIXES,
  SURFACE_CLASS_RULES,
  type AllowedOrigin,
  type ConsentCategory,
  type ConsoleId,
  type CspDirective,
  type SurfaceClass,
} from "./third-party-policy.ts";

/**
 * resolveSurfaceClass returns the class governing `pathname` in `consoleId`.
 *
 * First match wins over a longest-first list, so a prefix added above the catch-all governs and an
 * unclassified route falls through to the catch-all rather than to no rule at all.
 */
export function resolveSurfaceClass(consoleId: ConsoleId, pathname: string): SurfaceClass {
  for (const policy of CONSOLE_PREFIXES[consoleId]) {
    if (policy.prefix === "/") return policy.surface;
    if (pathname === policy.prefix || pathname.startsWith(`${policy.prefix}/`)) return policy.surface;
  }
  // Unreachable while every map ends in "/", and a hard failure rather than a permissive default if
  // somebody removes it: a request with no policy must not be a request with no restrictions.
  throw new Error(`no prefix policy governs ${pathname} in the ${consoleId} console`);
}

/**
 * originsFor returns the origins a surface class may name, given the categories currently granted.
 *
 * THREE independent narrowings, and each is needed for a different reason.
 *
 *  1. The origin must name this class in its own `surfaces` list — the allowlist's own statement.
 *  2. The class must permit the origin's CATEGORY. This is the structural refusal: `session_replay` is
 *     absent from the tenant and operator class lists, so a replay origin cannot appear on those
 *     surfaces by any grant, plan, role, flag or parameter. It is also why the classes whose
 *     `thirdPartyOrigins` is `"none"` are not special-cased here — "none" is a readable statement of
 *     the outcome, and the category list is the mechanism that produces it. Two spellings of the same
 *     rule, one of which does the work, is how the other one drifts.
 *  3. The visitor must have GRANTED that category. An origin whose category has not been granted is
 *     absent from the header, so a declined category is refused by the browser rather than merely
 *     unrequested by our own loader.
 */
export function originsFor(
  surface: SurfaceClass,
  granted: readonly ConsentCategory[],
): readonly AllowedOrigin[] {
  const rule = SURFACE_CLASS_RULES[surface];
  return ALLOWED_ORIGINS.filter(
    (o) =>
      o.surfaces.includes(surface) &&
      rule.categories.includes(o.category) &&
      granted.includes(o.category),
  );
}

/** CspInput is everything the header depends on. Nothing is read from ambient state. */
export type CspInput = {
  consoleId: ConsoleId;
  pathname: string;
  nonce: string;
  /** True only for a development server. A production build never reaches the relaxed branch. */
  dev: boolean;
  /** The categories the visitor has explicitly granted. Empty is the default, and it is correct. */
  granted?: readonly ConsentCategory[];
};

/**
 * sourcesFor renders the origins for one directive.
 *
 * An origin whose directive is `"strict-dynamic"` contributes NOTHING here, by design: it is a script
 * host reached through trust propagation from the nonced loader, it is declared on the allowlist so it
 * is budgeted and reviewable, and putting it in the header would give `script-src` a host — which is
 * the one thing that would make `'strict-dynamic'` decorative.
 */
function sourcesFor(
  directive: CspDirective,
  origins: readonly AllowedOrigin[],
): string {
  return origins
    .filter((o) => o.directive === directive)
    .map((o) => o.origin)
    .join(" ");
}

/**
 * buildContentSecurityPolicy returns the exact header value.
 *
 * The directive ORDER is fixed and matches the header both consoles shipped before P24, so the
 * migration from a literal to a construction is byte-identical on an empty allowlist — asserted, not
 * asserted-by-eye.
 */
export function buildContentSecurityPolicy(input: CspInput): string {
  const surface = resolveSurfaceClass(input.consoleId, input.pathname);
  const origins = originsFor(surface, input.granted ?? []);

  const scriptSrc = [
    `'self'`,
    `'nonce-${input.nonce}'`,
    `'strict-dynamic'`,
    input.dev ? `'unsafe-eval'` : "",
  ]
    .filter(Boolean)
    .join(" ");

  const connect = sourcesFor("connect-src", origins);
  const img = sourcesFor("img-src", origins);

  return [
    `default-src 'self'`,
    `script-src ${scriptSrc}`,
    `style-src 'self' 'unsafe-inline'`,
    `img-src 'self' data:${img ? ` ${img}` : ""}`,
    `connect-src 'self'${connect ? ` ${connect}` : ""}`,
    `font-src 'self'`,
    `object-src 'none'`,
    `base-uri 'self'`,
    `form-action 'self'`,
    `frame-ancestors 'none'`,
  ].join("; ");
}
