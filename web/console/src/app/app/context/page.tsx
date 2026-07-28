import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Row, Banner, DataTable } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { ContextAuthoring } from "./authoring";
import { AxisApplied, AxisRefusal } from "@/components/axisRefusal";

/**
 * The context surface (P16).
 *
 * # Why this surface exists
 *
 * Context was, until this phase, the platform's most complete axis left of the transform and its most
 * silent one right of it: a policy resolved, hashed, and participated in `config_hash`, and then the
 * codemod refused. P16 changed exactly one thing — Go now materialises a policy that SELECTS among the
 * turns already written — and left everything else refusing, deliberately.
 *
 * That makes this axis harder to explain than any other, because the honest answer is "it depends what
 * the policy does", and a reader who meets a decline on Configure has no way to tell which case they
 * are in. Three questions, and this surface answers all three in one place:
 *
 *   1. What can this platform actually write into my source?  → the coverage table, verbatim from
 *      `transform.ContextMaterializerCoverage()`, and the language list beside it.
 *   2. What does an applied change look like?                 → the engine's own diff.
 *   3. Why was mine declined, and is that a bug?              → the decline cards the console really
 *      renders, carrying the engine's real sentences.
 *
 * # Why the drop gate gets a tab of its own
 *
 * It is the one thing here that says NO to something a user asked for, before anything runs — and the
 * reason ("this policy would throw away more of the conversation than this node's job can afford") is
 * not guessable from the refusal alone. A gate nobody can find is a gate that reads as a bug.
 *
 * # What is live and what is a worked example
 *
 * None of this is the tenant's data, and the page says so where a reader will see it. The coverage
 * table mirrors the engine's own table; the diff and the decline messages are copied verbatim from what
 * the engine emits. A paraphrase would be documenting a page that does not exist.
 *
 * # Why tabs
 *
 * Viewport-first (NFR17): five sections stacked would put four below the fold, and the four that matter
 * are the ones a reader compares against each other. One tablist, no nesting.
 */
export const dynamic = "force-dynamic";

/**
 * COVERAGE mirrors `transform.ContextMaterializerCoverage()` — the engine's single source of truth for
 * what each policy does at a Go call site. It is transcribed rather than fetched because this page is a
 * capability explanation, not a live view; `TestContextCoverageTableMatchesEngine` in the console tests
 * is what stops the two drifting apart.
 */
const COVERAGE: { policy: string; mode: string; what: string }[] = [
  {
    policy: "full",
    mode: "identity",
    what: "Passes the whole conversation through. A call site that writes its turns out is already doing exactly this, so there is nothing to change — and no diff is the correct answer, not a missing one.",
  },
  {
    policy: "full-history",
    mode: "identity",
    what: "The same behaviour under its spec name. Both names resolve, so a pinned spec keeps working.",
  },
  {
    policy: "sliding-window",
    mode: "applied",
    what: "Keeps the most recent N turns. Applied by DELETING the older ones from the list you wrote — a deletion of your own code, which constructs nothing and cannot invent a shape.",
  },
  {
    policy: "semantic-compaction",
    mode: "applied",
    what: "Keeps the most recent turns that fit a token budget. Applied the same way, selecting by size instead of by count.",
  },
  {
    policy: "summarization",
    mode: "declined",
    what: "Replaces the history with a summary a model writes at run time. There is no summary in your source to select, and writing one in would freeze a model's answer into your repository.",
  },
  {
    policy: "hierarchical-summary",
    mode: "declined",
    what: "Keeps the recent turns verbatim and summarises the older ones — one model call, at run time. Its summarised tier is model output, not source you wrote.",
  },
  {
    policy: "rag-retrieval",
    mode: "declined",
    what: "Retrieves passages for the live question at run time. What comes back depends on the conversation, not on your source, so there is nothing at the call site to materialise it from.",
  },
  {
    policy: "structured-extraction",
    mode: "declined",
    what: "Rewrites message CONTENT into a declared field set. That means constructing new messages rather than selecting among yours, and a constructed message whose shape was guessed is the failure with no downstream net.",
  },
];

/**
 * APPLIED_DIFF is the real artifact — the unified diff the engine produced for a `sliding-window`
 * policy over the Go fixture's four-turn call site (`internal/transform/testdata/target/pipeline.go.txt`),
 * copied verbatim. A hand-drawn illustration would drift from what ships the first time the emitter
 * changed, and this page's whole claim is "what you read here is what the engine emits".
 */
const APPLIED_DIFF = `--- a/pipeline.go
+++ b/pipeline.go
@@ -68,7 +68,7 @@

 // history writes a four-turn conversation on ONE line, the shape a window materializes into cleanly.
 func history(client *anthropic.Client) {
-	client.Messages.New(nil, anthropic.MessageNewParams{Messages: []anthropic.MessageParam{turnOne, turnTwo, turnThree, turnFour}})
+	client.Messages.New(nil, anthropic.MessageNewParams{Messages: []anthropic.MessageParam{turnThree, turnFour}})
 }
`;

type Decline = {
  id: string;
  label: string;
  nodeId: string;
  submitted: string;
  message: string;
};

/**
 * The four shapes a declined context change takes. They are separate tabs rather than one list because
 * they have DIFFERENT ANSWERS, and the order below is the order the engine considers them:
 *
 *   run-time policy   permanent and correct — no rewriter will ever write a model's answer into source
 *   **kwargs          permanent for this call site — a rewriter would decline it for the same reason
 *   language          the ONE temporary decline: this language's splitter has not landed
 *   run-time list     the user's to change: write the turns out and the window applies
 *
 * 🔴 Ordering them any other way is what the engine used to do, and it produced a true-but-useless
 * answer: a **kwargs call site was told to wait for a rewriter that would have refused it too.
 */
const DECLINES: Decline[] = [
  {
    id: "runtime-policy",
    label: "A run-time policy",
    nodeId: "history",
    submitted:
      "A summarization policy on a Go node. The platform understood it, resolved it, and hashed it — and then declined to write it into the source, because the summary does not exist until a model produces it at run time. This decline is permanent and correct: the policy still runs, host-side, where the credential lives.",
    message:
      'context policy "summarization" assembles by CALLING a summarizer model, host-side through HostServices, at run time; there is no summary in the source to select, and writing one in would freeze a model\'s output into the diff and claim a provider call this engine never made. It is REFUSED at the call site rather than dropped; the policy still runs host-side where it belongs, and the policies this engine materializes into source are [full full-history semantic-compaction sliding-window]',
  },
  {
    id: "kwargs",
    label: "A call that unpacks its arguments",
    nodeId: "n_24ee3c42",
    submitted:
      "A window on a Python call site that passes **api_kwargs. The request is assembled somewhere else in the program, so there are no turns written here to select among — and that is a fact about this call site, not about language support. A rewriter would decline it for the same reason, so the decline says so instead of asking you to wait.",
    message:
      'this call site passes **api_kwargs, so its message list is assembled somewhere else in the program and is not written here — there are no turns at this call site for policy "sliding-window" to select among. 🔴 This is a property of the call site, NOT of Python support: a context rewriter for this language would refuse it for exactly this reason too. Apply the policy where **api_kwargs is built, or write the messages at the call site as a list this engine can select from',
  },
  {
    id: "language",
    label: "A language without a rewriter",
    nodeId: "n_9f1c04ab",
    submitted:
      "The same window, on a TypeScript node that DOES write its turns out. Go and Python can perform the selection; TypeScript's splitter has not landed. This is the one decline on this page that is a promise about future work — everything else is either permanent or yours to change.",
    message:
      'context assembly is not a call-site argument — it is how the surrounding code builds the message list — so materializing policy "sliding-window" is a REGION rewrite of that code, per language. P16 owns that rewrite (docs/prd/P16-context-strategy-optimization.md) and has landed it for Go; the TypeScript materializer is still being built, so this override is REFUSED rather than dropped — applying it as the base configuration would score a configuration that never ran',
  },
  {
    id: "runtime-list",
    label: "A run-time message list",
    nodeId: "runtimehistory",
    submitted:
      "A window over a call site that builds its messages with a function call instead of writing them out. There are no written turns to select among, so applying the window would mean guessing what that function returns. This one is about your code, and you can change it: write the turns at the call site and the window applies.",
    message:
      'this call site assembles its messages at runtime (a call to buildHistory), not as a written list, so policy "sliding-window" has no declared messages to select among; materializing it would mean guessing what the runtime assembly contains, and a guess that compiles is the failure mode with no downstream net',
  },
];

const TOLERANCE_DEFAULTS = [
  { job: "Retrieval (RAG)", tolerance: "60%", why: "It assembles from a corpus by design; reshaping what it carries is its normal behaviour, not a failure." },
  { job: "Memory management", tolerance: "75%", why: "Compaction is the job. A tight limit here would reject the node's whole purpose." },
  { job: "Reflection, planning, reasoning, guardrails", tolerance: "20%", why: "These reason OVER the conversation, so what a lossy policy removes is the material they reason about." },
  { job: "Everything else", tolerance: "40%", why: "The middle, chosen so the gate bites on obvious loss without blocking ordinary tuning." },
];

function ModeChip({ mode }: { mode: string }) {
  const tone = mode === "applied" ? "ok" : mode === "declined" ? "warn" : "info";
  return (
    <Chip tone={tone} title={`what this platform does with the policy at a Go call site`}>
      {mode}
    </Chip>
  );
}

function AxisTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What a context policy decides">
        <p className="text-sm leading-relaxed text-muted-foreground">
          Context is the set of messages a node is given. A policy decides which of them survive, whether
          anything is summarised, and whether anything is retrieved and added. It is part of a
          configuration&apos;s identity — two variants that differ only in their policy have different
          hashes and are scored separately, exactly like two variants that differ in their model.
        </p>
        <p className="text-sm leading-relaxed text-muted-foreground">
          What makes this axis different from the others is that a policy is not an argument you can
          swap. It is <em>how the surrounding code builds the message list</em>, so applying one means
          rewriting that code — which the platform will do for some policies and declines to do for
          others. The table below is that boundary, and it is the same table the engine refuses from.
        </p>
      </Section>

      {/*
        🔴 P16 10.15 — the two boundaries as TWO SENTENCES. "No language can materialize this policy" and
        "this language cannot select yet" have different owners and different lifespans; the first has no
        "when". Rendering them as one disabled control is especially costly here, because the policies
        readers most want (summarization, RAG) are exactly the ones in the first category.
      */}
      <Section title="Two different reasons a policy is declined">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-md border border-border/50 px-4 py-3">
            <p className="mono mb-1 text-[10px] uppercase tracking-wide">not in source</p>
            <p className="text-sm leading-snug text-muted-foreground">
              A summary, a tiered digest, or a retrieved chunk set does not exist until a model or a
              retriever produces it at run time. There is nothing in your source to select, in any
              language. <strong className="font-medium">This is not a wait</strong> — no rewriter will
              ever apply it, and the policy still runs where it belongs.
            </p>
          </div>
          <div className="rounded-md border border-dashed border-primary/50 px-4 py-3">
            <p className="mono mb-1 text-[10px] uppercase tracking-wide">not yet — ours</p>
            <p className="text-sm leading-snug text-foreground/80">
              A policy that SELECTS among the turns you wrote is a deletion, and it needs this
              language&rsquo;s list splitter. Where that has not landed, the platform says so and names
              it — see <strong className="font-medium">Coverage</strong>.
            </p>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          A third case belongs to neither: a call site that unpacks its arguments has written no message
          list at all, so a selection has nothing to select among — before or after any splitter lands.
        </p>
      </Section>
      <Section title="What this platform writes into your source">
        <p className="text-sm leading-relaxed text-muted-foreground">
          The table is a fact about each <em>policy</em>: whether its assembly exists in your source at
          all. Applying one is then a fact about the <em>language</em> — the selection rewriter has
          landed for <span className="mono">Go</span> and <span className="mono">Python</span>, and every
          other language declines a selection policy by name until its rewriter lands.
        </p>
        <DataTable
          caption="What each context policy does at a Go call site, and why"
          columns={[
            { key: "policy", label: "Policy" },
            { key: "mode", label: "At a Go call site" },
            { key: "what", label: "What happens, and why" },
          ]}
        >
          <tbody>
            {COVERAGE.map((c) => (
              <tr key={c.policy}>
                <td className="mono text-sm align-top">{c.policy}</td>
                <td className="align-top">
                  <ModeChip mode={c.mode} />
                </td>
                <td className="text-sm text-muted-foreground">{c.what}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
        <p className="hint">
          Partial coverage is stated rather than smoothed over: a decline is a correct answer
          (&ldquo;not applicable here yet&rdquo;), and a silent no-op would be an incorrect one.
        </p>
      </Section>

      <Section title="Why a decline is the safe half">
        <Banner tone="info" title="A declined change is never a dropped one">
          <p>
            If a policy could not be written into your source and the platform applied it as a no-op, the
            run would use your original message list while reporting the variant&apos;s configuration
            hash. The number would be wrong and would look exactly like a number that is right.
          </p>
          <p>
            So every context override ends in one of three states, and never in a fourth:{" "}
            <strong>applied</strong> (the change is in the diff),{" "}
            <strong>declined</strong> (nothing was persisted, and the reason names the policy), or{" "}
            <strong>equivalent</strong> (the policy and your call site already do the same thing — a
            window wider than the conversation, for instance — so no diff is the correct diff).
          </p>
        </Banner>
      </Section>
    </div>
  );
}

function AppliedTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What was submitted">
        <p className="text-sm leading-relaxed text-muted-foreground">
          A four-turn conversation written out at the call site, and a{" "}
          <span className="mono">sliding-window</span> policy that keeps the two most recent turns. The
          platform could apply this one, because keeping a subset of the turns you wrote is a deletion of
          your own code — it constructs nothing, needs no knowledge of your SDK, and cannot produce a
          value whose type you did not already write.
        </p>
        <dl className="flex flex-col gap-2">
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-32">the source assembles</dt>
            <dd className="mono break-words text-sm text-muted-foreground">
              turnOne, turnTwo, turnThree, turnFour
            </dd>
          </div>
          <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
            <dt className="caption shrink-0 sm:w-32">the policy assembles</dt>
            <dd className="mono break-words text-sm text-foreground">turnThree, turnFour</dd>
          </div>
        </dl>
      </Section>
      <Section title="What the console shows">
        <AxisApplied
          axis="context"
          nodeId="history"
          filename="pipeline.go"
          what="The turns outside the window were removed from the list at the call site. The turns that remain are the ones you wrote, byte for byte."
          invariant={
            <>
              <strong>Only turns were removed, and no line was added or removed.</strong> Nothing was
              constructed and no import was added, so this change cannot have introduced a value your
              code did not already contain — it can only have made the node read less. That is checked
              before the diff is offered, not asserted here.
            </>
          }
          diff={APPLIED_DIFF}
        />
      </Section>
      <Section title="Why the change is in the diff and not behind a handle">
        <p className="text-sm leading-relaxed text-muted-foreground">
          Context is <strong>code</strong>, so it is applied inline and shows up in the diff you review —
          even when the variant asks for the indirection mode that carries a model or a prompt as data.
          Hiding an assembly change behind a handle would mean approving a change you cannot read, and
          the message list is the last place that is acceptable.
        </p>
      </Section>
    </div>
  );
}

function DeclineTab({ decline }: { decline: Decline }) {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What was submitted">
        <p className="text-sm leading-relaxed text-muted-foreground">{decline.submitted}</p>
      </Section>
      <Section title="What the console shows">
        <AxisRefusal axis="context" nodeId={decline.nodeId} message={decline.message} />
      </Section>
    </div>
  );
}

function DropTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Losing context is measured, not assumed">
        <p className="text-sm leading-relaxed text-muted-foreground">
          A policy that summarises or compacts throws information away. How much it threw away is
          recorded on every run, per node — a real number from the assembly, not an estimate — and it is
          available to scoring and to diagnosis like any other signal.
        </p>
        <p className="text-sm leading-relaxed text-muted-foreground">
          A policy that cannot lose anything records nothing at all, deliberately. A zero from a
          summariser means &ldquo;this run happened to drop nothing&rdquo;; a zero from a window would
          mean &ldquo;this policy never drops&rdquo;. Publishing both as <span className="mono">0</span>{" "}
          would make the two unreadable, so only the first is published.
        </p>
      </Section>

      <Section title="A proposal that would lose too much is never run">
        <Banner tone="warn" title="Rejected before the transform, and before any evaluation spend">
          <p>
            Each node carries a tolerance: the fraction of its context its job can afford to lose. A
            proposed policy that would push a node past it is <strong>inadmissible</strong> — it is
            rejected when it is proposed, so it never becomes a diff and never consumes an evaluation
            run.
          </p>
          <p>
            Scoring would eventually punish the same change: a summariser that removed the answer shows
            up as a drop in task success. The gate reaches the same verdict earlier and for free. It is
            not a second opinion about quality — it is the same one, arriving before the bill.
          </p>
        </Banner>
      </Section>

      <Section title="What a node tolerates by default">
        <DataTable
          caption="The default drop tolerance for each kind of node, and the reasoning behind it"
          columns={[
            { key: "job", label: "The node's job" },
            { key: "tolerance", label: "Default tolerance" },
            { key: "why", label: "Why" },
          ]}
        >
          <tbody>
            {TOLERANCE_DEFAULTS.map((d) => (
              <tr key={d.job}>
                <td className="text-sm align-top">{d.job}</td>
                <td className="align-top">
                  <Chip tone="info">{d.tolerance}</Chip>
                </td>
                <td className="text-sm text-muted-foreground">{d.why}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
        <p className="hint">
          A node may declare its own tolerance, and an explicit value always wins — including an explicit
          zero, which means &ldquo;this node tolerates no loss at all&rdquo;. A node that declares
          nothing is unchanged in every other way: its configuration hash is byte-identical to what it
          was before this existed.
        </p>
      </Section>

      <Section title="What the gate will not do">
        <p className="text-sm leading-relaxed text-muted-foreground">
          It never rejects a policy simply because nothing has measured it yet. A change with no
          measurement and no estimate is admitted and goes to verification, because &ldquo;we have no
          data&rdquo; must not come to mean &ldquo;no&rdquo; — that would freeze the board on whatever
          happened to be measured first. When a measurement for that node does exist, it wins over any
          estimate.
        </p>
      </Section>
    </div>
  );
}

function RetrievalTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What retrieval tuning changes">
        <Row>
          <Chip tone="info">top-k</Chip>
          <Chip tone="info">chunk size</Chip>
          <Chip tone="info">rerank</Chip>
          <Chip tone="info">embedding model</Chip>
        </Row>
        <p className="text-sm leading-relaxed text-muted-foreground">
          These four decide what a retriever returns, and they are proposed only on a node classified as
          retrieval — on any other node they are meaningless. Each proposal names the knob it moved and
          what it moved it from, so a verified win is attributable to one change rather than to
          &ldquo;something about retrieval&rdquo;.
        </p>
      </Section>

      <Section title="Verified on cases the tuning never saw">
        <Banner tone="info" title="A win on the set it was tuned on is not a win">
          <p>
            Retrieval knobs are searchable, and a search against an evaluation set will find whatever
            scores best on that set. Reporting that number would be overfitting sold as a result — and
            indefensible the first time it regresses on real traffic.
          </p>
          <p>
            So a retrieval change is verified on a <strong>held-out</strong> set, disjoint from the one
            its parameters were selected on. The split is derived so the two halves cannot share a case;
            if a split is supplied whose halves DO intersect, verification is{" "}
            <strong>refused</strong> rather than computed — there is no honest number to report from it.
          </p>
          <p>
            An overlap of the intervals on held-out cases is a <em>tie</em>, and a tie is not a win.
          </p>
        </Banner>
      </Section>

      <Section title="The same configuration asks the same question twice">
        <p className="text-sm leading-relaxed text-muted-foreground">
          A measurement run pins the retriever, its parameters, and the seed, so re-running the same
          configuration at the same commit issues the identical retrieval request — the rerank included.
          A run that does not pin all three is not accepted as a measurement at all, because nothing
          could re-derive its number later.
        </p>
        <p className="hint">
          The promise is about the <em>request</em>, not about the provider&apos;s bytes. What a search
          index returns next week is outside anything this platform controls, and claiming otherwise
          would be a promise that fails silently.
        </p>
      </Section>

      <Section title="Adding passages is not losing context">
        <p className="text-sm leading-relaxed text-muted-foreground">
          Retrieval that prepends passages and keeps every turn records a loss of zero and a count of the
          passages it added. Recording augmentation as loss would make the tolerance gate reject
          retrieval — this axis&apos;s single best operator — for doing exactly what it is for.
        </p>
      </Section>
    </div>
  );
}

export default async function ContextPage() {
  await requireSession();
  const tabs: TabItem[] = [
    { id: "axis", label: "The axis", content: <AxisTab /> },
    // The APPLIED case comes before the declines, deliberately: a reader who opens this surface and
    // meets three refusals in a row learns the wrong thing about the axis.
    { id: "applied", label: "An applied window", content: <AppliedTab /> },
    // 🔴 The AUTHORING tab (P16 16c). Placed right after the applied case: a reader about to choose a
    // policy should meet the three verdicts before the worked refusals.
    { id: "authoring", label: "Choose a policy", content: <ContextAuthoring /> },
    ...DECLINES.map((d) => ({ id: d.id, label: d.label, content: <DeclineTab decline={d} /> })),
    { id: "drop", label: "Losing context", content: <DropTab /> },
    { id: "retrieval", label: "Retrieval tuning", content: <RetrievalTab /> },
  ];
  return (
    <PageFrame
      eyebrow="Context"
      title="Context strategy"
      lede="What each node is given to read — which turns survive, what is summarised, what is retrieved. Some policies are written into your source; the rest are declined by name and still run where they belong. Both are shown here, with what it costs to lose context."
      wide
    >
      <p className="hint">
        The outcome panels are worked examples — the applied one carries the engine&apos;s real diff, the
        declined ones its real wording. None of it is this tenant&apos;s data; your own changes appear
        where they happen: on Configure, at submit.
      </p>
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
