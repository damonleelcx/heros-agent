"use client";

/**
 * errorReporting.tsx installs the browser error reporter. It renders NOTHING.
 *
 * # Why this thin wrapper is duplicated in both consoles, and the logic is not
 *
 * Everything that decides WHAT is transmitted — the allowlist, the construction, the message drop, the
 * closed-enum surface, the absence of any breadcrumb collection, the rate limits and the encoder —
 * lives in ONE place, `web/design-system/error-report.ts`, which both consoles import. This file is the
 * React seam and nothing else.
 *
 * It is duplicated because `web/design-system/` has no `node_modules` and therefore cannot resolve
 * `react`; a shared component there would need a path alias in two tsconfigs and a matching resolver
 * alias in two webpack configs, which is three more places for the two consoles to disagree than the
 * eight lines below. The drift risk this leaves is asserted rather than assumed: a test requires both
 * copies to call `installErrorReporting` and to render null.
 *
 * # Why it is not a nonced inline script
 *
 * The obvious implementation is an inline `<script nonce={nonce}>` carrying the configuration. Refused
 * twice over: writing one in React means `dangerouslySetInnerHTML`, which `scan-markup.mjs` fails the
 * build on — and it would buy nothing, because the reporter is FIRST-PARTY bundled code reached through
 * `'strict-dynamic'` from the per-request nonced bootstrap. It already runs *because of* the nonce, and
 * a script that did not arrive from the nonced root does not execute at all. So `script-src` gains no
 * host and the document carries no inline script, on any prefix.
 *
 * # Why the effect is keyed on values, not on the props object
 *
 * A props object is new on every render, so an effect keyed on it would tear the listeners down and
 * re-attach them constantly — and an error raised in the gap would be lost. Keyed on the values, the
 * listeners survive re-renders and are replaced only when the configuration really changes, which is
 * what withdrawal needs: revoking the grant removes them on the next navigation, with no sign-out.
 */

import { useEffect } from "react";
import { installErrorReporting, type ReportingConfig } from "../../../design-system/error-report.ts";

export function ErrorReporting(config: ReportingConfig) {
  const { dsn, release, edition, surface, traceId, granted } = config;
  useEffect(
    () => installErrorReporting({ dsn, release, edition, surface, traceId, granted }),
    [dsn, release, edition, surface, traceId, granted],
  );
  return null;
}
