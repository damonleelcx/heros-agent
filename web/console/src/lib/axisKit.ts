/**
 * axisKit.ts is the editor kit's VOCABULARY TYPES and its one pure function (P37 FR5).
 *
 * # 🔴 Why this is not in `components/editorKit.tsx`, where it started
 *
 * That file is `"use client"`. `lib/axisVocabulary.ts` is a SERVER module and calls `paramsFromSchema`
 * while building the memory picker's options, and Next.js allows that import and then fails at REQUEST
 * TIME with:
 *
 *   Attempted to call paramsFromSchema() from the server but paramsFromSchema is on the client.
 *
 * The build was green, `tsc` was green, every source-reading test passed, and `/app/memory` answered 500
 * for every reader. Only the rendered acceptance run found it.
 *
 * So the pure half lives here, with no directive, importable from either side — and the kit re-exports
 * it so no call site needed changing. The rule this encodes: a directive is a property of a MODULE, so
 * shared pure logic does not live in one that has a directive.
 */

/** ParamField is one tunable parameter, derived from the entry's declared params schema. */
export type ParamField = {
  name: string;
  hint: string;
  required: boolean;
};

/**
 * AxisOption is one member of an axis's closed vocabulary.
 *
 * `unavailableReason` is FR7: an option this deployment cannot supply is rendered, disabled, NAMING the
 * service it needs — never hidden. A hidden option is indistinguishable from one that does not exist,
 * and a reader who cannot see it cannot ask for it.
 */
export type AxisOption = {
  id: string;
  title: string;
  /** tradeoff is one clause — what this option gives up. The full explanation is on the reading surface. */
  tradeoff?: string;
  params: ParamField[];
  /** identity marks the option that changes nothing, so selecting it is indistinguishable from clearing. */
  identity?: boolean;
  /** unavailableReason names the SERVICE this option needs and this deployment does not supply. */
  unavailableReason?: string;
};

/**
 * AxisVocabulary is a closed set at a recorded version (FR5).
 *
 * 🔴 `setVersion` is rendered beside the picker. A `config_hash` a reader pins today has to still mean
 * the same thing months later, and the only way a reader can check that is to be told which version of
 * the set their choice was made against.
 */
export type AxisVocabulary = {
  axis: string;
  setVersion: string;
  options: AxisOption[];
};

/** paramsFromSchema derives the form's fields from a JSON-Schema `params_schema`. */
export function paramsFromSchema(schema: unknown): ParamField[] {
  // 🔴 Derived, never hand-written (FR5, task 3.2). A hand-written field list is a second, staler copy
  // of the schema the registry validates against, and the two disagree in the direction that lets a
  // value through — discovered at run time, by the wrong person.
  if (typeof schema !== "object" || schema === null) return [];
  const s = schema as { properties?: Record<string, unknown>; required?: unknown };
  const properties = s.properties;
  if (typeof properties !== "object" || properties === null) return [];
  const required = new Set(Array.isArray(s.required) ? s.required.map(String) : []);
  return Object.entries(properties).map(([name, raw]) => {
    const field = (typeof raw === "object" && raw !== null ? raw : {}) as { description?: unknown };
    return {
      name,
      hint: typeof field.description === "string" ? field.description : "",
      required: required.has(name),
    };
  });
}

