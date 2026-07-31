import {
  Ban,
  GitCommitVertical,
  Minus,
  Package,
  Scissors,
  ShieldCheck,
  Wrench,
} from "lucide-react";
import type { Card as ProposalCard, DimChange } from "@/lib/types.generated";
import { Section, Card, Chip, Row, Banner, Empty } from "@/components/primitives";
import type { TabItem } from "@/components/tabs";
import { Diff } from "@/components/diff";
import { integer, plural } from "@/lib/format";

/**
 * P14SkillsToolsReview presents a skills-or-tools proposal
 * (openspec/changes/p14-skills-tools-optimization, tasks 8.1/8.2).
 *
 * # The one thing this surface exists to do
 *
 * Make a BOUND PLATFORM SKILL and a PRUNED PROVIDER TOOL look different, because they are.
 *
 *   - Binding a skill CONSTRUCTS an SDK tool value from the skill's sealed input schema. The shape is
 *     generated code; the version_id that pinned the skill pinned the shape.
 *   - Pruning a tool DELETES a declaration the author already wrote. Nothing is constructed and nothing
 *     is inferred.
 *
 * Before the tools-vs-skills split there was no field that could tell them apart, so both rendered as
 * "skills changed". A reviewer who approved that sentence had not approved either change in particular.
 * Everything below is drawn from the split the IR now records.
 *
 * # And a refusal is a first-class outcome, not a missing section
 *
 * The transform refuses a change it cannot make safely: a skill binding in a language with no
 * materializer, a prune over a tool set assembled at runtime. That refusal is rendered by NAME, above
 * everything else, with no diff beside it. A refusal that merely looked like an absent diff would read
 * as a change that happened.
 */

type Family = "skill" | "tool";

const OPERATOR_META: Record<
  string,
  { family: Family; label: string; blurb: string; Icon: typeof Wrench }
> = {
  add_skill: {
    family: "skill",
    label: "Bind a platform skill",
    blurb:
      "Constructs the SDK tool value from the skill's sealed input schema, so the pinned version pins the shape.",
    Icon: Package,
  },
  add_rerank: {
    family: "skill",
    label: "Bind a rerank skill",
    blurb: "Adds a rerank stage to a retrieval node, bound from its sealed contract.",
    Icon: Package,
  },
  fix_schema_binding: {
    family: "skill",
    label: "Correct a skill binding",
    blurb: "Swaps an erroring tool skill for a registry entry whose sealed schema matches.",
    Icon: ShieldCheck,
  },
  rag_tune: {
    family: "skill",
    label: "Tune retrieval",
    blurb: "Swaps the retriever or embedding skill bound at this node.",
    Icon: Package,
  },
  remove_skill: {
    family: "skill",
    label: "Unbind a platform skill",
    blurb:
      "Removes a skill the eval set never exercised or that errored. Proposed only on recorded usage, never on a hunch.",
    Icon: Minus,
  },
  tool_prune: {
    family: "tool",
    label: "Prune a provider tool",
    blurb:
      "Deletes one tool declaration the eval set never calls. A deletion of what the author wrote, not a construction.",
    Icon: Scissors,
  },
  tool_minimize: {
    family: "tool",
    label: "Minimize the tool set",
    blurb:
      "Keeps only the tools the eval set exercises, scored against the full set. Fewer declared-tool tokens, smaller tool-error surface.",
    Icon: Wrench,
  },
};

/** The operators this review surface owns. Anything else keeps the generic proposal layout. */
export const P14_OPERATORS = new Set(Object.keys(OPERATOR_META));

function metaFor(operator: string) {
  return (
    OPERATOR_META[operator] ?? {
      family: "tool" as Family,
      label: operator,
      blurb: "",
      Icon: GitCommitVertical,
    }
  );
}

/**
 * KIND_COPY is the single place a change kind becomes a sentence on this surface. It mirrors
 * ChangeKind.Legible() in internal/proposal, so a badge here and a line in a PR description cannot say
 * two different things about one change.
 */
const KIND_COPY: Record<string, { family: Family; verb: string; note: string }> = {
  skill_bind: {
    family: "skill",
    verb: "Bound a platform skill",
    note: "constructed from the skill's sealed input schema",
  },
  skill_unbind: {
    family: "skill",
    verb: "Unbound a platform skill",
    note: "the construction is removed from the call site",
  },
  skill_rerank: {
    family: "skill",
    verb: "Reordered the bound platform skills",
    note: "skill order is part of the configuration's identity, so this is a real change",
  },
  tool_prune: {
    family: "tool",
    verb: "Pruned a provider tool",
    note: "the tool's declaration is deleted; nothing is constructed",
  },
  tool_restore: {
    family: "tool",
    verb: "Restored a provider tool",
    note: "a previously pruned declaration is offered to the model again",
  },
};

function FamilyChip({ family }: { family: Family }) {
  return family === "skill" ? (
    <Chip tone="info" title="a registered platform capability, bound by constructing a value">
      platform skill
    </Chip>
  ) : (
    <Chip title="a provider-native function the model may call, selected from what the call site declares">
      provider tool
    </Chip>
  );
}

/**
 * RefusalNotice renders a change the transform DECLINED.
 *
 * It is exported separately from the review body and rendered for EVERY operator, not just the P14
 * ones: a refusal is about whether a change could be made at all, which is the same question whatever
 * dimension it was.
 */
export function RefusalNotice({ card }: { card: ProposalCard }) {
  if (!card.refused_reason) return null;
  const where = card.refused_dimension
    ? `node ${card.refused_node_id}, dimension ${card.refused_dimension}`
    : `node ${card.refused_node_id}`;
  return (
    <Banner tone="warn" title="The transform refused this change">
      <p>
        <span className="mono">{where}</span>
      </p>
      <p>{card.refused_reason}</p>
      <p>
        Nothing was applied for that dimension. No partial diff was generated, so there is no change
        here waiting to be approved.
      </p>
    </Banner>
  );
}

/** One entry of the Variant-Spec diff, rendered as a sentence rather than as a JSON blob. */
function ChangeLine({ change }: { change: DimChange }) {
  const copy = change.kind ? KIND_COPY[change.kind] : undefined;
  const items = change.items ?? [];
  if (!copy) {
    return (
      <li className="flex flex-col gap-1 border-t border-border/50 py-3 first:border-t-0 first:pt-0">
        <Row>
          <Chip>{change.dimension}</Chip>
          <span className="mono caption">{change.node_id}</span>
        </Row>
        <p className="caption">
          <span className="mono">{change.from}</span> to <span className="mono">{change.to}</span>
        </p>
      </li>
    );
  }
  return (
    <li className="flex flex-col gap-2 border-t border-border/50 py-3 first:border-t-0 first:pt-0">
      <Row>
        <FamilyChip family={copy.family} />
        <span className="text-sm font-medium text-foreground">{copy.verb}</span>
        <span className="mono caption">{change.node_id}</span>
      </Row>
      {items.length > 0 ? (
        <Row>
          {items.map((name) => (
            <Chip key={name} tone={copy.family === "tool" ? "bad" : "ok"} title={copy.note}>
              <span className="mono">{name}</span>
            </Chip>
          ))}
        </Row>
      ) : null}
      <p className="caption">
        {copy.note}. Offered before: <span className="mono">{change.from}</span>. After:{" "}
        <span className="mono">{change.to}</span>.
      </p>
    </li>
  );
}

/**
 * p14ReviewTabs splits this review into one tab per section (NFR17, and PageFrame's own rule: "a page
 * whose sections would stack tall should split them into <Tabs>").
 *
 * Stacked, this review ran four sections deep and the Decision sat below all of them — so on every
 * proposal the one control that matters was off-screen, and "what moved" (the section that exists
 * precisely so a bound skill and a pruned tool cannot be confused) was something a reader scrolled PAST
 * on the way to the diff. Nothing is removed by tabbing: every section that stacked is a tab.
 */
export function p14ReviewTabs(card: ProposalCard): TabItem[] {
  const changes = card.spec_diff ?? [];
  const hasToolChange = changes.some((c) => c.dimension === "tools");
  const tabs: TabItem[] = [
    { id: "offered", label: "The offered change", content: <P14Offered card={card} /> },
    { id: "moved", label: "What moved", content: <P14WhatMoved card={card} /> },
    { id: "diff", label: "The change", content: <P14Diff card={card} /> },
  ];
  if (hasToolChange) {
    tabs.push({ id: "scoring", label: "How this is scored", content: <P14Scoring /> });
  }
  return tabs;
}

function P14Offered({ card }: { card: ProposalCard }) {
  const meta = metaFor(card.operator);
  return (
    <Section title="The offered change" aside="P14 skills and tools optimization">
      <Card className="flex flex-col gap-4">
        <div className="flex items-start gap-3">
          <meta.Icon className="mt-0.5 size-5 shrink-0 text-primary" aria-hidden="true" />
          <div className="flex flex-col gap-2">
            <Row>
              <span className="text-sm font-medium text-foreground">{meta.label}</span>
              <FamilyChip family={meta.family} />
              <span className="mono caption">{card.node_id}</span>
            </Row>
            {meta.blurb ? <p className="caption max-w-3xl">{meta.blurb}</p> : null}
          </div>
        </div>
        <p className="max-w-3xl text-sm leading-relaxed text-foreground/90">{card.rationale}</p>
        {(card.evidence_case_ids ?? []).length > 0 ? (
          <p className="caption">
            From {integer((card.evidence_case_ids ?? []).length)}{" "}
            {plural((card.evidence_case_ids ?? []).length, "recorded case", "recorded cases")}:{" "}
            <span className="mono">{(card.evidence_case_ids ?? []).join(", ")}</span>
          </p>
        ) : null}
      </Card>
    </Section>
  );
}

function P14WhatMoved({ card }: { card: ProposalCard }) {
  const changes = card.spec_diff ?? [];
  const toolChanges = changes.filter((c) => c.dimension === "tools");
  const skillChanges = changes.filter((c) => c.dimension === "skills");
  const other = changes.filter((c) => c.dimension !== "tools" && c.dimension !== "skills");
  return (
    <Section title="What moved" aside="a bound skill and a pruned tool are different changes">
      <Card>
        {changes.length === 0 ? (
          <Empty title="This proposal records no dimension change." />
        ) : (
          <ul className="flex list-none flex-col p-0">
            {[...skillChanges, ...toolChanges, ...other].map((change, index) => (
              <ChangeLine key={`${change.dimension}-${change.node_id}-${index}`} change={change} />
            ))}
          </ul>
        )}
      </Card>
    </Section>
  );
}

function P14Diff({ card }: { card: ProposalCard }) {
  const refused = Boolean(card.refused_reason);
  return (
    <Section
      title="The change"
      aside={refused ? "refused: no diff was generated" : "the full diff, exactly as it would be proposed"}
    >
      {refused ? (
        <Empty title="No diff exists for a refused change.">
          <p>
            The transform declined to write code it could not stand behind, so there is nothing here to
            review. The reason is stated above the tabs, where it cannot be missed.
          </p>
        </Empty>
      ) : card.source_diff ? (
        <Diff patch={card.source_diff} />
      ) : (
        <Empty title="This proposal carries no source diff." />
      )}
    </Section>
  );
}

function P14Scoring() {
  return (
    <Section title="How this is scored" aside="no new metric">
      <Card className="flex items-start gap-3">
        <Ban className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <p className="caption max-w-3xl">
          A pruned tool set is scored by the same evaluation harness as everything else, from its config
          hash and its trace. The saving shows up as fewer total tokens and, where a pruned tool was
          erroring, a lower tool-error rate. Nothing measures the prune itself, because a change that
          measures itself always looks like a win.
        </p>
      </Card>
    </Section>
  );
}
