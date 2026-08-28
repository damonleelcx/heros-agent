"use client";

import { useId, useMemo, useState, type ReactNode } from "react";
import Link from "next/link";
import { Banner, Card, Chip, Row, Section } from "./primitives";
import { PreflightPanel, UnverifiedLabel, type PreflightResult } from "./authoring";
import { STATE_LABEL, STATE_TONE } from "@/lib/assessment";
import { NOT_CONNECTED, AXIS_DOC, subjectLabel, type AxisSubject } from "@/lib/axisSubject";
import type { AxisVocabulary as AxisVocabularyType } from "@/lib/axisKit";

/**
 * editorKit.tsx is the ONE editor every axis surface is built from (P37 FR5, design D7).
 *
 * # 🔴 It was EXTRACTED, not designed
 *
 * Every part below comes from `/app/memory`'s authoring panel, which already worked. That panel encodes
 * three decisions that are easy to lose in a rewrite and expensive to lose:
 *
 *   1. THE BOUNDARY IS STATED ABOVE THE CHOICE. A reader who composes a change and only then meets a
 *      wall has been given a technically honest bait-and-switch.
 *   2. THE CONTROL IS LIVE, NOT DISABLED. A greyed-out control says nothing about WHY, and invites the
 *      belief that some other strategy, language or plan would unlock it. The reason is stated instead.
 *   3. A REFUSAL IS NEVER RENDERED AS SUCCESS. The reader gets the real verdict in its own visual state.
 *
 * Rewriting from scratch re-decides all three, and (2) is the one a fresh implementation gets wrong —
 * disabling a control is what every component library makes easiest.
 *
 * This is also the THIRD occurrence of the pattern (context, memory, harness), which is the point at
 * which this repository's own rule says to abstract rather than keep copying.
 *
 * # 🔴 What the kit deliberately does NOT do
 *
 * It computes nothing. `config_hash`, the diff and the verdict are server-side and rendered as received
 * (NFR7.3). The panel this was extracted from had a local `hashFor()` producing a plausible-looking
 * twelve hex characters, and its own comment admitted it was not `config_hash`. That function has no
 * destination on the reading surface because the thing it warned about stops existing here: an editor is
 * a place to COMPOSE a value, never a place to COMPUTE one.
 */

// ── The vocabulary an axis binds a picker to ──────────────────────────────────────────────────────
//
// 🔴 The TYPES and the schema derivation live in `@/lib/axisKit`, which carries no `"use client"`
// directive, and they are re-exported here so a call site that already imports the kit does not need a
// second import.
//
// This is not tidiness. `paramsFromSchema` was declared in THIS file, and `lib/axisVocabulary.ts` — a
// server module — called it while building the memory picker. Next.js allows the import and fails at
// REQUEST TIME:
//
//   Attempted to call paramsFromSchema() from the server but paramsFromSchema is on the client.
//
// The build was green, every source test passed, and `/app/memory` answered 500 for every reader. It was
// found by the rendered acceptance run (`tests/p37-acceptance.test.mjs`) and by nothing else, which is
// the whole argument for having one.
// 🚫 NOT re-exported. A `export { paramsFromSchema } from "@/lib/axisKit"` through THIS file still
// marks the value as client — the directive belongs to the module doing the exporting, so a re-export
// launders nothing. That is exactly how the defect survived its first fix. Server callers import from
// `@/lib/axisKit` directly; the TYPES are re-exported below, because a type has no runtime and cannot
// cross a boundary at all.
export type { ParamField, AxisOption, AxisVocabulary } from "@/lib/axisKit";

// ── The subject, always named ─────────────────────────────────────────────────────────────────────

/**
 * SubjectName renders which node this surface is bound to (FR1, FR3).
 *
 * 🔴 Rendered even when there was exactly ONE candidate. Being told which node was chosen is not the
 * same as being defaulted into one, and the difference is R4 — a reader editing something they did not
 * mean to.
 */
export function SubjectName({
  subject,
  candidates,
  sole,
}: {
  subject: AxisSubject;
  candidates: AxisSubject[];
  sole: boolean;
}) {
  return (
    <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span className="caption">editing</span>
      <Chip variant="hash" title={`${subject.workflow_id} · ${subject.node_id}`}>
        {subjectLabel(subject, candidates)}
      </Chip>
      {subject.file ? <span className="mono">{subject.file}</span> : null}
      {sole ? (
        <span>the only node reported for this workflow — chosen without asking, and named so you can check</span>
      ) : null}
    </span>
  );
}

// ── not_connected: the fourth state ───────────────────────────────────────────────────────────────

/**
 * NotConnected is the state an axis surface renders with no source to read (FR4, D-37.5).
 *
 * 🔴 The reader's data position contains NOTHING. No sample node, no fixture value, no demonstration
 * diff. A demonstration node dressed as theirs is worse than an empty screen, because they cannot tell
 * which one they are looking at — and the whole point of this phase is that they no longer have to.
 *
 * Both links are here for a reason each. The connection flow is the action; the reading surface is where
 * the explanation went, and the disconnected reader IS the first-time reader (PRD §4). Sending them to
 * the document is what makes moving the explanation an improvement for them rather than a loss.
 */
export function NotConnected({ axis }: { axis: string }) {
  const doc = AXIS_DOC[axis];
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-dashed border-border bg-card/50 p-5">
      <p className="text-sm font-medium text-foreground">{NOT_CONNECTED.heading}</p>
      <p className="hint">
        The missing input is <strong className="font-medium">{NOT_CONNECTED.missingInput}</strong>.{" "}
        {NOT_CONNECTED.body}
      </p>
      <Row>
        <Link className="prose-link" href={NOT_CONNECTED.connectHref}>
          {NOT_CONNECTED.connectLabel}
        </Link>
        {doc ? (
          <Link className="prose-link" href={doc}>
            {NOT_CONNECTED.readLabel}
          </Link>
        ) : null}
      </Row>
    </div>
  );
}

/**
 * ReadOn is the ONE link a working surface carries to the reading-surface section its explanation moved
 * to (FR10, task 4.4).
 *
 * 🔴 A LINK, and never a tooltip, an accordion or a modal (FR11). Those are the three ways text gets
 * hidden while appearing to be kept: a tooltip is unreachable by keyboard on half the controls that have
 * one, an accordion is a paragraph nobody opens, and a modal is a paragraph that interrupts. The
 * destination is a document with a URL that a `finding` can link to.
 *
 * Every `href` this renders is checked by fence 6.10 — a destination that does not resolve fails the
 * build, because a link to a section that does not exist yet is the specific 404 nobody reports.
 */
export function ReadOn({ href, children }: { href: string; children: ReactNode }) {
  return (
    <p className="hint">
      <Link className="prose-link" href={href}>
        {children}
      </Link>
    </p>
  );
}

// ── The current value, or the absence of one ──────────────────────────────────────────────────────

/** CurrentValueProps is one axis value as the platform resolved it for the reader's node. */
export type CurrentValueProps = {
  state: "measured" | "observed" | "not_measured" | "refused";
  current?: string;
  detail?: string;
  missingInput?: string;
  because?: string;
};

/**
 * CurrentValue renders what this node does on this axis today — or `not_measured` with its named input.
 *
 * 🔴 FR14: absence is DRAWN, never omitted and never rendered as zero. The named missing input is the
 * only actionable part of a `not_measured`, so it is rendered as text beside the state rather than as a
 * tooltip on it.
 *
 * The four state words come from `lib/assessment.ts` — the shared vocabulary (FR8). A surface that
 * introduced a fifth word would be the fifth vocabulary in a product that has spent three phases
 * converging on one.
 */
export function CurrentValue({ state, current, detail, missingInput, because }: CurrentValueProps) {
  return (
    <div className="flex flex-col gap-2">
      <Row>
        <Chip tone={STATE_TONE[state]} title="how this value was established">
          {STATE_LABEL[state]}
        </Chip>
        {state === "observed" && current ? (
          <span className="mono text-sm text-foreground">{current}</span>
        ) : null}
        {detail ? <span className="text-xs text-muted-foreground">{detail}</span> : null}
      </Row>
      {state === "not_measured" ? (
        <p className="hint">
          <strong className="font-medium">Missing: </strong>
          <span className="mono">{missingInput ?? "unnamed"}</span>
          {because ? <> — {because}</> : null}
        </p>
      ) : null}
    </div>
  );
}

// ── The kit ───────────────────────────────────────────────────────────────────────────────────────

export type AxisEditorProps = {
  axis: string;
  subject: AxisSubject;
  vocabulary: AxisVocabularyType;
  /** boundary is stated ABOVE the picker, always. Pass null only for an axis with no boundary. */
  boundary?: ReactNode;
  /** current is the node's value today — the baseline this change is FROM. */
  current: CurrentValueProps;
  /** parentHash is the variant this change would derive from, shown in the preflight. */
  parentVariantId?: string;
};

/**
 * AxisEditor composes the kit: boundary → picker → params → preflight → save → `unverified`.
 *
 * # Why the preflight is a round trip rather than a local computation
 *
 * NFR7.3, and it is not negotiable: the browser derives nothing. The `config_hash` and the diff against
 * the parent are what the reader will pin, compare and hand to a colleague, and a number the browser
 * made up is a number that will disagree with the platform's exactly once — on the day somebody relies
 * on it.
 */
export function AxisEditor({
  axis,
  subject,
  vocabulary,
  boundary,
  current,
  parentVariantId,
}: AxisEditorProps) {
  const [selected, setSelected] = useState<string>("");
  const [params, setParams] = useState<Record<string, string>>({});
  const [result, setResult] = useState<PreflightResult | null>(null);
  const [saved, setSaved] = useState<{ config_hash: string; verification_state: string } | null>(null);
  const [pending, setPending] = useState(false);
  const [transportError, setTransportError] = useState<string | null>(null);
  const groupId = useId();

  const option = useMemo(
    () => vocabulary.options.find((o) => o.id === selected),
    [vocabulary.options, selected],
  );

  /**
   * missingRequired is the SAME requirement the registry's params schema enforces at seal, applied in
   * the form so it cannot accept something the platform would reject. One schema, two readers; a form
   * with its own idea of what is required is the copy that drifts.
   */
  const missing = option ? option.params.filter((p) => p.required && !(params[p.name] ?? "").trim()) : [];

  async function post<T>(url: string): Promise<T | null> {
    setTransportError(null);
    setPending(true);
    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "content-type": "application/json", accept: "application/json" },
        body: JSON.stringify({
          workflow_id: subject.workflow_id,
          parent_variant_id: parentVariantId ?? "",
          node_id: subject.node_id,
          axis,
          selection: selected,
          params,
        }),
      });
      const data = (await response.json().catch(() => null)) as T & { error?: string };
      if (!response.ok) {
        // 🔴 A transport failure is NOT a refusal, and the two are never rendered as one. A refusal is a
        // computed answer with a cause; this is the platform not having answered, and retrying is safe.
        setTransportError(
          (data && data.error) ||
            `The platform did not answer this check (HTTP ${response.status}). Nothing was written, and retrying is safe.`,
        );
        return null;
      }
      return data;
    } catch {
      setTransportError(
        "The platform could not be reached, so this change was neither checked nor saved. Nothing was written.",
      );
      return null;
    } finally {
      setPending(false);
    }
  }

  async function preflight() {
    const data = await post<PreflightResult>("/api/console/authoring/preflight");
    if (data) setResult(data);
  }

  async function save() {
    const data = await post<{ config_hash: string; verification_state: string }>(
      "/api/console/authoring/submit",
    );
    if (data) setSaved(data);
  }

  return (
    <div className="flex flex-col gap-6">
      {/*
        🔴 RULE 1 — the boundary, ABOVE the picker (FR15). Not below the submit button, not in a tooltip,
        not on the reading surface. It changes with the reader's axis and node, so the moving rule keeps
        it here — and §8.1 reviews its wording as a customer-facing commitment rather than layout text.
      */}
      {boundary}

      <Section title={`What ${subjectLabel(subject)} does today`}>
        <CurrentValue {...current} />
      </Section>

      <Section title="Change it" aside={`${vocabulary.axis} vocabulary · ${vocabulary.setVersion}`}>
        <Card>
          <fieldset className="flex flex-col gap-3">
            <legend className="mono mb-2 text-[10px] uppercase tracking-wide">
              {vocabulary.axis}
            </legend>
            {vocabulary.options.map((o) => {
              const unavailable = Boolean(o.unavailableReason);
              const inputId = `${groupId}-${o.id}`;
              return (
                <label
                  key={o.id}
                  htmlFor={inputId}
                  className="flex cursor-pointer items-start gap-3 rounded-md border border-border/50 px-4 py-3 hover:border-primary/40"
                >
                  <input
                    id={inputId}
                    type="radio"
                    name={groupId}
                    value={o.id}
                    checked={selected === o.id}
                    /*
                      🔴 FR7 — an option this deployment cannot supply is DISABLED and says which service
                      it needs. This is the ONE place a disabled control is correct, and it is correct
                      because the reason travels with it. Rule 2 forbids disabling a control whose reason
                      is a boundary; it does not forbid disabling one that is genuinely not there.
                    */
                    disabled={unavailable}
                    onChange={() => {
                      setSelected(o.id);
                      setParams({});
                      setResult(null);
                      setSaved(null);
                    }}
                    className="mt-1"
                  />
                  <span className="flex flex-col gap-1">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium text-foreground">{o.title}</span>
                      <span className="mono text-xs text-muted-foreground">{o.id}</span>
                      {unavailable ? (
                        <Chip tone="unknown" title="this deployment does not supply the service it needs">
                          needs {o.unavailableReason}
                        </Chip>
                      ) : null}
                    </span>
                    {o.tradeoff ? (
                      <span className="text-sm leading-snug text-muted-foreground">{o.tradeoff}</span>
                    ) : null}
                  </span>
                </label>
              );
            })}
          </fieldset>
        </Card>
      </Section>

      {option && option.params.length > 0 ? (
        <Section title={`Parameters for ${option.title}`}>
          <Card>
            <div className="flex flex-col gap-4">
              {option.params.map((p) => {
                const fieldId = `${groupId}-param-${p.name}`;
                const errorId = `${fieldId}-error`;
                const invalid = p.required && !(params[p.name] ?? "").trim() && result !== null;
                return (
                  <div key={p.name} className="flex flex-col gap-1">
                    <label htmlFor={fieldId} className="flex items-center gap-2">
                      <span className="mono text-xs text-foreground">{p.name}</span>
                      {p.required ? (
                        <Chip tone="warn" title="the schema requires this parameter">
                          required
                        </Chip>
                      ) : (
                        <Chip title="optional; the entry has a defined behaviour without it">optional</Chip>
                      )}
                    </label>
                    <input
                      id={fieldId}
                      type="text"
                      value={params[p.name] ?? ""}
                      onChange={(e) => setParams({ ...params, [p.name]: e.target.value })}
                      placeholder={p.hint}
                      /*
                        🔴 NFR7.2 — a validation error is ASSOCIATED WITH ITS FIELD rather than announced
                        as a page banner. A banner tells a keyboard reader that something is wrong and
                        makes them hunt for which control; `aria-describedby` puts the sentence where the
                        focus already is.
                      */
                      aria-invalid={invalid || undefined}
                      aria-describedby={invalid ? errorId : undefined}
                      className="w-full rounded-md border border-border/50 bg-transparent px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:border-primary/60 focus:outline-none"
                    />
                    <span className="hint" id={invalid ? undefined : `${fieldId}-hint`}>
                      {p.hint}
                    </span>
                    {invalid ? (
                      <span className="hint" id={errorId}>
                        The schema requires <span className="mono">{p.name}</span>. It is refused at save
                        rather than stored empty, so no version id is minted for content that was never
                        written.
                      </span>
                    ) : null}
                  </div>
                );
              })}
            </div>
          </Card>
        </Section>
      ) : null}

      {selected ? (
        <Section title="What the platform says about this change">
          <Row>
            <button
              type="button"
              onClick={preflight}
              disabled={pending}
              className="rounded-md border border-border/50 px-4 py-2 text-sm text-foreground hover:border-primary/40 disabled:opacity-60"
            >
              {pending ? "Checking…" : "Check this change"}
            </button>
            <button
              type="button"
              onClick={save}
              disabled={pending || missing.length > 0}
              className="rounded-md border border-primary/40 px-4 py-2 text-sm text-foreground hover:border-primary disabled:opacity-60"
            >
              Save
            </button>
          </Row>
          {transportError ? (
            <Banner tone="warn" title="The check did not happen">
              <p>{transportError}</p>
            </Banner>
          ) : null}
          {/*
            🔴 RULE 3 — the verdict in its OWN state. `refused` is not `failed` and not `pending`: nothing
            went wrong, and nothing is coming that a retry would catch. `PreflightPanel` renders the three
            verdicts as three different things for exactly that reason, and it renders the engine's cause
            text VERBATIM (FR13) — a paraphrase would be a second, softer statement of a safety boundary.
          */}
          {result ? <PreflightPanel result={result} /> : null}
        </Section>
      ) : null}

      {saved ? (
        <Section title="What your change produced">
          <Card>
            <dl className="flex flex-col gap-3">
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">config_hash</dt>
                <dd>
                  {/*
                    Computed server-side and rendered as received. §6.6 proves this string is the one the
                    registry row and the variant row produce — a 200 is not evidence of a write.
                  */}
                  <Chip variant="hash" title="the configuration this selection denotes">
                    {saved.config_hash}
                  </Chip>
                </dd>
              </Row>
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">state</dt>
                <dd>
                  {/* 🔴 FR16 — `unverified` until the harness has run. Not described as an improvement. */}
                  <UnverifiedLabel state={saved.verification_state} />
                </dd>
              </Row>
            </dl>
          </Card>
        </Section>
      ) : null}
    </div>
  );
}
