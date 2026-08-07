import { loadProjection } from "@/lib/projection";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { PageFrame, Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import {
  AuthoredChangeSummary,
  AuthoringSection,
  ApplyModeNote,
  PreflightPanel,
  ProviderBoundary,
  UnverifiedLabel,
  type PreflightResult,
} from "@/components/authoring";

/**
 * /app/authoring — the surface where a person makes a change themselves (P13 13c, section 11).
 *
 * # Why this is its own surface rather than a button on each axis page
 *
 * The rules that govern an authored change are the SAME on all four axes — one spine, origin-blind
 * refusals, the three preflight verdicts, unverified-until-measured, an exact undo. Spreading them
 * across four pages would restate them four times, and the day one restatement drifts is the day a
 * reader learns the rules differ per axis, which they do not.
 *
 * The axis-specific controls still live with their axis (the studio for model and prompt, wiring for
 * ordering, context for policy). This surface is where the shared contract is stated and where a
 * reader sees what they have authored.
 *
 * # What this page will not do
 *
 * It computes nothing. Every verdict, hash and state is rendered as received. There is no score, no
 * rank, no winner and no promotion path here — an authored change is applied or not, and whether it
 * was any good is a question only a multi-seed evaluation answers.
 */
export const metadata = { title: "Author a change" };

// Illustrative verdicts. Each is the shape the platform returns from
// `POST /api/v1/authoring/preflight`, rendered by the same component that renders a live one — so
// what a reader sees here is what they will see against their own workflow, not an approximation.
const ADMISSIBLE: PreflightResult = {
  verdict: "admissible",
  config_hash: "9f2c41ab77e0",
  dimensions: ["model"],
  nodes: ["classify"],
};

const REFUSED: PreflightResult = {
  verdict: "refused",
  node_id: "summarize",
  field: "provider_params",
  cause:
    "node \"summarize\", dim model: this call site applies inline, where provider parameters are code rather than data; there is no applicable parameter rewriter, so the override is refused rather than dropped from the diff",
};

const NOT_YET: PreflightResult = {
  verdict: "not_yet_measurable",
  node_id: "answer",
  missing_kind: "context_drop_ratio",
  missing_subject: "hierarchical-summary",
};

export default async function AuthoringPage() {
  // 🔴 P29 §5.10 — `coverage × your nodes`, BESIDE the worked examples and never instead of them.
  const projection = await loadProjection();
  return (
    <PageFrame
      eyebrow="Author a change"
      title="Make the change yourself"
      lede="You can set a model, a prompt version, a skill, a tool selection or a context policy directly — without waiting for the optimizer to propose one. Every change goes through the same gates the platform applies to its own proposals, and is recorded as unverified until a multi-seed evaluation has run."
      wide
    >
      <Tabs
        tabs={[
          { id: "how", label: "How it works", content: <HowItWorks /> },
          { id: "verdicts", label: "The three verdicts", content: <Verdicts /> },
          { id: "changes", label: "Your changes", content: <YourChanges /> },
          {
            id: "your-nodes",
            label: "Your nodes",
            // 🔴 A TAB, not a sibling: `Tabs` is `flex-1` and owns the remaining viewport, so a panel
            // placed after it collides with the tab strip.
            content: <AxisProjectionPanel axis="prompt" outcome={projection} />,
          },
        ]}
      />
    </PageFrame>
  );
}

function HowItWorks() {
  return (
    <div className="flex flex-col gap-6">
      <AuthoringSection
        title="One spine, two origins"
        lede="A change you author is derived, resolved, hashed, gated and transformed by exactly the components that process one the optimizer proposes. There is no separate path for hand-made changes — which means there is no gate a hand-made change can skip."
      >
        <Card>
          <Row>
            <Chip title="who originated the change">origin: you</Chip>
            <Chip title="the same pipeline an operator candidate travels">shared pipeline</Chip>
            <Chip title="authorship is recorded, never hashed">not in the hash</Chip>
          </Row>
          <p className="text-sm leading-relaxed text-muted-foreground">
            Who authored a change is recorded on the change, not in its configuration hash. A
            configuration you author and an identical one the optimizer proposes are the{" "}
            <strong>same configuration</strong> — they hash the same and are measured once.
          </p>
        </Card>
      </AuthoringSection>

      <AuthoringSection
        title="You author the change. You do not author the evidence."
        lede="You choose what to change. Which cases judge it, which cases are held out, and which seeds are used are derived by the platform."
      >
        <Banner tone="info" title="Why you cannot pick the cases that judge your own change">
          <p>
            A person authoring a cheaper model has the same incentive a cost-driven optimizer has, and
            better tools for acting on it. A result measured on cases chosen by the party who wants a
            particular answer is not evidence — so the held-out split is derived from the configuration
            itself and is disjoint from whatever motivated the change.
          </p>
        </Banner>
      </AuthoringSection>

      <AuthoringSection
        title="Applied is not verified"
        lede="A change you author can be applied without a verification run. It is then labelled unverified, wherever it appears."
      >
        <Card>
          <Row>
            <UnverifiedLabel state="unverified" />
            <Chip title="what an unverified change contributes to a savings or improvement figure">
              contributes nothing
            </Chip>
            <Chip title="delivery of an unverified change">never auto-merged</Chip>
          </Row>
          <p className="text-sm leading-relaxed text-muted-foreground">
            It is your repository, so the platform will not refuse to emit your edit. What it will not do
            is call it a result: an unverified change stays outside the verified-delta ledger,
            contributes nothing to any improvement or savings figure, and is never merged automatically
            at any automation level.
          </p>
        </Card>
      </AuthoringSection>

      <AuthoringSection
        title="Every change has an exact undo"
        lede="Reverting re-derives the parent you started from, rather than applying the inverse of your edits."
      >
        <Card>
          <p className="text-sm leading-relaxed text-muted-foreground">
            The result is byte-identical to the configuration you had — not merely equivalent to it.
            Applying an inverse edit is how an undo quietly becomes a third configuration; re-deriving
            from an immutable parent cannot drift.
          </p>
        </Card>
      </AuthoringSection>

      <AuthoringSection
        title="There is no override"
        lede="No plan, role or setting materialises a change the engine refuses."
      >
        <Banner tone="info" title="A refusal is not a permission problem">
          <p>
            Refusals exist because the change would be wrong in a way that is not visible at the moment
            of choosing it — a model string swapped across SDKs, a parameter a call site cannot carry, a
            prompt slot that stops binding. Asking for it more forcefully does not make the SDK match, so
            there is no button that does.
          </p>
        </Banner>
      </AuthoringSection>
    </div>
  );
}

function Verdicts() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Before you submit, you are told what will happen">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Every draft is checked before submission. The check publishes nothing, writes no diff and
          spends no evaluation budget — and it answers with one of three verdicts, never two.
        </p>
      </Section>

      <Section title="Admissible">
        <PreflightPanel result={ADMISSIBLE} />
      </Section>

      <Section title="Refused">
        <PreflightPanel result={REFUSED} />
      </Section>

      <Section title="Not yet measurable">
        <PreflightPanel result={NOT_YET} />
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          This third verdict is the one most easily lost. Rendering it as a refusal would point you at
          your own configuration to find a fault that is not there; rendering it as admissible would
          claim a safety check that never ran. It is neither, so it says so.
        </p>
      </Section>
    </div>
  );
}

function YourChanges() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Changes you have authored">
        <AuthoredChangeSummary
          changeId="ac_4f19c2ab7d3e5610bb42"
          configHash="9f2c41ab77e0"
          axis="model"
          verificationState="unverified"
          actorId="you"
        />
        <AuthoredChangeSummary
          changeId="ac_77b0e4c1aa9f3d2265be"
          configHash="1d84cc02be51"
          axis="prompt"
          verificationState="unverified"
          actorId="you"
          forkedFrom="cand-7"
        />
      </Section>

      <Section title="What each node can carry">
        <ApplyModeNote mode="bound" />
        <ApplyModeNote mode="inline" />
      </Section>

      <Section title="Why the model list is short">
        <ProviderBoundary provider="anthropic" />
      </Section>
    </div>
  );
}
