"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Sparkles, RefreshCw } from "lucide-react";

import { Banner } from "@/components/primitives";

/**
 * AssessControls is the only place on this surface that SPENDS MONEY, and its shape says so.
 *
 * # Two buttons, not one with a modifier
 *
 * They do different things and cost different amounts:
 *
 *   Assess       idempotent. On a revision already assessed under this configuration the platform
 *                returns the stored report and makes NO provider call. Free, and safe to press.
 *   Re-analyse   ignores the pin, runs the inference again, and spends. Its result renders as a DIFF
 *                against the previous report, because "it changed" is only useful with "and here is
 *                which input moved".
 *
 * 🔴 The second is a separate control with its own word and its own explanation rather than a checkbox
 * beside the first. A checkbox is a state a reader can leave set, and the person who leaves it set is
 * whoever pressed it last — which turns a deliberate spend into a default.
 *
 * # Why this is a client component at all
 *
 * The two actions are POSTs with different bodies whose results replace the page. A form would work,
 * and it would lose the one thing that matters here: while a re-analysis is running, the button must
 * say so, because it is the only feedback that distinguishes "spending" from "nothing happened".
 */
export function AssessControls({ workflowId, hasPrior }: { workflowId: string; hasPrior: boolean }) {
  const router = useRouter();
  const [busy, setBusy] = useState<"assess" | "reinfer" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function run(reinfer: boolean) {
    setBusy(reinfer ? "reinfer" : "assess");
    setError(null);
    const res = await fetch("/api/console/assessments", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ workflow_id: workflowId, reinfer }),
    });
    let data: { error?: string } = {};
    try {
      data = (await res.json()) as typeof data;
    } catch {
      // A body-less refusal is still a refusal; the status decides.
    }
    setBusy(null);
    if (!res.ok) {
      // 🔴 The platform's sentence verbatim. Its refusals name what was missing — a workflow with no
      // source, a subsystem this deployment does not mount — and replacing them with "assessment
      // failed" would delete the only part a reader can act on.
      setError(data.error ?? "the assessment could not be run");
      return;
    }
    router.refresh();
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className="assess__run" disabled={busy !== null} onClick={() => run(false)}>
          <Sparkles className="size-4" aria-hidden="true" />
          {busy === "assess" ? "Assessing…" : hasPrior ? "Assess again" : "Assess this workflow"}
        </button>
        {hasPrior ? (
          <button type="button" className="assess__reinfer" disabled={busy !== null} onClick={() => run(true)}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {busy === "reinfer" ? "Re-analysing…" : "Re-analyse and diff"}
          </button>
        ) : null}
      </div>
      <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
        Assessing again on an unchanged revision costs nothing and returns the same nine findings — that
        is what makes this report a fact about your repository rather than about the day.{" "}
        {hasPrior ? "Re-analysing ignores that and asks the model again; it is the only control here that spends." : null}
      </p>
      {error ? <Banner tone="warn" title={error} /> : null}
    </div>
  );
}
