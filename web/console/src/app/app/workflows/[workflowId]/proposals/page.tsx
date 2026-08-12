import Link from "next/link";
import { ArrowRight } from "lucide-react";
import type { ProposalSurface, Card as CardView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { routes } from "@/lib/subjects";
import {
  PageFrame,
  Section,
  Status,
  Chip,
  Empty,
  Failure,
  Banner,
  Value,
  Row,
  Card,
} from "@/components/primitives";
import { Disclosure } from "@/components/figure";
import { Diff } from "@/components/diff";
import { Tabs } from "@/components/tabs";
import { GenerationPass } from "@/components/proposalGeneration";
import { score, usd2, ms, integer, plural } from "@/lib/format";
import { cx } from "@/lib/cx";

export const dynamic = "force-dynamic";

export default async function ProposalsPage({
  params,
  searchParams,
}: {
  params: Promise<{ workflowId: string }>;
  // `?tab=` makes the withheld group linkable. "Look at what was held back on this workflow" has to be a
  // URL, not an instruction to click; Tabs validates the value and falls back to the first tab.
  searchParams: Promise<{ tab?: string }>;
}) {
  const { workflowId } = await params;
  const { tab } = await searchParams;
  const id = decodeURIComponent(workflowId);
  const { outcome } = await load<ProposalSurface>((paths) => paths.proposals(id));
  return (
    <PageFrame
      eyebrow="Proposals"
      title={id}
      lede="What the platform proposes for this workflow, each with the evidence that was actually measured."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="workflow" />
      ) : (
        <ProposalsBody workflowId={id} surface={outcome.data} tab={tab} />
      )}
    </PageFrame>
  );
}

function ProposalsBody({
  workflowId,
  surface,
  tab,
}: {
  workflowId: string;
  surface: ProposalSurface;
  tab?: string;
}) {
  const recommendations = surface.recommendations ?? [];
  const withheld = surface.withheld ?? [];

  if (surface.state === "error") {
    return (
      <Section title="This surface could not be assembled">
        <Failure kind="upstream" subject="proposals" error={surface.error || "no reason was given"} />
      </Section>
    );
  }

  return (
    <>
      <Section title="This surface">
        <Row>
          <Status value={surface.state} />
          <Chip title="what the platform is permitted to do without asking">
            automation: {surface.automation_level}
          </Chip>
          <Chip variant="count">{integer(recommendations.length)} recommended</Chip>
          <Chip tone="unknown">{integer(withheld.length)} withheld</Chip>
        </Row>
        {surface.automation_level === "advisory" ? (
          <p className="hint">
            At Advisory, a proposal opens a <strong>draft</strong> pull request. Nothing is merged
            without a person, at any automation level this platform offers.
          </p>
        ) : null}
      </Section>

      {/* 🔴 The state, its sentence and its implied action, above the lists rather than inside an empty
          one. It belongs here because it is true of the WHOLE surface — "you have linked no runs"
          explains an empty recommended tab and an empty withheld tab at once — and because it is the
          only part of this page that is still worth reading when both lists are empty. */}
      <Section title="The last generation pass">
        <GenerationPass workflowId={workflowId} state={surface.state} pass={surface.pass ?? null} />
      </Section>

      {/*
        🔴 Recommended and Withheld are TABS, not a stack (NFR17; PageFrame: "a page whose sections would
        stack tall should split them into <Tabs>").

        Stacked, this page was two grids of full proposal cards, one after the other — several thousand
        pixels on a real surface — so the withheld group was reachable only by scrolling past every
        recommendation. Tabs are also the honest shape for this particular pair: they are NOT a ranked
        continuation of each other. A withheld proposal is not a worse recommendation; it is a different
        claim ("we measured this and held it back"), and a stack invites reading it as the tail of the
        first list. The counts stay above, in "This surface", so neither group can be missed by not
        selecting its tab.
      */}
      <Tabs
        initial={tab}
        tabs={[
          {
            id: "recommended",
            label: `Recommended (${integer(recommendations.length)})`,
            content:
              recommendations.length === 0 ? (
                surface.state === "verifying" ? (
                  <Empty title="Verification is still running.">
                    <p>
                      Proposals appear here once their verdict passes its gate — not before, because a
                      proposal without a verified delta is a suggestion, not evidence.
                    </p>
                  </Empty>
                ) : (
                  /* 🔴 "Nothing is pending." is gone. It was rendered here unconditionally and it
                     covered two opposite situations — a workflow nobody had analysed and one that was
                     analysed and healthy — with the same three words. What the platform actually found,
                     and what to do about it, is stated once above, in "The last generation pass". */
                  <Empty title="No proposal on this workflow is recommended.">
                    <p>
                      A proposal is recommended only once a verdict your CI reported passes its gate.
                      What the last generation pass found, and what it implies, is stated above.
                    </p>
                  </Empty>
                )
              ) : (
                <Section title="Recommended" aside="gate-passing, in verified-delta order">
                  <div className="grid gap-4 xl:grid-cols-2">
                    {recommendations.map((card) => (
                      <ProposalCard key={card.proposal_id} card={card} workflowId={workflowId} verified />
                    ))}
                  </div>
                </Section>
              ),
          },
          {
            id: "withheld",
            label: `Withheld (${integer(withheld.length)})`,
            content:
              withheld.length === 0 ? (
                <Empty title="Nothing was withheld." />
              ) : (
                <Section title="Withheld" aside="did not pass verification — not recommendations">
                  <Banner tone="warn" title="These were measured and then held back">
                    <p>
                      Each of these failed its verification gate, failed to build, or was refused by the
                      transform. They are shown so the work is visible, not so it can be applied —
                      nothing here is evidence that the change helps.
                    </p>
                  </Banner>
                  <div className="grid gap-4 xl:grid-cols-2">
                    {withheld.map((card) => (
                      <ProposalCard key={card.proposal_id} card={card} workflowId={workflowId} verified={false} />
                    ))}
                  </div>
                </Section>
              ),
          },
        ]}
      />
    </>
  );
}

/**
 * ProposalCard.
 *
 * A withheld card is drawn on a warned surface and at reduced weight, and it keeps its Review link —
 * the work is visible because hiding it would be its own kind of dishonesty, and the link is what lets
 * a reader check the reasoning rather than take "withheld" on trust.
 */
function ProposalCard({
  card,
  workflowId,
  verified,
}: {
  card: CardView;
  workflowId: string;
  verified: boolean;
}) {
  // 🔴 `build_failed`, not `failed`. The producer emits proposal.BuildFailed, whose wire value is
  // "build_failed"; the old comparison against "failed" matched nothing, so a non-building candidate
  // rendered without the `unverified` qualifier that is the whole point of showing it.
  const buildFailed = card.build_status === "build_failed";
  // A REFUSED change was never measured (P14 task 8.2). It has no verdict at all, so the measurement
  // block below must not render a zero delta for it: "delta 0.00 +/- [0.00, 0.00]" reads as a measured
  // no-op, and a change nobody ran is not a change that made no difference.
  const refused = card.build_status === "refused" || Boolean(card.refused_reason);
  const flags = [
    ...(verified ? [] : ["withheld"]),
    ...(card.significant ? [] : ["low-confidence"]),
    ...(buildFailed || refused ? ["unverified"] : []),
  ];
  return (
    <Card
      className={cx(
        "flex flex-col gap-4",
        verified ? "bg-card" : "border-warn/25 bg-warn/4",
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Link
          className="mono text-xs text-primary underline-offset-2 hover:underline"
          href={routes.proposal(workflowId, card.proposal_id)}
        >
          {card.proposal_id}
        </Link>
        <Chip>{card.operator}</Chip>
        <Chip title="the node this proposal changes">{card.node_id}</Chip>
        {card.pattern ? <Chip tone="info">{card.pattern}</Chip> : null}
        <Status value={card.state} />
      </div>

      <p className="text-sm leading-relaxed text-foreground/90">{card.rationale}</p>

      {refused ? (
        <div className="flex flex-col gap-2 rounded-lg border border-warn/25 bg-warn/4 p-4">
          <p className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
            Nothing was measured
          </p>
          <p className="text-sm leading-relaxed text-foreground/90">
            The transform refused this change
            {card.refused_dimension ? (
              <>
                {" at "}
                <span className="mono">
                  node {card.refused_node_id}, dimension {card.refused_dimension}
                </span>
              </>
            ) : null}
            , so no diff was generated and no evaluation ran.
          </p>
          {card.refused_reason ? <p className="hint">{card.refused_reason}</p> : null}
        </div>
      ) : (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 bg-muted/15 p-4">
        <p className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
          What was measured
        </p>
        <Value flags={flags}>
          <span className="mono text-lg tabular-nums">
            delta {score(card.delta)}{" "}
            <span className="caption">
              ± [{score(card.ci_low)}, {score(card.ci_high)}]
            </span>
          </span>
        </Value>
        <Row>
          <Chip>cost {usd2(card.cost_delta)}</Chip>
          <Chip>latency {ms(card.latency_delta)}</Chip>
          <Chip tone="ok">
            {integer((card.cases_fixed ?? []).length)}{" "}
            {plural((card.cases_fixed ?? []).length, "case", "cases")} fixed
          </Chip>
          <Chip tone={(card.cases_broken ?? []).length > 0 ? "bad" : undefined}>
            {integer((card.cases_broken ?? []).length)} broken
          </Chip>
          {card.held_out ? <Chip tone="ok">{card.held_out_label || "held out"}</Chip> : null}
        </Row>
        {card.narration ? <p className="hint">{card.narration}</p> : null}
      </div>
      )}

      {card.source_diff ? (
        <Disclosure summary="The full diff a human would merge">
          <Diff patch={card.source_diff} />
        </Disclosure>
      ) : null}

      <div className="mt-auto flex justify-end">
        {card.can_open_pr ? (
          <Link
            className="inline-flex items-center gap-1.5 rounded-lg border border-primary/25 px-3 py-1.5 font-mono text-xs text-primary transition-colors hover:bg-primary/10"
            href={routes.proposal(workflowId, card.proposal_id)}
          >
            Review it and open a pull request
            <ArrowRight className="size-3" aria-hidden="true" />
          </Link>
        ) : (
          <span className="qualifier">
            <span className="qualifier__badge">no pull request</span>
            <span className="qualifier__copy">
              {card.pr_disabled_reason || "this proposal cannot open a pull request"}
            </span>
          </span>
        )}
      </div>
    </Card>
  );
}
