import { requireIdentity, hasCapability, holdersOf } from "@/lib/session";
import { adminFetch, AdminApiError } from "@/lib/adminApi";
import { OperatorShell } from "@/components/shell";
import { DegradedState, DeniedState, NotMountedState, Pill } from "@/components/states";
import { DataTable, Drawer, Num, PageFrame, Section } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { ActionForm } from "@/components/actionForm";
import {
  publishPlatformPrompt,
  publishAgentDefinition,
  activateAgentDefinition,
  rollbackAgentDefinition,
} from "@/lib/actions";
import type {
  AgentOverview,
  AgentAxisRow,
  AgentNodeRow,
  Availability,
  AdminIdentity,
} from "@/lib/types";

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
 *  3. **Hide the `graph` axis.** It is rendered in BOTH states — read-only WITH its reason when one
 *     node is declared, editable when more than one is. Same argument, and P36 task 6.3 adds a second
 *     half: *do not delete the reason*. Multi-node definitions existing does not make "there is no
 *     second node to order it against" untrue for the default shape, which is still one node.
 *
 * # 🔴 P36 — the node dimension, and the rule it is built to
 *
 * The definition is a GRAPH now: nine axes over N nodes. The rule for the density that creates is
 * **collapse, do not omit** — every node's eight rows are rendered, grouped under a node header, and a
 * long definition is scrolled rather than truncated. A hidden node is indistinguishable from one that
 * does not exist, and the failure it produces is an operator reading a configuration that is not the
 * one running.
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

/** groupByNode splits the flat axis list into per-node groups plus the definition-level rows.
 *
 * 🔴 The ORDER the platform sent is preserved. It is the definition's own ordering — the sequence the
 * runner walks and a replay visits — and re-sorting it here would render a graph in an order nothing
 * executes.
 */
function groupByNode(axes: AgentAxisRow[]): { nodeID: string; rows: AgentAxisRow[] }[] {
  const groups: { nodeID: string; rows: AgentAxisRow[] }[] = [];
  for (const row of axes) {
    const last = groups[groups.length - 1];
    if (last && last.nodeID === row.node_id) {
      last.rows.push(row);
      continue;
    }
    groups.push({ nodeID: row.node_id, rows: [row] });
  }
  return groups;
}

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
            It is described as an ordinary <strong>Variant Spec</strong> over the same{" "}
            <strong>nine axes</strong> the product sells — including its <strong>topology</strong> — so
            the platform&rsquo;s own eval harness can measure it. It does not serve anything until it
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

/**
 * AxisRow is one axis line.
 *
 * Extracted because P36 renders the same row in two places — under a node, and once at the definition
 * level for `graph` — and a second copy is where the `not_in_effect` reason quietly stops being
 * rendered in one of them.
 */
function AxisRow({ row }: { row: AgentAxisRow }) {
  return (
    <tr>
      <th scope="row">{row.axis}</th>
      <td>
        <Pill tone={AXIS_TONE[row.status]}>{AXIS_LABEL[row.status]}</Pill>
      </td>
      <td>
        <code>{row.value}</code>
      </td>
      {/* An em-dash ONLY where the status genuinely has nothing to explain. A `not_in_effect` row with
          an empty reason is the state this column exists to make impossible, and it would be visible
          here as a blank. */}
      <td>{row.status === "not_in_effect" ? row.reason : "—"}</td>
    </tr>
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
  const nodes = view.nodes ?? [];

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
              serving <code>{view.serving_config_hash.slice(0, 12)}</code>{" "}
              {/* 🔴 The SHAPE, beside the hash. After P36 "which definition is serving" has a second
                  half: a three-node graph and a single node are different products, and the header
                  said nothing about which was running. */}
              <Pill tone="neutral">
                {nodes.length === 1 ? "1 node" : `${nodes.length} nodes`}
              </Pill>
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
            label: "Configuration",
            content: (
              <Section title="Every axis of every node, and whether it is in effect" flush>
                <p className="hint">
                  Eight axes per node, plus <code>graph</code> — the topology — declared once for the
                  definition, because it is a property <em>between</em> nodes. Every node&rsquo;s rows
                  are rendered: a hidden axis is indistinguishable from one that does not exist, and so
                  is a hidden node.
                </p>
                {groupByNode(axes).map((group) =>
                  group.nodeID === "" ? (
                    /* The definition-level rows — `graph`. Rendered as its own section rather than
                       appended to the last node's table, because attaching it to a node would say that
                       node owns the topology. */
                    <DataTable
                      key="definition-level"
                      caption="The definition-level axis: the topology between the nodes above."
                      columns={[
                        { label: "Axis" },
                        { label: "Status" },
                        { label: "Value" },
                        { label: "Why not in effect" },
                      ]}
                    >
                      {group.rows.map((a) => (
                        <AxisRow key={`def-${a.axis}`} row={a} />
                      ))}
                    </DataTable>
                  ) : (
                    <DataTable
                      key={group.nodeID}
                      caption={`Node ${group.nodeID}: its eight axes. An axis that is not in effect always says why.`}
                      columns={[
                        { label: "Axis" },
                        { label: "Status" },
                        { label: "Value" },
                        { label: "Why not in effect" },
                      ]}
                    >
                      <tr>
                        {/* The node header row. `colSpan` rather than a fifth column repeating the
                            same id on every line — the id is a heading here, not a value. */}
                        <th scope="rowgroup" colSpan={4}>
                          node <code>{group.nodeID}</code>
                        </th>
                      </tr>
                      {group.rows.map((a) => (
                        <AxisRow key={`${group.nodeID}-${a.axis}`} row={a} />
                      ))}
                    </DataTable>
                  ),
                )}
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
                  title="Loop strategies"
                  note="The ITERATION POLICY a node runs under — which control loop, and what stops it. It became authorable in P36, so its availability is shown on exactly the same terms as the other two: an unavailable strategy is rendered with the host service it would need rather than hidden, because an operator who cannot see `react-loop` would conclude this platform has no such thing instead of that it needs a tool executor. A loop whose host service this deployment does not supply is refused at PUBLISH rather than at run — by the operator who chose it, not by whoever an analysis reaches."
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
                    { label: "Shape" },
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
                      {/* 🔴 The SHAPE. Two versions differing only in topology have the same model and
                          the same credential, and would otherwise be indistinguishable in this list. */}
                      <td>{v.nodes === 1 ? "1 node" : `${v.nodes} nodes`}</td>
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
                <RollbackControl view={view} canAdmin={canAdmin} identity={identity} />
              </Section>
            ),
          },
          {
            id: "nodes",
            label: "Nodes",
            content: <NodesSection view={view} />,
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
            id: "publish",
            label: "Publish",
            content: (
              <Section title="Publish a definition" flush>
                <p>
                  A definition is <strong>identified by its content</strong>: publishing composes the
                  axes below into one immutable version and names it by its <code>config_hash</code>.
                  Editing publishes a new version rather than changing an existing one, and republishing
                  identical axes creates nothing.
                </p>
                <p>
                  <strong>Publishing serves nothing.</strong> A new version lands <em>pending</em> and
                  analyses no customer&apos;s source until it passes the calibration set{" "}
                  <em>on every fixture individually</em> and is activated. Those are separate acts on
                  purpose — this control cannot change what is running.
                </p>
                <p>
                  The <code>prompt</code> axis takes the ref returned by the Instruction tab, and{" "}
                  <code>model</code> takes a model version id from the platform registry. Both must
                  resolve or the publish is refused — a definition that cannot render its instruction or
                  reach its model can be neither measured nor served.
                </p>
                <p>
                  A definition declares <strong>one or more nodes</strong>. One is the default and needs
                  no topology — an operator who wants what they have today does not have to author a
                  graph to keep it. Fill the second node&rsquo;s fieldset to make it a graph, and the{" "}
                  <code>graph</code> axis below becomes editable.
                </p>
                {canAdmin ? (
                  <ActionForm
                    title="Publish definition"
                    hint="Creates a pending version. Activation is a separate, gated act."
                    submitLabel="Publish definition"
                    actionName="agent.publish"
                    action={publishAgentDefinition}
                  >
                    {/* 🔴 The node fieldsets are RENDERED, all of them, rather than added by a script.
                        This console has no client-side state by design, and a "+ add node" button that
                        needed one would either not work or would smuggle a second rendering model into
                        a server-rendered page. Three empty fieldsets is the honest version: a node
                        whose every field is blank is DROPPED before submission, so leaving them empty
                        publishes the single-node definition it always did. */}
                    <NodeFieldset index={0} required />
                    <NodeFieldset index={1} />
                    <NodeFieldset index={2} />

                    <fieldset>
                      <legend>graph — the topology (definition-level)</legend>
                      {/* 🔴 THE REFUSAL TEXT IS NOT DELETED. It is rendered here, always, because it
                          is still TRUE whenever one node is declared — which is still the default.
                          P36 narrowed the rule to the cases where its premise holds; it did not
                          retire it. */}
                      <p className="hint">
                        Leave these empty for a single-node definition. There is then no second node to
                        order it against, so a topology would hash a configuration nothing can execute
                        — and it is <strong>refused at publish</strong> rather than accepted and
                        ignored. Fill a second node above first.
                      </p>

                      <label htmlFor="topo-order">order (comma-separated node ids)</label>
                      <p className="hint">
                        The sequence the runner walks and a replay visits. Concurrency is declared{" "}
                        <em>over</em> this ordering, never instead of it, so it still lists every node.
                      </p>
                      <input id="topo-order" name="topology.order" type="text" autoComplete="off" />

                      <label htmlFor="topo-edges">edges (JSON array)</label>
                      <p className="hint">
                        <code>
                          {`[{"from_node_id":"a","to_node_id":"b","kind":"data"}]`}
                        </code>
                        . <code>kind</code> is one of <code>data</code>, <code>control</code> or{" "}
                        <code>predicate</code>; a predicate edge also carries a{" "}
                        <code>predicate</code>, and it must name something the producing node actually
                        reports.
                      </p>
                      <textarea id="topo-edges" name="topology.edges" rows={3} />

                      <label htmlFor="topo-groups">graph_groups (JSON array)</label>
                      <p className="hint">
                        <code>
                          {`[{"nodes":["a","b"],"concurrent":true,"merge":{"into":"c","strategy":"namespaced","on_node_failure":"fail-fast"}}]`}
                        </code>
                        . A fan-in with no <code>merge</code> is refused — first-result-wins,
                        concatenate and last-writer are all semantic choices about your program, and
                        none is more obviously right than the others.
                      </p>
                      <textarea id="topo-groups" name="topology.graph_groups" rows={3} />
                    </fieldset>
                  </ActionForm>
                ) : (
                  <DeniedState
                    capability="agent.admin"
                    description="Publish the platform analysis agent's definition"
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
                {canAdmin ? (
                  <ActionForm
                    title="Activate a definition"
                    hint="Runs the calibration set against a live model on this deployment's own provider credential, and spends at the press. Only a definition that meets the floor on every fixture is served."
                    submitLabel="Run the gate and activate"
                    actionName="agent.activate"
                    action={activateAgentDefinition}
                    danger
                  >
                    <label htmlFor="activate-hash">config_hash</label>
                    <p className="hint">
                      The hash of the published definition to measure, from the Versions tab.
                    </p>
                    <input id="activate-hash" name="config_hash" type="text" autoComplete="off" required />
                  </ActionForm>
                ) : (
                  <DeniedState
                    capability="agent.admin"
                    description="Activate a published agent definition"
                    heldBy={holdersOf(identity, "agent.admin")}
                  />
                )}
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
 * NodeFieldset is ONE node's eight axes, in the publish form.
 *
 * # 🔴 Why every field is repeated per node rather than shared
 *
 * Each node binds its own prompt, model and credential (PRD §14 Q1, answered per-node): a node binds a
 * model, a model is served by exactly one vendor, and a definition-level credential would force every
 * node onto one vendor — which is a routing decision made by a field that is not about routing, and it
 * removes the main reason to want a graph at all.
 *
 * # 🔴 Why `graph` is not here
 *
 * Topology is a property BETWEEN nodes, so it is declared once for the definition. The platform refuses
 * it inside a node's axis map by name rather than hoisting it, because silently moving it would let an
 * operator believe one node owns the graph.
 *
 * # Why a blank fieldset is not an empty node
 *
 * A node whose every field is blank is dropped before submission. An operator who filled one fieldset
 * and left two empty publishes a single-node definition — the default shape — rather than a definition
 * with two nodes that bind nothing, which the platform would refuse with a message about the prompt
 * axis rather than about the empty node.
 */
function NodeFieldset({ index, required }: { index: number; required?: boolean }) {
  const p = `node.${index}.`;
  const id = (name: string) => `n${index}-${name}`;
  return (
    <fieldset>
      <legend>
        {index === 0 ? "node 1 — the definition's first call site" : `node ${index + 1} (optional)`}
      </legend>
      {index > 0 ? (
        <p className="hint">
          Leave every field blank to publish without this node. Filling it makes the definition a graph:
          the <code>graph</code> axis below becomes editable, and the definition must then declare an
          ordering that contains every node.
        </p>
      ) : null}

      <label htmlFor={id("node_id")}>node_id {index === 0 ? "(optional)" : "(required for a graph)"}</label>
      <p className="hint">
        {index === 0
          ? "Leave blank for the default. A single-node definition with the default id serialises and hashes exactly as it did before topology existed, which is what keeps every pinned result reachable."
          : "How edges and the ordering name this node."}
      </p>
      <input id={id("node_id")} name={p + "node_id"} type="text" autoComplete="off" />

      <label htmlFor={id("prompt")}>prompt_ref {required ? "(required)" : ""}</label>
      <p className="hint">The version id returned by the Instruction tab.</p>
      <input id={id("prompt")} name={p + "prompt"} type="text" autoComplete="off" required={required} />

      <label htmlFor={id("model")}>model_ref {required ? "(required)" : ""}</label>
      <p className="hint">A model version id from the platform registry — not a vendor model name.</p>
      <input id={id("model")} name={p + "model"} type="text" autoComplete="off" required={required} />

      <label htmlFor={id("credential_ref")}>credential_ref {required ? "(required)" : ""}</label>
      <p className="hint">
        A provider <strong>name</strong> such as <code>anthropic</code> or <code>openai</code> — never a
        key. It must match the provider that serves this node&rsquo;s model, or the run authenticates
        against one vendor and calls another.
      </p>
      <input
        id={id("credential_ref")}
        name={p + "credential_ref"}
        type="text"
        autoComplete="off"
        required={required}
      />

      <label htmlFor={id("context")}>context {required ? "(required)" : ""}</label>
      <input id={id("context")} name={p + "context"} type="text" autoComplete="off" required={required} />

      <label htmlFor={id("harness")}>harness {required ? "(required)" : ""}</label>
      <p className="hint">
        The execution ENVELOPE — the imposed policy: ceilings, host services, sandbox posture.
      </p>
      <input id={id("harness")} name={p + "harness"} type="text" autoComplete="off" required={required} />

      <label htmlFor={id("loop")}>loop (optional)</label>
      <p className="hint">
        The ITERATION POLICY this node chooses — which control loop runs and what stops it. A loop whose
        host service this deployment does not supply, or whose turn count exceeds the envelope&rsquo;s
        ceiling, is refused at publish with both numbers named.
      </p>
      <input id={id("loop")} name={p + "loop"} type="text" autoComplete="off" />

      <label htmlFor={id("memory")}>memory (optional)</label>
      <input id={id("memory")} name={p + "memory"} type="text" autoComplete="off" />

      <label htmlFor={id("skill_refs")}>skill_refs (optional, comma-separated)</label>
      <input id={id("skill_refs")} name={p + "skill_refs"} type="text" autoComplete="off" />

      <label htmlFor={id("tool_names")}>tool_names (optional, comma-separated)</label>
      <input id={id("tool_names")} name={p + "tool_names"} type="text" autoComplete="off" />
    </fieldset>
  );
}

/**
 * RollbackControl returns a previously serving definition to service (P36 task 5.5).
 *
 * # 🔴 Why the control exists rather than the capability alone
 *
 * Rollback is one act — activating a version that already exists. Without a control it is a route
 * nothing can reach, and this console has shipped three of those, each found by a person going to do
 * the thing and discovering there was no thing to press. Discovering the fourth during an incident is
 * the worst version of that.
 *
 * # 🔴 Why the list of targets comes from the platform
 *
 * `rollback_target` is decided by the backend — passed, and not serving. A console that worked that out
 * itself would eventually offer a button the backend refuses, which is the "offered and then refused"
 * failure the surfaces map exists to prevent.
 *
 * 🚫 The form takes a HASH and nothing else. It never re-authors: retyping a configuration under
 * pressure produces a different `config_hash` on any transcription error, which is a third
 * configuration nobody has measured, activated in place of the one known to work.
 */
function RollbackControl({
  view,
  canAdmin,
  identity,
}: {
  view: AgentOverview;
  canAdmin: boolean;
  identity: AdminIdentity;
}) {
  const targets = (view.versions ?? []).filter((v) => v.rollback_target);
  return (
    <Drawer summary="Roll back to a previous definition" id="rollback">
      <p>
        Rolling back is <strong>one act</strong>: activating a version that is already published. It
        creates nothing, re-authors nothing, and does not re-run the calibration set — the version
        already passed, and that verdict is on its immutable row.
      </p>
      <p>
        It also does not re-run pinned inferences. A configuration change is a{" "}
        <strong>pinning event</strong>, not a re-inference: results produced under the definition you
        are leaving stay readable and stay attributed to it.
      </p>
      {targets.length === 0 ? (
        <p className="hint">
          No previous definition is available to return to. A rollback target is one that{" "}
          <strong>passed its rehearsal and is not serving</strong> — a pending version was never
          measured, so it is not a state to go back to.
        </p>
      ) : (
        <DataTable
          caption="Definitions that passed and are not serving — the ones a rollback can return to."
          columns={[{ label: "config_hash" }, { label: "Shape" }, { label: "Model" }]}
        >
          {targets.map((v) => (
            <tr key={v.config_hash}>
              <th scope="row">
                <code>{v.display}</code>
              </th>
              <td>{v.nodes === 1 ? "1 node" : `${v.nodes} nodes`}</td>
              <td>{v.model_ref}</td>
            </tr>
          ))}
        </DataTable>
      )}
      {canAdmin ? (
        <ActionForm
          title="Roll back"
          hint="Activates a definition that is already published. Nothing is created and nothing is re-authored."
          submitLabel="Roll back to this definition"
          actionName="agent.rollback"
          action={rollbackAgentDefinition}
          danger
        >
          <label htmlFor="rollback-hash">config_hash</label>
          <p className="hint">The hash of the definition to return to, from the table above.</p>
          <input id="rollback-hash" name="config_hash" type="text" autoComplete="off" required />
        </ActionForm>
      ) : (
        <DeniedState
          capability="agent.admin"
          description="Roll back the platform analysis agent to a previous definition"
          heldBy={holdersOf(identity, "agent.admin")}
        />
      )}
    </Drawer>
  );
}

/**
 * NodesSection is the per-node view: which node produced what, and what each one costs.
 *
 * # 🔴 Why per node, and why an aggregate is not an answer
 *
 * "An aggregate over a graph says the agent is slow, not which node is." A five-node definition whose
 * latency doubled did so at ONE node, and every remediation — a cheaper model, a tighter loop, a
 * removed node — is a decision about which one.
 *
 * # 🔴 Zero is not a measurement here
 *
 * A node with no inferences renders "not yet run" rather than a row of zeros: zero latency reads as
 * instantaneous, and zero failures reads as reliable. Both are claims about a node nobody has observed.
 *
 * 🚫 These numbers are OPERATOR-SIDE ONLY (PRD §14 Q2). A customer sees the evidence, not our
 * topology — a node id beside a finding invites a conversation about our implementation instead of
 * about their code.
 */
function NodesSection({ view }: { view: AgentOverview }) {
  const nodes = view.nodes ?? [];
  return (
    <Section title="Each node, and what it has done" flush>
      <p>
        Per node, because an aggregate over a graph says the agent is slow rather than{" "}
        <strong>which node</strong> is — and which node is the only form of the answer anybody can act
        on.
      </p>
      <p className="hint">
        These counters are held in this process and reset when it restarts. A node with no inferences
        and a freshly restarted process look the same from here, which is why nothing below is rendered
        as a zero.
      </p>
      {nodes.length === 0 ? (
        <p>
          <em>
            No definition is published, so there are no nodes to describe. That is different from a
            definition whose nodes have not run.
          </em>
        </p>
      ) : (
        <DataTable
          caption="Each node of the definition being described, its bindings, and what it has produced and spent."
          columns={[
            { label: "Node" },
            { label: "Model" },
            { label: "Loop" },
            { label: "Inferences", numeric: true },
            { label: "Tokens", numeric: true },
            { label: "Latency", numeric: true },
            { label: "Failures", numeric: true },
            { label: "Skipped", numeric: true },
          ]}
        >
          {nodes.map((n) => (
            <NodeRow key={n.node_id} node={n} known={view.nodes_known} />
          ))}
        </DataTable>
      )}
    </Section>
  );
}

/** NodeRow renders one node's numbers, or says why there are none. */
function NodeRow({ node, known }: { node: AgentNodeRow; known: boolean }) {
  /* 🔴 UNKNOWN and NOT-YET-RUN are different, and both are different from zero.
   *
   *   unknown      — no per-node source is wired on this deployment. The counters are meaningless.
   *   not yet run  — the source is wired and this node has produced nothing.
   *   a number     — measured.
   *
   * Collapsing any pair of these renders a claim nobody made. */
  const unknown = <span className="hint">unknown — no per-node source is wired</span>;
  const notRun = <span className="hint">not yet run</span>;
  const cell = (value: number, render: () => React.ReactNode) => {
    if (!known) return unknown;
    if (node.inferences === 0 && node.failures === 0 && node.skips === 0) return notRun;
    return render();
  };
  return (
    <tr>
      <th scope="row">
        <code>{node.node_id}</code>
      </th>
      <td>{node.model_ref}</td>
      <td>{node.loop_ref ? <code>{node.loop_ref}</code> : <span className="hint">single turn</span>}</td>
      <td className="num">{cell(node.inferences, () => <Num value={node.inferences} />)}</td>
      <td className="num">
        {cell(node.tokens_in, () => (
          <Num value={node.tokens_in + node.tokens_out} kind="quantity" />
        ))}
      </td>
      <td className="num">
        {cell(node.latency_ms, () => (
          <>
            <Num value={node.latency_ms} kind="quantity" /> ms
          </>
        ))}
      </td>
      <td className="num">{cell(node.failures, () => <Num value={node.failures} />)}</td>
      {/* Skipped is its own column and never folded into failures. A node a predicate routed around
          did not fail — it was not entered — and a definition whose conditional edge never fires is a
          different situation from one whose node keeps erroring. */}
      <td className="num">{cell(node.skips, () => <Num value={node.skips} />)}</td>
    </tr>
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
