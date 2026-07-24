/**
 * cx joins class names, dropping anything falsy.
 *
 * It is deliberately not `clsx` + `tailwind-merge`. Those exist to resolve CONFLICTING utilities at
 * runtime — `px-2` beating `px-4` because it came last — and a component whose padding depends on the
 * order two callers happened to pass their props in is a component nobody can reason about. Where a
 * variant changes a property here, the variant owns the whole property, so there is nothing to merge.
 *
 * Keeping it to four lines also keeps it off the payload budget: this is the single most frequently
 * imported module in the console.
 */
export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}
