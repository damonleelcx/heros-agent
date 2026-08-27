import { paramsFromSchema, type AxisOption, type AxisVocabulary } from "./axisKit";

/**
 * axisVocabularyShapes.ts adapts each axis's wire shape into the ONE shape a picker binds to (P37 FR5).
 *
 * # 🔴 Why these are here and not in `axisVocabulary.ts`
 *
 * That module is `server-only` — it fetches. These are pure functions over data that has already
 * arrived, and a CLIENT component needs them: the editor is interactive, so the picker it renders is a
 * client component, and a client component that imported a `server-only` module is a build error.
 *
 * Keeping them apart also keeps the honest split visible: fetching is a server concern and SHAPING is
 * not, so the shaping can be unit-tested without a platform and a fixture.
 */

/** PolicyCoverage is what the engine does with one context policy at a call site in one language. */
export type PolicyCoverage = { policy: string; mode: string; reason?: string };

/**
 * contextVocabularyFrom turns the live per-language coverage into a picker's options.
 *
 * 🔴 A `declined` policy is STILL OFFERED (FR7), rendered with the reason it cannot reach source. A
 * policy the reader cannot see is one they cannot ask about — and on this axis the declines are the
 * policies readers most want, so hiding them would hide the whole boundary.
 *
 * Returns `null` when the language has no coverage at all. That is NOT an empty picker: an empty picker
 * says "no options", and the truth is "no rewriter has landed for this language", which is a different
 * sentence with a different owner. The surface renders the reason instead.
 *
 * The context axis carries no params schema on the wire today, so every option's params list is empty.
 * Stated rather than faked: a hand-written parameter list here would be exactly the second copy
 * `paramsFromSchema` exists to prevent.
 */
export function contextVocabularyFrom(
  coverage: PolicyCoverage[],
  setVersion: string,
): AxisVocabulary | null {
  if (coverage.length === 0) return null;
  return {
    axis: "context policy",
    setVersion,
    options: coverage.map((c) => ({
      id: c.policy,
      title: c.policy,
      tradeoff: c.reason,
      params: [],
      identity: c.mode === "identity",
      // What a declined policy needs is not a service this deployment could install — it is a rewriter,
      // or it is nothing at all — so the reason is the engine's own word rather than a service name.
      unavailableReason:
        c.mode === "declined" ? "a rewriter this policy will never have at a call site" : undefined,
    })),
  };
}

/** MemoryStrategyRow is one entry of the memory axis's closed vocabulary, as the platform sends it. */
export type MemoryStrategyRow = {
  strategy: string;
  title: string;
  description: string;
  params_schema?: unknown;
  identity?: boolean;
  applies?: boolean;
};

/**
 * memoryVocabularyFrom turns the registry's closed strategy set into a picker's options.
 *
 * 🔴 Every option's params are DERIVED from the entry's own `params_schema` (task 3.2) — the same schema
 * the registry validates against at seal. One schema, two readers; a form with its own idea of what is
 * required is the copy that drifts, and it drifts in the direction that lets a value through.
 */
export function memoryVocabularyFrom(rows: MemoryStrategyRow[], setVersion: string): AxisVocabulary {
  const options: AxisOption[] = rows.map((s) => ({
    id: s.strategy,
    title: s.title,
    tradeoff: s.description,
    params: paramsFromSchema(s.params_schema),
    identity: s.identity === true,
  }));
  return { axis: "memory strategy", setVersion, options };
}

/** ModelRow is one entry of the registered model catalogue. */
export type ModelRow = { version_id: string; name: string; provider?: string; model_id?: string };

/**
 * modelVocabularyFrom turns the model catalogue into a picker's options for ONE call site.
 *
 * 🔴 A model from another provider is RETURNED, disabled, naming the SDK it would need — never omitted
 * (FR7). A call site written against one SDK does not become another by changing a model string, so
 * offering it would produce a diff that compiles and then calls the wrong provider in production. But a
 * list that is silently short reads as an incomplete catalogue, and the reader's next move is to look for
 * the missing entries or file a bug.
 */
export function modelVocabularyFrom(
  rows: ModelRow[],
  provider: string,
  setVersion: string,
): AxisVocabulary {
  return {
    axis: "model",
    setVersion,
    options: rows.map((m) => ({
      id: m.version_id,
      title: m.model_id ?? m.name,
      tradeoff: m.provider,
      params: [],
      unavailableReason:
        provider && m.provider && m.provider !== provider
          ? `a call site written against the ${m.provider} SDK`
          : undefined,
    })),
  };
}
