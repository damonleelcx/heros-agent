import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Banner, DataTable } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { HarnessAuthoring } from "./authoring";
import { HARNESS_STRATEGIES, HARNESS_BOUNDARY, MAX_TURNS_CEILING } from "./strategies";

/**
 * The harness surface (P18).
 *
 * # What makes this axis different from every other one
 *
 * Every other axis changes what ONE model call does — which model, which prompt, which tools, which
 * context, what it remembers. This one changes HOW MANY CALLS HAPPEN. That single fact drives both
 * things this surface does that no sibling does:
 *
 *   1. It leads with COST. A heavier scaffold multiplies what a node costs on every case, including the
 *      ones that already pass, and that multiplier is arithmetic a reader can be told before anything
 *      runs. Its benefit is a measurement and cannot be. Saying the first and withholding the second is
 *      the honest shape — and it is what keeps a picker from reading as a recommendation.
 *   2. Its boundary is PER CELL, so the reader picks their language and the badges change. A single
 *      verdict for the axis would be wrong in both directions: it would tell a Python reader that
 *      `reflexion` is unavailable, and it would tell every reader that `react-loop` is merely pending.
 *
 * # The third answer, which is the one nobody expects
 *
 * Three of the five strategies are refused in EVERY language, permanently. `react-loop` needs a tool
 * executor, `plan-execute` needs a planner, `critic-loop` needs a separate critic model — and a call
 * site has nowhere to inject any of them. The generated module makes no provider call and dispatches no
 * tool by design, because a generated file that reached a provider would put the customer's credential
 * in their own process, spent on turns they did not write.
 *
 * That is not a backlog item, and this surface says so in a different visual state from "waiting on us".
 * Telling a reader to wait for something that is not coming is worse than telling them no.
 *
 * # Why tabs
 *
 * Viewport-first (NFR17). Four sections stacked would put three below the fold, and the two that matter
 * most — the picker and the boundary that governs it — are the ones a reader moves between.
 */
export const dynamic = "force-dynamic";

/**
 * COVERAGE mirrors `transform.CoverageFor("harness")`, which is a PER-CELL read of the materializer
 * table. `tests/harness.test.mjs` reads the registry's and the engine's own Go source and asserts this
 * agrees with them. Without that gate this is a second source of truth, and its failure is silent.
 */
const COVERAGE = HARNESS_STRATEGIES.map((s) => ({
  strategy: s.strategy,
  turns: s.maxTurnCeiling === 1 ? "1" : `up to ${s.maxTurnCeiling}`,
  mode: s.identity
    ? "applies everywhere"
    : s.hostService
      ? "not at a call site, in any language"
      : `applies in ${HARNESS_BOUNDARY.materializedIn.join(", ")}`,
  what: s.identity
    ? "The identity strategy. It changes nothing, so there is nothing to write into your source and no diff is the correct diff — not a missing one."
    : s.hostService
      ? `Needs ${s.hostService}, which a call site has nowhere to inject. Refused by name in every language — never degraded to a loop that skips the part that makes it this strategy.`
      : "Where the language can read an answer's text: the written call is wrapped in a bounded loop that re-invokes it and reads each answer against the stop condition. Elsewhere it still resolves, hashes, and is recorded, and is refused by name rather than quietly applied.",
}));

/** What separates this axis from the two it is most often confused with, in the reader's own terms. */
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
  {
    axis: "Harness",
    scope: "AROUND the call",
    question: "How many calls does this node make, and what makes it stop?",
    where: "The control flow wrapping the call — a loop that re-invokes it.",
  },
];

function AuthorTab() {
  return <HarnessAuthoring />;
}

function BoundaryTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="A harness is not a context policy, and it is not memory">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          All three concern what a model effectively sees, which is why they are easy to conflate — and
          why the platform keeps them strictly apart. They have separate dimensions, separate registries,
          and separate hashes. A harness reference used where a memory strategy is expected does not
          quietly bind the wrong thing; it fails to resolve.
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
          That third row is why a harness needed a runtime and a generated module before it could be
          applied at all: a loop is not an argument you can replace, it is code that wraps the call. What
          a materialized call site gets instead is its own call passed into that module as something the
          loop can re-invoke.
        </p>
      </Section>

      <Section title="What this platform writes into your source">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          The table is the same one the engine materializes and refuses from. Read the middle column
          carefully: <em>waiting on a module</em> and <em>not at a call site, in any language</em> are
          different answers, and only the first is work the platform owes you.
        </p>
        <DataTable
          caption="What each harness strategy does at a call site, and why"
          columns={[
            { key: "strategy", label: "Strategy" },
            { key: "turns", label: "Turns" },
            { key: "mode", label: "At a call site" },
            { key: "what", label: "What happens, and why" },
          ]}
        >
          <tbody>
            {COVERAGE.map((c) => (
              <tr key={c.strategy}>
                <td className="mono text-sm align-top">{c.strategy}</td>
                <td className="mono tabular-nums text-sm align-top">{c.turns}</td>
                <td className="align-top">
                  <Chip
                    tone={c.mode.startsWith("applies") ? "ok" : "halt"}
                    title="what this platform does with the strategy"
                  >
                    {c.mode}
                  </Chip>
                </td>
                <td className="text-sm text-muted-foreground">{c.what}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </Section>

      <Section title="Why every strategy has a ceiling">
        <Banner tone="warn" title="No strategy can express an unbounded loop">
          <p>
            Every multi-turn strategy declares a bounded{" "}
            <span className="mono">max_turns</span>, capped at{" "}
            <span className="mono">{MAX_TURNS_CEILING}</span> by the registry schema itself — so a
            configuration that exceeded it is rejected when it is registered, not when a run reaches the
            node. A run that reaches its ceiling terminates, returns its last answer, and{" "}
            <strong>records that it stopped there</strong>, distinguishably from one whose stop condition
            was satisfied.
          </p>
          <p>
            The cap is a policy, not a technical limit. More turns of autonomous tool-calling are —
            honestly — more opportunities to act, so the added surface is <strong>observable</strong> in
            the trace: how many turns ran, what each produced, and why the loop stopped. That is the
            guarantee. It is not that the risk is gone.
          </p>
          <p>
            The added turns run inside the sandbox and tool grant this node already has. They re-invoke
            the call you already wrote and nothing else, so no destination or tool becomes reachable
            because you chose a heavier scaffold.
          </p>
        </Banner>
      </Section>

      <Section title="Why a refusal is the safe half">
        <Banner tone="info" title="A refused change is never a dropped one">
          <p>
            If a harness could not be written into your source and the platform applied it as a no-op,
            the run would use your original single call while reporting the variant&apos;s configuration
            hash. The number would be wrong, and it would look exactly like a number that is right.
          </p>
          <p>
            So a harness override ends in one of three states and never a fourth:{" "}
            <strong>materialized</strong> (the loop is in the diff, with the generated module beside it),{" "}
            <strong>refused</strong> (nothing was written, and the reason names the strategy or the call
            site), or <strong>equivalent</strong> (the identity strategy, which changes nothing).
          </p>
          <p>
            There is deliberately no <em>partially applied</em>. A loop that can re-invoke a call but
            cannot decide when to stop is the same question asked N times and billed N times — a single
            shot wearing another strategy&apos;s name — so a call site that can carry only one half is
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
      <Section title="The platform can propose a scaffold change too">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          When a diagnosis finds that a node&apos;s failing cases needed more than one turn — or that its
          revisions never converge — the catalog proposes a scaffold swap. It is the same change you can
          author yourself, reached from the other direction: both origins produce the same configuration
          and the same hash, are transformed by the same rewriter, and pass the same gate.
        </p>
        <Banner tone="warn" title="A heavier scaffold has to earn its cost, on data it was not tuned against">
          <p>
            This is the one axis whose admissibility is not only a quality question. A heavier scaffold
            almost always raises <span className="mono">task_success</span> somewhere, because taking a
            second look at a wrong answer sometimes fixes it — while multiplying{" "}
            <span className="mono">eval_cost_usd</span> and <span className="mono">eval_latency_ms</span>{" "}
            on every case.
          </p>
          <p>
            So a heavier scaffold is admitted <strong>only</strong> when the measured{" "}
            <span className="mono">task_success</span> gain outweighs the cost and latency it added,
            computed on <strong>held-out</strong> cases disjoint from the ones that shaped the proposal.
            A win measured on its own tuning set is overfitting with a confidence interval, and the gate
            refuses it as an evidence failure rather than a quality one.
          </p>
          <p>
            A scaffold that costs more and answers no better is rejected. So is one that buys a point of
            quality for a five-fold bill. No number was invented for this axis — the gate is arithmetic
            over the three metrics the harness already reports.
          </p>
        </Banner>
      </Section>

      <Section title="What you can do with an unapplied change">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          More than it may look like. A harness configuration you author is addressable by its hash,
          diffable against its parent, attributable to you, and re-materializable unchanged. You can pin
          the scaffold you believe in, hand the hash to a colleague, and have it apply the day your
          language&apos;s module lands — without re-authoring it.
        </p>
        <p className="hint">
          What you cannot do is get a diff where the cell refuses, or a measured result before the
          harness has run. Those are the two things the platform declines to fake.
        </p>
      </Section>
    </div>
  );
}

export default async function HarnessPage() {
  await requireSession();

  const tabs: TabItem[] = [
    { id: "author", label: "Set a scaffold", content: <AuthorTab /> },
    { id: "boundary", label: "What applies where", content: <BoundaryTab /> },
    { id: "proposals", label: "Proposals", content: <ProposalsTab /> },
  ];

  return (
    <PageFrame
      eyebrow="Harness"
      title="How many calls this node makes, and what makes it stop"
      lede={
        <>
          Set a node&apos;s control loop yourself, instead of waiting for a diagnosis to propose one.
          Every other axis changes what one call does; this one changes how many happen — so it is the
          one that can multiply what the node costs. The multiplier is stated before you choose; whether
          it was worth it is verification&apos;s answer, not this page&apos;s.
        </>
      }
    >
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
