import { Section } from "@/components/primitives";

/**
 * WiringBoundaries states, BEFORE a reader reaches for a node, the two facts that decide whether a
 * reorder can ship (P15 20.14, FR36).
 *
 * # Why two, and why they must not be one sentence
 *
 * They have different owners and different next steps:
 *
 *   1. Can this node's LANGUAGE carry a transposition?   — ours. There is a "when", and the missing
 *      piece is a statement resolver, which the coverage surface names.
 *   2. Does this WORKFLOW offer a transposable pair?     — the source's. No amount of platform work
 *      changes it: on a real repository this is the refusal a reader hits first, because most nodes are
 *      not adjacent sibling statements.
 *
 * Collapsing them into "reordering is unavailable here" sends the second reader to wait for the first
 * reader's fix. That is the failure this component exists to prevent, and it is why the wording of the
 * second deliberately contains no "yet".
 */
export function WiringBoundaries() {
  return (
    <Section title="Before you move anything">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="rounded-md border border-dashed border-primary/50 px-4 py-3">
          <p className="mono mb-1 text-[10px] uppercase tracking-wide">not yet — ours</p>
          <p className="text-sm leading-snug text-foreground/80">
            A reorder is materialised by exchanging two adjacent statements, which needs this
            language&rsquo;s statement boundaries. Where that resolver has not landed, the platform says
            so and names it — see <strong className="font-medium">Coverage</strong>. Nothing in your code
            unlocks it.
          </p>
        </div>
        <div className="rounded-md border border-l-2 border-border border-l-foreground/45 px-4 py-3">
          <p className="mono mb-1 text-[10px] uppercase tracking-wide">your workflow</p>
          <p className="text-sm leading-snug text-foreground/80">
            Even where the resolver exists, this axis materialises exactly one shape: a single exchange
            of two <em>adjacent sibling statements</em>. A workflow whose nodes are not adjacent — or a
            merge, a prune, an edge change, a non-adjacent move — is refused by shape, in every language.
            That is a fact about the source, and it has no &ldquo;when&rdquo;.
          </p>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Both answers are identical on every plan. A refused shape is also never scored: evaluating a
        wiring hash against source that was never rearranged would be a false measurement, not a partial
        one.
      </p>
    </Section>
  );
}
