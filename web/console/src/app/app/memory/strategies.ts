/**
 * strategies.ts mirrors the platform's closed memory-strategy vocabulary
 * (`registry.BuiltinMemoryStrategies()`, `internal/registry/memory_builtins.go`).
 *
 * # Why a mirror and not a fetch
 *
 * The vocabulary is a property of the BUILD, not of a tenant — the platform serves it at
 * `GET /api/p17/memory` with no tenant, plan, or role input, precisely so no entitlement can change it.
 * Mirroring it here lets the surface render its full explanation without a live platform, which is what
 * the browser-checkable preview needs.
 *
 * 🔴 A mirror with no gate is a second source of truth, and its failure is silent: the page keeps
 * rendering, confidently, a vocabulary that has moved. `tests/memory.test.mjs` reads the engine's own Go
 * source and asserts this file agrees with it, strategy for strategy — that test is the gate, and it is
 * the reason this comment is not just a promise.
 */

export type Strategy = {
  /** The wire name stored in the registry entry. Stable forever; a rename orphans stored hashes. */
  strategy: string;
  /** The human layer, free to be reworded because it is never hashed. */
  title: string;
  /** What this strategy trades away — the thing a person actually chooses between. */
  tradeoff: string;
  /** The parameters the schema bounds, as {name, hint} pairs the form renders. */
  params: { name: string; hint: string; required: boolean }[];
  /**
   * identity marks `none`. It is the one strategy the transform does NOT refuse, because it changes
   * nothing — and selecting it is indistinguishable from clearing.
   */
  identity?: boolean;
};

export const STRATEGIES: Strategy[] = [
  {
    strategy: "none",
    title: "No memory",
    tradeoff:
      "The node carries nothing across invocations. Every call starts from the same blank state — the baseline the other four are measured against.",
    params: [],
    identity: true,
  },
  {
    strategy: "entity-memory",
    title: "Entity facts",
    tradeoff:
      "Structured facts about named entities, and nothing else. The narrowest strategy, and the only one whose loss is readable from the configuration: it carries exactly the keys it declares.",
    params: [
      { name: "entity_keys", hint: "The entity attributes carried across invocations. At least one.", required: true },
    ],
  },
  {
    strategy: "scratchpad",
    title: "Scratchpad",
    tradeoff:
      "Recent working notes, kept verbatim and bounded by count. The oldest note is dropped whole rather than summarized, so what survives is exact and what is lost is gone.",
    params: [{ name: "max_entries", hint: "How many notes to retain before the oldest is dropped.", required: true }],
  },
  {
    strategy: "summary-buffer",
    title: "Rolling summary",
    tradeoff:
      "Everything older than the retained tail is folded into a rolling summary. Cheapest in tokens and the most lossy: what the summary omits cannot be recovered from the configuration.",
    params: [
      { name: "max_tokens", hint: "Token budget for the rolling summary.", required: true },
      { name: "keep_last_turns", hint: "Turns kept verbatim before the summary begins.", required: false },
    ],
  },
  {
    strategy: "vector-recall",
    title: "Vector recall",
    tradeoff:
      "Prior turns are retrieved by embedding similarity rather than recency. Pays a retrieval step to avoid carrying everything, and is only as reproducible as the embedding it pins.",
    params: [
      { name: "top_k", hint: "How many prior turns to recall per invocation.", required: true },
      {
        name: "embedding_ref",
        hint: "The embedding that produced the vectors. Pinned because recall is only reproducible against one embedding.",
        required: true,
      },
    ],
  },
];

/**
 * THE BOUNDARY, mirrored from `transform.CoverageFor("memory")`.
 *
 * At this milestone every non-identity strategy refuses in EVERY language, because what is missing is a
 * memory runtime — a store, a lifetime, a key scheme — plus the call-site rewriter that reads and writes
 * it. Not a per-language rewriter.
 *
 * 🔴 That distinction is the whole reason this constant exists rather than a sentence in the page.
 * Saying "your language's support is pending" would imply another language works and would send the
 * reader to wait for the wrong thing.
 */
export const BOUNDARY = {
  applicable: false,
  missingArtifact:
    "a memory runtime (a store, a lifetime, and a key scheme) plus the call-site rewriter that reads and writes it",
  reason:
    "A memory strategy is read and written BETWEEN invocations, so no expression — and no region — at the call site holds it. This is missing in every language, not in yours.",
  languageIsTheBlocker: false,
  /** Modeling is never refused; only materialization is. This is what keeps the control live. */
  authorableAnyway: true,
} as const;
