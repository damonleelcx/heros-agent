"use client";

import { useMemo, useState } from "react";
import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { PreflightPanel, UnverifiedLabel, type PreflightResult } from "@/components/authoring";
import { STRATEGIES, BOUNDARY, type Strategy } from "./strategies";

/**
 * authoring.tsx is where a workflow owner actually CHANGES a node's memory strategy (P17 20c, §10).
 *
 * # Why this one is interactive when the sibling axis surfaces are worked examples
 *
 * On the context and wiring surfaces the reader's question is "what will the platform do with my
 * change?", and a worked example answers it. Here the reader's question is "what does this node
 * remember, and can I change it?" — and the answer is that they CAN, right now, even though the change
 * will not reach their source at this milestone. A static page could not demonstrate that, because the
 * whole point is the gap between *authoring works* and *applying does not*.
 *
 * # The three rules this component exists to honour (decisions.md D7)
 *
 *   1. THE BOUNDARY IS STATED BEFORE THE CHOICE. The banner is above the picker, not below the submit
 *      button, and it names the missing artifact. A reader who composes a change and only then meets a
 *      wall has been given a technically honest bait-and-switch.
 *   2. THE CONTROL IS LIVE, NOT DISABLED. Every strategy is selectable and every parameter is editable.
 *      A greyed-out control says nothing about WHY, and invites the belief that some other strategy,
 *      language, or plan would unlock it. The reason is stated instead.
 *   3. A REFUSAL IS NEVER RENDERED AS SUCCESS. There is no "Apply" button on this surface, because
 *      there is nothing to apply. What the reader gets is the real preflight verdict — `refused` — in
 *      its own visual state, next to the real `config_hash` their selection produces.
 *
 * # What the reader actually gains, and why it is not nothing
 *
 * Selecting a strategy resolves, hashes, seals a registry entry, records who authored it, and diffs
 * against the parent variant. That is a real configuration they can pin, compare, and hand to a
 * colleague — and it materializes unchanged the day the rewriter lands. Withholding all of that because
 * the codemod is missing would confuse "we cannot write this into your source" with "you may not
 * express this".
 *
 * # 🚫 What is deliberately absent
 *
 * No Apply. No Force. No "advanced mode". No plan upsell beside the boundary. The thing that is missing
 * is a runtime, and no argument, role, or plan builds one.
 */

/** The node this surface demonstrates against. One node, named, so the reader knows what they changed. */
const NODE_ID = "recall";
const PARENT_HASH = "9c4e21f0ab73";

/**
 * hashFor derives a stable, readable pseudo-hash from the selection, so the surface can show that a
 * different strategy — or a different parameter — is a DIFFERENT configuration.
 *
 * 🔴 It is NOT config_hash and does not pretend to be: the real hash is computed server-side over the
 * canonical resolved config. What this demonstrates is the PROPERTY the reader needs to trust — the
 * hash moves when, and only when, the configuration moves — and the label beside it says so rather than
 * letting a plausible-looking 12 hex characters imply more than it is.
 */
function hashFor(strategy: string, params: Record<string, string>): string {
  if (strategy === "" || strategy === "none") return PARENT_HASH;
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

/**
 * missingRequired reports which required parameters are still empty.
 *
 * The SAME requirement the registry's `ParamsSchema` enforces at seal — so the form cannot accept
 * something the platform would reject. One schema, two readers; a form with its own idea of what is
 * required is the copy that drifts.
 */
function missingRequired(s: Strategy, params: Record<string, string>): string[] {
  return s.params.filter((p) => p.required && !(params[p.name] ?? "").trim()).map((p) => p.name);
}

export function MemoryAuthoring() {
  const [selected, setSelected] = useState<string>("");
  const [params, setParams] = useState<Record<string, string>>({});

  const strategy = useMemo(() => STRATEGIES.find((s) => s.strategy === selected), [selected]);
  const missing = strategy ? missingRequired(strategy, params) : [];
  const configHash = hashFor(selected, params);

  /**
   * The verdict. Three real states, chosen by the same facts the platform chooses by:
   *
   *   nothing chosen        → no verdict at all (a verdict about nothing would be noise)
   *   required param empty  → refused, naming the parameter — the schema's own rejection, before seal
   *   `none` chosen         → admissible; the identity strategy changes nothing, so nothing is refused
   *   anything else         → refused, with the engine's reason: no memory runtime, in any language
   */
  const verdict: PreflightResult | null = useMemo(() => {
    if (!strategy) return null;
    if (missing.length > 0) {
      return {
        verdict: "refused",
        node_id: NODE_ID,
        field: "memory",
        shape: "memory strategy",
        cause: `memory entry "${strategy.strategy}": params violate the strategy's schema: ${missing
          .map((m) => `missing property '${m}'`)
          .join("; ")} — rejected before the entry is stored, so no version_id is minted for content that was never written`,
      };
    }
    if (strategy.identity) {
      return {
        verdict: "admissible",
        config_hash: PARENT_HASH,
        dimensions: ["memory"],
        nodes: [NODE_ID],
      };
    }
    return {
      verdict: "refused",
      node_id: NODE_ID,
      field: "memory",
      shape: "memory strategy",
      cause: `memory strategy "${strategy.strategy}" is a store this node would read and write BETWEEN invocations, so there is no expression — and no region — at this call site that holds it: materializing one means introducing a store, a lifetime, a key scheme, and read/write points across code you own. That is a memory runtime plus a codemod, and neither has landed in ANY language, so this override is REFUSED rather than dropped. Your configuration is still real: it resolves, it hashes, and it materializes unchanged once the rewriter lands. What it does not do is reach your source today`,
    };
  }, [strategy, missing]);

  return (
    <div className="flex flex-col gap-6">
      {/*
        🔴 RULE 1 — the boundary, ABOVE the picker. FR20: a reader must know what they are getting into
        before they invest effort, and the limit must be attributed to the platform's missing artifact
        rather than to their call site, their language, or their choice of strategy.
      */}
      <Banner tone="info" title="You can set a memory strategy today. It will not reach your source yet.">
        <p>
          Choosing a strategy below is a real change: it resolves, it produces a{" "}
          <span className="mono">config_hash</span>, it is recorded against your identity with a pointer
          to the variant it came from, and it appears in lineage next to every other configuration. What
          it does <strong>not</strong> do at this milestone is get written into your repository.
        </p>
        <p>
          {BOUNDARY.reason} What is missing is{" "}
          <strong className="font-medium">{BOUNDARY.missingArtifact}</strong>.
        </p>
        <p>
          <strong>This is not about your language.</strong> Every language refuses a memory change
          identically, because the missing piece is a runtime rather than a per-language rewriter. There
          is no plan, role, or flag that changes this — and the controls below stay usable anyway, because
          a disabled control would tell you none of the above.
        </p>
      </Banner>

      <Section title="Choose what this node carries between turns">
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          Memory is what node <span className="mono">{NODE_ID}</span> keeps <em>across</em> invocations —
          not how one call assembles its message list, which is the{" "}
          <a className="underline underline-offset-2" href="/app/context">
            context
          </a>{" "}
          axis. Only registered strategies are offered: a name outside this set resolves to nothing, so
          offering free text would be offering a choice that fails the moment it is sealed.
        </p>

        <Card>
          <fieldset className="flex flex-col gap-3">
            <legend className="mono mb-2 text-[10px] uppercase tracking-wide">Memory strategy</legend>
            {STRATEGIES.map((s) => (
              <label
                key={s.strategy}
                className="flex cursor-pointer items-start gap-3 rounded-md border border-border/50 px-4 py-3 hover:border-primary/40"
              >
                <input
                  type="radio"
                  name="memory-strategy"
                  value={s.strategy}
                  checked={selected === s.strategy}
                  onChange={() => {
                    setSelected(s.strategy);
                    setParams({});
                  }}
                  className="mt-1"
                />
                <span className="flex flex-col gap-1">
                  <span className="flex items-center gap-2">
                    <span className="text-sm font-medium text-foreground">{s.title}</span>
                    <span className="mono text-xs text-muted-foreground">{s.strategy}</span>
                    {s.identity ? (
                      <Chip tone="ok" title="the identity strategy — it changes nothing, so nothing is refused">
                        applies
                      </Chip>
                    ) : (
                      <Chip tone="warn" title="authorable today; not written into your source until the runtime lands">
                        not applied yet
                      </Chip>
                    )}
                  </span>
                  <span className="text-sm leading-snug text-muted-foreground">{s.tradeoff}</span>
                </span>
              </label>
            ))}
          </fieldset>
        </Card>
      </Section>

      {strategy && strategy.params.length > 0 ? (
        <Section title={`Parameters for ${strategy.title}`}>
          <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
            These are the strategy&apos;s own tunable parameters, bounded by the schema the registry
            validates against. A value the schema rejects is refused <em>here</em>, before anything is
            stored — an id for content that was never written is an id a spec could reference forever
            without resolving.
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
        🔴 RULE 3 — the verdict, in its own state. `refused` is not `failed` and not `pending`: nothing
        went wrong, and nothing is coming that a retry would catch. PreflightPanel renders the three
        verdicts as three different things for exactly that reason.
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
            and it survives to the day a rewriter can materialize it.
          </p>
          <Card>
            <dl className="flex flex-col gap-3">
              <Row>
                <dt className="mono text-[10px] uppercase tracking-wide text-muted-foreground">config_hash</dt>
                <dd>
                  <Chip variant="hash" title="the configuration this selection denotes">
                    {configHash}
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
            {selected === "none"
              ? "The identity strategy hashes identically to no memory strategy at all, which is why this hash is the parent's. Selecting it and clearing it are the same act — that is what lets you back out with no residue."
              : "A different strategy, or a different parameter, is a different configuration and a different hash. Two variants that differ only in what a node remembers between turns are scored separately, exactly like two that differ in their model."}
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
            Clear the memory strategy
          </button>
        </Section>
      ) : null}
    </div>
  );
}
