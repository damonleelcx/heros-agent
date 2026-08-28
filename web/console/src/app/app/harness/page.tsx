import { requireSession } from "@/lib/session";
import { PageFrame, Banner, Section } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { AxisFrame } from "@/components/axisFrame";
import { CurrentValue, ReadOn } from "@/components/editorKit";
import { loadProjection } from "@/lib/projection";
import { loadAxisValues, valueFor } from "@/lib/axisRead";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";
import { AXIS_DOC } from "@/lib/axisSubject";
import { ENVELOPE_BOUNDARY, SANDBOX_CONCURRENCY_CEILING } from "./envelope";

/**
 * The HARNESS surface — the execution envelope, bound to the reader's own node (P34 ADR-014, P37).
 *
 * # 🔴 This is the axis with NO editor, and that is a product fact rather than a gap P37 left
 *
 * An envelope is a property of how a node is DEPLOYED — where it may reach, what it may spend, how long
 * it may run. None of that is written at a call site in any language, so there is no rewriter that could
 * ever emit one and no vocabulary endpoint to bind a picker to. `lib/axisVocabulary.ts` deliberately has
 * no loader for it.
 *
 * P34 §7.3 already settles what to render in that case: **read-only, with its reason, and the reason
 * leads.** A picker that cannot produce a diff reads as a bug, and a hand-written array standing in for
 * a vocabulary would be a second source of truth for a set nothing serves.
 *
 * # 🔴 Refused everywhere is NOT unenforced, and that sentence is protected text
 *
 * It is the one misreading this axis cannot afford: a reader who took the badge to mean "ignored" would
 * be wrong about their own blast radius. It stays on the working surface (FR15), above everything else.
 *
 * # What moved
 *
 * The nine-field reference table (430 words) is at `/docs/concepts/execution-envelope`, and its gate
 * moved with it: `tests/envelope.test.mjs` now asserts the DOCUMENT against
 * `registry.EnvelopeHarness{}.ParamsSchema()`, field for field and required-flag for required-flag.
 *
 * ⚠️ That test did not exist before. `envelope.ts` claimed it did — *"tests/envelope.test.mjs reads the
 * engine's own Go source and asserts this file agrees with it … that test is the gate, and it is the
 * reason this comment is not just a promise"* — and the file was never written. The mirror had been
 * ungated since it shipped. P37 delivers the promised fence rather than repeating the promise.
 */
export const dynamic = "force-dynamic";

export default async function HarnessPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  await requireSession();
  const outcome = await resolveSubject(subjectFromSearchParams(await searchParams));
  const projection = await loadProjection();
  const values = await loadAxisValues(
    outcome.state === "resolved" ? outcome.subject.workflow_id : undefined,
  );

  return (
    <PageFrame
      eyebrow="Harness"
      title="What this node is allowed to do, and inside what walls"
      lede="The execution envelope: where this node may reach, the most it may spend, the most turns any loop inside it may take. It imposes; the loop chooses within it."
    >
      <AxisFrame axis="harness" outcome={outcome} returnTo="/app/harness">
        {(subject) => {
          const current = valueFor(values, subject.node_id, "harness");
          const tabs: TabItem[] = [
            {
              id: "envelope",
              label: "This node",
              content: (
                <div className="flex flex-col gap-6">
                  {/*
                    🔴 The boundary FIRST, and it is the sentence this surface exists to protect. §8.1
                    reviews it as a customer-facing commitment.
                  */}
                  <Banner
                    tone="warn"
                    title="Written into your source: nothing, in any language, permanently. That is not the same as unenforced."
                  >
                    <p>{ENVELOPE_BOUNDARY.reason}</p>
                    <p>
                      The turn ceiling and the host services are checked when your configuration{" "}
                      <em>resolves</em>. The spend ceiling is checked before each provider call. The
                      concurrency limit is checked twice — and the sandbox caps it at{" "}
                      <span className="mono">{SANDBOX_CONCURRENCY_CEILING}</span> whatever the spec said,
                      because the early gate is bypassed by any path that reaches an executor without
                      resolving a spec.
                    </p>
                    <ReadOn href={AXIS_DOC.harness}>The nine fields, and the two places each one bites</ReadOn>
                  </Banner>

                  <Section title="This node's envelope">
                    {/*
                      🔴 FR14 — `not_measured` with its named missing input, which on this axis is the
                      ORDINARY answer for every node in every repository. It is drawn, never omitted and
                      never rendered as an empty region, because absence here is the whole finding.
                    */}
                    <CurrentValue
                      {...(current
                        ? {
                            state: current.state,
                            current: current.current,
                            detail: current.detail,
                            missingInput: current.missing_input,
                            because: current.because,
                          }
                        : {
                            state: "not_measured" as const,
                            missingInput: "not_visible_in_static_ir",
                            because:
                              "an execution envelope is a property of how this node is deployed, so there is nothing in a source snapshot to read it from",
                          })}
                    />
                  </Section>
                </div>
              ),
            },
            {
              id: "your-nodes",
              label: "Your nodes",
              content: <AxisProjectionPanel axis="harness" outcome={projection} />,
            },
          ];
          return <Tabs tabs={tabs} />;
        }}
      </AxisFrame>
    </PageFrame>
  );
}
