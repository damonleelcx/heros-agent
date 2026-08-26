import Link from "next/link";
import { ArrowUpRight, CircleSlash, GitPullRequest, ShieldAlert, Sparkles } from "lucide-react";

import { Chip, Section, Stat, Status } from "@/components/primitives";
import { instant, usd2 } from "@/lib/format";
import type {
  AxisStageView,
  ImprovementDeliveryView,
  ImprovementDecisionSummary,
  ImprovementEmptyView,
  ImprovementPlanView,
  ImprovementProposalView,
  ImprovementRunView,
  ImprovementWithdrawalView,
} from "@/lib/types.generated";

/**
 * The improvement-run surface's read-only pieces (P35 §8).
 *
 * # What is computed here: nothing
 *
 * The delta label, the bound's sentence, the withdrawal's sentence, each decision's sentence and the
 * per-axis breakdown all arrive from the platform. P9's founding rule — the browser derives nothing —
 * applies with more force here than anywhere else on the console, because every number on this page is
 * a claim somebody will act on by merging a change into their repository.
 *
 * The only work on this side is layout, and two layout decisions carry meaning:
 *
 *  1. **The plan is drawn as a projection, not as a result.** It is the one panel on the page reporting
 *     something that has NOT happened and can be declined.
 *  2. **A decided proposal stays.** It is recessed, not removed. A proposal that disappeared when it was
 *     declined looks exactly like one that was never made.
 */

/** PlanPanel renders the plan BEFORE anything runs (FR1/FR2, task 8.4). */
export function PlanPanel({ plan }: { plan: ImprovementPlanView }) {
  return (
    <div className="improve__plan">
      <div className="flex flex-col gap-1">
        <span className="stat__label">The plan, before anything runs</span>
        <p className="max-w-prose text-sm leading-relaxed text-foreground">
          {/* The question, verbatim. A plan that lost its question cannot be reviewed for whether it
              answered it. */}
          &ldquo;{plan.question}&rdquo;
        </p>
      </div>

      <div className="improve__bounds">
        {/* 🔴 `?? []` rather than a non-null assertion: the generated type says this can be null,
            because Go's `[]string` marshals to `null` when empty — and an empty scope is a real state
            the platform refuses to produce but the WIRE can carry. A `!` here would render a crash. */}
        <Stat
          label="Axes in scope"
          value={(plan.axes ?? []).length}
          dense
          note={(plan.axes ?? []).join(", ")}
        />
        <Stat label="Candidate cap" value={plan.candidate_cap} dense />
        <Stat
          label="Spend budget"
          value={usd2(plan.spend_budget_usd)}
          dense
          /* 🔴 The BUDGET is the promise and the projection is an estimate, and the note says which is
             which. Presenting the estimate as the promise is how a person agrees to a number the run is
             not actually bound by. */
          note={`projected ${usd2(plan.projected_spend_usd)} — an estimate; the budget is the bound`}
        />
        <Stat
          label="Stops when"
          value="gains fall below"
          unit={plan.min_improvement.toFixed(3)}
          dense
          note="a run that stops here found the best change reachable"
        />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Chip variant="hash" title="The revision this run is pinned to">
          {plan.source_revision_short}
        </Chip>
        <Chip title="Where this run started, which decides how a change would be delivered">
          {plan.origin}
        </Chip>
      </div>

      {plan.requires_acknowledgement ? (
        <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
          This plan projects more than {usd2(plan.disclosure_threshold_usd)} — more than a full assessment
          of this repository costs — so nothing runs until you say so. Nothing has been spent.
        </p>
      ) : (
        <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
          This plan projects less than {usd2(plan.disclosure_threshold_usd)}, so it runs when you ask. The
          budget above is the bound the run is actually held to.
        </p>
      )}
    </div>
  );
}

/**
 * ProposalCard is ONE proposal with ITS OWN approve and decline (task 8.1).
 *
 * 🚫 There is no bulk control anywhere on this surface, and the component's shape is part of the
 * refusal: it takes one proposal, renders one pair of buttons, and there is no list-level equivalent.
 * Design D4: *a bundle approval is one click that means several things, and the person will read the
 * first item and accept the rest.*
 */
export function ProposalCard({
  proposal,
  decision,
  withdrawal,
  delivery,
  controls,
}: {
  proposal: ImprovementProposalView;
  decision: ImprovementDecisionSummary | undefined;
  withdrawal: ImprovementWithdrawalView | undefined;
  delivery: ImprovementDeliveryView | undefined;
  /** The decide buttons, injected so this stays a server component and the client half is one file. */
  controls?: React.ReactNode;
}) {
  const decided = decision !== undefined && decision.state !== "pending";
  return (
    <article className={decided ? "improve__proposal improve__proposal--decided" : "improve__proposal"}>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="stat__label">{proposal.axis}</span>
          <p className="text-sm font-medium text-foreground">
            <code className="font-mono">{proposal.operator}</code> on{" "}
            <code className="font-mono">{proposal.node}</code>
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {decision ? <Status value={decision.state} title={decision.sentence} /> : null}
          <Chip variant="hash" title="The configuration an approval binds to">
            {proposal.config_hash_short}
          </Chip>
        </div>
      </header>

      <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">{proposal.rationale}</p>

      {/* 🔴 THE VERIFIED DELTA, with its interval and the size of the set behind it, rendered exactly as
          the platform computed it. FR10 requires it to travel with the proposal WHEREVER it renders —
          a card with an operator name and a hash asks somebody to approve a change on faith. */}
      <div className="flex flex-col gap-2">
        <Stat
          label="Verified delta, on held-out data"
          value={proposal.delta_label}
          /* The flags a server computed. 🚫 Never derived here: deciding in the browser would be a
             client-side statistic, and this is the number a merge is decided on. */
          flags={[
            ...(proposal.significant ? [] : ["tie"]),
            ...(proposal.held_out ? [] : ["not-held-out"]),
          ]}
        />
        {proposal.eval_set_cannot_fail ? (
          <p className="max-w-prose text-xs leading-relaxed text-warn">
            Every case in the set behind this number passes whatever the agent does. The delta is
            therefore not evidence that this change improves quality — treat it as unproven.
          </p>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
        <span>Cost {proposal.cost_delta >= 0 ? "+" : ""}{proposal.cost_delta.toFixed(4)} $/run</span>
        <span>Latency {proposal.latency_delta >= 0 ? "+" : ""}{proposal.latency_delta.toFixed(0)} ms/run</span>
        <span>{proposal.diff_stat}</span>
        <span title="The provider model version this was measured against">
          {proposal.provider_model_version}
        </span>
      </div>

      {/* 🔴 THE DESIGNED SEQUENCE (task 8.3): approved → applied → WITHDRAWN. Without a state of its own
          this reads as a failure; with one it reads as the system working. */}
      {withdrawal ? <WithdrawalPanel withdrawal={withdrawal} /> : null}
      {delivery ? <DeliveryPanel delivery={delivery} /> : null}

      {controls}
    </article>
  );
}

/** WithdrawalPanel is task 8.3 — the state that makes the sequence readable. */
export function WithdrawalPanel({
  withdrawal,
}: {
  withdrawal: ImprovementWithdrawalView;
}) {
  return (
    <div className="improve__withdrawn">
      <p className="flex items-center gap-2 text-sm font-medium text-warn">
        <ShieldAlert className="size-4 shrink-0" aria-hidden="true" />
        {withdrawal.about_the_change
          ? "Approved, applied — and withdrawn before delivery"
          : "Withdrawn, and not because of your change"}
      </p>
      {/* 🔴 BOTH measurements, equally weighted. A withdrawal with one number looks like a bug; with two
          it is a finding about the eval set as much as about the change. */}
      <div className="improve__measurements">
        <Stat label="Verified before applying" value={withdrawal.verified_label} dense />
        <Stat label="Re-measured after applying" value={withdrawal.remeasured_label} dense />
      </div>
      <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">{withdrawal.sentence}</p>
      <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
        Nothing reached your repository. The second measurement is allowed to disagree with the first —
        that is what makes the first one worth having.
      </p>
    </div>
  );
}

/** DeliveryPanel is task 8.5 — the pull request URL, which is where the run ends and a review starts. */
export function DeliveryPanel({
  delivery,
}: {
  delivery: ImprovementDeliveryView;
}) {
  if (delivery.withheld_kind) {
    return (
      <div className="improve__withdrawn">
        <p className="flex items-center gap-2 text-sm font-medium text-warn">
          <CircleSlash className="size-4 shrink-0" aria-hidden="true" />
          The pull request was not opened
        </p>
        <p className="max-w-prose text-sm leading-relaxed text-foreground">{delivery.withheld_detail}</p>
        {delivery.withheld_next_action ? (
          <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
            {delivery.withheld_next_action}
          </p>
        ) : null}
        <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
          The verified change and its evidence are kept. Only the delivery is withheld.
        </p>
      </div>
    );
  }
  return (
    <div className="improve__delivered">
      <p className="flex items-center gap-2 text-sm font-medium text-good">
        <GitPullRequest className="size-4 shrink-0" aria-hidden="true" />
        {delivery.deduplicated ? "This change was already delivered" : "A pull request is open"}
      </p>
      {/* 🔴 The URL the FORGE returned, rendered as received. 🚫 Never composed from a repository name
          and a number: a URL the platform invented is one that 404s in a browser while looking exactly
          like one that works. */}
      {delivery.pull_request_url ? (
        <Link
          className="inline-flex items-center gap-1 text-sm text-primary underline underline-offset-2"
          href={delivery.pull_request_url}
        >
          {delivery.pull_request_ref}
          <ArrowUpRight className="size-3.5" aria-hidden="true" />
        </Link>
      ) : null}
      <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
        {delivery.deduplicated
          ? "Asking again returned the pull request that already exists rather than opening a second one."
          : "Nothing is merged. Review it, and merge it yourself — the platform does not merge at this plan's automation level."}
      </p>
    </div>
  );
}

/**
 * AxisBreakdown is §9.5 / task 7.15 rendered: proposals per axis at every stage.
 *
 * 🔴 All nine axes, always, with `in_scope` on each. An axis at zero because the SCOPE excluded it and
 * an axis at zero because its operators produced nothing are opposite findings, and a bare zero cannot
 * tell them apart. 🚫 There is deliberately no total row: an operator with a 5% pass rate hidden inside
 * a healthy average is an operator that is not working, and the aggregate is what gets built if nobody
 * checks.
 */
export function AxisBreakdown({ rows }: { rows: readonly AxisStageView[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="improve__axes">
        <thead>
          <tr>
            <th scope="col">Axis</th>
            <th scope="col">Generated</th>
            <th scope="col">Verified</th>
            <th scope="col">Approved</th>
            <th scope="col">Delivered</th>
            <th scope="col">Withdrawn</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.axis} data-out-of-scope={row.in_scope ? "false" : "true"}>
              <th scope="row" className="font-normal text-foreground">
                {row.axis}
                {row.in_scope ? null : (
                  <span className="text-xs text-muted-foreground"> · not in scope</span>
                )}
              </th>
              <td className="tabular-nums">{row.in_scope ? row.generated : "—"}</td>
              <td className="tabular-nums">{row.in_scope ? row.verified : "—"}</td>
              <td className="tabular-nums">{row.in_scope ? row.approved : "—"}</td>
              <td className="tabular-nums">{row.in_scope ? row.delivered : "—"}</td>
              <td className="tabular-nums">{row.in_scope ? row.withdrawn : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
        An em-dash means the plan did not cover that axis, which is not the same as an axis that produced
        nothing. There is deliberately no total: an axis whose operators fail is invisible in one.
      </p>
    </div>
  );
}

/** RunOutcome states which bound stopped the run, or which fault ended it (FR26). */
export function RunOutcome({ run }: { run: ImprovementRunView }) {
  return (
    <Section
      title={run.fault ? "This run could not finish" : "How this run ended"}
      aside={<span className="text-xs text-muted-foreground">{instant(run.finished_at_ms)}</span>}
    >
      <p className="max-w-prose text-sm leading-relaxed text-foreground">{run.bound_sentence}</p>
      <div className="mt-4 flex flex-wrap items-center gap-4">
        <Stat label="Provider spend" value={usd2(run.spend_usd)} dense />
        {run.withdrawn_spend_usd > 0 ? (
          <Stat
            label="Of which, on withdrawn changes"
            value={usd2(run.withdrawn_spend_usd)}
            dense
            /* Reported separately on purpose: a run that spent much of its budget on changes it
               withdrew is telling you something about the eval set, and a total hides it. */
            note="charged against the budget, billed to nobody"
          />
        ) : null}
      </div>
    </Section>
  );
}

/** EmptyReason renders the NAMED "nothing to propose" state (FR7, D5). */
export function EmptyReason({ empty }: { empty: ImprovementEmptyView }) {
  return (
    <div className={empty.healthy ? "improve__delivered" : "improve__withdrawn"}>
      <p className="flex items-center gap-2 text-sm font-medium">
        <Sparkles className="size-4 shrink-0" aria-hidden="true" />
        {empty.headline}
      </p>
      {/* The pass's OWN sentence, verbatim. It knows which two revisions disagreed and which half of a
          model join was missing; a re-wording here would drop exactly that. */}
      <p className="max-w-prose text-sm leading-relaxed text-foreground">{empty.detail}</p>
      {empty.next_action ? (
        <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">{empty.next_action}</p>
      ) : (
        <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
          There is nothing to do here. That is a result, not a gap.
        </p>
      )}
    </div>
  );
}
