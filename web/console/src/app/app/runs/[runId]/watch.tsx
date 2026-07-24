"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Eye, EyeOff } from "lucide-react";

const TERMINAL = new Set(["succeeded", "failed", "halted", "build-rejected"]);
const POLL_MS = 1000;

/**
 * WatchToggle re-reads the run record while it is still running.
 *
 * It is a poll rather than a stream on purpose: the record view is server-rendered and its refresh is
 * `router.refresh()`, which repaints the whole view in place. The live view next door is the streaming
 * one. Two mechanisms for two different questions — "has this finished?" and "what is happening right
 * now?" — rather than one that half-answers both.
 */
export function WatchToggle({ runId, initialStatus }: { runId: string; initialStatus: string }) {
  const router = useRouter();
  const [watching, setWatching] = useState(false);
  const [status, setStatus] = useState(initialStatus);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);

  // P2-25: one ref, and every start clears it first. Two timers cannot coexist because there is only
  // one place to keep one.
  function stop() {
    if (timer.current !== null) {
      clearInterval(timer.current);
      timer.current = null;
    }
  }

  useEffect(() => {
    if (!watching) {
      stop();
      return;
    }
    stop();
    timer.current = setInterval(async () => {
      try {
        const res = await fetch(`/api/console/runs/${encodeURIComponent(runId)}`, { cache: "no-store" });
        if (!res.ok) {
          // A failing poll stops the watch rather than hammering a failing endpoint once a second. The
          // page's own error state is what tells the user what happened; a silent retry loop would
          // hide it.
          setWatching(false);
          return;
        }
        const run: { status?: string } = await res.json();
        const next = run.status ?? "";
        setStatus(next);
        // P2-24. The RECORD's status. Not the node list.
        if (TERMINAL.has(next)) setWatching(false);
        // P2-26: refresh repaints in place. No skeleton, no flash — the first load already showed one.
        router.refresh();
      } catch {
        setWatching(false);
      }
    }, POLL_MS);
    return stop;
  }, [watching, runId, router]);

  const terminal = TERMINAL.has(status);
  return (
    <button
      className="button px-2.5 py-1 text-xs"
      type="button"
      onClick={() => setWatching((was) => !was)}
      disabled={terminal && !watching}
      aria-live="polite"
    >
      {watching ? (
        <>
          <EyeOff className="size-3.5" aria-hidden="true" />
          Stop watching
        </>
      ) : (
        <>
          <Eye className="size-3.5" aria-hidden="true" />
          {terminal ? "Run is terminal" : "Watch"}
        </>
      )}
    </button>
  );
}
