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
  // P31. The conversational surface. It has no subject in its path: the workflow is chosen in the
  // composer, because a person who arrives here does not yet know which workflow their question is
  // about — that is the whole reason they are typing a sentence instead of navigating.
  ask: () => "/app/ask",
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
  // P34: `/app/wiring` became `/app/graph`, which carries the wiring axis AND topology. The old path
  // still resolves (it redirects), and it is kept here so a caller holding a stored link is not broken.
  wiring: () => "/app/graph",
  // 🚫 NOT named `graph`: that key is already taken by a WORKFLOW's own graph view
  // (`/app/workflows/{id}/graph`), which is a different thing — one workflow's discovered shape, versus
  // the axis surface that explains what the platform can change about any shape. Two routes, two names.
  graphAxis: () => "/app/graph",
  loop: () => "/app/loop",
  harness: () => "/app/harness",
  coverage: () => "/app/coverage",
  // P33. The nine-axis assessment of one workflow: what the repository does on each surface, what
  // evidence says so, and — where there is none — that there is none.
  //
  // 🔴 Its own surface rather than a tab on Workflows, because the reader's question is about the
  // REPOSITORY ("what is weak here?") rather than about a workflow's runs, and because it is the one
  // page whose product is reporting ABSENCE: nine rows, always nine, and the ones that say "we could
  // not" are the ones with the most to read.
  assess: () => "/app/assess",
  // P35. Where a question becomes a bounded plan, a set of verified proposals, and — one approval at a
  // time — a pull request.
  //
  // 🔴 Its own surface rather than a tab on Assess, because the reader's question changes: Assess
  // answers "what is weak here?" and this answers "fix it". They are read at different moments by
  // people in different states, and the second one SPENDS MONEY and authorizes a write to a
  // repository. A tab would put a paid, consequential act one keystroke from a free, read-only report.
  improve: () => "/app/improve",
  // P32. Where a workflow's SOURCE comes from — a pushed bundle, a connected repository, or a local
  // machine. Its own surface rather than a tab on Workflows because the question it answers is about
  // the GRANT ("what may the platform read, and when did it") rather than about a workflow, and a
  // person revoking access after an incident must be able to find it without knowing which workflow.
  connections: () => "/app/connections",
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

/**
 * WORKING_SURFACES is the set the conversational console's intent table must be EQUAL to (P31 D9,
 * task 6.15).
 *
 * # Why this list exists beside `routes` rather than being derived from it
 *
 * `routes` holds every canonical route, and most of them are not agent goals. `/app/billing` is a
 * surface; it is not something to ask an agent to do. `/app` is a shell. `/install` is public. Deriving
 * the intent set from `routes` would demand an intent for each of those, and the only way to satisfy it
 * would be an exemption list — which is the hand-maintained artefact the fence exists to replace.
 *
 * So every route is classified exactly once, HERE, into one of three buckets, and
 * `tests/intent-surfaces.test.mjs` fails when a route belongs to none. Adding a route therefore forces
 * a decision — "is this something a person can ask for?" — rather than letting one be inherited.
 *
 * # 🔴 The drift this prevents runs in one direction only
 *
 * A surface ships, nobody adds its intent, and the conversation quietly cannot reach it. Nothing fails:
 * the user asks and gets a REFUSAL — well-formed, polite, and indistinguishable from the surface not
 * existing. That is the shape P26 found after fourteen phases of operator-console drift, with nothing
 * going red the whole time.
 */
export const WORKING_SURFACES: readonly string[] = [
  "/app/workflows",
  "/app/runs",
  "/app/variants",
  "/app/transforms",
  "/app/delivery",
  "/app/studio",
  "/app/authoring",
  "/app/graph",
  "/app/context",
  "/app/memory",
  "/app/loop",
  "/app/harness",
  "/app/coverage",
  // P33. A person can absolutely ask for this: it is the sentence the whole conversational surface was
  // built around — look at my repository and tell me what is weak — so it is a WORKING surface rather
  // than a shell one. (No quotation marks in this comment: the Go-side fence reads every quoted string
  // out of the array block, so a quoted phrase here would arrive as a fourteenth surface.)
  "/app/assess",
  // P35. The sentence the whole program was built around ends here: fix it, and open a pull request.
  // It was the one intent backed by a CAPABILITY rather than a route, because the capability had
  // nowhere to render. It has somewhere now, and flipping it is the event that classification was
  // waiting for rather than a tidy-up.
  "/app/improve",
];

/**
 * OUT_OF_SCOPE_SURFACES are surfaces a person can legitimately ask ABOUT and that the agent will not
 * DO (FR26). They are surfaces, they are not agent goals: an agent that offers to change a plan or a
 * password has crossed from answering about a system to administering an account.
 *
 * The conversational router names these in its refusal, so the two lists are compared by the fence.
 */
export const OUT_OF_SCOPE_SURFACES: readonly string[] = [
  "/app/billing",
  "/app/account",
  "/app/settings/members",
  // P32 · the source surface. 🔴 Out of scope for a reason that is NOT "this is administration":
  // connecting a repository creates a standing read grant, and FR10 requires the disclosure be
  // DISPLAYED before authorization can complete. An agent acting on "connect my repo" would create
  // that grant from a sentence — the exact path around the consent screen the requirement closes.
  // Revoking is out of scope for the mirror reason: its confirmation states what will be deleted, and
  // a conversational shortcut past a destructive confirmation is the same mistake pointed the other
  // way. The agent names the surface and stops.
  "/app/connections",
];

/**
 * SHELL_SURFACES are routes that are neither a working surface nor a redirection target: the overview,
 * the configure form, the device approval, the personal account page, and the public install page.
 * Classified rather than omitted, because "not listed anywhere" is how a route escapes the fence.
 */
export const SHELL_SURFACES: readonly string[] = [
  "/app",
  "/app/ask",
  "/app/configure",
  "/app/device",
  "/app/settings/account",
  "/install",
];
