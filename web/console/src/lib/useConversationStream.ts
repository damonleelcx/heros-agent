"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { ConversationMessage, ConversationTurnState } from "@/lib/types.generated";

/**
 * useConversationStream is the client half of FR6: stream, reconnect, resume — no duplicate, no gap.
 *
 * # 🔴 Why the cursor is a ref and not state
 *
 * The reconnect closure has to read the LATEST acknowledged id, and a value captured from a render
 * would be whatever it was when that render happened. A stale cursor is not a subtle bug: it replays
 * messages the user already has (duplicates) or, if it were ever ahead, skips ones they do not (a gap).
 * Both are exactly what FR6 forbids, and both would be intermittent — appearing only when a reconnect
 * happens to land between two renders.
 *
 * # Why `fetch` + a reader rather than `EventSource`
 *
 * `EventSource` cannot send a credential-bearing header and — more importantly here — cannot be told
 * where to resume from on the FIRST connection. It reconnects automatically using `Last-Event-ID`,
 * which is right for a dropped connection and wrong for a page that just loaded holding a cursor from
 * the previous mount. Reading the body ourselves means one resume mechanism instead of two that
 * disagree. (The server honours `Last-Event-ID` as well, for the case where a browser reconnects
 * beneath us — see the stream handler.)
 *
 * # Why the backoff is capped and jittered
 *
 * A deployment restart drops every open stream at once. Without jitter, every browser retries in
 * lockstep and the first thing the newly-started process meets is a synchronised thundering herd — so
 * the reconnect that was supposed to make a restart invisible is what makes it visible.
 */

/** Backoff bounds. Written here rather than inline so the retry behaviour is readable in one place. */
const BACKOFF_MIN_MS = 500;
const BACKOFF_MAX_MS = 15_000;

/**
 * StreamStatus is what the surface says about the CONNECTION, which is never the same thing as what it
 * says about the RUN.
 *
 * 🔴 They are separate on purpose. A dropped stream while a run continues is the single most likely
 * moment for a person to conclude the product broke, and the honest rendering is "the view dropped, the
 * run did not". A status that conflated them could not draw that.
 */
export type StreamStatus = "idle" | "connecting" | "open" | "reconnecting" | "closed" | "failed";

export type ConversationStream = {
  messages: ConversationMessage[];
  /** state is the RUN's own view of the current turn, from the stream's `state` frame (FR21). */
  state: ConversationTurnState | null;
  status: StreamStatus;
  /** error is the last transport failure, if the stream is not open. */
  error: string | null;
  /** resumed is true once a reconnect has replayed from a cursor, so the surface can say so. */
  resumed: boolean;
  /** start opens (or re-opens) the stream for a conversation. */
  start: (conversationId: string) => void;
  /** stop closes the stream. It does NOT cancel the run — see FR7. */
  stop: () => void;
};

type Frame = { event: string; data: string; id: string };

/**
 * parseFrames splits an SSE buffer into complete frames and returns the unconsumed tail.
 *
 * 🔴 The tail is the whole point. A chunk boundary falls wherever TCP decides, so a naive `split("\n\n")`
 * that dropped the remainder would lose a message every time a frame straddled two reads — a gap that
 * appears under load and never in development.
 */
export function parseFrames(buffer: string): { frames: Frame[]; rest: string } {
  const frames: Frame[] = [];
  let rest = buffer;
  for (;;) {
    const boundary = rest.indexOf("\n\n");
    if (boundary === -1) break;
    const block = rest.slice(0, boundary);
    rest = rest.slice(boundary + 2);

    let event = "message";
    let id = "";
    const dataLines: string[] = [];
    for (const line of block.split("\n")) {
      // A comment frame (`: keep-alive`) is the server's connection heartbeat. Skipped silently: it
      // exists to defeat an idle-connection timer, not to be rendered.
      if (line.startsWith(":")) continue;
      if (line.startsWith("event: ")) event = line.slice(7);
      else if (line.startsWith("data: ")) dataLines.push(line.slice(6));
      else if (line.startsWith("id: ")) id = line.slice(4);
    }
    if (dataLines.length > 0) frames.push({ event, id, data: dataLines.join("\n") });
  }
  return { frames, rest };
}

/**
 * mergeMessage inserts a message in id order, replacing any existing one with the same id.
 *
 * # Why it de-duplicates at all, when the server promises not to send a duplicate
 *
 * Because the server's promise covers ONE stream, and the client can have two: a reconnect that races
 * an in-flight read, or a React strict-mode double-mount in development. Making the client idempotent
 * costs a linear scan over a transcript of tens of messages and removes an entire class of "it
 * duplicated once and I could not reproduce it".
 *
 * 🚫 It never DROPS a message that does not fit a pattern. De-duplication is by id equality only;
 * anything cleverer would be the client deciding what the transcript contains.
 */
export function mergeMessage(existing: ConversationMessage[], incoming: ConversationMessage): ConversationMessage[] {
  const at = existing.findIndex((m) => m.id === incoming.id);
  if (at >= 0) {
    const copy = existing.slice();
    copy[at] = incoming;
    return copy;
  }
  const insertAt = existing.findIndex((m) => m.id > incoming.id);
  if (insertAt === -1) return [...existing, incoming];
  return [...existing.slice(0, insertAt), incoming, ...existing.slice(insertAt)];
}

/** backoffFor returns the delay before attempt `n` (0-based), capped and jittered. */
export function backoffFor(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(BACKOFF_MIN_MS * 2 ** attempt, BACKOFF_MAX_MS);
  // Full jitter over [base/2, base]. Half the window rather than the whole one so a restart's herd is
  // spread without making the first retry feel like a hang.
  return Math.round(base / 2 + random() * (base / 2));
}

export function useConversationStream(): ConversationStream {
  const [messages, setMessages] = useState<ConversationMessage[]>([]);
  const [state, setState] = useState<ConversationTurnState | null>(null);
  const [status, setStatus] = useState<StreamStatus>("idle");
  const [error, setError] = useState<string | null>(null);
  const [resumed, setResumed] = useState(false);

  // The acknowledgement cursor: the id of the last message this client has PROCESSED. See the header.
  const cursor = useRef(0);
  const abort = useRef<AbortController | null>(null);
  const attempt = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // `generation` invalidates an in-flight connection when a newer one starts, so a slow reader from a
  // previous conversation cannot append to the current one.
  const generation = useRef(0);

  const stop = useCallback(() => {
    generation.current += 1;
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
    abort.current?.abort();
    abort.current = null;
    setStatus("closed");
  }, []);

  const start = useCallback((conversationId: string) => {
    generation.current += 1;
    const mine = generation.current;
    attempt.current = 0;

    const connect = async () => {
      if (generation.current !== mine) return;
      abort.current?.abort();
      const controller = new AbortController();
      abort.current = controller;
      setStatus(attempt.current === 0 ? "connecting" : "reconnecting");

      try {
        const response = await fetch(
          `/api/stream/conversations?conversation_id=${encodeURIComponent(conversationId)}&after=${cursor.current}`,
          { signal: controller.signal, cache: "no-store" },
        );
        if (!response.ok || !response.body) {
          // The BFF preserves the taxonomy: 503 not-mounted, 404 not-found, 502 transport. The body
          // carries the platform's own words, which are what the surface renders — a message
          // manufactured here would destroy the distinction before any component saw it.
          const body = (await response.json().catch(() => null)) as { error?: string } | null;
          throw new Error(body?.error ?? `the stream could not be opened (${response.status})`);
        }
        setError(null);
        setStatus("open");
        if (cursor.current > 0) setResumed(true);
        attempt.current = 0;

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          if (generation.current !== mine) return;
          buffer += decoder.decode(value, { stream: true });
          const { frames, rest } = parseFrames(buffer);
          buffer = rest;
          for (const frame of frames) {
            if (frame.event === "state") {
              // 🔴 The RUN's own state. Not derived from the transcript above it — the two are produced
              // by different code paths on purpose, and a state reconstructed from messages would be
              // the browser's recomputation moved one file over.
              setState(JSON.parse(frame.data) as ConversationTurnState);
              continue;
            }
            if (frame.event === "lagged") {
              // The server closed us for falling behind. Reconnecting from the cursor is the recovery,
              // and it is the same path a dropped connection takes.
              throw new Error("the stream fell behind and was closed; resuming");
            }
            if (frame.event !== "message") continue;
            const message = JSON.parse(frame.data) as ConversationMessage;
            setMessages((prev) => mergeMessage(prev, message));
            // 🔴 The cursor advances AFTER the message is merged, which is what makes it an
            // ACKNOWLEDGEMENT rather than a receipt: a message parsed but not applied must be replayed.
            if (message.id > cursor.current) cursor.current = message.id;
          }
        }
        // The server closed the stream cleanly — the run reached a terminal message.
        if (generation.current === mine) setStatus("closed");
      } catch (cause) {
        if (controller.signal.aborted || generation.current !== mine) return;
        setError(cause instanceof Error ? cause.message : "the stream failed");
        const delay = backoffFor(attempt.current);
        attempt.current += 1;
        if (attempt.current > 8) {
          // Eight attempts is roughly a minute of trying. Past that the surface says FAILED rather
          // than retrying forever behind a spinner — a client that retries silently for an hour is a
          // client that has decided not to tell anybody.
          setStatus("failed");
          return;
        }
        setStatus("reconnecting");
        timer.current = setTimeout(connect, delay);
      }
    };

    void connect();
  }, []);

  useEffect(() => stop, [stop]);

  return { messages, state, status, error, resumed, start, stop };
}
