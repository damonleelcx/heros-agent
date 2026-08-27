import { requireSession } from "@/lib/session";
import { PageFrame, Section } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { AxisFrame } from "@/components/axisFrame";
import { MemoryEditor } from "./authoring";
import { loadProjection } from "@/lib/projection";
import { loadAxisValues, valueFor } from "@/lib/axisRead";
import { loadMemoryVocabulary } from "@/lib/axisVocabulary";
import { resolveSubject, subjectFromSearchParams } from "@/lib/subjectResolver";

/**
 * The memory surface — an editor bound to the reader's own node (P17, re-bound by P37).
 *
 * # 🔴 The demonstration node is gone
 *
 * `authoring.tsx` opened with `const NODE_ID = "recall"` — a node that belongs to nobody, in the exact
 * position the reader's own node now occupies. It was the honest maximum when it was written: the
 * memory runtime existed and the reader's repository could not be read. P32 changed the input.
 *
 * The demonstration itself is not deleted. It is at `/docs/concepts/memory-strategies` §Worked example,
 * labelled as the platform's fixture, and the page links there.
 *
 * # 🔴 What stayed, because it varies with the reader's data
 *
 *   · the BOUNDARY, above the picker, naming the missing artifact and the two preconditions (FR15)
 *   · the reader's own refusal cause, verbatim (FR13)
 *   · `not_measured` with its named missing input — and on this axis that is the ORDINARY answer, because
 *     the reported structure carries no memory field at all (FR14)
 *   · `unverified` (FR16)
 *
 * # 🔴 `hashFor()` is deleted rather than moved, and that is the one block with no destination
 *
 * The panel computed a twelve-hex-character pseudo-hash in the browser, and its own comment admitted it
 * was not `config_hash`. NFR7.3: the browser derives nothing. The real hash comes from the server
 * preflight, so the caveat paragraph that warned about the fake one has nothing left to warn about.
 * block-inventory.md M10 records it as the single deliberate deletion in this phase.
 */
export const dynamic = "force-dynamic";
export const metadata = { title: "Memory strategy" };

export default async function MemoryPage({
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
  // 🔴 The reader's OWN node's language. The platform's handler fails closed on an empty one rather than
  // guessing `go`, because a boundary computed for the wrong language is a claim about code they do not
  // have — and this axis's boundary is the whole point of the surface.
  const language = outcome.state === "resolved" ? (outcome.subject.language ?? "") : "";
  const vocabulary = await loadMemoryVocabulary(language);

  return (
    <PageFrame
      eyebrow="Memory"
      title="What this node carries between turns"
      lede="What this node keeps ACROSS invocations. Within ONE call, how the message list is built is the context axis — a different thing, hashed and scored separately."
      wide
    >
      <AxisFrame axis="memory" outcome={outcome} returnTo="/app/memory">
        {(subject) => {
          const current = valueFor(values, subject.node_id, "memory");
          const tabs: TabItem[] = [
            {
              id: "editor",
              label: "This node",
              content: (
                <MemoryEditor
                  subject={subject}
                  vocabulary={vocabulary.state === "ok" ? vocabulary.vocabulary : null}
                  boundary={vocabulary.state === "ok" ? vocabulary.boundary : null}
                  readFailed={vocabulary.state !== "ok" ? vocabulary.detail : undefined}
                  current={
                    current
                      ? {
                          state: current.state,
                          current: current.current,
                          detail: current.detail,
                          missingInput: current.missing_input,
                          because: current.because,
                        }
                      : {
                          state: "not_measured",
                          missingInput: "unresolved_in_ir",
                          because:
                            "this node was not present in the structure this workflow reported, so there is no strategy here to read",
                        }
                  }
                />
              ),
            },
            {
              id: "your-nodes",
              label: "Your nodes",
              content: (
                <Section title="Every node, on this axis">
                  <AxisProjectionPanel axis="memory" outcome={projection} />
                </Section>
              ),
            },
          ];
          return <Tabs tabs={tabs} />;
        }}
      </AxisFrame>
    </PageFrame>
  );
}
