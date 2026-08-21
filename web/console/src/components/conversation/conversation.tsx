"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Copy, Loader2, Send } from "lucide-react";
import { Banner, Card, Chip, Stat, Stats } from "@/components/primitives";
import { cx } from "@/lib/cx";
import { integer } from "@/lib/format";
import { MessageCard } from "@/components/conversation/messages";
import { useConversationStream } from "@/lib/useConversationStream";
import {
  MEMORY_BOUNDARY,
  NO_COMPOSITE_SCORE,
  PERSISTENCE_FALLBACK,
  PHASE_COPY,
  RESUME_NOTICE,
  STREAM_LOST,
} from "@/lib/conversationCopy";
import type { ConversationStepState, ConversationView, ConversationTurnView } from "@/lib/types.generated";

/**
 * conversation.tsx is the surface (task 4.1): a composer, a transcript, and a live header.
 *
 * # What this component is NOT allowed to do, restated where it would be tempting
 *
 * It derives nothing. Not a score, not a status, not whether a run "counts as complete", not which
 * steps are done. Every one of those arrives — from the message payloads, and from the stream's own
 * `state` frame, which is the RUN's answer rather than a reconstruction from the transcript (FR21).
 * The one thing computed here is scroll position.
 *
 * # 🔴 Why the completed-step map comes from `state` and not from the messages
 *
 * The transcript is what the reader sees; the state is what the server knows, and they are produced by
 * different code paths deliberately. Deriving the checklist from the messages would pass the letter of
 * FR21 and miss all of it — a client that reconstructs the run's state from its own history is exactly
 * what "no state is reconstructed from the client's message history" is about, moved one file over.
 */

/** The fourteen questions, offered as starters. Not a menu: the box is free text and this is a hint. */
const STARTERS = [
  "What does my agent actually do, step by step?",
  "What did you measure, and what did you not?",
  "What does this node remember between calls?",
  "How does an approved change reach my repository?",
];

type Phase = "idle" | "opening" | "asking";

export function Conversation({ workflowIds }: { workflowIds: string[] }) {
  const [workflowId, setWorkflowId] = useState(workflowIds[0] ?? "");
  const [question, setQuestion] = useState("");
  const [conversation, setConversation] = useState<ConversationView | null>(null);
  const [turn, setTurn] = useState<ConversationTurnView | null>(null);
  const [phase, setPhase] = useState<Phase>("idle");
  const [failure, setFailure] = useState<string | null>(null);
  const [approving, setApproving] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const stream = useConversationStream();
  const tail = useRef<HTMLDivElement | null>(null);

  // Follow the tail as messages arrive. `block: "nearest"` so a reader who has scrolled up to read a
  // finding is not yanked back down by a `progress` line — a transcript that fights the reader is one
  // they stop reading.
  useEffect(() => {
    tail.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }, [stream.messages.length]);

  const ask = useCallback(async () => {
    const text = question.trim();
    if (!text || !workflowId || phase !== "idle") return;
    setFailure(null);
    setPhase("opening");

    let active = conversation;
    if (!active) {
      const opened = await fetch("/api/console/conversations", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ workflow_id: workflowId }),
      });
      if (!opened.ok) {
        const body = (await opened.json().catch(() => null)) as { error?: string } | null;
        // The platform's own words. A message manufactured here would flatten the three failure classes
        // before any component saw them.
        setFailure(body?.error ?? "the conversation could not be opened");
        setPhase("idle");
        return;
      }
      active = (await opened.json()) as ConversationView;
      setConversation(active);
      stream.start(active.conversation_id);
    }

    setPhase("asking");
    const submitted = await fetch("/api/console/conversations/turns", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ conversation_id: active.conversation_id, question: text }),
    });
    if (!submitted.ok) {
      const body = (await submitted.json().catch(() => null)) as { error?: string } | null;
      setFailure(body?.error ?? "the question could not be submitted");
      setPhase("idle");
      return;
    }
    setTurn((await submitted.json()) as ConversationTurnView);
    setQuestion("");
    setPhase("idle");
  }, [conversation, phase, question, stream, workflowId]);

  const approve = useCallback(
    async (approvalId: string) => {
      if (!conversation) return;
      setApproving(approvalId);
      const response = await fetch("/api/console/conversations/approvals", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ conversation_id: conversation.conversation_id, approval_id: approvalId }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as { error?: string } | null;
        setFailure(body?.error ?? "the approval was not recorded");
      }
      setApproving(null);
    },
    [conversation],
  );

  const state = stream.state;
  const completed: Record<string, ConversationStepState> = state?.completed ?? {};
  const traceId = state?.trace_id ?? turn?.trace_id ?? "";

  return (
    <div className="ask">
      <Banner title="What this surface keeps, and what it does not" tone="info">
        {/* 🔴 Task 4.10: a stated, visible consequence of D1. The sentence comes from the PLATFORM
            (`conversation.persistence`) so the day ADR-015's Q1 is revisited the console is not still
            promising the old behaviour from a literal. */}
        <p>{conversation?.persistence || PERSISTENCE_FALLBACK}</p>
        <p>{MEMORY_BOUNDARY}</p>
      </Banner>

      <Card className="ask__composer">
        <label className="ask__label" htmlFor="ask-workflow">
          Workflow
        </label>
        <select
          className="ask__select"
          id="ask-workflow"
          onChange={(event) => setWorkflowId(event.target.value)}
          value={workflowId}
        >
          {workflowIds.map((id) => (
            <option key={id} value={id}>
              {id}
            </option>
          ))}
        </select>

        <label className="ask__label" htmlFor="ask-question">
          Question
        </label>
        <textarea
          className="ask__input"
          id="ask-question"
          onChange={(event) => setQuestion(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) void ask();
          }}
          placeholder="Ask about this workflow in English…"
          rows={3}
          value={question}
        />
        <div className="ask__actions">
          <button className="ask__send" disabled={phase !== "idle" || !question.trim() || !workflowId} onClick={() => void ask()}>
            {phase === "idle" ? <Send className="size-3.5" aria-hidden="true" /> : <Loader2 className="size-3.5 motion-safe:animate-spin" aria-hidden="true" />}
            {phase === "idle" ? "Ask" : "Sending…"}
          </button>
          <span className="hint">⌘↵ to send</span>
        </div>
        <ul className="ask__starters">
          {STARTERS.map((starter) => (
            <li key={starter}>
              <button className="ask__starter" onClick={() => setQuestion(starter)} type="button">
                {starter}
              </button>
            </li>
          ))}
        </ul>
      </Card>

      {failure ? (
        <Banner title="That did not go through" tone="bad">
          <p>{failure}</p>
        </Banner>
      ) : null}

      {state ? <TurnHeader completedCount={Object.keys(completed).length} state={state} /> : null}

      {/*
       * 🔴 The transcript is a POLITE live region (task 4.7).
       *
       * `aria-live="polite"`, never `assertive`. A four-minute run emits dozens of messages, and an
       * assertive region would interrupt a screen-reader user on every one of them — a four-minute
       * interruption, which is worse than no announcement at all.
       */}
      <section aria-label="Conversation" aria-live="polite" className="ask__transcript">
        {stream.messages.length === 0 ? (
          <Card>
            <p className="hint">{NO_COMPOSITE_SCORE}</p>
          </Card>
        ) : null}
        {stream.messages.map((message) => (
          <MessageCard
            approving={approving}
            completed={completed}
            key={message.id}
            message={message}
            onApprove={(id) => void approve(id)}
          />
        ))}
        <div ref={tail} />
      </section>

      {stream.resumed && stream.status === "open" ? <p className="hint">{RESUME_NOTICE}</p> : null}
      {stream.status === "reconnecting" || stream.status === "failed" ? (
        <Banner title={stream.status === "failed" ? "The connection did not come back" : "Reconnecting"} tone="warn">
          {/* The stream and the run are DIFFERENT things, and this is the moment a person most easily
              concludes the product broke. The copy says which one dropped. */}
          <p>{STREAM_LOST}</p>
          {stream.error ? <p className="mono">{stream.error}</p> : null}
        </Banner>
      ) : null}

      {traceId ? (
        <p className="ask__trace">
          {/* Task 4.14: the turn's trace id, displayed and COPYABLE. An agent whose reasoning is
              unobservable cannot be debugged by the customer, and "contact support" is not an
              observability strategy for a product they run against their own source. */}
          <span className="hint">Trace</span>
          <code className="mono">{traceId}</code>
          <button
            className="ask__copy"
            onClick={() => {
              void navigator.clipboard?.writeText(traceId);
              setCopied(true);
              setTimeout(() => setCopied(false), 2000);
            }}
            type="button"
          >
            <Copy className="size-3" aria-hidden="true" />
            {copied ? "Copied" : "Copy"}
          </button>
        </p>
      ) : null}
    </div>
  );
}

/**
 * TurnHeader renders the phase, the declared budget and the REMAINING budget while a turn runs
 * (task 4.11) — the four facts a spinner asks the reader to supply from nothing.
 */
function TurnHeader({
  state,
  completedCount,
}: {
  state: NonNullable<ReturnType<typeof useConversationStream>["state"]>;
  completedCount: number;
}) {
  const phase = PHASE_COPY[state.phase];
  return (
    <Card className="turn">
      <div className="turn__head">
        <span className={cx("turn__phase", state.terminal && "turn__phase--done")}>{phase.label}</span>
        <span className="hint">{state.terminal ? "This turn has ended." : phase.detail}</span>
        <Chip variant="plan">{state.intent}</Chip>
      </div>
      <Stats fill>
        <Stat
          dense
          label="Tokens left"
          note={`of ${integer(state.envelope.token_budget)} declared`}
          value={integer(state.remaining.tokens)}
        />
        <Stat
          dense
          label="Reads left"
          note={`of ${integer(state.envelope.tool_call_ceiling)} declared`}
          value={integer(state.remaining.tool_calls)}
        />
        <Stat
          dense
          label="Time left"
          note={`of ${integer(state.envelope.wall_clock_seconds)}s declared`}
          unit="s"
          value={integer(state.remaining.wall_clock_seconds)}
        />
        <Stat dense label="Steps resolved" value={integer(completedCount)} />
      </Stats>
    </Card>
  );
}
