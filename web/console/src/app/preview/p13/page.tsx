import type { Card as ProposalCard } from "@/lib/types.generated";
import { PageFrame } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { P13OptimizationReview } from "@/components/p13Optimization";

/**
 * A self-contained preview of the P13 optimization-review surface. It renders the same
 * P13OptimizationReview component the proposal detail page uses, seeded with representative verified
 * cards, so the presentation can be reviewed without a live platform backend. It uses only the root
 * layout (no session, no BFF), which is why it lives outside /app.
 */
export const dynamic = "force-static";

const COMPRESS: ProposalCard = {
  proposal_id: "p13-compress-answer",
  operator: "prompt_compress",
  node_id: "answer",
  pattern: "prompt_chaining",
  diag_id: "diag-answer-drift",
  evidence_case_ids: ["case-014", "case-031", "case-052"],
  rationale:
    "Prompt bloat on 3 case(s) → compress; competes on verified quality, not token count.",
  source_diff: [
    "--- a/pipeline.go",
    "+++ b/pipeline.go",
    "@@ -18,7 +18,7 @@",
    '-\t\tanthropic.NewTextBlock("Answer the question clearly.   \\n\\n\\n\\nBe concise."),',
    '+\t\tanthropic.NewTextBlock("Answer the question clearly.\\nBe concise."),',
  ].join("\n"),
  spec_diff: null,
  build_status: "built",
  state: "recommended",
  gate_result: "5 seeds · 95% CI",
  held_out: false,
  held_out_label: "",
  delta: 0.012,
  ci_low: -0.004,
  ci_high: 0.028,
  significant: false,
  cost_delta: -0.0011,
  latency_delta: -40,
  cases_fixed: ["case-014", "case-031"],
  cases_broken: [],
  narration:
    "Shorter prompt, quality held within the confidence interval — a tie on quality with a small cost win.",
  can_open_pr: true,
};

const DOWNGRADE: ProposalCard = {
  proposal_id: "p13-downgrade-router",
  operator: "model_downgrade",
  node_id: "router",
  pattern: "routing",
  diag_id: "",
  evidence_case_ids: ["case-002", "case-009"],
  rationale:
    "Cost bottleneck → downgrade to a cheaper model; admissible only under the held-out quality guardrail (equal-quality-cheaper tie).",
  source_diff: [
    "--- a/pipeline.go",
    "+++ b/pipeline.go",
    "@@ -42,7 +42,7 @@",
    '-\t\tModel: "claude-opus-4-8",',
    '+\t\tModel: "claude-haiku-4-5",',
  ].join("\n"),
  spec_diff: null,
  build_status: "built",
  state: "recommended",
  gate_result: "held-out · 5 seeds",
  held_out: true,
  held_out_label: "held out · 12 cases",
  delta: 0.0,
  ci_low: 0.91,
  ci_high: 0.97,
  significant: false,
  cost_delta: -0.0185,
  latency_delta: -220,
  cases_fixed: [],
  cases_broken: [],
  narration:
    "Task-success intervals overlap on the held-out cases — the models are statistically indistinguishable there.",
  can_open_pr: true,
};

const PARAM: ProposalCard = {
  proposal_id: "p13-param-answer",
  operator: "param_tune",
  node_id: "answer",
  pattern: "prompt_chaining",
  diag_id: "diag-answer-drift",
  evidence_case_ids: ["case-014"],
  rationale: "Format drift → tune provider params {temperature: 0.1} (bound apply mode).",
  source_diff: "",
  spec_diff: null,
  build_status: "built",
  state: "recommended",
  gate_result: "5 seeds · 95% CI",
  held_out: false,
  held_out_label: "",
  delta: 0.034,
  ci_low: 0.006,
  ci_high: 0.062,
  significant: true,
  cost_delta: 0,
  latency_delta: 0,
  cases_fixed: ["case-014"],
  cases_broken: [],
  narration: "Lower temperature tightened output-contract adherence on the drift cases.",
  can_open_pr: true,
};

// One tab per proposal — the console is viewport-first (NFR17): a page whose sections would stack tall
// splits into <Tabs> so one proposal is on screen at a time and the active panel owns the scroll,
// rather than relying on a long body scroll.
const TABS = [
  { id: "compress", label: "Prompt compression", content: <P13OptimizationReview card={COMPRESS} /> },
  { id: "downgrade", label: "Model downgrade", content: <P13OptimizationReview card={DOWNGRADE} /> },
  { id: "param", label: "Parameter tune", content: <P13OptimizationReview card={PARAM} /> },
];

export default function P13PreviewPage() {
  return (
    <PageFrame
      eyebrow="Preview · P13"
      title="Prompt & model optimization"
      lede="How the platform presents a P13 optimization proposal for review: prompt rewrites as a diff of a new immutable version with their grounding, and a model downgrade as an equal-quality-cheaper tie judged on held-out cases."
      wide
    >
      <Tabs tabs={TABS} />
    </PageFrame>
  );
}
