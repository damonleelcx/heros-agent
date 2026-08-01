"use client";

import { useEffect } from "react";
import { reportHandledFailure } from "../../../design-system/error-report.ts";

/**
 * error.tsx is the console's DEGRADED rendering for a failure that reaches the route boundary (FR26).
 *
 * # The defect this exists to fix
 *
 * Every page resolves the acting principal before it renders anything (`requireIdentity`), which is
 * what makes an unauthenticated request redirect rather than render a shell that then fails (FR20).
 * But when the PLATFORM itself is unreachable, that same call throws — and without a boundary the
 * operator gets a framework error page: no chrome, no identification of which console they are
 * looking at, no statement of what failed, and nothing to do next.
 *
 * That is the exact collapse FR26 forbids, at the worst possible moment: the platform being
 * unreachable is precisely when an operator is trying to find out what is wrong. A stack trace tells
 * them the console is broken; the truth is that the console is fine and the platform is not.
 *
 * It was found by walking the degraded path in a real browser (task 14.7) — a green build and a
 * passing type check are both perfectly compatible with this page never having existed.
 */
export default function ConsoleError({ error, reset }: { error: Error; reset: () => void }) {
  // P24 task 3.5. A failure that reaches this boundary is REPORTED as well as rendered.
  //
  // The failures that matter most here are the ones that do not crash: a chunk that failed to load, a
  // hydration mismatch, a script the policy refused. React catches those and renders this page, and
  // before P24 the only signal was an operator choosing to mention it. Reported in an effect rather
  // than during render, because a boundary can re-render and a render-time report transmits the same
  // failure once per render.
  useEffect(() => {
    reportHandledFailure(error);
  }, [error]);

  return (
    <>
      <header className="chrome">
        <span className="chrome__mark">
          <span className="chrome__glyph" aria-hidden="true">
            OP
          </span>
          Operator Console · Internal
        </span>
      </header>
      <main id="main">
        <p className="page__eyebrow">Operations</p>
        <h1>The console cannot reach the platform</h1>
        <div className="state state--degraded" role="alert">
          <p className="state__title">Degraded — this is a transport failure, not a permission problem</p>
          <p className="state__body">
            The console is running. The admin API did not answer, so it cannot tell you what is halted
            or what is wrong — and it will not guess. Nothing you were looking at has changed as a
            result of this failure.
          </p>
          <p className="state__body">
            <strong>The fleet is not un-stoppable because of this.</strong> The P6 kill switch is armable
            independently of this console, and kill-switch state reads fail closed to halt.
          </p>
          <p className="state__body mono">{error.message}</p>
        </div>
        <button type="button" className="primary" onClick={reset}>
          Try again
        </button>
      </main>
    </>
  );
}
