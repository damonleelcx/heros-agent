import Link from "next/link";
import { AlertTriangle, GitPullRequest } from "lucide-react";
import type { Card as ProposalCard, ProposalSurface } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { requireSession } from "@/lib/session";
import { recordVisit, routes } from "@/lib/subjects";
import {
  PageFrame,
  Section,
  Status,
  Chip,
  Failure,
  Banner,
  Value,
  Empty,
  Row,
  Card,
  Stat,
  Stats,
} from "@/components/primitives";
import { Disclosure } from "@/components/figure";
import { Diff } from "@/components/diff";
import { P13OptimizationReview } from "@/components/p13Optimization";
import { score, usd2, ms, integer, plural } from "@/lib/format";

// P13 prompt & model optimization operators get the dedicated optimization-review presentation
// (grounding for a rewrite; the held-out equal-quality-cheaper tie for a downgrade). Every other
// operator keeps the generic proposal layout.
const P13_OPERATORS = new Set([
  "instruction_harden",
  "few_shot_curate",
  "prompt_compress",
  "redundancy_remove",
  "prompt_rewrite",
  "model_downgrade",
  "param_tune",
]);

export const dynamic = "force-dynamic";

export default async function ProposalPage({
  params,
}: {
  params: Promise<{ workflowId: string; proposalId: string }>;
}) {
  const session = await requireSession();
  const { workflowId, proposalId } = await params;
  const wid = decodeURIComponent(workflowId);
  const pid = decodeURIComponent(proposalId);
  recordVisit(session, { kind: "proposal", id: pid, label: pid, href: routes.proposal(wid, pid), hint: wid });
  const { outcome } = await load<ProposalSurface>((paths) => paths.proposals(wid));
  return (
    <PageFrame eyebrow="Proposal" title={pid} lede={`Proposed for ${wid}.`} wide>
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="workflow" />
      ) : (
        <Body surface={outcome.data} workflowId={wid} proposalId={pid} />
      )}
    </PageFrame>
  );
}

function Body({
  surface,
  workflowId,
  proposalId,
}: {
  surface: ProposalSurface;
  workflowId: string;
  proposalId: string;
}) {
  const recommended = (surface.recommendations ?? []).find((c) => c.proposal_id === proposalId);
  const withheld = (surface.withheld ?? []).find((c) => c.proposal_id === proposalId);
  const card = recommended ?? withheld;

  if (!card) {
    return (
      <Empty title="This proposal is not on the current surface.">
        <p>
          The workflow&apos;s surface loaded, and no proposal with this identifier is on it. It may have
          been superseded by a newer round.{" "}
          <Link className="text-primary underline underline-offset-2" href={routes.proposals(workflowId)}>
            See what is on the surface now
          </Link>
          .
        </p>
      </Empty>
    );
  }

  const verified = Boolean(recommended);
  const flags = [...(verified ? [] : ["withheld"]), ...(card.significant ? [] : ["low-confidence"])];
  // A P13 prompt-or-model optimization gets the dedicated review presentation — a rewrite as a diff of
  // a new immutable version with its grounding, a downgrade as an equal-quality-cheaper held-out tie.
  // It REPLACES the generic rationale/delta/diff sections (it renders its own), and leaves the withheld
  // banner and the Decision block below untouched: those are about whether a change may ship at all,
  // which is the same question for every operator.
  const isP13 = P13_OPERATORS.has(card.operator);

  return (
    <>
      {!verified ? (
        <Banner tone="warn" title="This proposal was withheld">
          <p>
            It did not pass its verification gate. Nothing below is evidence that the change helps — it
            is shown so the work is visible, and it cannot open a pull request.
          </p>
        </Banner>
      ) : null}

      {isP13 ? <P13OptimizationReview card={card} /> : <GenericReview card={card} flags={flags} />}

      <Section title="Decision">
        <Card className="flex flex-col gap-4">
          {/*
            The caveat renders ABOVE the button and always, including when the button is disabled. It
            is the one sentence that stops "open a pull request" being read as "apply this change",
            and a reader who has already clicked has not read it.
          */}
          <p className="flex items-start gap-3 rounded-lg border border-border/60 bg-muted/20 px-4 py-3 text-xs leading-relaxed text-muted-foreground">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warn" aria-hidden="true" />
            Opening a pull request puts this change in front of a reviewer in the repository. It does not
            run the change and it does not merge it — at any automation level this deployment offers, a
            person does.
          </p>
          {card.can_open_pr ? (
            <form
              method="post"
              action={`/api/console/proposals/${encodeURIComponent(workflowId)}/${encodeURIComponent(proposalId)}/open-pr`}
            >
              <button className="button button--primary" type="submit">
                <GitPullRequest className="size-4" aria-hidden="true" />
                Open a pull request
              </button>
            </form>
          ) : (
            <span className="qualifier">
              <span className="qualifier__badge">no pull request</span>
              <span className="qualifier__copy">
                {card.pr_disabled_reason || "this proposal cannot open a pull request"}
              </span>
            </span>
          )}
        </Card>
      </Section>
    </>
  );
}

/** The presentation every non-P13 operator keeps, unchanged. */
function GenericReview({ card, flags }: { card: ProposalCard; flags: string[] }) {
  return (
    <>
      <Section title="Why this was proposed">
        <Row>
          <Chip>{card.operator}</Chip>
          <Chip title="the node this changes">{card.node_id}</Chip>
          {card.pattern ? <Chip tone="info">{card.pattern}</Chip> : null}
          <Status value={card.state} />
          <Status value={card.build_status} />
        </Row>
        <Card>
          <p className="max-w-3xl text-sm leading-relaxed text-foreground/90">{card.rationale}</p>
          {(card.evidence_case_ids ?? []).length > 0 ? (
            <p className="caption mt-3">
              From {integer((card.evidence_case_ids ?? []).length)}{" "}
              {plural((card.evidence_case_ids ?? []).length, "failing case", "failing cases")}:{" "}
              <span className="mono">{(card.evidence_case_ids ?? []).join(", ")}</span>
            </p>
          ) : null}
          {card.diag_id ? <p className="caption mono mt-1">diagnosis {card.diag_id}</p> : null}
        </Card>
      </Section>

      <Section title="The verified delta" aside={card.gate_result}>
        <Card className="flex flex-col gap-6">
          <Stats>
            <Stat
              label="Delta"
              value={score(card.delta)}
              flags={flags}
              note={`95% interval ${score(card.ci_low)} to ${score(card.ci_high)}`}
            />
            <Stat label="Cost" value={usd2(card.cost_delta)} note="change per run" />
            <Stat label="Latency" value={ms(card.latency_delta)} note="change per run" />
          </Stats>
          <Row>
            <Chip tone="ok">{integer((card.cases_fixed ?? []).length)} fixed</Chip>
            <Chip tone={(card.cases_broken ?? []).length > 0 ? "bad" : undefined}>
              {integer((card.cases_broken ?? []).length)} broken
            </Chip>
            {card.held_out ? <Chip tone="ok">{card.held_out_label || "held out"}</Chip> : null}
          </Row>
          {card.narration ? <p className="text-sm leading-relaxed text-muted-foreground">{card.narration}</p> : null}
        </Card>
      </Section>

      <Section title="The change" aside="the full diff, exactly as it would be proposed">
        {card.source_diff ? (
          <Diff patch={card.source_diff} />
        ) : (
          <Empty title="This proposal carries no source diff." />
        )}
        {(card.spec_diff ?? []).length > 0 ? (
          <Disclosure summary={`Variant Spec changes (${(card.spec_diff ?? []).length})`}>
            <ul className="flex list-none flex-col gap-1 p-0">
              {(card.spec_diff ?? []).map((change, index) => (
                <li key={index} className="mono caption">
                  {JSON.stringify(change)}
                </li>
              ))}
            </ul>
          </Disclosure>
        ) : null}
      </Section>
    </>
  );
}
