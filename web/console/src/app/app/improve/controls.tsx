"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Check, Loader2, Play, Sparkles, X } from "lucide-react";

import { Banner } from "@/components/primitives";
import { PlanPanel, ProposalCard, AxisBreakdown, EmptyReason, RunOutcome } from "@/components/improvement";
import type { ImprovementPlanView, ImprovementRunView } from "@/lib/types.generated";

/**
 * ImproveControls is the whole interactive surface of P35, and its shape is the phase's argument.
 *
 * # Three steps, three explicit acts, and no step that happens by itself
 *
 *   ask       a question becomes a PLAN. Spends nothing. The plan is shown, and it can be declined by
 *             simply not pressing the next button.
 *   run       the plan executes. This is the one that costs money, and above the disclosure threshold
 *             it carries the acknowledgement.
 *   decide    ONE proposal, approved or declined. 🚫 There is no third button that does several.
 *
 * 🔴 Ask and run are SEPARATE PRESSES rather than one call that plans-and-runs. That is FR1/FR2 rather
 * than interaction taste: a plan a person sees only after the money is spent is a receipt, and the
 * entire value of the artifact is that it exists before.
 *
 * # Why this is a client component
 *
 * Three POSTs whose results replace parts of the page, and one of them takes minutes. While a run is
 * executing, the button must say so — it is the only feedback that distinguishes "spending" from
 * "nothing happened", which is the four-facts-a-spinner-withholds problem P31 §9.1 names.
 *
 * # 🚫 What this component never decides
 *
 * Whether a plan needs acknowledging (`requires_acknowledgement` is the server's answer), whether a
 * delta is significant, whether a set can fail, which bound stopped a run, or what any of those means
 * in words. Every sentence on this page is rendered server-side. A console that re-derived any of them
 * would be a second implementation of the one thing this product sells.
 */
export function ImproveControls() {
  const router = useRouter();
  const [question, setQuestion] = useState("");
  const [plan, setPlan] = useState<ImprovementPlanView | null>(null);
  const [run, setRun] = useState<ImprovementRunView | null>(null);
  const [busy, setBusy] = useState<"ask" | "run" | string | null>(null);
  const [error, setError] = useState<{ detail: string; next?: string } | null>(null);

  /** post is the one fetch shape. It preserves the platform's own words on a refusal. */
  async function post<T>(path: string, body: unknown): Promise<T | null> {
    const res = await fetch(path, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    let data: Record<string, unknown> = {};
    try {
      data = (await res.json()) as Record<string, unknown>;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    if (!res.ok) {
      // 🔴 The platform's sentence VERBATIM, with its next action beside it. A refusal here names what
      // was missing — an unboundable question, a workflow with no revision, an organization with no
      // budget — and every one of those is something the person can act on. Replacing them with "the
      // run failed" would delete the only part that is useful.
      setError({
        detail: (data.detail as string) ?? (data.error as string) ?? "the platform refused this request",
        next: data.next_action as string | undefined,
      });
      return null;
    }
    setError(null);
    return data as T;
  }

  async function ask() {
    setBusy("ask");
    setRun(null);
    const next = await post<ImprovementPlanView>("/api/console/improvement-plans", { question });
    setBusy(null);
    if (next) setPlan(next);
  }

  async function execute() {
    if (!plan) return;
    setBusy("run");
    // 🔴 The acknowledgement is recorded against THIS plan id, in its own call, before the run. It is
    // agreement to a specific scope at a specific budget — a plan whose bounds moved has a different id
    // and needs a new one.
    if (plan.requires_acknowledgement) {
      const ack = await post<ImprovementPlanView>("/api/console/improvement-plans", {
        question: plan.question,
        acknowledge: true,
      });
      if (!ack) {
        setBusy(null);
        return;
      }
    }
    const next = await post<ImprovementRunView>("/api/console/improvement-runs", {
      plan_id: plan.plan_id,
      question: plan.question,
    });
    setBusy(null);
    if (next) {
      setRun(next);
      router.refresh();
    }
  }

  async function decide(proposalId: string, decision: "approve" | "decline") {
    if (!run) return;
    setBusy(proposalId);
    const next = await post<{ run: ImprovementRunView }>("/api/console/improvement-decisions", {
      run_id: run.run_id,
      proposal_id: proposalId,
      decision,
    });
    setBusy(null);
    // 🔴 The RUN comes back, not just the decision, and it replaces the whole run in state. Approving
    // applies and re-measures, so this same response may carry a WITHDRAWAL — and a console that
    // updated only the decision would render a green tick over a change that was withdrawn.
    if (next?.run) setRun(next.run);
  }

  const proposals = run?.proposals ?? [];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <label className="stat__label" htmlFor="improve-question">
          Ask for a change you can prove
        </label>
        <textarea
          id="improve-question"
          className="improve__question"
          rows={2}
          placeholder="fix what you can prove, and open a pull request"
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
        />
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            className="improve__ask"
            disabled={busy !== null || question.trim() === ""}
            onClick={ask}
          >
            {busy === "ask" ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <Sparkles className="size-4" aria-hidden="true" />}
            {busy === "ask" ? "Planning…" : "Show me the plan"}
          </button>
        </div>
        <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
          Asking produces a plan and stops. It spends nothing. A question that cannot be bounded — one
          asking for an unlimited search, or naming more than one repository — is refused rather than run
          with bounds nobody chose.
        </p>
      </div>

      {error ? (
        <Banner tone="warn" title={error.detail}>
          {error.next ? <p>{error.next}</p> : null}
        </Banner>
      ) : null}

      {plan && !run ? (
        <div className="flex flex-col gap-4">
          <PlanPanel plan={plan} />
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" className="improve__run" disabled={busy !== null} onClick={execute}>
              {busy === "run" ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <Play className="size-4" aria-hidden="true" />}
              {busy === "run"
                ? "Running…"
                : plan.requires_acknowledgement
                  ? "I have read the plan — run it"
                  : "Run this plan"}
            </button>
            <button
              type="button"
              className="improve__ask"
              disabled={busy !== null}
              onClick={() => setPlan(null)}
            >
              Not this
            </button>
          </div>
        </div>
      ) : null}

      {run ? (
        <div className="flex flex-col gap-6">
          <PlanPanel plan={run.plan} />
          <RunOutcome run={run} />

          {run.empty ? <EmptyReason empty={run.empty} /> : null}

          {proposals.map((proposal) => (
            <ProposalCard
              key={proposal.proposal_id}
              proposal={proposal}
              decision={run.decisions?.[proposal.proposal_id]}
              withdrawal={(run.withdrawals ?? []).find((w) => w.proposal_id === proposal.proposal_id)}
              delivery={(run.deliveries ?? []).find((d) => d.proposal_id === proposal.proposal_id)}
              controls={
                run.decisions?.[proposal.proposal_id]?.state === "pending" ? (
                  /* 🚫 TWO buttons, on THIS card, for THIS proposal. There is no list-level control and
                     there will not be one — see design D4. A person deciding on five proposals presses
                     ten times and reads five diffs, which is the whole point. */
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      className="improve__approve"
                      disabled={busy !== null}
                      onClick={() => decide(proposal.proposal_id, "approve")}
                    >
                      {busy === proposal.proposal_id ? (
                        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                      ) : (
                        <Check className="size-4" aria-hidden="true" />
                      )}
                      Approve this one
                    </button>
                    <button
                      type="button"
                      className="improve__decline"
                      disabled={busy !== null}
                      onClick={() => decide(proposal.proposal_id, "decline")}
                    >
                      <X className="size-4" aria-hidden="true" />
                      Decline
                    </button>
                  </div>
                ) : (
                  /* 🔴 A decided proposal keeps its card and gains a SENTENCE. It is not removed: a
                     proposal that disappeared when it was declined looks exactly like one that was
                     never made (FR12). */
                  <p className="text-xs leading-relaxed text-muted-foreground">
                    {run.decisions?.[proposal.proposal_id]?.sentence}
                  </p>
                )
              }
            />
          ))}

          <AxisBreakdown rows={run.per_axis ?? []} />
        </div>
      ) : null}
    </div>
  );
}
