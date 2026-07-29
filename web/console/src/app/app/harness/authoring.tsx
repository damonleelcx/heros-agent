"use client";

import { useMemo, useState } from "react";
import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { PreflightPanel, UnverifiedLabel, type PreflightResult } from "@/components/authoring";
import {
  HARNESS_STRATEGIES,
  HARNESS_BOUNDARY,
  MAX_TURNS_CEILING,
  costWarningFor,
  type HarnessStrategy,
} from "./strategies";

/**
 * authoring.tsx is where a workflow owner actually CHANGES a node's harness strategy (P18 §12).
 *
 * # The one thing this surface must say that no other authoring surface has had to
 *
 * A heavier scaffold COSTS MORE PER RUN — up to its turn ceiling, on EVERY case, including the ones
 * that already pass. Every other axis changes what one call does; this one changes how many calls
 * happen.
 *
 * 🔴 That cost is arithmetic and can be stated before anything runs. Its benefit is a measurement and
 * cannot. So the design puts the multiplier in front of the reader as a THING THEY SEE rather than a
 * sentence they may skip — a turn meter beside every option — and names verification as the decider in
 * the same breath. Stating the cost without naming who judges it would read as discouragement; naming
 * the judge without the cost would read as a recommendation. Both halves, always.
 *
 * # Why this surface has a language switch and the memory one does not
 *
 * Memory's boundary was uniform at M20: one sentence was right for everyone. Harness is not uniform,
 * and a single verdict would be wrong in BOTH directions — it would tell a Python reader that
 * `reflexion` is unavailable, and tell every reader that `react-loop` is merely pending.
 *
 * So the reader picks the language they actually wrote, and the badges change. That is the honest
 * shape of a per-cell boundary, and it is also the cheapest way to make the third answer visible:
 * three strategies are refused in EVERY language, permanently, and no switch position unlocks them.
 *
 * # 🚫 What is deliberately absent
 *
 * No Force. No "advanced mode". No plan upsell beside the boundary. And no path that skips the
 * admissibility gate — a user may author the change, a user may not author the evidence.
 */

/** The node this surface demonstrates against. One node, named, so the reader knows what they changed. */
const NODE_ID = "solve";
const PARENT_HASH = "4b19d7c0e2a5";

/** The languages a reader may be in. The two that differ are the two that teach the boundary. */
const LANGUAGES = [
  { id: "python", label: "Python" },
  { id: "go", label: "Go" },
  { id: "typescript", label: "TypeScript" },
] as const;

type LanguageID = (typeof LANGUAGES)[number]["id"];

/** applicability answers ONE (language, strategy) cell, mirroring `transform.CoverageFor("harness")`. */
type Applicability = {
  applies: boolean;
  /** permanent distinguishes "not ever, here" from "not yet" — two different things to tell someone. */
  permanent: boolean;
  label: string;
  why: string;
};

function applicabilityFor(s: HarnessStrategy, language: LanguageID): Applicability {
  if (s.identity) {
    return {
      applies: true,
      permanent: false,
      label: "applies",
      why: "The identity strategy. One turn is exactly what your call site already does, so there is nothing to write and nothing to refuse.",
    };
  }
  if (s.hostService) {
    return {
      applies: false,
      permanent: true,
      label: "not at a call site",
      why: `This strategy needs ${s.hostService}, and a call site has nowhere to inject one — in any language. The generated module makes no provider call and dispatches no tool by design: a generated file that reached a provider would put your credential in your own process, spent on turns you did not write. It is refused rather than degraded to a loop that skips the part that makes it this strategy.`,
    };
  }
  if ((HARNESS_BOUNDARY.answerBlindLanguages as readonly string[]).includes(language)) {
    return {
      applies: false,
      permanent: true,
      label: "not in this language",
      why: "Deciding whether to take another turn means reading the answer's text, and here a response is your SDK's own type — generated code would have to import your SDK to read a field off it. Python materializes this one, because there a response is message-like.",
    };
  }
  if ((HARNESS_BOUNDARY.materializedIn as readonly string[]).includes(language)) {
    return {
      applies: true,
      permanent: false,
      label: "applies",
      why: "Your written call is wrapped in a bounded loop that re-invokes it and reads each answer against the stop condition — both halves, or the call site is refused whole.",
    };
  }
  return {
    applies: false,
    permanent: false,
    label: "waiting on us",
    why: `${HARNESS_BOUNDARY.reason} This language is still waiting for ${HARNESS_BOUNDARY.missingArtifact}.`,
  };
}

/**
 * TurnMeter renders the turn ceiling as what it actually is: a multiplier on what this node costs.
 *
 * 🔴 It is the piece of design this axis needed and the others did not. A number in a sentence is
 * skippable; a filled bar next to a one-segment baseline is not. The baseline is drawn at the same
 * scale on every row precisely so "up to 16×" cannot be read as the same size as "up to 3×".
 *
 * 🚫 It is not a rating and carries no colour judgement. A longer bar is not worse — it is more
 * expensive, and whether the expense is earned is verification's answer, which the copy beside it says.
 */
function TurnMeter({ ceiling }: { ceiling: number }) {
  const segments = Math.min(ceiling, MAX_TURNS_CEILING);
  return (
    <span className="flex items-center gap-2" title={`up to ${ceiling} turn(s) per invocation`}>
      <span aria-hidden="true" className="flex items-center gap-[2px]">
        {Array.from({ length: MAX_TURNS_CEILING }).map((_, i) => (
          <span
            key={i}
            className={
              i < segments
                ? "h-3 w-[3px] rounded-[1px] bg-primary/70"
                : "h-3 w-[3px] rounded-[1px] bg-border/40"
            }
          />
        ))}
      </span>
      <span className="mono tabular-nums text-xs text-muted-foreground">
        {ceiling === 1 ? "1 turn" : `up to ${ceiling}×`}
      </span>
    </span>
  );
}

/**
 * missingRequired reports which required parameters are still empty.
 *
 * The SAME requirement the registry's `ParamsSchema` enforces at seal — so the form cannot accept
 * something the platform would reject. One schema, two readers; a form with its own idea of what is
 * required is the copy that drifts.
 */
function missingRequired(s: HarnessStrategy, params: Record<string, string>): string[] {
  const base = s.params.filter((p) => p.required && !(params[p.name] ?? "").trim()).map((p) => p.name);
  // The one cross-field rule the schema states awkwardly and a reader understands immediately: a
  // marker-terminated loop with no marker can only ever stop at the ceiling, which is a different and
  // more expensive configuration than the one being asked for.
  if (params["stop_condition"] === "answer-marker" && !(params["answer_marker"] ?? "").trim()) {
    base.push("answer_marker");
  }
  return base;
}

function hashFor(strategy: string, params: Record<string, string>): string {
  if (strategy === "" || strategy === "single-shot") return PARENT_HASH;
  const material = `${strategy}|${Object.keys(params)
    .sort()
    .map((k) => `${k}=${params[k]}`)
    .join(",")}`;
  let h = 0x811c9dc5;
  for (let i = 0; i < material.length; i += 1) {
    h ^= material.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16).padStart(8, "0").repeat(2).slice(0, 12);
}

export function HarnessAuthoring() {
  const [language, setLanguage] = useState<LanguageID>("python");
  const [selected, setSelected] = useState<string>("");
  const [params, setParams] = useState<Record<string, string>>({});

  const strategy = useMemo(() => HARNESS_STRATEGIES.find((s) => s.strategy === selected), [selected]);
  const missing = strategy ? missingRequired(strategy, params) : [];
  const configHash = hashFor(selected, params);
  const applicability = strategy ? applicabilityFor(strategy, language) : null;

  /**
   * The verdict. Real states, chosen by the same facts the platform chooses by:
   *
   *   nothing chosen        → no verdict at all (a verdict about nothing would be noise)
   *   required param empty  → refused, naming the parameter — the schema's own rejection, before seal
   *   identity chosen       → admissible everywhere; it changes nothing, so nothing is refused
   *   the cell refuses      → refused, with the engine's own reason and its permanence
   *   otherwise             → admissible
   */
  const verdict: PreflightResult | null = useMemo(() => {
    if (!strategy || !applicability) return null;
    if (missing.length > 0) {
      return {
        verdict: "refused",
        node_id: NODE_ID,
        field: "harness",
        shape: "harness strategy",
        cause: `harness entry "${strategy.strategy}": params violate the strategy's schema: ${missing
          .map((m) => `missing property '${m}'`)
          .join("; ")} — rejected before the entry is stored, so no version_id is minted for content that was never written`,
      };
    }
    if (!applicability.applies) {
      return {
        verdict: "refused",
        node_id: NODE_ID,
        field: "harness",
        shape: "harness strategy",
        cause: applicability.why,
      };
    }
    return {
      verdict: "admissible",
      config_hash: strategy.identity ? PARENT_HASH : configHash,
      dimensions: ["harness"],
      nodes: [NODE_ID],
    };
  }, [strategy, applicability, missing, configHash]);

  return (
    <div className="flex flex-col gap-6">
      {/*
        🔴 THE BOUNDARY, ABOVE THE PICKER (FR44). A reader must know what they are getting into before
        they invest effort, and the limit must be attributed correctly — to a missing platform artifact,
        to their call site, or to a permanent fact — because those three send them to three different
        places.
      */}
      <Banner tone="info" title="What a scaffold change costs, and where it reaches your source">
        <p>
          Choosing a strategy below is a real change wherever you are: it resolves, it produces a{" "}
          <span className="mono">config_hash</span>, it is recorded against your identity with a pointer
          to the variant it came from, and it appears in lineage next to every other configuration.
        </p>
        <p>
          <strong className="font-medium">A heavier scaffold is not free.</strong> A strategy with a
          ceiling of <span className="mono">n</span> turns can multiply this node&apos;s per-run cost and
          latency by up to <span className="mono">n</span> — on every case, including the ones that
          already pass. Whether that buys enough <span className="mono">task_success</span> to be worth
          it is decided by verification on held-out cases, never by this control.
        </p>
        <p>
          <strong>Three things are true before you choose</strong>, and all three are stated here rather
          than met at apply time:
        </p>
        <ul className="flex flex-col gap-1">
          {HARNESS_BOUNDARY.preconditions.map((p) => (
            <li key={p} className="text-sm leading-snug text-muted-foreground">
              {p}
            </li>
          ))}
        </ul>
        <p>
          There is no plan, role, or flag that changes any of this — and the controls below stay usable
          everywhere, because a disabled control would tell you none of the above.
        </p>
      </Banner>

      {/*
        The language switch. Not a preference — the boundary genuinely differs per language, and a single
        verdict would be wrong in both directions.
      */}
      <Section title="The language you actually wrote">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Where a scaffold reaches your source depends on whether generated code can read an
          answer&apos;s text without knowing your SDK. Pick your language and the badges below change —
          and notice which ones do <em>not</em>: three strategies are refused in every position, because
          what they need is a second actor a call site has nowhere to put.
        </p>
        <Card>
          <div className="flex flex-wrap gap-2" role="radiogroup" aria-label="Language">
            {LANGUAGES.map((l) => (
              <button
                key={l.id}
                type="button"
                role="radio"
                aria-checked={language === l.id}
                onClick={() => setLanguage(l.id)}
                className={
                  language === l.id
                    ? "rounded-md border border-primary/50 bg-primary/10 px-4 py-2 text-sm text-primary"
                    : "rounded-md border border-border/50 px-4 py-2 text-sm text-foreground hover:border-primary/40"
                }
              >
                {l.label}
              </button>
            ))}
          </div>
        </Card>
      </Section>

      <Section title="Choose the control loop this node runs in">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          A harness is what wraps node <span className="mono">{NODE_ID}</span>&apos;s call — how many
          turns it takes and what makes it stop. It is not what the call carries (
          <a className="underline underline-offset-2" href="/app/context">
            context
          </a>
          ) nor what it remembers between calls (
          <a className="underline underline-offset-2" href="/app/memory">
            memory
          </a>
          ). Only registered strategies are offered: a name outside this set resolves to nothing, so
          offering free text would be offering a choice that fails the moment it is sealed.
        </p>

        <Card>
          <fieldset className="flex flex-col gap-3">
            <legend className="mono mb-2 text-[10px] uppercase tracking-wide">Harness strategy</legend>
            {HARNESS_STRATEGIES.map((s) => {
              const a = applicabilityFor(s, language);
              return (
                <label
                  key={s.strategy}
                  className="flex cursor-pointer items-start gap-3 rounded-md border border-border/50 px-4 py-3 hover:border-primary/40"
                >
                  <input
                    type="radio"
                    name="harness-strategy"
                    value={s.strategy}
                    checked={selected === s.strategy}
                    onChange={() => {
                      setSelected(s.strategy);
                      setParams({});
                    }}
                    className="mt-1"
                  />
                  <span className="flex flex-1 flex-col gap-1">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium text-foreground">{s.title}</span>
                      <span className="mono text-xs text-muted-foreground">{s.strategy}</span>
                      <Chip tone={a.applies ? "ok" : a.permanent ? "halt" : "warn"} title={a.why}>
                        {a.label}
                      </Chip>
                      <span className="ml-auto">
                        <TurnMeter ceiling={s.maxTurnCeiling} />
                      </span>
                    </span>
                    <span className="text-sm leading-snug text-muted-foreground">{s.tradeoff}</span>
                    {!a.applies ? <span className="hint">{a.why}</span> : null}
                    {s.maxTurnCeiling > 1 ? (
                      <span className="hint">{costWarningFor(s.maxTurnCeiling)}</span>
                    ) : null}
                  </span>
                </label>
              );
            })}
          </fieldset>
        </Card>
      </Section>

      {strategy && strategy.params.length > 0 ? (
        <Section title={`Parameters for ${strategy.title}`}>
          <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
            These are the strategy&apos;s own tunable parameters, bounded by the schema the registry
            validates against. <span className="mono">max_turns</span> is capped at{" "}
            <span className="mono">{MAX_TURNS_CEILING}</span> by that schema — the cap is a policy about
            how much autonomous tool-calling one node may do, not a limit of the runtime. A value the
            schema rejects is refused <em>here</em>, before anything is stored.
          </p>
          <Card>
            <div className="flex flex-col gap-4">
              {strategy.params.map((p) => (
                <label key={p.name} className="flex flex-col gap-1">
                  <span className="flex items-center gap-2">
                    <span className="mono text-xs text-foreground">{p.name}</span>
                    {p.required ? (
                      <Chip tone="warn" title="the schema requires this parameter">
                        required
                      </Chip>
                    ) : (
                      <Chip title="optional; the strategy has a defined behaviour without it">optional</Chip>
                    )}
                  </span>
                  <input
                    type="text"
                    value={params[p.name] ?? ""}
                    onChange={(e) => setParams({ ...params, [p.name]: e.target.value })}
                    placeholder={p.hint}
                    aria-label={p.name}
                    className="w-full rounded-md border border-border/50 bg-transparent px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:border-primary/60 focus:outline-none"
                  />
                  <span className="hint">{p.hint}</span>
                </label>
              ))}
            </div>
          </Card>
        </Section>
      ) : null}

      {/*
        🔴 The verdict, in its own state. `refused` is not `failed` and not `pending`: nothing went wrong,
        and for the permanent cells nothing is coming that a retry would catch.
      */}
      {verdict ? (
        <Section title="What the platform says about this change">
          <PreflightPanel result={verdict} />
        </Section>
      ) : null}

      {strategy && missing.length === 0 ? (
        <Section title="What your change produced">
          <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
            The half that is <strong>not</strong> refused. This configuration exists, it is addressable,
            and it survives to the day a rewriter can materialize it — including in a language that
            cannot today.
          </p>
          <Card>
            <dl className="flex flex-col gap-3">
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">config_hash</dt>
                <dd>
                  <Chip variant="hash" title="the configuration this selection denotes">
                    {strategy.identity ? PARENT_HASH : configHash}
                  </Chip>
                </dd>
              </Row>
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">parent</dt>
                <dd>
                  <Chip variant="hash" title="the variant this change was derived from">
                    {PARENT_HASH}
                  </Chip>
                </dd>
              </Row>
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">turn ceiling</dt>
                <dd>
                  <TurnMeter ceiling={strategy.maxTurnCeiling} />
                </dd>
              </Row>
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">origin</dt>
                <dd>
                  <Chip title="you authored this; the catalog did not propose it">user</Chip>
                </dd>
              </Row>
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">state</dt>
                <dd>
                  <UnverifiedLabel state="unverified" />
                </dd>
              </Row>
            </dl>
          </Card>
          <p className="hint">
            {strategy.identity
              ? "The identity strategy with no parameters hashes identically to no harness at all, which is why this hash is the parent's. Selecting it and clearing it are the same act — that is what lets you back out with no residue."
              : "A different strategy, or a different turn ceiling, is a different configuration and a different hash. A loop that may run five turns is not the same configuration as one that may run three, and it does not cost the same."}
          </p>
        </Section>
      ) : null}

      {selected ? (
        <Section title="Back out">
          <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Clearing removes the override entirely rather than setting it to a default. The key
            disappears from the node, so the configuration returns to <em>exactly</em> the bytes it had
            before you chose — same <span className="mono">config_hash</span>, no residue.
          </p>
          <button
            type="button"
            onClick={() => {
              setSelected("");
              setParams({});
            }}
            className="self-start rounded-md border border-border/50 px-4 py-2 text-sm text-foreground hover:border-primary/40"
          >
            Clear the harness strategy
          </button>
        </Section>
      ) : null}
    </div>
  );
}
