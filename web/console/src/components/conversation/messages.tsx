import type { ReactNode } from "react";
import Link from "next/link";
import {
  AlertTriangle,
  ArrowUpRight,
  Ban,
  CircleCheck,
  CircleDashed,
  CircleSlash,
  Clock,
  FileSearch,
  History,
  Info,
  ListChecks,
  MessageSquareText,
  Package,
  Search,
  ShieldQuestion,
  Wifi,
} from "lucide-react";
import { Card, Chip, Stat, Stats } from "@/components/primitives";
import { cx } from "@/lib/cx";
import { integer } from "@/lib/format";
import {
  FAILURE_COPY,
  FINDING_STATE_COPY,
  PHASE_COPY,
  STEP_STATE_COPY,
  STOP_COPY,
  unapprovableCopy,
} from "@/lib/conversationCopy";
import type {
  ApprovalRequestPayload,
  AnswerPayload,
  ConversationFindingState,
  ConversationMessage,
  ConversationStepState,
  FindingPayload,
  PlanPayload,
  ProgressPayload,
  ProposalPayload,
  RefusalPayload,
  ResultPayload,
} from "@/lib/types.generated";

/**
 * messages.tsx is ONE RENDERER PER MESSAGE KIND (task 4.2).
 *
 * # Why there is no `default:` arm anywhere in this file
 *
 * `ConversationKind` is generated from the Go vocabulary (ADR-007). With no default arm, adding a kind
 * in Go and regenerating makes `MessageCard`'s switch non-exhaustive and the **type-check fails** —
 * which is task 4.5's requirement stated as a property of the code rather than as a convention. A
 * `default: return null` would swallow the new kind and render a gap in the transcript that is
 * indistinguishable from a message that was never sent.
 *
 * # Why these reuse the scorecard and proposal shapes rather than inventing a chat aesthetic
 *
 * A conversation beside a console that already has a card vocabulary is the classic place a second
 * design language starts: bubbles, avatars, rounded tails, a different type ramp — none of it decided,
 * all of it "just here". So every card below is `Card` + `Chip` + `Stat` from `primitives.tsx`, and the
 * only new CSS is the transcript's own rail and the finding states. If a shape is needed that
 * `primitives.tsx` does not have, that file changes first, in front of a reviewer.
 *
 * # 🔴 The hazard palette is reserved for hazard (§7.4)
 *
 * A `refusal` may use it. An armed `approval_request` may use it. A `finding` may NOT — including a
 * `refused` finding, which is a state of a measurement rather than a danger. Colouring a finding in the
 * hazard palette teaches a reader to discount the palette, and then it is not available for the two
 * things that need it.
 */

// ── shared bits ──────────────────────────────────────────────────────────────

/** EvidenceLink renders a reference as a link when it addresses a surface, and as a token otherwise. */
function EvidenceLink({ href, label }: { href: string; label: string }) {
  if (!href) return <Chip variant="hash">{label}</Chip>;
  return (
    <Link className="chip font-mono text-primary underline underline-offset-2" href={href}>
      {label}
      <ArrowUpRight className="size-2.5 opacity-60" aria-hidden="true" />
    </Link>
  );
}

/** MessageShell is the frame every card shares: an icon, a kind label, and the body. */
function MessageShell({
  icon,
  kind,
  aside,
  children,
  hazard,
}: {
  icon: ReactNode;
  kind: string;
  aside?: ReactNode;
  children: ReactNode;
  hazard?: boolean;
}) {
  return (
    <Card className={cx("msg", hazard && "msg--hazard")}>
      <div className="msg__head">
        <span className="msg__kind">
          {icon}
          {kind}
        </span>
        {aside ? <span className="msg__aside">{aside}</span> : null}
      </div>
      <div className="msg__body">{children}</div>
    </Card>
  );
}

// ── plan ─────────────────────────────────────────────────────────────────────

/**
 * PlanCard renders the plan as a CHECKLIST THAT FILLS IN, plus the declared budget (tasks 4.11, 4.12).
 *
 * The checklist is the denominator. Without it, "I looked at your workflow" cannot be short — an agent
 * that ran three of eight steps produces prose indistinguishable from one that ran eight, and the reader
 * has nothing to compare against. The four budget numbers are on screen BEFORE the first step runs for
 * the same reason: a limit met without warning is indistinguishable from a bug.
 */
export function PlanCard({
  payload,
  completed,
}: {
  payload: PlanPayload;
  /** completed is the RUN's own record of which steps resolved, read from the stream's state frame. */
  completed: Record<string, ConversationStepState>;
}) {
  const steps = payload.steps ?? [];
  return (
    <MessageShell
      icon={<ListChecks className="size-3.5" aria-hidden="true" />}
      kind="Plan"
      aside={<Chip variant="plan">{payload.intent}</Chip>}
    >
      <ol className="plan">
        {steps.map((step) => {
          const state = completed[step.id];
          return (
            <li className={cx("plan__step", state && `plan__step--${state}`)} key={step.id}>
              <StepMark state={state} />
              <span className="plan__title">{step.title}</span>
              {step.surface ? (
                <Link className="plan__surface" href={step.surface}>
                  {step.surface}
                </Link>
              ) : null}
            </li>
          );
        })}
      </ol>
      <div className="msg__section">
        <p className="hint">
          Declared before the first step ran. A run that reaches one of these stops and says which.
        </p>
        <Stats>
          <Stat dense label="Turn ceiling" value={integer(payload.budget.turn_ceiling)} />
          <Stat dense label="Token budget" value={integer(payload.budget.token_budget)} />
          <Stat dense label="Tool calls" value={integer(payload.budget.tool_call_ceiling)} />
          <Stat dense label="Time" value={integer(payload.budget.wall_clock_seconds)} unit="s" />
        </Stats>
      </div>
    </MessageShell>
  );
}

/**
 * StepMark draws a step's state.
 *
 * 🔴 Four distinct marks, not one mark with four colours (task 4.12). A `skipped` step and a `done` step
 * differing only in hue is unreadable in greyscale, to a colour-blind reader, and to anybody scanning
 * quickly — which is everybody reading a checklist.
 */
function StepMark({ state }: { state?: ConversationStepState }) {
  if (!state) return <CircleDashed className="size-3.5 opacity-40" aria-hidden="true" />;
  const copy = STEP_STATE_COPY[state];
  const icon =
    state === "done" ? (
      <CircleCheck className="size-3.5" aria-hidden="true" />
    ) : state === "refused" ? (
      <Ban className="size-3.5" aria-hidden="true" />
    ) : state === "skipped" ? (
      <CircleSlash className="size-3.5" aria-hidden="true" />
    ) : (
      <ShieldQuestion className="size-3.5" aria-hidden="true" />
    );
  return (
    <span className="plan__mark" title={copy.label}>
      {icon}
      <span className="visually-hidden">{copy.label}</span>
    </span>
  );
}

// ── progress ─────────────────────────────────────────────────────────────────

/**
 * ProgressCard renders the four facts a spinner withholds (PRD §9.1, task 4.11): what it is doing,
 * which phase it is in, how much budget is left, and — through the plan above — how far along it is.
 */
export function ProgressCard({ payload }: { payload: ProgressPayload }) {
  const phase = PHASE_COPY[payload.phase];
  return (
    <div className="progress">
      <span className="progress__phase">{phase.label}</span>
      <span className="progress__detail">{payload.detail || phase.detail}</span>
      <span className="progress__budget">
        {integer(payload.remaining.tokens)} tokens · {integer(payload.remaining.tool_calls)} reads ·{" "}
        {integer(payload.remaining.wall_clock_seconds)}s left
      </span>
    </div>
  );
}

// ── finding ──────────────────────────────────────────────────────────────────

/** FINDING_ICON gives each state its own mark, so the four are distinguishable without colour. */
const FINDING_ICON: Record<ConversationFindingState, ReactNode> = {
  measured: <FileSearch className="size-3.5" aria-hidden="true" />,
  not_measured: <ShieldQuestion className="size-3.5" aria-hidden="true" />,
  refused: <Ban className="size-3.5" aria-hidden="true" />,
  stale: <History className="size-3.5" aria-hidden="true" />,
};

/**
 * FindingCard renders all four states distinctly (task 4.3) and LINKS to the surface that owns the
 * claim rather than restating its numbers (task 4.15, FR25).
 *
 * 🚫 It never renders the hazard palette, including for `refused`. See the file header.
 */
export function FindingCard({ payload }: { payload: FindingPayload }) {
  const copy = FINDING_STATE_COPY[payload.state];
  return (
    <MessageShell
      icon={FINDING_ICON[payload.state]}
      kind="Finding"
      aside={
        <>
          <Chip>{payload.surface}</Chip>
          <span className={`finding__state finding__state--${payload.state}`}>{copy.label}</span>
        </>
      }
    >
      <p className="msg__claim">{payload.claim}</p>

      {payload.state === "not_measured" && payload.missing_input ? (
        <p className="finding__lede">
          {copy.lede} <span className="finding__reason">{payload.missing_input}</span>
        </p>
      ) : null}
      {payload.state === "refused" && payload.cause ? (
        <p className="finding__lede">
          {copy.lede}
          {/* 🔴 The lower layer's own words, VERBATIM and monospaced so it reads as a quotation rather
              than as this surface's sentence. A re-worded refusal is a second, softer statement of a
              safety boundary. */}
          <span className="finding__cause mono">{payload.cause}</span>
        </p>
      ) : null}
      {payload.state === "stale" && payload.source_revision ? (
        <p className="finding__lede">
          {copy.lede} <Chip variant="hash">{payload.source_revision}</Chip>
        </p>
      ) : null}

      <p className="msg__section flex flex-wrap items-center gap-2">
        <span className="hint">Evidence</span>
        <EvidenceLink href={payload.surface_href} label={payload.evidence_ref} />
        {payload.node ? <Chip title="node">{payload.node}</Chip> : null}
        {payload.axis ? <Chip title="axis">{payload.axis}</Chip> : null}
      </p>
    </MessageShell>
  );
}

// ── proposal ─────────────────────────────────────────────────────────────────

/** ProposalCard reuses the existing proposal card's shape: the verified delta, and the diff behind it. */
export function ProposalCard({ payload }: { payload: ProposalPayload }) {
  return (
    <MessageShell
      icon={<CircleCheck className="size-3.5" aria-hidden="true" />}
      kind="Proposal"
      aside={<Chip variant="hash">{payload.proposal_id}</Chip>}
    >
      <p className="msg__claim">
        {payload.axis} on {payload.node}
      </p>
      {/* Computed server-side and rendered as received. The browser derives nothing — a conversation is
          not an exemption from P9's founding rule. */}
      {payload.delta_label ? <p className="msg__delta">{payload.delta_label}</p> : null}
      <p className="msg__section flex flex-wrap items-center gap-2">
        <EvidenceLink href={payload.href} label="open this proposal" />
        {payload.diff_ref ? <Chip variant="hash">{payload.diff_ref}</Chip> : null}
      </p>
    </MessageShell>
  );
}

// ── approval_request ─────────────────────────────────────────────────────────

/**
 * ApprovalCard is the one card that can arm the hazard palette, and only when it can actually be
 * approved — an armed control is a hazard; an un-approvable one is a statement.
 *
 * 🔴 It is DELIVERED even when it cannot be approved, carrying the reason (FR9, design.md D4). A hidden
 * control is indistinguishable from one that does not exist, and the person needs to know an action was
 * considered and why it is unavailable — which is usually a plan fact they can act on.
 */
export function ApprovalCard({
  payload,
  onApprove,
  pending,
}: {
  payload: ApprovalRequestPayload;
  onApprove: (approvalId: string) => void;
  pending: boolean;
}) {
  return (
    <MessageShell
      icon={<AlertTriangle className="size-3.5" aria-hidden="true" />}
      kind="Approval"
      hazard={payload.approvable}
      aside={<Chip variant="hash">{payload.proposal_id}</Chip>}
    >
      <p className="msg__claim">{payload.action}</p>
      <dl className="approval__facts">
        <dt>Blast radius</dt>
        <dd>{payload.blast_radius}</dd>
        <dt>Reversible</dt>
        <dd>{payload.reversible}</dd>
      </dl>
      {payload.approvable ? (
        <div className="msg__section">
          <button className="approval__button" disabled={pending} onClick={() => onApprove(payload.approval_id)}>
            {pending ? "Recording…" : "Approve"}
          </button>
          <p className="hint mt-2">
            This is the same act as approving anywhere else in the console — it goes to the same gate.
          </p>
        </div>
      ) : (
        <p className="approval__blocked">{unapprovableCopy(payload.unapprovable_reason)}</p>
      )}
    </MessageShell>
  );
}

// ── result ───────────────────────────────────────────────────────────────────

/**
 * ResultCard renders the stop reason on EVERY terminal message including `satisfied` (task 4.13) and
 * reconciles every step the plan declared (task 4.12).
 *
 * 🔴 The `stopped_on_limit` flag comes from the SERVER. A browser deciding whether a run "counts as
 * complete" would be a second implementation of a rule the platform already owns — and it is the rule
 * that decides whether a budget-exhausted run is drawn as a success.
 */
export function ResultCard({ payload }: { payload: ResultPayload }) {
  const stop = STOP_COPY[payload.stop_reason];
  return (
    <MessageShell
      icon={payload.stopped_on_limit ? <Clock className="size-3.5" aria-hidden="true" /> : <CircleCheck className="size-3.5" aria-hidden="true" />}
      kind="Result"
      aside={
        <span className={cx("result__stop", payload.stopped_on_limit && "result__stop--limit")}>
          {stop.label}
        </span>
      }
    >
      <p className="msg__claim">{stop.body}</p>
      {/*
        🔴 The DENOMINATOR, immediately under the stop reason rather than at the bottom of the card.
        `summary` is the server's own count — "Completed 0 of 3 planned steps" — and a browser run showed
        why its position matters: rendered last, a reader met "Finished" and left. The count is the one
        line that stops a satisfied run with nothing measured from reading as a complete answer.
      */}
      {payload.summary ? <p className="result__summary">{payload.summary}</p> : null}
      {payload.stopped_at_step ? (
        <p className="hint">It stopped at the step named {payload.stopped_at_step}.</p>
      ) : null}
      {stop.next ? <p className="result__next">{stop.next}</p> : null}

      <div className="msg__section">
        <p className="hint">What the plan declared, and what happened to each part of it</p>
        <ul className="reconcile">
          {(payload.reconciliation ?? []).map((entry) => (
            <li className={`reconcile__row reconcile__row--${entry.state}`} key={entry.step_id}>
              <StepMark state={entry.state} />
              <span className="reconcile__step">{entry.step_id}</span>
              <span className="reconcile__state">{STEP_STATE_COPY[entry.state].label}</span>
              {/* §7.6: every state except `done` names WHY. "Skipped" alone is the omission problem
                  with a label on it. */}
              {entry.reason ? <span className="reconcile__reason">{entry.reason}</span> : null}
            </li>
          ))}
        </ul>
      </div>

      {payload.verified_claim ? (
        <p className="msg__section flex flex-wrap items-center gap-2">
          <span className="hint">Verified against</span>
          <Chip variant="hash">{payload.verdict_ref}</Chip>
        </p>
      ) : null}
      {payload.pull_request_url ? (
        <p className="msg__section">
          <Link className="text-primary underline underline-offset-2" href={payload.pull_request_url}>
            The pull request this opened
          </Link>
        </p>
      ) : null}
    </MessageShell>
  );
}

// ── refusal ──────────────────────────────────────────────────────────────────

/** REFUSAL_ICON keeps the three failure classes visually three (task 4.4). */
const REFUSAL_ICON = {
  not_mounted: <Package className="size-3.5" aria-hidden="true" />,
  not_found: <Search className="size-3.5" aria-hidden="true" />,
  transport: <Wifi className="size-3.5" aria-hidden="true" />,
} as const;

/**
 * RefusalCard renders the three failure classes as three messages with three copy strings (task 4.4),
 * and an abstention as a fourth thing entirely — a boundary rather than a failure.
 *
 * The hazard palette IS used here. This is one of the two places it is allowed.
 */
export function RefusalCard({ payload }: { payload: RefusalPayload }) {
  const failure = payload.failure ? FAILURE_COPY[payload.failure] : null;
  return (
    <MessageShell
      icon={payload.failure ? REFUSAL_ICON[payload.failure] : <Info className="size-3.5" aria-hidden="true" />}
      kind={failure ? failure.label : "Not something this surface does"}
      hazard
      aside={payload.axis ? <Chip>{payload.axis}</Chip> : undefined}
    >
      {/* 🔴 The lower layer's cause, VERBATIM. Never re-worded, never summarised, never softened. */}
      <p className="msg__claim">{payload.cause}</p>
      {failure ? <p className="refusal__next">{failure.next}</p> : null}
      {payload.node ? <p className="hint">On the node named {payload.node}.</p> : null}

      {payload.surface_href ? (
        <p className="msg__section">
          <Link className="text-primary underline underline-offset-2" href={payload.surface_href}>
            Go to the surface that does this
          </Link>
        </p>
      ) : null}

      {payload.can_do && payload.can_do.length > 0 ? (
        <details className="candoo msg__section">
          {/* An open text box implies infinity. This is the finite list, generated from the intent
              table — so the copy a person reads and the table a fence checks cannot drift (§7.7). */}
          <summary>What this surface can be asked ({payload.can_do.length})</summary>
          <ul className="candoo__list">
            {payload.can_do.map((question) => (
              <li key={question}>{question}</li>
            ))}
          </ul>
        </details>
      ) : null}
    </MessageShell>
  );
}

// ── answer ───────────────────────────────────────────────────────────────────

/**
 * AnswerCard is the only prose on this surface, and it may not assert a property of the repository
 * (FR3). That is enforced SERVER-SIDE by the route the question took, not by anything here — a client
 * check would be a second, weaker copy of the rule.
 */
export function AnswerCard({ payload }: { payload: AnswerPayload }) {
  return (
    <MessageShell
      icon={<MessageSquareText className="size-3.5" aria-hidden="true" />}
      kind="Answer"
      aside={payload.topic ? <Chip>{payload.topic}</Chip> : undefined}
    >
      <p className="msg__prose">{payload.text}</p>
    </MessageShell>
  );
}

// ── the switch ───────────────────────────────────────────────────────────────

/**
 * MessageCard dispatches on `kind`.
 *
 * 🔴 NO `default:` ARM. `ConversationKind` is generated from Go, so a kind added there and regenerated
 * makes this switch non-exhaustive and the build fails — which is task 4.5, as a property rather than a
 * convention. The `payload!` assertions are safe for the same reason the server enforces: exactly one
 * payload is non-null and it is the one `kind` names.
 */
export function MessageCard({
  message,
  completed,
  onApprove,
  approving,
}: {
  message: ConversationMessage;
  completed: Record<string, ConversationStepState>;
  onApprove: (approvalId: string) => void;
  approving: string | null;
}) {
  switch (message.kind) {
    case "plan":
      return <PlanCard completed={completed} payload={message.plan!} />;
    case "progress":
      return <ProgressCard payload={message.progress!} />;
    case "finding":
      return <FindingCard payload={message.finding!} />;
    case "proposal":
      return <ProposalCard payload={message.proposal!} />;
    case "approval_request":
      return (
        <ApprovalCard
          onApprove={onApprove}
          payload={message.approval_request!}
          pending={approving === message.approval_request!.approval_id}
        />
      );
    case "result":
      return <ResultCard payload={message.result!} />;
    case "refusal":
      return <RefusalCard payload={message.refusal!} />;
    case "answer":
      return <AnswerCard payload={message.answer!} />;
  }
}
