"use client";

import { useEffect, useRef, useState } from "react";
import type { RunMonitor } from "@/lib/types.generated";
import { Section, Status, Chip, Empty, DataTable, Banner, Row } from "@/components/primitives";
import { integer, usd5, ms } from "@/lib/format";
import { cx } from "@/lib/cx";

const POLL_MS = 2000;

type Connection = "streaming" | "polling" | "closed";

/**
 * The live monitor.
 *
 * # The transport is stated on screen, not hidden
 *
 * A stream that silently degrades to polling looks identical to a stream that is working — until a
 * reader waits thirty seconds for a node they expected instantly and concludes the platform is stuck.
 * The indicator names which transport is in use and says what the difference actually costs them
 * ("the same snapshot, a little less often"), which turns a mystery into a fact.
 *
 * # Why the fallback exists at all
 *
 * SSE is the first thing a corporate proxy breaks. Falling back to a poll keeps the view working where
 * a stream cannot, and doing so only when NO message has arrived distinguishes "the stream never
 * connected" from "the stream connected and then the run ended" — which is a normal close, not a
 * failure to be recovered from.
 */
export function LiveMonitor({ runId, initial }: { runId: string; initial: RunMonitor }) {
  const [monitor, setMonitor] = useState<RunMonitor>(initial);
  const [connection, setConnection] = useState<Connection>(initial.terminal ? "closed" : "streaming");
  const sawMessage = useRef(false);
  const pollTimer = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (initial.terminal) return;

    function stopPolling() {
      if (pollTimer.current !== null) {
        clearInterval(pollTimer.current);
        pollTimer.current = null;
      }
    }

    function startPolling() {
      stopPolling();
      setConnection("polling");
      pollTimer.current = setInterval(async () => {
        try {
          const res = await fetch(`/api/console/runs/${encodeURIComponent(runId)}/monitor`, { cache: "no-store" });
          if (!res.ok) return; // the page's error state owns the message; a poll does not invent one
          const next: RunMonitor = await res.json();
          setMonitor(next);
          if (next.terminal) {
            stopPolling();
            setConnection("closed");
          }
        } catch {
          // Deliberately silent: a transient poll failure is not news, and the next tick retries.
        }
      }, POLL_MS);
    }

    const source = new EventSource(`/api/stream/runs/${encodeURIComponent(runId)}`);
    source.onmessage = (event) => {
      sawMessage.current = true;
      try {
        const next: RunMonitor = JSON.parse(event.data);
        setMonitor(next);
        if (next.terminal) {
          source.close();
          setConnection("closed");
        }
      } catch {
        // A malformed frame is dropped rather than replacing good state with nothing.
      }
    };
    source.onerror = () => {
      source.close();
      if (sawMessage.current) {
        setConnection("closed");
        return;
      }
      startPolling();
    };

    return () => {
      source.close();
      stopPolling();
    };
  }, [runId, initial.terminal]);

  const nodes = monitor.nodes ?? [];

  return (
    <>
      <Section
        title="This run"
        aside={
          <span className="mono" title="the configuration this run is executing">
            config {monitor.config_hash}
          </span>
        }
      >
        <Row>
          <span className="mono">{monitor.run_id}</span>
          <Status value={monitor.status} />
          <Chip variant="count">{nodes.length} node metrics</Chip>
        </Row>

        <p className="flex items-center gap-2 text-xs" role="status" aria-live="polite">
          <span
            className={cx(
              "size-2 shrink-0 rounded-full",
              connection === "streaming" && "bg-ok motion-safe:animate-pulse",
              connection === "polling" && "bg-warn",
              connection === "closed" && "bg-muted-foreground/50",
            )}
            aria-hidden="true"
          />
          <span
            className={cx(
              "font-mono",
              connection === "streaming" && "text-ok",
              connection === "polling" && "text-warn",
              connection === "closed" && "text-muted-foreground",
            )}
          >
            {connection === "streaming"
              ? "Streaming metrics as they arrive."
              : connection === "polling"
                ? "Streaming was unavailable, so this view is polling instead. Nothing is lost — the same snapshot arrives, a little less often."
                : `Stream closed. This run is ${monitor.status}.`}
          </span>
        </p>

        {monitor.halted ? (
          <Banner tone="warn" title="This run was halted">
            <Row>
              <Chip>{monitor.halted.node_id}</Chip>
              <span className="mono caption">{monitor.halted.reason}</span>
            </Row>
          </Banner>
        ) : null}
      </Section>

      <Section title="Nodes">
        {nodes.length === 0 ? (
          monitor.terminal ? (
            <Empty title="This run produced no node metrics." />
          ) : (
            <Empty title="Run in progress — waiting for the first node." />
          )
        ) : (
          <DataTable
            caption="Per-node latency, cost and token counts, as the platform reported them"
            columns={[
              { key: "node", label: "Node" },
              { key: "state", label: "State" },
              { key: "latency", label: "Latency", numeric: true },
              { key: "cost", label: "Cost (USD)", numeric: true },
              { key: "prompt", label: "Prompt tokens", numeric: true },
              { key: "completion", label: "Completion tokens", numeric: true },
            ]}
          >
            <tbody>
              {nodes.map((node) => (
                <tr key={node.node_id} className={`node-row node-row--${stateClass(node.state)}`}>
                  <td className="mono">{node.node_id}</td>
                  <td>
                    <Status value={node.state} />
                  </td>
                  <td className="num mono">{ms(node.latency_ms)}</td>
                  <td className="num mono">{usd5(node.cost_usd)}</td>
                  <td className="num mono">{integer(node.tokens_prompt)}</td>
                  <td className="num mono">{integer(node.tokens_completion)}</td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>
    </>
  );
}

/**
 * stateClass maps a node state to a row marker.
 *
 * 🔴 The `default` arm is `unknown`, never `ok`. A state this console does not model must not be
 * drawn as a state it does — that is the `p25monitor.html` defect, where an unrecognised value fell
 * through to the running style and asserted something false about a node nobody had looked at.
 */
function stateClass(state: string): string {
  switch (state) {
    case "ok":
      return "ok";
    case "failed":
      return "bad";
    case "timed_out":
      return "warn";
    default:
      return "unknown";
  }
}
