import { FileText, Layers, Scissors, Sparkles, SlidersHorizontal, TrendingDown, GitCommitVertical, ShieldCheck } from "lucide-react";
import type { Card as ProposalCard } from "@/lib/types.generated";
import { Section, Card, Chip, Row, Stat, Stats } from "@/components/primitives";
import type { TabItem } from "@/components/tabs";
import { Diff } from "@/components/diff";
import { score, usd2, integer, plural } from "@/lib/format";

/**
 * P13OptimizationReview presents the *offered change* for a P13 prompt-or-model optimization proposal
 * (openspec/changes/archive/2026-08-01-p13-prompt-model-optimization, tasks 6.1/6.2). It is a REVIEW surface, not an
 * evaluator: it renders no score, rank, winner, or promotion path of its own — it reads the verified
 * card the platform produced and frames it honestly:
 *
 *   - a PROMPT rewrite (harden / curate / compress / redundancy) is a reviewable diff of a NEW
 *     immutable version, with its grounding — the cases it addresses — attached;
 *   - a MODEL downgrade is an equal-quality-cheaper TIE judged on held-out cases: a win on cost and a
 *     tie on quality, never a quality win;
 *   - a PARAMETER tune is materialized in bound apply mode.
 *
 * The distinction matters because the prompt/model axis is *applied* — the diff reaches a branch — so
 * the presentation has to make the discipline visible, not decorative.
 */

type Family = "prompt" | "downgrade" | "param" | "model" | "other";

const OPERATOR_META: Record<
  string,
  { family: Family; label: string; blurb: string; Icon: typeof FileText }
> = {
  instruction_harden: {
    family: "prompt",
    label: "Instruction hardening",
    blurb: "Added explicit constraints answering the under-specified cases.",
    Icon: Sparkles,
  },
  few_shot_curate: {
    family: "prompt",
    label: "Few-shot curation",
    blurb: "Removed dead / duplicate exemplars that spent context without signal.",
    Icon: Layers,
  },
  prompt_compress: {
    family: "prompt",
    label: "Prompt compression",
    blurb: "Reduced tokens without dropping a live slot — competes on verified quality, not token count.",
    Icon: Scissors,
  },
  redundancy_remove: {
    family: "prompt",
    label: "Redundancy removal",
    blurb: "De-duplicated repeated instructions.",
    Icon: Scissors,
  },
  prompt_rewrite: {
    family: "prompt",
    label: "Grounded prompt rewrite",
    blurb: "Pinned the violated output contract, grounded in the failing cases.",
    Icon: FileText,
  },
  model_downgrade: {
    family: "downgrade",
    label: "Model downgrade",
    blurb: "A cheaper model, admissible only under the held-out quality guardrail.",
    Icon: TrendingDown,
  },
  param_tune: {
    family: "param",
    label: "Parameter tune",
    blurb: "Temperature / max-tokens, materialized as data in bound apply mode.",
    Icon: SlidersHorizontal,
  },
};

function metaFor(operator: string) {
  return (
    OPERATOR_META[operator] ?? {
      family: "other" as Family,
      label: operator,
      blurb: "",
      Icon: GitCommitVertical,
    }
  );
}

/**
 * p13ReviewTabs splits this review into one tab per section (NFR17 / PageFrame's own rule: "a page whose
 * sections would stack tall should split them into <Tabs>").
 *
 * The stacked version was five sections deep — the diff alone is a scrolling block — so a reader looking
 * for the verified delta scrolled past the grounding to find it, and the Decision at the bottom was
 * below the fold on every proposal. Tabs make each section a viewport rather than a stop on a long
 * descent. Nothing is removed: every section that stacked is a tab.
 */
export function p13ReviewTabs(card: ProposalCard): TabItem[] {
  const meta = metaFor(card.operator);
  const tabs: TabItem[] = [
    { id: "offered", label: "The offered change", content: <P13Offered card={card} /> },
  ];
  if (meta.family === "prompt") {
    tabs.push(
      { id: "grounding", label: "Grounding", content: <PromptGrounding card={card} /> },
      { id: "delta", label: "Verified delta", content: <VerifiedDelta card={card} /> },
      { id: "diff", label: "The change", content: <P13Diff card={card} aside="a reviewable diff of the new version" /> },
    );
  }
  if (meta.family === "downgrade") {
    tabs.push(
      { id: "guardrail", label: "Held-out guardrail", content: <DowngradeGuardrail card={card} /> },
      { id: "diff", label: "The change", content: <P13Diff card={card} aside="an intra-provider model swap" /> },
    );
  }
  if (meta.family === "param") {
    tabs.push({ id: "param", label: "Parameter tune", content: <ParamFacets card={card} /> });
  }
  return tabs;
}

function P13Offered({ card }: { card: ProposalCard }) {
  const meta = metaFor(card.operator);
  return (
    <>
      <Section title="The offered change" aside="P13 · prompt & model optimization">
        <Card className="flex flex-col gap-4">
          <div className="flex items-start gap-3">
            <span className="mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg border border-primary/25 bg-primary/10 text-primary">
              <meta.Icon className="size-4.5" aria-hidden="true" />
            </span>
            <div className="flex min-w-0 flex-col gap-1.5">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-display text-base leading-tight text-foreground">{meta.label}</span>
                <Chip variant="hash" title="the node this changes">
                  {card.node_id}
                </Chip>
                {card.pattern ? <Chip tone="info">{card.pattern}</Chip> : null}
              </div>
              {meta.blurb ? <p className="text-sm leading-relaxed text-muted-foreground">{meta.blurb}</p> : null}
            </div>
          </div>
          <p className="max-w-3xl text-sm leading-relaxed text-foreground/90">{card.rationale}</p>
        </Card>
      </Section>
    </>
  );
}

/** The diff, as its own tab: it is the tallest block on the page and the one a reviewer opens last. */
function P13Diff({ card, aside }: { card: ProposalCard; aside: string }) {
  return (
    <Section title="The change" aside={aside}>
      {card.source_diff ? <Diff patch={card.source_diff} /> : <p className="caption">No source diff.</p>}
    </Section>
  );
}

/** Grounding + the immutable-version framing for a prompt rewrite. */
function PromptGrounding({ card }: { card: ProposalCard }) {
  const cases = card.evidence_case_ids ?? [];
  return (
    <>
      <Section title="Grounding" aside="the cases this rewrite addresses">
        <Card className="flex flex-col gap-3">
          <p className="text-sm leading-relaxed text-muted-foreground">
            Grounded-or-silent: this rewrite exists because it answers specific failing cases. An
            operator with nothing grounded to say says nothing — so a candidate you can see is a
            candidate tied to evidence.
          </p>
          {cases.length > 0 ? (
            <Row>
              {cases.map((id) => (
                <Chip key={id} variant="hash" title="grounding case">
                  {id}
                </Chip>
              ))}
            </Row>
          ) : (
            <p className="caption">No grounding cases were attached.</p>
          )}
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <GitCommitVertical className="size-3.5 text-primary" aria-hidden="true" />
            Publishes a new content-addressed prompt version — the parent stays resolvable, nothing is
            mutated in place.
          </p>
        </Card>
      </Section>
    </>
  );
}

/** The held-out guardrail tie for a model downgrade — a cost win and a quality tie, never a quality win. */
function DowngradeGuardrail({ card }: { card: ProposalCard }) {
  const admitted = card.held_out; // the platform admits a downgrade only under the held-out guardrail
  return (
    <>
      <Section title="Held-out quality guardrail" aside="judged on cases the operator did not select">
        <Card className="flex flex-col gap-4">
          <p className="text-sm leading-relaxed text-muted-foreground">
            A cheaper model is admissible only when the platform cannot tell it apart from the incumbent
            on <strong className="text-foreground/90">held-out</strong> cases — its task-success interval
            overlaps. That is a predicate the operator cannot game by fitting the cases that motivated it.
          </p>
          <Row>
            <Chip tone={admitted ? "ok" : "bad"}>
              <ShieldCheck className="size-3.5" aria-hidden="true" />
              {admitted ? "guardrail cleared" : "inadmissible"}
            </Chip>
            {card.held_out_label ? <Chip tone="ok">{card.held_out_label}</Chip> : null}
          </Row>
          <Stats>
            <Stat
              label="Quality"
              value="tie"
              flags={["tie"]}
              note={`held-out interval ${score(card.ci_low)} to ${score(card.ci_high)} — overlaps the incumbent`}
            />
            <Stat label="Cost" value={usd2(card.cost_delta)} note="per run — the only win claimed" />
          </Stats>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Reported as a <strong className="text-foreground/90">cost win and a quality tie</strong> — never
            a quality win. A cost win that silently cost quality is exactly what the held-out guardrail
            exists to refuse.
          </p>
        </Card>
      </Section>
    </>
  );
}

/** The bound-mode framing for a parameter tune. */
function ParamFacets({ card }: { card: ProposalCard }) {
  return (
    <Section title="Parameter tune" aside="materialized in bound apply mode">
      <Card className="flex flex-col gap-3">
        <p className="text-sm leading-relaxed text-muted-foreground">
          A temperature / max-tokens change is carried as <strong className="text-foreground/90">data</strong>{" "}
          in the binding document (bound apply mode, ADR-004). The same change inline has no call-site
          rewriter and is refused — never a config that hashes one thing and runs another.
        </p>
        <Stats>
          <Stat label="Quality delta" value={score(card.delta)} note={`95% interval ${score(card.ci_low)} to ${score(card.ci_high)}`} />
          <Stat label="Cost" value={usd2(card.cost_delta)} note="change per run" />
        </Stats>
      </Card>
    </Section>
  );
}

function VerifiedDelta({ card }: { card: ProposalCard }) {
  const flags = card.significant ? [] : ["low-confidence"];
  return (
    <Section title="The verified delta" aside={card.gate_result || "multi-seed, with confidence intervals"}>
      <Card className="flex flex-col gap-6">
        <Stats>
          <Stat
            label="Task-success delta"
            value={score(card.delta)}
            flags={flags}
            note={`95% interval ${score(card.ci_low)} to ${score(card.ci_high)}`}
          />
          <Stat label="Cost" value={usd2(card.cost_delta)} note="change per run" />
        </Stats>
        <Row>
          <Chip tone="ok">{integer((card.cases_fixed ?? []).length)} fixed</Chip>
          <Chip tone={(card.cases_broken ?? []).length > 0 ? "bad" : undefined}>
            {integer((card.cases_broken ?? []).length)}{" "}
            {plural((card.cases_broken ?? []).length, "broken", "broken")}
          </Chip>
        </Row>
        {card.narration ? (
          <p className="text-sm leading-relaxed text-muted-foreground">{card.narration}</p>
        ) : null}
      </Card>
    </Section>
  );
}
