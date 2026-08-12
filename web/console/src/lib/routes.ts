/**
 * routes.ts is every canonical route, in one place (information-architecture.md §3).
 *
 * # Why it is a separate module from subjects.ts
 *
 * `subjects.ts` is `server-only` — it reads the session. Client components build links too: the
 * leaderboard links each row to its variant's scorecard, and the command path navigates. Importing a
 * server-only module from either would be a build error, and inlining the paths at those call sites
 * would be worse — a "recently visited" entry pointing at a route that no longer exists is worse than
 * no entry at all.
 *
 * So the routes live here, with no imports, usable from both sides. There is exactly one definition of
 * where each subject lives.
 */

/**
 * routes builds every canonical route in one place (information-architecture.md §3).
 *
 * Centralised so a link and a `recordVisit` cannot disagree about where a subject lives — a
 * "recently visited" entry pointing at a route that no longer exists is worse than no entry.
 */
export const routes = {
  overview: () => "/app",
  configure: () => "/app/configure",
  workflow: (id: string) => `/app/workflows/${encodeURIComponent(id)}`,
  graph: (id: string) => `/app/workflows/${encodeURIComponent(id)}/graph`,
  board: (id: string, profile?: string) =>
    `/app/workflows/${encodeURIComponent(id)}/board` + (profile ? `?profile=${encodeURIComponent(profile)}` : ""),
  proposals: (id: string) => `/app/workflows/${encodeURIComponent(id)}/proposals`,
  // P30 §1.12 · deep-linkable, not a modal: "which cases is this number over?" is a question somebody
  // asks in a review and links to, and an overlay has no URL to send.
  evalSet: (id: string) => `/app/workflows/${encodeURIComponent(id)}/evalset`,
  proposal: (workflowId: string, proposalId: string) =>
    `/app/workflows/${encodeURIComponent(workflowId)}/proposals/${encodeURIComponent(proposalId)}`,
  run: (id: string) => `/app/runs/${encodeURIComponent(id)}`,
  runLive: (id: string) => `/app/runs/${encodeURIComponent(id)}/live`,
  transform: (configHash: string, sourceRevision: string) =>
    `/app/transforms/${encodeURIComponent(configHash)}/${encodeURIComponent(sourceRevision)}`,
  scorecard: (variantId: string) => `/app/variants/${encodeURIComponent(variantId)}/scorecard`,
  studio: () => "/app/studio",
  authoring: () => "/app/authoring",
  wiring: () => "/app/wiring",
  harness: () => "/app/harness",
  coverage: () => "/app/coverage",
  account: () => "/app/account",
  members: () => "/app/settings/members",
  // Where a person approves a terminal login. The path is also compiled into the platform's device
  // response (`deviceVerificationPath`), because the CLI prints it before any browser is open — the two
  // are asserted equal by `tests/routes.test.mjs` rather than kept in step by memory.
  device: () => "/app/device",
  billing: () => "/app/billing",
  delivery: () => "/app/delivery",
  // Public: no session. The people who need it do not have an account yet — see src/app/install/page.tsx.
  install: () => "/install",
};
