import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Banner, DataTable } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { MemoryAuthoring } from "./authoring";
import { STRATEGIES, BOUNDARY } from "./strategies";

/**
 * The memory surface (P17).
 *
 * # What makes this axis different from every other one
 *
 * On the model, prompt, skills, tools, context and wiring surfaces, an authored change reaches the
 * reader's source as a diff and is merely *unscored* until the harness runs. Memory is the axis where
 * whether it reaches the source AT ALL depends on the cell — Python call sites materialize it, and every
 * other language is refused by name until its module lands.
 *
 * That makes this the hardest surface in the console to write honestly, because the temptation runs in
 * two opposite directions and both are wrong:
 *
 *   - Disable the controls and explain later. The reader learns nothing about WHY, and reasonably
 *     concludes that some other strategy, language, or plan would unlock it.
 *   - Let them compose a change and refuse at the end. Technically honest; practically a bait-and-switch,
 *     because the platform knew before they started.
 *
 * So: the boundary is stated FIRST and PER CELL, the controls stay live everywhere, and a refusal is
 * shown in its own state beside the real thing the change did produce — a configuration that resolves,
 * hashes, records, and survives to the day its rewriter lands.
 *
 * # Memory is not context
 *
 * The one distinction a reader must leave with. Memory persists ACROSS invocations and sessions; context
 * assembly is how a SINGLE call builds its message list. They are separate dimensions, separate
 * registries, and separate hashes — a memory ref pasted into the context dimension does not resolve, it
 * fails closed. The Boundary tab says this in the reader's own terms rather than in ours.
 *
 * # Why tabs
 *
 * Viewport-first (NFR17). Four sections stacked would put three below the fold, and the two that matter
 * most — the picker and the boundary that governs it — are the ones a reader moves between.
 */
export const dynamic = "force-dynamic";

/**
 * COVERAGE mirrors `transform.CoverageFor("memory")`, which is now a PER-CELL read of the materializer
 * table rather than the uniform refusal it was before the memory runtime landed.
 *
 * `tests/memory.test.mjs` reads the registry's and the engine's own Go source and asserts this agrees
 * with them. Without that gate this is a second source of truth, and its failure is silent.
 */
const COVERAGE = STRATEGIES.map((s) => ({
  strategy: s.strategy,
  mode: s.identity ? "applies everywhere" : `applies in ${BOUNDARY.applicableIn.join(", ")}`,
  what: s.identity
    ? "The identity strategy. It changes nothing, so there is nothing to write into your source and no diff is the correct diff — not a missing one."
    : "Where the language has a memory module: the written message list is wrapped in a recall, and the turn is recorded after the call — both halves, or the call site is refused whole. Elsewhere it still resolves, hashes, and is recorded, and is refused by name rather than quietly applied.",
}));

/** What separates this axis from the context axis, in the terms a reader thinks in. */
const BOUNDARY_ROWS = [
  {
    axis: "Context",
    scope: "Within ONE call",
    question: "How is this message list assembled?",
    where: "The code around the call site that builds the messages.",
  },
  {
    axis: "Memory",
    scope: "ACROSS invocations",
    question: "What does this node carry from prior turns?",
    where: "A store read and written between turns — nowhere in the call itself.",
  },
];

function AuthorTab() {
  return <MemoryAuthoring />;
}

function BoundaryTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Memory is not context">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          The two axes both concern what a model effectively sees, which is why they are easy to
          conflate — and why the platform keeps them strictly apart. They have separate dimensions,
          separate registries, and separate hashes. A memory reference used where a context policy is
          expected does not quietly bind the wrong thing; it fails to resolve.
        </p>
        <DataTable
          caption="What each axis decides, and where its answer lives in your code"
          columns={[
            { key: "axis", label: "Axis" },
            { key: "scope", label: "Scope" },
            { key: "question", label: "The question it answers" },
            { key: "where", label: "Where it lives in your code" },
          ]}
        >
          <tbody>
            {BOUNDARY_ROWS.map((r) => (
              <tr key={r.axis}>
                <td className="text-sm align-top font-medium text-foreground">{r.axis}</td>
                <td className="mono text-sm align-top">{r.scope}</td>
                <td className="text-sm align-top text-muted-foreground">{r.question}</td>
                <td className="text-sm text-muted-foreground">{r.where}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
        <p className="hint">
          That second row is why memory needed a runtime and a generated module before it could be
          applied at all: there is no argument, and no region of code, at the call site that holds a
          store. What a materialized call site gets instead is a call into that module.
        </p>
      </Section>

      <Section title="What this platform writes into your source">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          The table is the same one the engine materializes and refuses from, and it <em>does</em> vary by
          language: the shared memory runtime has landed, and a language materializes once its own module
          and call-site rewriter have. A language without one is refused by name and told which languages
          do work — never quietly applied.
        </p>
        <DataTable
          caption="What each memory strategy does at a call site, and why"
          columns={[
            { key: "strategy", label: "Strategy" },
            { key: "mode", label: "At a call site" },
            { key: "what", label: "What happens, and why" },
          ]}
        >
          <tbody>
            {COVERAGE.map((c) => (
              <tr key={c.strategy}>
                <td className="mono text-sm align-top">{c.strategy}</td>
                <td className="align-top">
                  <Chip tone="ok" title="what this platform does with the strategy">
                    {c.mode}
                  </Chip>
                </td>
                <td className="text-sm text-muted-foreground">{c.what}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </Section>

      <Section title="Why a refusal is the safe half">
        <Banner tone="info" title="A refused change is never a dropped one">
          <p>
            If a memory strategy could not be written into your source and the platform applied it as a
            no-op, the run would use your original behaviour while reporting the variant&apos;s
            configuration hash. The number would be wrong, and it would look exactly like a number that
            is right.
          </p>
          <p>
            So a memory override ends in one of three states and never a fourth:{" "}
            <strong>materialized</strong> (both halves — the recall and the record — are in the diff),{" "}
            <strong>refused</strong> (nothing was written, and the reason names the strategy or the call
            site), or <strong>equivalent</strong> (the identity strategy, which changes nothing).
          </p>
          <p>
            There is deliberately no <em>partially applied</em>. A memory that recalls from a store
            nothing ever fills, or fills one nothing ever reads, behaves exactly like no memory at all
            while its configuration hash claims otherwise — so a call site that can carry only one half is
            refused whole.
          </p>
        </Banner>
      </Section>
    </div>
  );
}

function ProposalsTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="The platform can propose a memory change too">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          When a diagnosis finds stale reads or contradictory recall on a node the classifier labels{" "}
          <span className="mono">memory_management</span>, the catalog proposes a strategy swap — the
          same change you can author yourself, reached from the other direction. Both origins produce the
          same configuration and the same hash, and both are refused by the same rewriter.
        </p>
        <Banner tone="warn" title="A proposal here carries no result, and will not until the rewriter lands">
          <p>
            Diagnosis proposes; verification decides. While the transform refuses a memory change, a
            memory proposal <strong>cannot be verified</strong> — so it cannot be a win, a regression, or
            a tie. It is surfaced as <strong>refused, not scored</strong>, and it never enters the
            verified-delta ledger.
          </p>
          <p>
            The improvement signal, when it becomes measurable, is the one the classifier already
            collects for this pattern: <span className="mono">memory_hit_rate</span>,{" "}
            <span className="mono">staleness</span>, <span className="mono">recall_precision</span>,{" "}
            <span className="mono">write_amplification</span>. No number was invented for this axis.
          </p>
        </Banner>
      </Section>

      <Section title="What you can do with an unapplied change">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          More than it may look like. A memory configuration you author is addressable by its hash,
          diffable against its parent, attributable to you, and re-materializable unchanged. You can pin
          the strategy you believe in, hand the hash to a colleague, and have it apply the day the runtime
          lands — without re-authoring it.
        </p>
        <p className="hint">
          What you cannot do is get a diff today, or a measured result before the harness has run. Those
          are the two things the platform declines to fake.
        </p>
      </Section>
    </div>
  );
}

export default async function MemoryPage() {
  await requireSession();

  const tabs: TabItem[] = [
    { id: "author", label: "Set a strategy", content: <AuthorTab /> },
    { id: "boundary", label: "What applies where", content: <BoundaryTab /> },
    { id: "proposals", label: "Proposals", content: <ProposalsTab /> },
  ];

  return (
    <PageFrame
      eyebrow="Memory"
      title="What this agent carries between turns"
      lede={
        <>
          Set a node&apos;s memory strategy yourself, instead of waiting for a diagnosis to propose one.
          The change resolves, hashes, and is recorded everywhere; it is written into your source where
          the language has a memory module, and refused by name where it does not. This surface states
          which you are in before you choose, rather than after.
        </>
      }
    >
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
