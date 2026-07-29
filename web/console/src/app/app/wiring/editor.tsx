import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { PreflightPanel, UnverifiedLabel, type PreflightResult } from "@/components/authoring";

/**
 * editor.tsx renders the WIRING AUTHORING surface (P15 15d, tasks 19.10–19.13).
 *
 * # Why this is the riskiest surface in the product
 *
 * A graph editor looks like it can do anything. Dragging a node is a two-second gesture that expresses
 * an arbitrary rewiring, and nothing about the gesture says the platform materializes exactly ONE shape.
 * A surface that accepts every gesture and refuses at apply time is a machine for generating
 * rearrangements that can never ship.
 *
 * Worse — and this is the part that is not merely a UX problem — an unmaterializable wiring change is
 * not just un-appliable, it is UNSCOREABLE. Evaluating its configuration hash against source that was
 * never rearranged would score the base configuration under a variant's hash. So a refused draft must
 * never appear in the variant list, and must never wear the word "pending", which implies somebody is
 * working on it.
 *
 * # What this surface does instead
 *
 * Every gesture gets its verdict AS IT IS MADE, from the same coherence gate the compiler runs:
 *
 *   admissible          — the one applicable shape, on a language with a statement materializer
 *   refused (shape)     — a merge, a prune, an edge change, a non-adjacent move, a multi-swap
 *   refused (coherence) — this ordering breaks the graph, naming the consumer, producer AND field
 *   adapted             — legal only because an adapter would be inserted, shown as a visible node
 *
 * The incoherence case names all three, and highlights them in the graph. A graph error with no names
 * is unactionable: the reader is left staring at a diagram with no idea which part to look at.
 */

type Gesture = {
  id: string;
  label: string;
  /** What the reader did. */
  did: string;
  result: PreflightResult;
  /** Nodes to highlight in the graph, so the refusal points at something. */
  highlight?: { consumer?: string; producer?: string; field?: string };
  /** Adapters this ordering would insert, shown before submission. */
  adapters?: { node_id: string; from: string; to: string; kind: string }[];
  /** Whether this draft may be submitted for evaluation. Refused shapes are never scoreable. */
  scoreable: boolean;
};

const GESTURES: Gesture[] = [
  {
    id: "swap",
    label: "Swap two adjacent nodes",
    did: "You exchanged two adjacent, independent statements in the same function.",
    result: {
      verdict: "admissible",
      config_hash: "3ac91f0b52de",
      dimensions: ["wiring"],
      nodes: ["classify", "enrich"],
    },
    scoreable: true,
  },
  {
    id: "adapted",
    label: "Swap, reconciled by an adapter",
    did: "You reordered two nodes whose schemas do not line up.",
    result: {
      verdict: "admissible",
      config_hash: "8be2740fa19c",
      dimensions: ["wiring"],
      nodes: ["retrieve", "summarize"],
      // 🚫 Deliberately NOT also listed in the verdict's own `adapters`. PreflightPanel renders that
      // list as a bare chip row; AdapterNodes below renders the same adapter with its edge and its kind.
      // Passing both drew the adapter twice, which reads as two adapters — the opposite of the clarity
      // showing it early is for.
    },
    adapters: [
      { node_id: "adp_retrieve_summarize_rename", from: "retrieve", to: "summarize", kind: "rename" },
    ],
    scoreable: true,
  },
  {
    id: "incoherent",
    label: "Move a consumer before its producer",
    did: "You dragged a node ahead of the one that produces a field it reads.",
    result: {
      verdict: "refused",
      node_id: "summarize",
      field: "passages",
      shape: "adjacent transposition",
      cause:
        'node "summarize" consumes "passages", which node "retrieve" produces — this ordering runs the consumer first, so the field would be undefined',
    },
    highlight: { consumer: "summarize", producer: "retrieve", field: "passages" },
    scoreable: false,
  },
  {
    id: "merge",
    label: "Merge two nodes into one",
    did: "You fused two adjacent calls.",
    result: {
      verdict: "refused",
      node_id: "classify",
      shape: "merge",
      cause:
        "a merge cannot be materialized as source: the call-site rewriter emits exactly one wiring shape today — a transposition of two adjacent, independent statements. A merge is modelled and hashed, but no rewriter moves, fuses or deletes a call at the source yet, so it is refused rather than applied as a no-op that would let its configuration be scored against unrearranged code",
    },
    scoreable: false,
  },
  {
    id: "nonadjacent",
    label: "Move a node across the graph",
    did: "You dragged a node past two others.",
    result: {
      verdict: "refused",
      node_id: "answer",
      shape: "non-adjacent move",
      cause:
        "a non-adjacent move cannot be materialized as source: the call-site rewriter emits exactly one wiring shape today — a transposition of two adjacent, independent statements",
    },
    scoreable: false,
  },
];

export function WiringEditor() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Every gesture gets its verdict as you make it">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          You are told what a rearrangement would do <strong>before</strong> you submit it — whether it
          can be applied, whether it breaks the graph, or whether the shape is one no rewriter emits yet.
          The check runs the same coherence gate the compiler runs, so the editor cannot bless something
          the compiler will reject.
        </p>
      </Section>

      {GESTURES.map((g) => (
        <Section key={g.id} title={g.label}>
          <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">{g.did}</p>
          <PreflightPanel result={g.result} />
          {g.highlight ? <BreakHighlight highlight={g.highlight} /> : null}
          {g.adapters?.length ? <AdapterNodes adapters={g.adapters} /> : null}
          <ScoreabilityNote scoreable={g.scoreable} shape={g.result.shape} />
        </Section>
      ))}

      <Section title="A refused rearrangement is not a variant">
        <Banner tone="info" title="Kept as a recorded intent, not queued for anything">
          <p>
            A rearrangement the platform cannot apply is retained so you do not lose it — but it is{" "}
            <strong>not</strong> a variant. It has no configuration hash, it is never evaluated, and it is
            not waiting for a number. Calling it pending would imply somebody is working on it.
          </p>
          <p>
            The reason it is not evaluated is not tidiness. Scoring a rearranged configuration against
            source that was never rearranged would produce a number for a change that did not happen —
            which is worse than no number at all.
          </p>
        </Banner>
      </Section>
    </div>
  );
}

/**
 * BreakHighlight names the three things a coherence refusal owes, and points at them in the graph.
 *
 * "Invalid ordering" is the refusal this component exists to make impossible. The platform knows which
 * consumer, which producer, and which field; a reader who is told only that something is wrong has a
 * diagram and no next step.
 */
function BreakHighlight({
  highlight,
}: {
  highlight: { consumer?: string; producer?: string; field?: string };
}) {
  return (
    <Card>
      <Row>
        <Chip tone="warn" title="the node whose input would become undefined">
          consumer: {highlight.consumer}
        </Chip>
        <Chip title="the node that produces the field it needs">producer: {highlight.producer}</Chip>
        <Chip title="the field that would become undefined">field: {highlight.field}</Chip>
      </Row>
      <p className="text-sm leading-relaxed text-muted-foreground">
        These three are highlighted in the graph above. Move{" "}
        <span className="mono">{highlight.consumer}</span> back after{" "}
        <span className="mono">{highlight.producer}</span>, or give it another source for{" "}
        <span className="mono">{highlight.field}</span>.
      </p>
    </Card>
  );
}

/**
 * AdapterNodes shows the component an ordering would require, BEFORE submission.
 *
 * An indirection never hides a value from review. A reader who reorders two nodes and later finds a
 * component in the diff that they never saw proposed cannot review it — and the gate having proved the
 * adapter drops nothing the consumer requires is not the same as their having agreed to it.
 */
function AdapterNodes({
  adapters,
}: {
  adapters: { node_id: string; from: string; to: string; kind: string }[];
}) {
  return (
    <Card>
      <p className="text-sm leading-relaxed text-muted-foreground">
        <strong>This ordering is legal only because an adapter would be inserted.</strong> It is a real
        node in your graph and ships as generated source in the same diff — not a hidden runtime
        coercion. It is shown here so the change you submit is the change you saw.
      </p>
      {adapters.map((a) => (
        <Row key={a.node_id}>
          <Chip variant="hash" title="the adapter node that would be inserted">
            {a.node_id}
          </Chip>
          <Chip title="the edge it sits on">
            {a.from} → {a.to}
          </Chip>
          <Chip title="the catalog adapter kind">{a.kind}</Chip>
        </Row>
      ))}
    </Card>
  );
}

/** ScoreabilityNote states, per gesture, whether it may be evaluated at all. */
function ScoreabilityNote({ scoreable, shape }: { scoreable: boolean; shape?: string }) {
  if (scoreable) {
    return (
      <Row>
        <Chip title="this change can be applied and evaluated">can be evaluated</Chip>
        <UnverifiedLabel state="unverified" />
      </Row>
    );
  }
  return (
    <Row>
      <Chip title="a change the platform cannot apply cannot be evaluated either">
        not evaluated{shape ? ` — ${shape}` : ""}
      </Chip>
    </Row>
  );
}
