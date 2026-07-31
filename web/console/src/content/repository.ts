/**
 * repository.ts is the one place the project's public repository is named (tasks 7.1–7.5).
 *
 * # 🔴 The link is unconditional. The count is opt-in, default OFF.
 *
 * The console sets a per-request CSP in `middleware.ts`: `default-src 'self'`, `connect-src 'self'`,
 * `img-src 'self' data:`. That file's own comment names the public page's no-third-party-origin rule as
 * the reason. So the three usual ways to render a star count are **already refused at runtime**:
 *
 *   shields.io / badge `<img>`                    refused by `img-src 'self' data:`
 *   GitHub buttons widget / iframe                refused by `default-src 'self'`
 *   browser-side `fetch('https://api.github.com')` refused by `connect-src 'self'`
 *
 * This module takes the constraint as the design rather than carving an exception:
 *
 *   - **The link is an anchor.** It makes no request until clicked — no privacy leak, no CSP change, no
 *     air-gapped degradation, no loading state that can spin forever, and no client component.
 *   - **The count, if ever shown, is captured during the BUILD** by `scripts/gen-repo-measurement.mjs`
 *     and rendered as a server-side string stamped with its measurement date. One machine we control,
 *     once per deploy — not once per visitor.
 *   - **Never hand-typed.** Same rule as a release checksum, same reason.
 *   - **Degrades to the plain link** when the measurement is unavailable (offline build, rate limit, API
 *     failure). Never `0`, never a placeholder, never a broken badge: an unavailable measurement rendered
 *     as zero is a false statement, and it is the one a reader will believe.
 *
 * # Why the display decision was escalated rather than defaulted
 *
 * The repository is public and has **0 stars** today. "★ 0" on a marketing home page is worse than
 * nothing. That is a judgement a person makes, so it was escalated (task 7.5) and the answer was: ship
 * the link, keep the machinery, leave the count off. `SHOW_STAR_COUNT` is that answer, and turning it on
 * is a configuration change in a reviewed pull request rather than a behaviour that appears on its own
 * when a number becomes available.
 *
 * # Why not a threshold ("hide it below 50")
 *
 * That is the same dishonesty with extra machinery: a number that appears only when it flatters is a
 * number a reader cannot interpret. Off is honest. On is honest. Conditional-on-flattery is not.
 */

/**
 * REPOSITORY names the project's public repository.
 *
 * `scripts/scan-repo-link.mjs` resolves this against the forge at build time and FAILS THE BUILD if it is
 * private or does not exist — the same rule that forbids an install command that 404s. A link to a
 * repository a reader cannot open is worse than no link: it says the project is open and then proves it
 * is not.
 */
export const REPOSITORY = {
  owner: "damonleelcx",
  name: "heros-agent",
  url: "https://github.com/damonleelcx/heros-agent",
} as const;

/**
 * SHOW_STAR_COUNT is the escalated display decision (task 7.5). OFF.
 *
 * The link does not depend on it. Only the count does.
 */
export const SHOW_STAR_COUNT = false;

/** A build-time measurement of a public fact about the repository, with the date it was taken. */
export type RepoMeasurement = {
  stars: number;
  /** YYYY-MM-DD, in UTC. Rendered beside the number — a measurement with no date is a claim. */
  measured_on: string;
};
