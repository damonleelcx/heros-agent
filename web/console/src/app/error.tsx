"use client";

import { useEffect } from "react";
import { reportHandledFailure } from "../../../design-system/error-report.ts";

/**
 * error.tsx is the console's degraded rendering for a failure that reaches a route boundary — and the
 * place a caught failure becomes a REPORT rather than a page that looks fine (P24 task 3.5).
 *
 * # The failure mode this is for, stated plainly
 *
 * The expensive failures on this surface are not the ones that crash. They are the ones where the page
 * *renders correctly and does nothing*: a chunk that failed to load, a hydration mismatch, a script the
 * Content-Security-Policy refused. React catches those, renders a fallback, and the build stays green —
 * so before this file existed, the only person who knew was the reader, and only if they chose to tell
 * us. That is the gap P24 opened this integration for.
 *
 * # Why the report is in an effect and not in the render
 *
 * A boundary can re-render (a parent state change, a `reset`), and reporting during render would
 * transmit the same failure once per render. The effect runs once per distinct error.
 *
 * # What the reader gets
 *
 * The same thing every other failure on this console gives them: what failed, that it is a transport
 * class rather than a permission problem, that nothing they were looking at changed, and a control that
 * retries. No stack trace and no error message — the message is the field this whole phase drops, and
 * showing a reader an internal error string is the same disclosure one screen closer.
 */
export default function ConsoleError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  useEffect(() => {
    // Transmits only when `error_diagnostics` is granted AND a reporting target is configured; with
    // either absent this is a no-op and the reader still gets the page below.
    reportHandledFailure(error);
  }, [error]);

  return (
    <main id="main" className="page">
      <p className="page__eyebrow">Console</p>
      <h1>This view could not be rendered</h1>
      <div className="state state--transport" role="alert">
        <p className="state__title">A transport failure, not an empty result</p>
        <p className="state__body">
          Something the page needed did not arrive. Nothing you were looking at has changed, and no
          command was sent as a result of this failure.
        </p>
        <p className="state__body">
          <button type="button" className="button" onClick={reset}>
            Try again
          </button>
        </p>
      </div>
    </main>
  );
}
