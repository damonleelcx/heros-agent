import type { Card as ProposalCard } from "@/lib/types.generated";
import { PageFrame } from "@/components/primitives";
import Link from "next/link";
import { Tabs } from "@/components/tabs";
import { skillsToolsReviewTabs, RefusalNotice } from "@/components/skillsToolsOptimization";

/**
 * A self-contained preview of the P14 skills-and-tools review surface. It renders the same components
 * the proposal detail page uses, seeded with representative cards, so the presentation can be checked
 * in a browser without a live platform backend. It uses only the root layout (no session, no BFF),
 * which is why it lives outside /app.
 *
 * The three tabs are the three states that have to be SEEN rather than asserted on:
 *
 *   1. a bound platform skill  - a CONSTRUCTION from a sealed contract;
 *   2. a pruned provider tool  - a DELETION of a declaration the author wrote;
 *   3. a refusal               - named, with no diff beside it.
 *
 * `?tab=` selects which one opens, so each state is directly linkable. That is not a convenience for
 * this file: a review surface whose states are only reachable by clicking cannot be put in a PR
 * description, a bug report, or a screenshot pipeline, so "go and click the second tab" becomes the
 * instruction and the state nobody clicks is the state nobody checks.
 */
export const dynamic = "force-dynamic";

const TAB_IDS = ["bind", "prune", "refused"] as const;

function safeTab(tab: string | undefined): string {
  return TAB_IDS.includes(tab as (typeof TAB_IDS)[number]) ? (tab as string) : TAB_IDS[0];
}

const BIND_SKILL: ProposalCard = {
  proposal_id: "p14-bind-search-kb",
  operator: "add_skill",
  node_id: "agent",
  pattern: "tool_use",
  diag_id: "diag-agent-tool-schema",
  evidence_case_ids: ["case-004", "case-017", "case-041"],
  rationale:
    "Missing/erroring tool on 3 case(s) -> bind skill search_kb from the registry.",
  source_diff: [
    "--- a/pipeline.go",
    "+++ b/pipeline.go",
    "@@ -14,3 +14,3 @@",
    "-\tclient.Messages.New(nil, anthropic.MessageNewParams{})",
    '+\tclient.Messages.New(nil, anthropic.MessageNewParams{Tools: []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "search_kb", InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{"query": map[string]any{"type": "string"}}}}}}})',
  ].join("\n"),
  spec_diff: [
    {
      node_id: "agent",
      dimension: "skills",
      from: "0 skill(s)",
      to: "1 skill(s)",
      kind: "skill_bind",
      items: ["search_kb@99"],
    },
  ],
  build_status: "built",
  state: "recommended",
  gate_result: "5 seeds, 95% CI",
  held_out: true,
  held_out_label: "held out",
  delta: 0.084,
  ci_low: 0.021,
  ci_high: 0.147,
  significant: true,
  cost_delta: 0.0004,
  latency_delta: 120,
  cases_fixed: ["case-004", "case-017"],
  cases_broken: [],
  narration:
    "The bound skill fixed two of the three generating cases on held-out data, at a small cost increase.",
  can_open_pr: true,
};

const PRUNE_TOOL: ProposalCard = {
  proposal_id: "p14-prune-sqltool",
  operator: "tool_prune",
  node_id: "agent",
  pattern: "tool_use",
  diag_id: "",
  evidence_case_ids: ["case-004", "case-017"],
  rationale:
    "Tool sqlTool is declared but the eval set never calls it -> prune it (fewer declared-tool tokens; scored on the standard metric family, not on the saving).",
  source_diff: [
    "--- a/pipeline.go",
    "+++ b/pipeline.go",
    "@@ -20,3 +20,3 @@",
    "-\tclient.Messages.New(nil, anthropic.MessageNewParams{Tools: []anthropic.ToolUnionParam{weatherTool, sqlTool, searchTool}})",
    "+\tclient.Messages.New(nil, anthropic.MessageNewParams{Tools: []anthropic.ToolUnionParam{weatherTool, searchTool}})",
  ].join("\n"),
  spec_diff: [
    {
      node_id: "agent",
      dimension: "tools",
      from: "3 tool(s): searchTool, sqlTool, weatherTool",
      to: "2 tool(s): searchTool, weatherTool",
      kind: "tool_prune",
      items: ["sqlTool"],
    },
  ],
  build_status: "built",
  state: "recommended",
  gate_result: "5 seeds, 95% CI",
  held_out: true,
  held_out_label: "held out",
  delta: 0.004,
  ci_low: -0.011,
  ci_high: 0.019,
  significant: false,
  cost_delta: -0.0007,
  latency_delta: -35,
  cases_fixed: [],
  cases_broken: [],
  narration:
    "Quality held within the confidence interval and the run spent fewer tokens declaring a tool nothing called.",
  can_open_pr: true,
};

const REFUSED: ProposalCard = {
  proposal_id: "p14-refused-dynamic-tools",
  operator: "tool_prune",
  node_id: "dynamictools",
  pattern: "tool_use",
  diag_id: "",
  evidence_case_ids: ["case-052"],
  rationale:
    "Tool legacyTool is declared but the eval set never calls it -> prune it.",
  source_diff: "",
  spec_diff: [
    {
      node_id: "dynamictools",
      dimension: "tools",
      from: "1 tool(s): buildTools()",
      to: "(none recorded)",
      kind: "tool_prune",
      items: ["buildTools()"],
    },
  ],
  build_status: "refused",
  state: "gate_failed",
  gate_result: "refused",
  held_out: false,
  held_out_label: "",
  delta: 0,
  ci_low: 0,
  ci_high: 0,
  significant: false,
  cost_delta: 0,
  latency_delta: 0,
  cases_fixed: [],
  cases_broken: [],
  narration:
    "Withheld: the transform refused this change at node dynamictools, dimension tools. Nothing was applied for that dimension - no partial diff was generated.",
  can_open_pr: false,
  pr_disabled_reason: "the transform refused this change",
  refused_node_id: "dynamictools",
  refused_dimension: "tools",
  refused_reason:
    "this call site's tool set is assembled at runtime (a function call), not written as a static list, so there is no declaration to delete; pruning it would mean guessing which of the tools it builds to drop, and a guess that compiles is the failure mode with no downstream net",
};

const CARDS: { id: string; label: string; card: ProposalCard }[] = [
  { id: "bind", label: "Bound a skill", card: BIND_SKILL },
  { id: "prune", label: "Pruned a tool", card: PRUNE_TOOL },
  { id: "refused", label: "Refused", card: REFUSED },
];

export default async function P14PreviewPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string }>;
}) {
  const active = safeTab((await searchParams).tab);
  const chosen = CARDS.find((c) => c.id === active) ?? CARDS[0];
  return (
    <PageFrame
      eyebrow="Preview"
      title="Skills and tools review"
      lede="The P14 review surface, seeded with representative cards. A bound platform skill and a pruned provider tool are different changes, and a refusal is a named outcome rather than an absent diff."
      wide
    >
      {/*
        The fixture picker is LINKS, not a second tablist. The card body below uses the same <Tabs> the
        real proposal page uses, and a tablist nested inside a tablist is two roving tab-stops fighting
        over the arrow keys — so choosing WHICH fixture is navigation (a URL, shareable) and choosing
        which SECTION of it is the tabs.
      */}
      <nav className="flex flex-wrap items-center gap-2" aria-label="Preview fixture">
        {CARDS.map((c) => (
          <Link
            key={c.id}
            className={
              c.id === chosen.id
                ? "rounded-lg border border-primary/40 bg-primary/10 px-3 py-1.5 font-mono text-xs text-primary"
                : "rounded-lg border border-border px-3 py-1.5 font-mono text-xs text-muted-foreground transition-colors hover:text-foreground"
            }
            href={`/preview/p14?tab=${c.id}`}
          >
            {c.label}
          </Link>
        ))}
      </nav>

      <RefusalNotice card={chosen.card} />
      <Tabs key={chosen.id} tabs={skillsToolsReviewTabs(chosen.card)} />
    </PageFrame>
  );
}
