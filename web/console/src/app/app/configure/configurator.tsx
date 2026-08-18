"use client";

import { useState } from "react";
import Link from "next/link";
import { Loader2, Play, RotateCcw, ShieldCheck } from "lucide-react";
import { Chip, Banner, Row, Section } from "@/components/primitives";
import { AxisRefusal } from "@/components/axisRefusal";
import { routes } from "@/lib/routes";
import { cx } from "@/lib/cx";

const EXAMPLE = JSON.stringify(
  {
    workflow_id: "wf-demo",
    source_revision: "rev1",
    ordering: ["plan", "draft"],
    nodes: {
      plan: { model_ref: "gpt-4o-mini@2024-07-18" },
      draft: {},
    },
    edges: [{ from: "plan", to: "draft", kind: "data" }],
  },
  null,
  2,
);

/**
 * The four override dimensions.
 *
 * They are rendered as a legend rather than as a tab strip, and that is the honest shape: a spec may
 * override any combination of them at once. A tab strip would say "pick one", which is not how the
 * Variant Spec works, and the design bundle's version of this screen makes exactly that mistake — it
 * offers Model / Prompt / Skills / Context as mutually exclusive buttons over a single editor whose
 * content they do not change.
 */
const DIMENSIONS = [
  { key: "model_ref", label: "Model", help: "Which model this call site uses." },
  { key: "prompt_ref", label: "Prompt", help: "Which registered prompt version it sends." },
  { key: "skill_refs", label: "Skills", help: "Which skills are bound to it." },
  { key: "context_policy", label: "Context", help: "How its context window is assembled." },
];

type Outcome =
  | { kind: "idle" }
  | { kind: "validating" }
  | { kind: "valid"; nodes: number; refs: string[] }
  | { kind: "invalid"; message: string; nodeId?: string; dimension?: string; ref?: string }
  | { kind: "submitting" }
  | {
      kind: "submitted";
      configHash: string;
      configHashDisplay: string;
      sourceRevision: string;
      transformStatus: string;
      runId?: string;
      rejectedNodeId?: string;
      rejectedDimension?: string;
    }
  | { kind: "refused"; axis: string; nodeId?: string; message: string }
  | { kind: "failed"; status: number; message: string };

export function Configurator() {
  const [spec, setSpec] = useState(EXAMPLE);
  const [variantId, setVariantId] = useState("");
  const [label, setLabel] = useState("");
  const [seed, setSeed] = useState("0");
  const [outcome, setOutcome] = useState<Outcome>({ kind: "idle" });

  function reset() {
    setSpec(EXAMPLE);
    setOutcome({ kind: "idle" }); // P2-4: resetting clears the previous result too.
  }

  async function validate() {
    setOutcome({ kind: "validating" });
    let parsed: unknown;
    try {
      parsed = JSON.parse(spec);
    } catch (error) {
      setOutcome({ kind: "invalid", message: `The spec is not valid JSON: ${(error as Error).message}` });
      return;
    }
    try {
      const res = await fetch("/api/console/spec/resolve", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(parsed),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setOutcome({
          kind: "invalid",
          message: body.error ?? `The platform rejected this spec (${res.status}).`,
          nodeId: body.node_id,
          dimension: body.dimension,
          ref: body.ref,
        });
        return;
      }
      setOutcome({ kind: "valid", nodes: body.nodes ?? 0, refs: body.refs ?? [] });
    } catch {
      setOutcome({
        kind: "invalid",
        message:
          "Could not reach the console to check this spec. This is a transport failure, not a rejection — nothing about the spec has been judged.",
      });
    }
  }

  async function submit() {
    if (!variantId.trim()) {
      setOutcome({
        kind: "invalid",
        message: "A variant id is required — it is the stable label this configuration is one version of.",
      });
      return;
    }
    if (!/^\d+$/.test(seed.trim())) {
      setOutcome({
        kind: "invalid",
        message:
          "The seed must be a non-negative integer. It is part of the run's identity, so a random default would make the platform non-reproducible.",
      });
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(spec);
    } catch (error) {
      setOutcome({ kind: "invalid", message: `The spec is not valid JSON: ${(error as Error).message}` });
      return;
    }
    setOutcome({ kind: "submitting" }); // P2-7: the button is disabled while this is in flight.
    try {
      const res = await fetch("/api/console/spec/submit", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          spec: parsed,
          variant_id: variantId.trim(),
          label: label.trim() || undefined,
          seed: Number(seed),
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        // A 400 that names a DIMENSION is a refusal, not a breakage: the platform read the spec, knew
        // exactly what was asked for, and declined that one axis by name. It gets its own outcome so it
        // can be said in those words — see AXIS_NOTE.
        if (res.status === 400 && typeof body.dimension === "string" && body.dimension !== "") {
          setOutcome({
            kind: "refused",
            axis: body.dimension,
            nodeId: body.node_id,
            message: body.error ?? "The platform declined this change without saying why, which is itself a defect.",
          });
          return;
        }
        setOutcome({ kind: "failed", status: res.status, message: body.error ?? `The platform returned ${res.status}.` });
        return;
      }
      setOutcome({
        kind: "submitted",
        configHash: body.config_hash,
        configHashDisplay: body.config_hash_display,
        sourceRevision: body.source_revision,
        transformStatus: body.transform_status,
        runId: body.run_id,
        rejectedNodeId: body.rejected_node_id,
        rejectedDimension: body.rejected_dimension,
      });
    } catch {
      setOutcome({
        kind: "failed",
        status: 0,
        message:
          "Could not reach the console. The outcome of this submission is unknown — it may or may not have started a run. Check the runs list before submitting again.",
      });
    }
  }

  const busy = outcome.kind === "submitting" || outcome.kind === "validating";

  return (
    <>
      <Section title="What you can override">
        <ul className="flex list-none flex-wrap gap-2 p-0" aria-label="The four override dimensions">
          {DIMENSIONS.map((dimension) => (
            <li key={dimension.key}>
              <Chip title={dimension.help}>{dimension.label}</Chip>
            </li>
          ))}
        </ul>
        <p className="hint">
          A dimension you leave out is <strong>not a missing value</strong>. It means no override: that
          call site runs exactly as it does today.
        </p>
      </Section>

      <Section title="Variant Spec">
        <div className="field field--wide">
          <label htmlFor="spec">Variant Spec (JSON)</label>
          <textarea
            className="textarea"
            id="spec"
            value={spec}
            spellCheck={false}
            rows={16}
            onChange={(event) => setSpec(event.target.value)}
          />
        </div>

        <div className="grid gap-4 sm:grid-cols-3">
          <div className="field">
            <label htmlFor="variant_id">Variant id</label>
            <input
              className="input mono"
              id="variant_id"
              value={variantId}
              onChange={(e) => setVariantId(e.target.value)}
              spellCheck={false}
              aria-describedby="variant_id-hint"
            />
            <span className="hint" id="variant_id-hint">
              Stable across versions of this configuration — it is what a board ranks.
            </span>
          </div>
          <div className="field">
            <label htmlFor="label">Label (optional)</label>
            <input className="input" id="label" value={label} onChange={(e) => setLabel(e.target.value)} />
          </div>
          <div className="field">
            <label htmlFor="seed">Seed</label>
            <input
              className="input mono"
              id="seed"
              value={seed}
              onChange={(e) => setSeed(e.target.value)}
              inputMode="numeric"
              aria-describedby="seed-hint"
            />
            <span className="hint" id="seed-hint">
              Part of the run&apos;s identity, with the config hash and the revision.
            </span>
          </div>
        </div>

        <Row>
          <button className="button" type="button" onClick={reset} disabled={busy}>
            <RotateCcw className="size-3.5" aria-hidden="true" />
            Reset to example
          </button>
          <button className="button" type="button" onClick={validate} disabled={busy}>
            {outcome.kind === "validating" ? (
              <Loader2 className="size-3.5 motion-safe:animate-spin" aria-hidden="true" />
            ) : (
              <ShieldCheck className="size-3.5" aria-hidden="true" />
            )}
            {outcome.kind === "validating" ? "Validating…" : "Validate only"}
          </button>
          <button className="button button--primary" type="button" onClick={submit} disabled={busy}>
            {outcome.kind === "submitting" ? (
              <Loader2 className="size-3.5 motion-safe:animate-spin" aria-hidden="true" />
            ) : (
              <Play className="size-3.5" aria-hidden="true" />
            )}
            {outcome.kind === "submitting" ? "Submitting…" : "Submit and run"}
          </button>
        </Row>

        {outcome.kind === "submitting" ? (
          <p className="hint" role="status">
            Resolving refs against the registries, generating the transform, and running a compiler over
            it. This is slower than a validate because it does real work.
          </p>
        ) : null}

        <Outcome outcome={outcome} />
      </Section>
    </>
  );
}

function Outcome({ outcome }: { outcome: Outcome }) {
  if (outcome.kind === "valid") {
    return (
      <Banner tone="info" title="Valid">
        <Row>
          <Chip tone="ok">valid</Chip>
          <Chip variant="count">{outcome.nodes} node(s)</Chip>
          {outcome.refs.length > 0 ? (
            outcome.refs.map((ref) => (
              <Chip key={ref} variant="hash" title="a resolved override">
                {ref}
              </Chip>
            ))
          ) : (
            <Chip>no overrides — every call site unchanged</Chip>
          )}
        </Row>
        <p className="hint">
          This checked the spec&apos;s structure only. It is not a green light: a spec that passes here
          can still fail to resolve against the registries, which only a submit can tell you.
        </p>
      </Banner>
    );
  }

  if (outcome.kind === "invalid") {
    return (
      <Banner tone="bad" title="This spec was rejected">
        <p>{outcome.message}</p>
        <Row>
          {outcome.nodeId ? <Chip>{outcome.nodeId}</Chip> : null}
          {outcome.dimension ? <Chip>{outcome.dimension}</Chip> : null}
          {outcome.ref ? (
            <Chip variant="hash" title="the offending reference">
              {outcome.ref}
            </Chip>
          ) : null}
        </Row>
      </Banner>
    );
  }

  if (outcome.kind === "refused") {
    // The refusal card is a shared component: the submit path and the /preview/wiring state page render
    // the SAME element, so what is checked in a browser is what a customer sees.
    return <AxisRefusal axis={outcome.axis} nodeId={outcome.nodeId} message={outcome.message} />;
  }

  if (outcome.kind === "failed") {
    return (
      <Banner tone="bad" title="The submission failed">
        <p className="mono">{outcome.message}</p>
        {outcome.status === 400 ? (
          <p>
            <strong>Nothing was persisted: no spec, no transform, no run.</strong>
          </p>
        ) : (
          <p className="hint">
            Whether anything was persisted is unknown at this status. Check the run and transform routes
            before resubmitting.
          </p>
        )}
      </Banner>
    );
  }

  if (outcome.kind === "submitted") {
    const rejected = outcome.transformStatus === "build-rejected";
    return (
      <Banner tone={rejected ? "warn" : "info"} title={rejected ? "The transform does not build" : "Submitted"}>
        <Row>
          <Chip variant="hash" title={outcome.configHash}>
            config {outcome.configHashDisplay}
          </Chip>
          <Chip>rev {outcome.sourceRevision}</Chip>
          {outcome.rejectedNodeId ? <Chip>{outcome.rejectedNodeId}</Chip> : null}
          {outcome.rejectedDimension ? <Chip>{outcome.rejectedDimension}</Chip> : null}
        </Row>
        {rejected ? (
          <p>
            The transform was generated and reviewed, and then it did not build — so it was never run.
            There is no run to watch.{" "}
            <Link
              className={cx("text-primary underline underline-offset-2")}
              href={routes.transform(outcome.configHash, outcome.sourceRevision)}
            >
              Read the build log
            </Link>
            .
          </p>
        ) : (
          <p className="flex flex-wrap gap-3">
            <Link
              className="text-primary underline underline-offset-2"
              href={routes.transform(outcome.configHash, outcome.sourceRevision)}
            >
              Review the diff
            </Link>
            {outcome.runId ? (
              <>
                <Link className="text-primary underline underline-offset-2" href={routes.run(outcome.runId)}>
                  Inspect the run
                </Link>
                <Link className="text-primary underline underline-offset-2" href={routes.runLive(outcome.runId)}>
                  Watch it live
                </Link>
              </>
            ) : null}
          </p>
        )}
      </Banner>
    );
  }

  return null;
}
