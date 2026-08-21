import { requireSession } from "@/lib/session";
import { visitedSubjects } from "@/lib/subjects";
import { listWorkflows, orderByRecentlyVisited, discardedVisits } from "@/lib/enumeration";
import { SubjectPicker } from "@/components/subjectPicker";
import { PageFrame, Section, Banner, Failure, Chip, Empty } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { instant } from "@/lib/format";
import { Findings, StateLegend, TallyStrip, DiffList } from "@/components/assessment";
import { AssessControls } from "./controls";
import { fetchAssessment } from "./data";

/**
 * The assessment surface (P33 §6).
 *
 * # What this page is for
 *
 * A person asked *score the repo*, and this is the answer — which is deliberately not a score.
 *
 * For each of the nine axes it reports what the repository does, what evidence says so, and where
 * there is no evidence, that there is none. **The last clause is the product.** A repository with no
 * memory strategy at all and one whose memory strategy could not be determined are different findings,
 * and the difference is the whole value of the report.
 *
 * # 🚫 What is not on this page, by decision
 *
 * A score, a grade, a maturity band, a percentage, a progress ring, or a comparison against another
 * customer. (The words this comment does NOT spell out are the ones `scan-claims.mjs` bans outright —
 * it scans this file too, and an argument against a phrase is indistinguishable from the phrase to a
 * substring matcher. That is the correct trade: a fence with no exemptions is worth a reworded
 * sentence.) Program ruling R4. Every score this platform produces is comparative and verified —
 * variant against variant, multi-seed, ties declared when intervals overlap — because *diagnosis
 * proposes, verification decides*. An absolute "your repository is 62 out of 100" is a model's
 * judgement rendered in a metric's typeface, and no held-out set exists that would make it true.
 *
 * The manager who wants one number is a real reader with a real need, and §8.2's answer is given here
 * IN THE FIRST SCREEN rather than discovered: what we measured, what we could not, and why there is no
 * single number. Said first, that is rigour. Discovered in a demo, it is a gap.
 *
 * # The three rules the layout enforces
 *
 *  1. **All nine axes, always.** A report that omitted the axes it could not assess would be shorter,
 *     prettier and would lie by construction. The bands are ordered by evidence strength, and the last
 *     band — the one a shortening instinct deletes — is where the reader learns what to fix.
 *  2. **`not_measured` is a different message, not a dimmer `observed`.** It has its own band, its own
 *     heading and its own sentence, and it names what was missing.
 *  3. **An inference is visible without hovering.** A chip with a word in it, in the row.
 *
 * # What is computed here: nothing
 *
 * The nine findings, their order, the tally, whether the report is partial, and whether an eval set can
 * fail all arrive from the platform. The only work on this side is grouping the findings into the
 * bands their rank already put them in — which is layout, not judgement.
 */
export const dynamic = "force-dynamic";

export default async function AssessPage({
  searchParams,
}: {
  searchParams: Promise<{ workflow_id?: string }>;
}) {
  const session = await requireSession();
  const params = await searchParams;
  const workflowId = params.workflow_id?.trim();

  if (!workflowId) {
    // 🚫 No default subject. A page that fell back to "the first workflow we know about" would render a
    // fully confident nine-axis report for a repository that is not the one the reader came for —
    // strictly worse than an empty state, because a wrong default asserts a falsehood with the full
    // authority of a populated UI.
    const visited = visitedSubjects(session, "workflow");
    const enumeration = await listWorkflows();
    const available =
      enumeration.state === "ok"
        ? { state: "ok" as const, subjects: orderByRecentlyVisited(enumeration.subjects, visited) }
        : enumeration;
    const discarded = enumeration.state === "ok" ? discardedVisits(enumeration.subjects, visited) : 0;

    return (
      <PageFrame eyebrow="Assess" title="What is weak in this repository">
        <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">
          Pick a workflow and we will report on nine surfaces: model, prompt, skills, context, tools,
          memory, harness, loop and graph. Always all nine — including the ones we cannot determine,
          which say what they were missing.
        </p>
        <SubjectPicker
          kind="workflow"
          available={available}
          discarded={discarded}
          action="/app/assess"
          field={{ name: "workflow_id", label: "Workflow", placeholder: "wf-…" }}
          help="An assessment is of one revision of one repository. It is reproducible: the same revision and the same configuration give the same nine findings."
        />
      </PageFrame>
    );
  }

  const outcome = await fetchAssessment(workflowId);

  if (!outcome.ok && outcome.kind === "not-found") {
    // 🔴 404 here is "nobody has assessed this yet", which is NOT an error and NOT an empty report.
    // The three states a reader can be in — never assessed, assessed and we could determine nothing,
    // and the platform is unreachable — are three different sentences with three different next
    // actions, and this page renders three.
    return (
      <PageFrame eyebrow="Assess" title={workflowId} wide>
        <Empty title="This workflow has not been assessed yet">
          <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">
            Nothing has been read about it. An assessment covers nine surfaces and reports what it can
            establish on each — and, where it cannot, what was missing.
          </p>
          <AssessControls workflowId={workflowId} hasPrior={false} />
        </Empty>
      </PageFrame>
    );
  }

  if (!outcome.ok) {
    return (
      <PageFrame eyebrow="Assess" title={workflowId} wide>
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="the assessment" />
      </PageFrame>
    );
  }

  const view = outcome.data;
  const findings = view.findings ?? [];
  const diff = view.diff ?? null;

  const tabs: TabItem[] = [
    {
      id: "findings",
      label: "Findings",
      content: <Findings view={view} />,
    },
    {
      id: "states",
      label: "The four states",
      content: (
        <Section title="How to read a finding">
          <StateLegend />
          <p className="mt-4 max-w-prose text-xs leading-relaxed text-muted-foreground">
            Four states, not three. Collapsing <em>read from your code</em> into <em>measured</em> would
            conflate true-by-construction with true-by-experiment, and those support different actions:
            you can check an observation in your editor in thirty seconds and cannot check a measurement
            at all without the board behind it. Collapsing <em>refused</em> into <em>not measured</em>{" "}
            would conflate &ldquo;we could not&rdquo; with &ldquo;this build cannot&rdquo;, and only the
            second is ours to fix.
          </p>
        </Section>
      ),
    },
  ];

  if (diff) {
    tabs.push({
      id: "diff",
      label: "What changed",
      content: (
        <Section title="Since the last assessment">
          <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
            Every row names WHICH INPUT MOVED. Three of the four possible answers are not about your
            repository at all — we changed how we ask, or the provider changed the model underneath us.
            Without that attribution a routine provider upgrade reads as your code getting worse.
          </p>
          <div className="mt-3">
            <DiffList diff={diff} />
          </div>
        </Section>
      ),
    });
  }

  return (
    <PageFrame
      eyebrow="Assess"
      title={workflowId}
      wide
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <Chip variant="hash" title="The revision these findings describe">
            {view.source_revision.slice(0, 12)}
          </Chip>
          <Chip variant="hash" title="The agent configuration that produced them">
            {view.agent_config_hash_short}
          </Chip>
        </div>
      }
    >
      {view.partial ? (
        // 🔴 §7.3 — a partial report is never presented as complete, and that has to be a statement the
        // page makes rather than something a reader notices from one odd row.
        <Banner tone="warn" title="This report stopped early">
          {/* 🔴 One <p>, not a bare fragment. `Banner`'s body is a flex COLUMN, so every child element
              becomes a flex ITEM — an inline <em> in loose text lands on a line of its own with the
              following full stop orphaned onto the next. It renders as three broken lines, which is
              the shape of a bug in the sentence a partial report most needs read. */}
          <p>
            The assessment reached its ${view.spend_cap_usd.toFixed(2)} cap after $
            {view.spend_usd.toFixed(2)}. The axes it had not reached report{" "}
            <em>not measured — budget</em>. They are not findings about your repository; re-running with
            a higher cap will answer them.
          </p>
        </Banner>
      ) : null}

      {view.all_not_measured ? (
        <Banner tone="warn" title="We could not establish anything about this repository">
          <p>
            All nine axes came back not measured. That is one finding about US, not nine about you —
            most often it means no frontend in this build read your language, or the source we hold is
            not the source you meant. Each row names what it was missing.
          </p>
        </Banner>
      ) : null}

      <Section title="Nine axes" aside={<span className="text-xs text-muted-foreground">{instant(view.completed_at_ms)}</span>}>
        <TallyStrip tally={view.tally} axes={findings.length} />
        <div className="mt-4">
          <AssessControls workflowId={workflowId} hasPrior />
        </div>
      </Section>

      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
