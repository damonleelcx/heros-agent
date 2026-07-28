import { Banner, Card, Chip, DataTable, Row, Section } from "@/components/primitives";
import {
  ApplyModeNote,
  PreflightPanel,
  ProviderBoundary,
  UnverifiedLabel,
  type PreflightResult,
} from "@/components/authoring";

/**
 * StudioAuthoring adds MODEL and PROVIDER-PARAMETER authoring beside the prompt authoring the studio
 * already had (P13 13c task 11.2).
 *
 * # Why it is a new tab rather than a rewrite of the matrix
 *
 * Everything the studio already does keeps doing it, in the same place. A UI revision that moves an
 * existing capability while adding a new one makes the two indistinguishable in review — and the one
 * that gets lost is always the old one, because nobody is looking for it. So this is additive: the
 * matrix, the prompt library and the bound-nodes view are untouched.
 *
 * # Why models from other providers are ABSENT, and said to be
 *
 * A call site written against one SDK does not become another by changing a model string — the client,
 * the parameter types and the response shape all differ. Offering those models would produce a diff
 * that compiles and then calls the wrong provider in production, which is the worst failure this
 * surface could cause: silent, and in production.
 *
 * But a list that is silently short reads as an incomplete catalogue, and the reader's next move is to
 * look for the missing entries or file a bug. So the boundary is STATED at the point of choice.
 *
 * # Still not an evaluator
 *
 * No score, no rank, no winner, no interval, no promotion path. Authoring changes who chooses a model.
 * It changes nothing about who judges whether the choice was good.
 */

/** The nodes and what each can carry. `apply_mode` is a structural property, known before you choose. */
const NODES = [
  { id: "classify", provider: "anthropic", model: "claude-haiku-4-5", mode: "bound" as const },
  { id: "summarize", provider: "anthropic", model: "claude-sonnet-4-5", mode: "inline" as const },
  { id: "answer", provider: "anthropic", model: "claude-opus-4-1", mode: "bound" as const },
];

/** The model choices offered for an anthropic call site — every one of them anthropic. */
const OFFERED = ["claude-haiku-4-5", "claude-sonnet-4-5", "claude-opus-4-1"];

const PARAM_REFUSAL: PreflightResult = {
  verdict: "refused",
  node_id: "summarize",
  field: "provider_params",
  cause:
    'node "summarize", dim model: this call site applies inline, where provider parameters are code rather than data; there is no applicable parameter rewriter, so the override is refused rather than dropped from the diff',
};

export function StudioAuthoring() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="Set a model yourself">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Pick the model for a node directly, instead of waiting for a diagnosis to propose one. The
          change goes through the same gates a proposed one does, produces a reviewable diff, and is
          recorded <strong>unverified</strong> until a multi-seed evaluation has run.
        </p>

        <DataTable
          caption="Nodes, the model each runs, and what each can carry"
          columns={[
            { key: "node", label: "Node" },
            { key: "provider", label: "Provider" },
            { key: "model", label: "Model" },
            { key: "mode", label: "Apply mode" },
            { key: "params", label: "Parameters" },
            { key: "state", label: "State" },
          ]}
        >
          <tbody>
            {NODES.map((n) => (
              <tr key={n.id}>
                <td className="mono">{n.id}</td>
                <td>
                  <Chip title="the SDK this call site is written against">{n.provider}</Chip>
                </td>
                <td className="mono">{n.model}</td>
                <td>
                  <Chip title="inline writes values at the call site; bound carries them as data">
                    {n.mode}
                  </Chip>
                </td>
                <td>
                  {n.mode === "bound" ? (
                    "can be carried"
                  ) : (
                    <span className="text-muted-foreground">declined on this node</span>
                  )}
                </td>
                <td>
                  <UnverifiedLabel state="unverified" />
                </td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </Section>

      <Section title="The models offered for these call sites">
        <Card>
          <Row>
            {OFFERED.map((m) => (
              <Chip key={m} title="a model this call site can be changed to">
                {m}
              </Chip>
            ))}
          </Row>
        </Card>
        <ProviderBoundary provider="anthropic" />
      </Section>

      <Section title="Provider parameters">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Temperature and max-tokens are data in bound apply mode and code in inline mode. Whether a node
          can carry them is known before you choose, so it is said before you choose.
        </p>
        <ApplyModeNote mode="bound" />
        <ApplyModeNote mode="inline" />
        <PreflightPanel result={PARAM_REFUSAL} />
      </Section>

      <Section title="What this tab is not">
        <Banner tone="info" title="Authoring is not an evaluator">
          <p>
            Nothing here ranks a model, declares a winner, or promotes a configuration. Choosing a model
            yourself changes who picked the candidate; it changes nothing about who judges it, and the
            judge is still a multi-seed evaluation with confidence intervals.
          </p>
        </Banner>
      </Section>
    </div>
  );
}
