import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Banner, DataTable } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import { AxisProjectionPanel } from "@/components/axisProjection";
import { loadProjection } from "@/lib/projection";
import {
  ENVELOPE_FIELDS,
  ENVELOPE_BOUNDARY,
  TURN_CEILING_MAX,
  SANDBOX_CONCURRENCY_CEILING,
} from "./envelope";

/**
 * The HARNESS surface, re-scoped by P34 to the EXECUTION ENVELOPE (ADR-014, FR5).
 *
 * # What this page stopped being
 *
 * Until this change it described a scaffold that was two things at once — its own doc comment said
 * "how many turns it runs and in what control loop" — so an operator tightening a spend ceiling and an
 * engineer changing a reflection prompt edited the same axis and produced the same class of change,
 * with nothing to tell them apart.
 *
 * The control loop moved to `/app/loop`. Every item that went with it has a named destination in
 * `openspec/changes/p34-harness-loop-graph-split/frontend-inventory.md`; nothing was dropped.
 *
 * # What makes this page unlike its three siblings
 *
 * 🔴 It is the first axis surface whose answer is "this is never written into your source, and it is
 * enforced anyway." Model, prompt, context, memory, loop and graph all end in a diff or in a refusal
 * that names a missing rewriter. This one ends in neither: an envelope is a fact about how a node is
 * DEPLOYED, so there is no call site for it to live at — and it still decides what the node may reach,
 * spend and overlap.
 *
 * That makes the page's job unusual and narrow: say what each field bounds, say WHERE it bites, and
 * make it impossible to read "refused at every call site" as "ignored". A reader who drew that
 * conclusion would be wrong about their own blast radius, which is the one misreading this surface
 * cannot afford.
 *
 * # Why tabs
 *
 * Viewport-first (NFR17), and the same three-plus-projection shape every other axis page uses. No
 * improvised structure: `PageFrame` + `Tabs` + `DataTable` + `AxisProjectionPanel`, as on `/app/loop`
 * and `/app/graph`.
 */
export const dynamic = "force-dynamic";

/** What separates this axis from the one it was split out of, in the reader's own terms. */
const SPLIT_ROWS = [
  {
    axis: "Loop",
    who: "The engineer authoring the change",
    verb: "CHOOSES",
    example: "“stop after four turns; reflect between them”",
    where: "/app/loop",
  },
  {
    axis: "Harness",
    who: "The operator who owns the deployment",
    verb: "IMPOSES",
    example: "“never spend more than a dollar and never reach the network”",
    where: "this page",
  },
];

function EnvelopeTab() {
  return (
    <div className="flex flex-col gap-6">
      <Section title="What an envelope declares">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Nine fields, three of them required. The three that are required are each a{" "}
          <strong>blast-radius statement</strong> — where this node may reach, the most turns any loop
          inside it may take, and the most it may spend — and the registry refuses an envelope that
          omits one. There is no default for those, deliberately: an omitted ceiling reads as
          “unbounded” to a person and has to be read as <em>some</em> number by the code, and those two
          readings differing is how a policy stops being a policy.
        </p>
        <DataTable
          caption="Each envelope field, what it bounds, and where it is enforced"
          columns={[
            { key: "name", label: "Field" },
            { key: "required", label: "Required" },
            { key: "bounds", label: "What it bounds" },
            { key: "enforced", label: "Enforced at" },
          ]}
        >
          <tbody>
            {ENVELOPE_FIELDS.map((f) => (
              <tr key={f.name}>
                <td className="align-top">
                  <div className="mono text-sm">{f.name}</div>
                  <div className="hint">{f.label}</div>
                </td>
                <td className="align-top">
                  <Chip tone={f.required ? "halt" : "info"} title="whether the registry refuses an envelope without it">
                    {f.required ? "required" : "optional"}
                  </Chip>
                </td>
                <td className="text-sm align-top text-muted-foreground">{f.bounds}</td>
                <td className="text-sm text-muted-foreground">{f.enforcedAt}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </Section>

      <Section title="The ceiling is imposed; the value is chosen">
        <DataTable
          caption="Who decides what, and where each decision is made"
          columns={[
            { key: "axis", label: "Axis" },
            { key: "who", label: "Who decides" },
            { key: "verb", label: "" },
            { key: "example", label: "The sentence it answers" },
          ]}
        >
          <tbody>
            {SPLIT_ROWS.map((r) => (
              <tr key={r.axis}>
                <td className="text-sm align-top font-medium text-foreground">
                  {r.where === "this page" ? r.axis : <a href={r.where}>{r.axis}</a>}
                </td>
                <td className="text-sm align-top text-muted-foreground">{r.who}</td>
                <td className="mono text-sm align-top">{r.verb}</td>
                <td className="text-sm text-muted-foreground">{r.example}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
        <Banner tone="info" title="Why the two numbers cannot live on one axis">
          <p>
            A loop&apos;s <span className="mono">max_turns</span> is checked against this
            envelope&apos;s <span className="mono">turn_ceiling</span> when the configuration resolves.
            If it is higher, the configuration is <strong>refused</strong> — naming both numbers, never
            quietly clamped, because clamping would run a different configuration than the one recorded.
          </p>
          <p>
            Both numbers are named because they are two different requests. Lowering{" "}
            <span className="mono">max_turns</span> is yours; raising{" "}
            <span className="mono">turn_ceiling</span> is a question for whoever owns this envelope. A
            refusal that said only “too many turns” would leave you unable to tell which.
          </p>
          <p>
            🔴 And raising a ceiling changes <strong>no</strong> loop configuration. The two live in
            separate sealed entries, so a policy change cannot re-hash the configurations underneath it —
            which is what stops every measurement taken under the old ceiling from becoming unreachable.
          </p>
        </Banner>
      </Section>
    </div>
  );
}

function EnforcementTab() {
  return (
    <div className="flex flex-col gap-6">
      {/*
        🔴 P34 §7.3 — the axis renders read-only WITH ITS REASON. This is the first surface where that
        is the ordinary case rather than the exception, so the reason leads rather than being a footnote
        under a picker that cannot produce a diff. HEROS's own axis editor states the rule: a hidden
        axis is indistinguishable from one that does not exist.
      */}
      <Section title="What this platform writes into your source">
        <Banner tone="warn" title="Nothing — in any language, permanently. That is not the same as unenforced.">
          <p>
            An execution envelope is a property of how a node is <strong>deployed</strong>: where it may
            reach on the network, what it may spend, how long it may run, how many of its steps may
            overlap, which guardrail and approval gate it answers to. None of that is written at a call
            site in any language, so there is no rewriter that could ever emit it — and this is the one
            axis where “no materializer” is a permanent fact rather than work the platform owes you.
          </p>
          <p>
            <strong>Refused is not ignored.</strong> Read the previous table&apos;s last column: the
            turn ceiling and the host services are checked when your configuration <em>resolves</em>,
            before any diff or worktree or provider call exists. The spend ceiling is checked before
            each call. The concurrency limit is checked twice — once at resolve, and again by the
            sandbox at execution.
          </p>
        </Banner>
      </Section>

      <Section title="Why the concurrency limit is checked twice">
        <Banner tone="info" title="A limit with one entrance is a limit with a way around it">
          <p>
            The first check refuses a concurrent group wider than this envelope allows when the
            configuration resolves. That gate is early and legible, and it is the one you see.
          </p>
          <p>
            It is also the one that is <strong>bypassed</strong> by every path that reaches an executor
            without resolving a spec. So the sandbox enforces its own limit and does not trust the
            number it is handed: at most <span className="mono">{SANDBOX_CONCURRENCY_CEILING}</span>{" "}
            overlapping steps per group, whatever the spec declared.
          </p>
          <p>
            When the sandbox has to narrow something, it says so on its health endpoint rather than in a
            log line — because “the early gate is not running” is invisible in every aggregate: nothing
            errors, nothing retries, and the work simply runs narrower than it asked to.
          </p>
        </Banner>
      </Section>

      <Section title="When the loop needs something the envelope does not grant">
        <Banner tone="warn" title="Refused when the configuration resolves, not when the run reaches the node">
          <p>
            Three control loops need a second actor: <span className="mono">react-loop</span> needs a
            tool executor, <span className="mono">plan-execute</span> a planner, and{" "}
            <span className="mono">critic-loop</span> a critic. If this envelope grants none of them,
            binding that loop is refused — naming the loop <em>and</em> the missing service.
          </p>
          <p>
            🚫 It is never degraded to a loop that needs no second actor. A{" "}
            <span className="mono">critic-loop</span> without a critic <em>is</em>{" "}
            <span className="mono">reflexion</span>, and running it under critic-loop&apos;s
            configuration hash would report one strategy&apos;s result as another&apos;s.
          </p>
          <p>
            The refusal moved <strong>left</strong> in this change. It used to arrive when a run reached
            the node — after the change was already generated and applied. Now it is a preflight answer.
          </p>
        </Banner>
      </Section>
    </div>
  );
}

export default async function HarnessPage() {
  // 🔴 P29 §5.10 — `coverage × your nodes`, BESIDE the explanation and never instead of it.
  const projection = await loadProjection();
  await requireSession();

  const tabs: TabItem[] = [
    { id: "envelope", label: "What an envelope declares", content: <EnvelopeTab /> },
    { id: "enforcement", label: "Where it is enforced", content: <EnforcementTab /> },
    {
      id: "your-nodes",
      label: "Your nodes",
      content: <AxisProjectionPanel axis="harness" outcome={projection} />,
    },
  ];

  return (
    <PageFrame
      eyebrow="Harness"
      title="What this node is allowed to do, and inside what walls"
      lede={
        <>
          The execution envelope: where a node may reach on the network, the most it may spend, the most
          turns any loop inside it may take, how many of its steps may overlap, and which guardrail it
          answers to. It <strong>imposes</strong>; the loop <strong>chooses</strong> within it.
        </>
      }
    >
      <p className="hint">
        This page described a node&apos;s control loop until the axis was split. That moved to{" "}
        <a href="/app/loop">Loop</a>, where you pick a strategy and a turn count; the ceiling that turn
        count is checked against lives here. Nothing was dropped in the re-cut — every item has a named
        destination.
        {ENVELOPE_BOUNDARY.materializesAnywhere ? null : (
          <> This axis writes nothing into your source, in any language; the second tab says why.</>
        )}
      </p>
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}
