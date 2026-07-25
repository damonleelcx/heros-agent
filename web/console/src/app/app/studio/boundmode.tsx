"use client";

import { Card, Chip, Banner, Row } from "@/components/primitives";
import { cx } from "@/lib/cx";

/**
 * Bound-mode surfaces (P10 §10). These render what `bound` apply mode makes visible: a node's apply
 * mode, the EFFECTIVE resolved values (not the indirection), the verified-vs-unverified distinction,
 * and the degraded resolver state.
 *
 * Two distinctions here are load-bearing and must never collapse:
 *   - "someone selected this" (unverified) and "this was proven better" (verified) must render
 *     DIFFERENTLY (task 10.2) — collapsing them would destroy the distinction P5.5 exists to create.
 *   - a bound node shows its resolved VALUES, never the pointer (task 10.1) — a reviewer approves a
 *     configuration, not `agentcfg.Node("n").Model()`.
 */

export type BoundNode = {
  nodeId: string;
  applyMode: "inline" | "bound";
  // Effective resolved values (from the binding document), rendered instead of the indirection.
  modelId?: string;
  params?: Record<string, unknown>;
  promptTemplate?: string;
  literalBindings?: Record<string, string>;
  envBindings?: Record<string, string>;
  // exprBindings/inputBindings are call-site STRUCTURE — shown as needing a new change, never as data.
  exprBindings?: Record<string, string>;
  inputBindings?: Record<string, string>;
  // verified is true iff the resolved config carries a matching verified-delta record.
  verified: boolean;
};

export type ResolverHealth = {
  degraded: boolean;
  failedSource?: string;
  reason?: string;
  resolvedConfigHash: string;
  unverified: boolean;
};

/** VerifiedBadge renders "proven better" and "someone selected this" as visibly different states. */
export function VerifiedBadge({ verified }: { verified: boolean }) {
  return verified ? (
    <Chip tone="ok" title="A verified delta proved this configuration better">
      Verified — proven better
    </Chip>
  ) : (
    <Chip tone="warn" title="Someone selected this; it has no verified delta">
      Unverified — selected, not proven
    </Chip>
  );
}

/** DegradedBanner renders the resolver's degraded state, naming which source failed (task 10.3). */
export function DegradedBanner({ health }: { health: ResolverHealth }) {
  if (!health.degraded) return null;
  return (
    <Banner tone="warn" title="Configuration resolver is degraded">
      The <span className="font-mono">{health.failedSource ?? "override"}</span> source could not be
      adopted{health.reason ? ` (${health.reason})` : ""}. The last known-good configuration is in force —
      the resolver did not fall back to an empty or default configuration. Resolved config hash:{" "}
      <span className="font-mono">{health.resolvedConfigHash.slice(0, 12)}</span>.
    </Banner>
  );
}

/** BoundNodePanel renders one node's apply mode, effective values, verified state, and the honest
 *  per-node runtime-changeable statement (tasks 10.1, 10.2, 10.4). */
export function BoundNodePanel({ node }: { node: BoundNode }) {
  const bound = node.applyMode === "bound";
  return (
    <Card className={cx(bound ? "border-primary/30" : "")}>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <Row>
          <Chip variant="hash" title={node.nodeId}>
            {node.nodeId}
          </Chip>
          <Chip tone={bound ? "info" : "neutral"}>{node.applyMode}</Chip>
        </Row>
        {bound ? <VerifiedBadge verified={node.verified} /> : null}
      </div>

      {bound ? (
        <div className="flex flex-col gap-3">
          <dl className="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-1 text-sm">
            {node.modelId ? (
              <>
                <dt className="text-muted-foreground">model</dt>
                <dd className="font-mono text-foreground">{node.modelId}</dd>
              </>
            ) : null}
            {node.params && Object.keys(node.params).length > 0 ? (
              <>
                <dt className="text-muted-foreground">params</dt>
                <dd className="font-mono text-foreground">{JSON.stringify(node.params)}</dd>
              </>
            ) : null}
          </dl>

          {node.promptTemplate ? (
            <div>
              <span className="text-xs text-muted-foreground">prompt template (effective)</span>
              <pre className="mt-1 overflow-x-auto whitespace-pre-wrap rounded-lg border border-border bg-canvas p-3 font-mono text-xs text-foreground">
                {node.promptTemplate}
              </pre>
            </div>
          ) : null}

          <BindingList label="literal (data — runtime-changeable)" bindings={node.literalBindings} />
          <BindingList label="env (data — runtime-changeable)" bindings={node.envBindings} />
          <BindingList label="expr (call-site — needs a new change)" bindings={node.exprBindings} muted />
          <BindingList label="input (call-site — needs a new change)" bindings={node.inputBindings} muted />

          <RuntimeChangeableForBound />
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          Inline mode: changing any configured fact requires a new source change.
        </p>
      )}
    </Card>
  );
}

function BindingList({
  label,
  bindings,
  muted,
}: {
  label: string;
  bindings?: Record<string, string>;
  muted?: boolean;
}) {
  const entries = Object.entries(bindings ?? {});
  if (entries.length === 0) return null;
  return (
    <div>
      <span className={cx("text-xs", muted ? "text-muted-foreground/70" : "text-muted-foreground")}>{label}</span>
      <div className="mt-1 flex flex-wrap gap-1">
        {entries.map(([slot, value]) => (
          <Chip key={slot} variant="count" title={`${slot} = ${value}`}>
            {slot}={value}
          </Chip>
        ))}
      </div>
    </div>
  );
}

/** RuntimeChangeableForBound states the honest boundary for a bound node (task 10.4). */
function RuntimeChangeableForBound() {
  return (
    <div className="rounded-lg border border-border bg-canvas p-3 text-sm text-muted-foreground">
      <span className="font-medium text-foreground">Runtime-changeable: </span>
      the model, its parameters, the prompt version, and <span className="font-mono">literal</span>/
      <span className="font-mono">env</span> bindings — edit the binding document, no new codemod.{" "}
      <span className="font-medium text-foreground">Needs a new change: </span>
      the wiring, the skills, the context policy, and <span className="font-mono">expr</span>/
      <span className="font-mono">input</span> bindings — they name things in the program's lexical scope.
    </div>
  );
}
