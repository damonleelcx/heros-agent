import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, NotMountedState, Pill } from "@/components/states";
import { DataTable, Num, PageFrame, Section } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { ActionForm } from "@/components/actionForm";
import { publishPlatformPrompt } from "@/lib/actions";
import type { AgentOverview, AgentAxisRow, Availability, AdminIdentity } from "@/lib/types";

/**
 * The platform's own analysis agent (P30 §6).
 *
 * # 🔴 The three things this page must never do
 *
 *  1. **Render a version as active before its gate passed.** A definition that has not met the floor on
 *     every fixture individually is not serving anything, and the platform is emphatic about which one
 *     IS: `serving_config_hash` is a separate field precisely so "the definition I am looking at" and
 *     "the definition answering analyses right now" cannot be confused. This page never derives
 *     activity from recency or from `rehearsal_state`.
 *
 *  2. **Hide an unavailable strategy.** `react-loop` and `plan-execute` need host services this
 *     runner does not supply, and they are SHOWN with what they would need — because "a hidden option
 *     is indistinguishable from one that does not exist", and an operator who cannot see the option
 *     cannot ask for the service that would enable it.
 *
 *  3. **Hide the wiring axis.** It is fixed, it is rendered read-only WITH its reason, and it is not
 *     omitted. Same argument.
 *
 * # Why the axis status is three-valued and not a checkbox
 *
 * `set`, `defaulted` and `not_in_effect` are three different situations with three different next
 * actions, and a boolean can express two of them. `not_in_effect` always carries a reason: an axis that
 * is inert for an unstated reason is a configuration an operator cannot act on.
 */

const AXIS_TONE: Record<AgentAxisRow["status"], "ok" | "neutral" | "warn"> = {
  set: "ok",
  // NEUTRAL, not a warning. Most axes are defaulted on a fresh definition and painting them amber
  // would spend the colour the kill switch needs on the commonest row on the page.
  defaulted: "neutral",
  not_in_effect: "warn",
};

const AXIS_LABEL: Record<AgentAxisRow["status"], string> = {
  set: "set",
  defaulted: "defaulted",
  not_in_effect: "not in effect",
};

export default async function AgentPage() {
  const { identity, sessionToken } = await requireIdentity();

  if (!hasCapability(identity, "agent.read")) {
    return (
      <OperatorShell identity={identity} sessionToken={sessionToken}>
        <PageFrame eyebrow="Optimization" title="Analysis Agent">
          <DeniedState
            capability="agent.read"
            description="Read the platform analysis agent's definition, rehearsal and spend"
            heldBy={holdersOf(identity, "agent.read")}
          />
        </PageFrame>
      </OperatorShell>
    );
  }

  // Authoring the instruction IS changing what the platform infers with, so it needs the write
  // capability — not the read one that got us onto this page.
  const canAdmin = hasCapability(identity, "agent.admin");

  let view: AgentOverview | null = null;
  let failure: { kind: string; message: string } | null = null;
  try {
    view = await adminFetch<AgentOverview>("/admin/api/agent", { sessionToken });
  } catch (error) {
    failure =
      error instanceof AdminApiError
        ? { kind: error.kind, message: error.message }
        : { kind: "degraded", message: String(error) };
  }

  return (
    <OperatorShell identity={identity} sessionToken={sessionToken}>
      <PageFrame
        eyebrow="Optimization"
        title="Analysis Agent"
        lede={
          <>
            The agent this platform runs over customers&rsquo; source to fill what the parsers missed.
            It is described as an ordinary <strong>Variant Spec</strong> over the six axes, so the
            platform&rsquo;s own eval harness can measure it — and it does not serve anything until it
            has met its floor on <strong>every</strong> calibration fixture individually.
          </>
        }
      >
        {failure ? (
          failure.kind === "not_mounted" ? (
            <NotMountedState what="the analysis agent" detail={failure.message} />
          ) : (
            <DegradedState what="the analysis agent" detail={failure.message} />
          )
        ) : !view ? (
          <DegradedState what="the analysis agent" />
        ) : (
          <AgentBody view={view} canAdmin={canAdmin} identity={identity} />
        )}
      </PageFrame>
    </OperatorShell>
  );
}

function AgentBody({
  view,
  canAdmin,
  identity,
}: {
  view: AgentOverview;
  /** Whether this operator may CHANGE the agent, not merely read it. */
  canAdmin: boolean;
  identity: AdminIdentity;
}) {
  const axes = view.axes ?? [];
  const versions = view.versions ?? [];

  return (
    <>
      {/* 🔴 WHAT IS SERVING, stated first and by name, before anything else on the page. Every other
          section describes a definition; this one says which of them is answering analyses. */}
      <Section title="What is serving inference" flush>
        <p>
          <Pill tone={view.state === "serving" ? "ok" : "neutral"}>
            {view.state.replace(/_/g, " ")}
          </Pill>{" "}
          {view.serving_config_hash ? (
            <>
              serving <code>{view.serving_config_hash.slice(0, 12)}</code>
            </>
          ) : (
            <strong>nothing is serving inference</strong>
          )}
        </p>
        <p>{view.sentence}</p>
        {view.kill_switch_armed ? (
          /* The EXISTING durable brake, applied to HEROS. Carried on this view so the agent surface
             cannot show a healthy agent that is in fact halted. */
          <p>
            <Pill tone="danger">halted</Pill> The platform kill switch is armed
            {view.kill_switch_note ? `: ${view.kill_switch_note}` : "."} No analysis runs while it is.
          </p>
        ) : null}
        <p>
          Stored inferences:{" "}
          {view.inferences_known ? (
            <Num value={view.stored_inferences} />
          ) : (
            /* 🔴 UNKNOWN, never zero. No inference store is wired, so the count is meaningless — and a
               zero would read as "this agent has never done anything", which is a different claim. */
            <em>unknown — no inference store is wired on this deployment</em>
          )}
        </p>
      </Section>

      <Tabs
        tabs={[
          {
            id: "axes",
            label: "Axes",
            content: (
              <Section title="Every axis, and whether it is in effect" flush>
                <DataTable
                  caption="Each of the six authorable axes plus wiring, which is fixed. An axis that is not in effect always says why."
                  columns={[
                    { label: "Axis" },
                    { label: "Status" },
                    { label: "Value" },
                    { label: "Why not in effect" },
                  ]}
                >
                  {axes.map((a) => (
                    <tr key={a.axis}>
                      <th scope="row">{a.axis}</th>
                      <td>
                        <Pill tone={AXIS_TONE[a.status]}>{AXIS_LABEL[a.status]}</Pill>
                      </td>
                      <td>
                        <code>{a.value}</code>
                      </td>
                      {/* An em-dash ONLY where the status genuinely has nothing to explain. A
                          `not_in_effect` row with an empty reason is the state this column exists to
                          make impossible, and it would be visible here as a blank. */}
                      <td>{a.status === "not_in_effect" ? a.reason : "—"}</td>
                    </tr>
                  ))}
                </DataTable>
              </Section>
            ),
          },
          {
            id: "availability",
            label: "Availability",
            content: (
              <>
                <AvailabilitySection
                  title="Harness strategies"
                  note="Computed from the host services the analysis runner actually supplies — not from a list. An unavailable strategy is shown with what it would need, never hidden, and no neighbouring strategy is offered as a substitute: a critic-loop without a critic IS reflexion, and running it under critic-loop's config_hash would report one strategy as another."
                  items={view.harness_availability ?? []}
                />
                <AvailabilitySection
                  title="Memory strategies"
                  note="Memory is scoped to ONE inference and its session id is the inference id, so it never spans inferences, workflows or tenants. HEROS does not learn across analyses: a repository analysed twice starts cold both times. That is a deliberate scope, not a gap — it is what makes cross-tenant leakage structurally impossible and keeps the three-part cache key honest."
                  items={view.memory_availability ?? []}
                />
              </>
            ),
          },
          {
            id: "versions",
            label: "Versions",
            content: (
              <Section title="Every published definition" flush>
                <DataTable
                  caption="Published definitions, newest first. A definition is immutable and identified by its content, so editing publishes a new one rather than changing this row."
                  columns={[
                    { label: "config_hash" },
                    { label: "Model" },
                    { label: "Credential" },
                    { label: "Rehearsal" },
                    { label: "Serving" },
                  ]}
                >
                  {versions.map((v) => (
                    <tr key={v.config_hash}>
                      <th scope="row">
                        <code>{v.display}</code>
                      </th>
                      <td>{v.model_ref}</td>
                      {/* A PROVIDER NAME. There is no key here, no field that could hold one, and no
                          column in the store one could occupy. */}
                      <td>{v.credential_ref}</td>
                      <td>
                        <Pill
                          tone={
                            v.rehearsal_state === "passed"
                              ? "ok"
                              : v.rehearsal_state === "failed"
                                ? "danger"
                                : "neutral"
                          }
                        >
                          {v.rehearsal_state}
                        </Pill>
                      </td>
                      {/* 🔴 From the platform's `active` field alone. Never derived from recency, and
                          never from `rehearsal_state === "passed"` — a definition that passed and was
                          not activated is serving nothing. */}
                      <td>{v.active ? <Pill tone="ok">serving</Pill> : "—"}</td>
                    </tr>
                  ))}
                </DataTable>
              </Section>
            ),
          },
          {
            id: "instruction",
            label: "Instruction",
            content: (
              <Section title="The agent's instruction" flush>
                <p>
                  A definition binds its instruction by <code>prompt_ref</code>, and publishing one is
                  refused if that ref does not resolve — a definition that cannot render its instruction
                  can be neither measured nor served. Publish the text here first, then bind the ref it
                  returns on the definition&apos;s prompt axis.
                </p>
                <p>
                  This is the <strong>platform&apos;s own</strong> instruction and belongs to no tenant.
                  It is stored outside every tenant namespace, so it can never collide with, or be
                  enumerated by, a customer&apos;s prompts.
                </p>
                <p>
                  Versions are <strong>immutable and content-addressed</strong>: editing publishes a new
                  version and leaves the previous one resolvable, and publishing identical text twice
                  returns the ref that already existed rather than making a second version. Publishing
                  changes nothing on its own — the new text reaches customers only once a definition
                  binds it <em>and</em> that definition passes the activation gate.
                </p>
                {canAdmin ? (
                  <ActionForm
                    title="Publish the instruction"
                    hint="Returns the prompt_ref to bind on a definition. Nothing is served until a definition binds it and passes its rehearsal."
                    submitLabel="Publish instruction"
                    actionName="agent.publish_prompt"
                    action={publishPlatformPrompt}
                  >
                    <label htmlFor="prompt-name">Name</label>
                    <p className="hint">
                      How you find this instruction again, and what its versions line up under. Not
                      shown to customers.
                    </p>
                    <input
                      id="prompt-name"
                      name="name"
                      type="text"
                      autoComplete="off"
                      required
                    />
                    <label htmlFor="prompt-body">Instruction</label>
                    <p className="hint">
                      Stored exactly as typed — leading and trailing whitespace included, because the
                      text the agent is given is the text you wrote.
                    </p>
                    <textarea id="prompt-body" name="body" rows={12} required />
                  </ActionForm>
                ) : (
                  <DeniedState
                    capability="agent.admin"
                    description="Publish the platform analysis agent's instruction"
                    heldBy={holdersOf(identity, "agent.admin")}
                  />
                )}
              </Section>
            ),
          },
          {
            id: "rehearsal",
            label: "Rehearsal",
            content: (
              <Section title="The gate's verdict" flush>
                <p>
                  A published definition is inactive until it runs against the pinned calibration
                  repositories and meets the floor <strong>on every fixture individually</strong>. The
                  mean is reported; the gate reads the minimum — because a mean is exactly the aggregate
                  that hides a per-repository catastrophe, and an agent that is excellent on four
                  languages and connects everything it sees on the fifth passes it.
                </p>
                <p>
                  Newest definition: <Pill tone={view.rehearsal_state === "passed" ? "ok" : "neutral"}>{view.rehearsal_state || "not run"}</Pill>
                </p>
                {view.rehearsal_report ? (
                  /* `mono` is the stylesheet's existing preformatted treatment. An invented `.report`
                     class was caught here by the class fence — the console has one visual language and
                     a new class is a second one, which is how a design forks a literal at a time. */
                  <pre className="mono">{view.rehearsal_report}</pre>
                ) : (
                  <p>
                    <em>
                      No report is stored for it. A definition that has not been rehearsed has no
                      numbers — which is different from having bad ones.
                    </em>
                  </p>
                )}
              </Section>
            ),
          },
        ]}
      />
    </>
  );
}

/**
 * One availability list.
 *
 * 🚫 It renders the UNAVAILABLE entries too, with the service each would need. That is the whole
 * decision: a hidden option is indistinguishable from one that does not exist, so an operator looking
 * for `react-loop` would conclude this platform has no such thing rather than that it needs a tool
 * executor.
 */
function AvailabilitySection({
  title,
  note,
  items,
}: {
  title: string;
  note: string;
  items: Availability[];
}) {
  return (
    <Section title={title} flush>
      <p>{note}</p>
      <DataTable
        caption="Each strategy, whether this runner can execute it, and the host service it would need."
        columns={[{ label: "Strategy" }, { label: "Available" }, { label: "Needs" }, { label: "Why not" }]}
      >
        {items.map((a) => (
          <tr key={a.name}>
            <th scope="row">
              {a.name}
              {a.second_spend_line ? (
                /* 🔴 A second model is a SECOND COST and a SECOND credential. Said on the row rather
                   than in a footnote, so a dropdown cannot quietly double the cost of an analysis. */
                <>
                  {" "}
                  <Pill tone="warn">second spend line</Pill>
                </>
              ) : null}
            </th>
            <td>
              <Pill tone={a.available ? "ok" : "neutral"}>{a.available ? "available" : "unavailable"}</Pill>
            </td>
            <td>{a.needs ? a.needs : "no host service"}</td>
            <td>{a.reason ? a.reason : "—"}</td>
          </tr>
        ))}
      </DataTable>
    </Section>
  );
}
