"use client";

import { useId } from "react";
import type { AxisSubject } from "@/lib/axisSubject";

/**
 * SubjectSwitcher is how a reader changes which node the axis surfaces are bound to (P37 FR2).
 *
 * # 🔴 A form POST, not a link and not client-side state
 *
 * Three reasons, in the order they matter:
 *
 *   1. The choice has to survive a navigation to a different axis surface, and it has to be readable
 *      while the NEXT page is being rendered on the server — otherwise the subject's name arrives after
 *      paint and the reader sees one node, then another. That is a cookie, and a cookie is set by a
 *      response.
 *   2. A GET that changes stored state can be triggered by a prefetch, a link checker or an `<img>` —
 *      the same argument `layout.tsx` records for sign-out being a POST.
 *   3. It works with JavaScript disabled and it works from the keyboard, because it is a real form
 *      control with a real label rather than a div with a click handler (NFR7.2).
 *
 * # Why it renders even when there is one candidate
 *
 * It does not. With one candidate the shell renders the NAME (`SubjectName`), not a control — FR3, the
 * input you can remove is the one you remove. This component appears when there is a choice to make.
 */
export function SubjectSwitcher({
  candidates,
  selected,
  returnTo,
}: {
  candidates: AxisSubject[];
  selected?: string;
  /** returnTo is the surface to come back to, so changing the node does not also change the page. */
  returnTo: string;
}) {
  const id = useId();
  return (
    <form action="/api/console/subject" method="post" className="flex flex-wrap items-center gap-2">
      <label className="caption" htmlFor={id}>
        node
      </label>
      <input type="hidden" name="return_to" value={returnTo} />
      <input type="hidden" name="workflow_id" value={candidates[0]?.workflow_id ?? ""} />
      <select
        id={id}
        name="node_id"
        defaultValue={selected}
        className="rounded-md border border-border/50 bg-transparent px-2 py-1 text-xs text-foreground focus:border-primary/60 focus:outline-none"
      >
        {candidates.map((c) => (
          <option key={c.node_id} value={c.node_id}>
            {c.symbol || c.node_id}
            {c.file ? ` · ${c.file}` : ""}
          </option>
        ))}
      </select>
      <button className="button px-2.5 py-1 text-xs" type="submit">
        Switch
      </button>
    </form>
  );
}
