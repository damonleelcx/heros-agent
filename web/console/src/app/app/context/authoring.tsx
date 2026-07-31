import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { PreflightPanel, UnverifiedLabel, type PreflightResult } from "@/components/authoring";

/**
 * authoring.tsx renders CONTEXT authoring (P16 16c, tasks 8.11–8.15).
 *
 * # This surface is governed by loss, not by permission
 *
 * Context is the one axis where a change silently destroys information. So the question a reader needs
 * answered is not "may I pick this policy?" — they may — but "what does this one discard?", and the
 * honest answer has three shapes, not two:
 *
 *   admissible          — measured, and within the tolerance this node declares
 *   refused             — measured, and over it, with both numbers shown
 *   not yet measurable  — nobody has measured this pair, and we say so
 *
 * The third is the one that gets lost. Drawn as a refusal it points the reader at their own
 * configuration to find a fault that is not there; drawn as admissible it claims a safety check that
 * never ran. It is neither, and it renders as neither.
 *
 * # Why the drop ratio is never a saving
 *
 * A lossier policy shows fewer tokens. "Tokens saved" next to a falling count is the easiest chart in
 * the product to draw, and on an unverified change it is false: until the harness runs, task success is
 * unmeasured, and a policy that discarded the answer looks exactly like one that discarded filler. So
 * the number is always described as information discarded.
 */

const ADMISSIBLE: PreflightResult = {
  verdict: "admissible",
  config_hash: "b41c7e09aa25",
  dimensions: ["context"],
  nodes: ["summarize"],
};

const OVER_TOLERANCE: PreflightResult = {
  verdict: "refused",
  node_id: "answer",
  field: "context",
  shape: "context policy",
  cause:
    'node "answer" declares a drop tolerance of 0.20, and "ctx-summarization" was measured to discard 0.62 of its context — refused before any evaluation spend',
};

const NOT_MEASURED: PreflightResult = {
  verdict: "not_yet_measurable",
  node_id: "classify",
  missing_kind: "context_drop_ratio",
  missing_subject: "ctx-hierarchical-summary",
};

const NO_REWRITER: PreflightResult = {
  verdict: "refused",
  node_id: "route",
  field: "context",
  shape: "context policy",
  cause:
    'node "route", dim context: context assembly is how the surrounding code builds the message list, so applying a policy means rewriting that code — and no rewriter for kotlin has landed yet (covered today: go, python)',
};

/** The registered policies. A policy outside this set is not a lesser choice — nothing resolves it. */
const POLICIES = [
  "ctx-full-history",
  "ctx-sliding-window",
  "ctx-summarization",
  "ctx-hierarchical-summary",
  "ctx-rag-retrieval",
];

export function ContextAuthoring() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Choose a context policy">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Pick the strategy a node uses to build its message list, instead of waiting for a diagnosis to
          propose one. Only registered policies are offered — a policy nothing resolves is not a choice.
        </p>
        <Card>
          <Row>
            {POLICIES.map((p) => (
              <Chip key={p} title="a registered context policy">
                {p}
              </Chip>
            ))}
          </Row>
          <Row>
            <UnverifiedLabel state="unverified" />
          </Row>
        </Card>
      </Section>

      <Section title="Within tolerance">
        <PreflightPanel result={ADMISSIBLE} />
      </Section>

      <Section title="Over tolerance — refused before anything is spent">
        <PreflightPanel result={OVER_TOLERANCE} />
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Both numbers are shown because either could be the thing to change: relax the tolerance if this
          node can afford the loss, or pick a policy that discards less. A refusal that said only
          &ldquo;exceeds tolerance&rdquo; would give you neither.
        </p>
      </Section>

      <Section title="Not yet measurable">
        <PreflightPanel result={NOT_MEASURED} />
        <Banner tone="info" title="A measured check, not a guarantee">
          <p>
            The drop gate judges on a <strong>measurement</strong>. Where we have one, it decides; where we
            do not, it says so rather than guessing in either direction. It is not a promise that no
            context will ever be lost — it is a promise that we will not pretend to have looked.
          </p>
        </Banner>
      </Section>

      <Section title="When a node&rsquo;s language has no rewriter">
        <PreflightPanel result={NO_REWRITER} />
      </Section>

      <Section title="Retrieval tuning is gated by the classifier">
        <Banner tone="info" title="Offered only on a node the classifier labels as retrieval">
          <p>
            Top-k, chunk size, rerank and the embedding model are meaningful only where retrieval happens.
            On a node the classifier labelled something else, they are not offered and the reason is
            stated — a control that were simply absent would read as a missing feature.
          </p>
          <p>
            The label is <strong>not</strong> settable here. A node relabelled to unlock the parameters
            would let a result be attributed to parameters that did nothing; a misclassification is a
            classifier defect to fix, not an override to hand out.
          </p>
        </Banner>
      </Section>

      <Section title="What a smaller context claims">
        <Banner tone="info" title="Nothing, until the harness runs">
          <p>
            A lossier policy shows fewer tokens immediately. That number is reported as{" "}
            <strong>information discarded</strong>, never as a saving: until a multi-seed evaluation runs,
            task success is unmeasured, and a policy that discarded the answer looks exactly like one that
            discarded filler.
          </p>
          <p>
            Pure augmentation is recorded as retrieval rather than loss — a drop ratio of zero with a
            positive retrieved-chunk count.
          </p>
        </Banner>
      </Section>
    </div>
  );
}
