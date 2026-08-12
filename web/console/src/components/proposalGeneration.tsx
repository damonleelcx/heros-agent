"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import type { PassView } from "@/lib/types.generated";
import { Banner, Chip, Row } from "@/components/primitives";

/**
 * The generation pass: what the last one found, and the control that runs another (P30 tasks 1.7, 1.11).
 *
 * # What this replaces
 *
 * "Nothing is pending." — rendered unconditionally whenever the recommendation list was empty, which
 * covered two opposite situations. A workflow nobody had ever analysed and a workflow that had been
 * analysed and was genuinely healthy got the same three words. One reader should press a button; the
 * other is finished; neither could tell which they were.
 *
 * The platform now carries a closed state and the generator's own sentence, and this renders both plus
 * the ACTION that state implies. The action is the part that was missing even when the state was right:
 * a page that says "you have linked no runs" and offers nothing has told a reader they are stuck.
 *
 * # Why it refreshes rather than reloads
 *
 * `router.refresh()` re-renders the server component with fresh data in place. A reload would lose the
 * selected tab and the scroll position, and — worse for this control specifically — a full navigation
 * looks identical whether the pass changed anything or not, so a reader watching it cannot tell the
 * difference between "it ran" and "the page came back".
 */

/** ACTIONS maps a proposalgen.State to what the reader should do about it. */
const ACTIONS: Record<string, string> = {
  no_linked_runs:
    "Run your eval and link it with `heros link`. Until a run is linked there is no cost to attribute, " +
    "so no bottleneck can be found.",
  no_per_node_metrics:
    "Re-link a run recorded by a current CLI. The linked runs carry no per-node breakdown, which older " +
    "CLIs did not record — so cost cannot be attributed to any one node.",
  no_discovered_graph:
    "Push a source snapshot with `heros push-source`. Without one no node carries a pattern label, and " +
    "every operator's admissibility check is unanswerable.",
  no_model_menu:
    "Publish a model catalog with tiers. Nothing is wrong with your workflow — this deployment cannot " +
    "say which model is cheaper, so no downgrade is expressible.",
  revision_mismatch:
    "Bring the run and the graph to one revision: push source at the run's revision, or link a run " +
    "measured at the graph's.",
  no_bottleneck:
    "Nothing to do. This is a healthy result: no node dominates this workflow's cost or latency, so " +
    "there is nothing here to make cheaper.",
  no_admissible_candidate:
    "Nothing to do right now. A bottleneck was found and every operator declined it — most often " +
    "because the node already runs the cheapest model with a published tier.",
  generated:
    "Verify them. Each proposal is unverified until your CI runs the gate and reports back with " +
    "`heros report-verdict`.",
};

export function GenerationPass({
  workflowId,
  state,
  pass,
}: {
  workflowId: string;
  /** The SURFACE state: ready | empty | verifying | error | never_analysed. */
  state: string;
  /** The last recorded pass, or null when none has run. */
  pass: PassView | null;
}) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    setBusy(true);
    setError(null);
    const res = await fetch("/api/console/proposals/generate", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ workflow_id: workflowId }),
    });
    let data: { error?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(false);
    if (!res.ok) {
      // The platform's sentence, as written. A generic "something went wrong" here would discard the
      // one thing that tells a reader what to do next.
      setError(data.error ?? "the generation pass could not be run");
      return;
    }
    // Re-render the server component in place. The state, the sentence and the card lists all come
    // from one read, so refreshing is what keeps them from disagreeing with each other.
    startTransition(() => router.refresh());
  }

  const working = busy || pending;
  const action = pass ? ACTIONS[pass.state] : null;

  return (
    <div className="flex flex-col gap-3">
      {state === "never_analysed" ? (
        <Banner tone="info" title="No generation pass has run for this workflow.">
          <p>
            This is not the same as &ldquo;nothing was found&rdquo;. Nobody has asked the platform to
            look yet, so there is no result to report — press the button below and it will say what it
            finds, including when what it finds is nothing.
          </p>
        </Banner>
      ) : null}

      {pass ? (
        <div className="flex flex-col gap-2">
          <Row>
            <Chip tone="info" title="what the last pass found">
              {pass.state.replace(/_/g, " ")}
            </Chip>
            <Chip variant="count">{pass.proposals} recorded by that pass</Chip>
            <span className="caption">
              last run <RelativeTime ms={pass.ran_at_ms} />
            </span>
          </Row>
          {/* The generator's own sentence. It names what the pass actually saw — revisions, counts —
              which a console cannot reconstruct without running a pass of its own. */}
          <p className="text-sm leading-relaxed text-foreground/90">{pass.detail}</p>
          {action ? <p className="hint">{action}</p> : null}
        </div>
      ) : null}

      {error ? (
        <Banner tone="bad" title="The generation pass did not run.">
          <p>{error}</p>
        </Banner>
      ) : null}

      <div className="flex items-center gap-3">
        <button className="button" type="button" disabled={working} onClick={() => void run()}>
          {working ? "Running…" : pass ? "Run another pass" : "Run a generation pass"}
        </button>
        <span className="caption">
          Reads your linked runs and your discovered graph. It writes proposals and never changes your
          source.
        </span>
      </div>
    </div>
  );
}

/**
 * RelativeTime renders an epoch-millisecond timestamp.
 *
 * 🔴 Absolute, in UTC, not "3 hours ago". A relative rendering is computed against the READER's clock,
 * so a server-rendered one is wrong the moment it is hydrated and drifts further while the page is
 * open — and "2 hours ago" on a page nobody refreshed is a quiet lie about when the platform last
 * looked, which is exactly the fact this surface exists to state.
 */
function RelativeTime({ ms }: { ms: number }) {
  if (!ms) return <span className="mono">at an unrecorded time</span>;
  return <span className="mono">{new Date(ms).toISOString().replace("T", " ").slice(0, 16)} UTC</span>;
}
