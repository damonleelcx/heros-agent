import type { ReactNode } from "react";

import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { Disclosure } from "@/components/figure";

/**
 * authoring.tsx renders the USER-AUTHORED change surface (P13 13c, section 11).
 *
 * # The one rule this file exists to hold: three states stay three
 *
 * Preflight answers with one of three verdicts, and a surface that renders two of them has already
 * lied. `admissible` and `refused` are the obvious pair. The third — `not_yet_measurable` — means the
 * platform has never measured the input a gate needs, and it is neither of the others:
 *
 *   - drawing it as a refusal ("this control is disabled") blames the reader's change for OUR missing
 *     measurement, and points them at their own configuration to look for a fault that is not there;
 *   - drawing it as admissible would assert that a safety check passed when it never ran.
 *
 * Those two mistakes lead a reader to opposite actions, which is exactly why the greyed-out control
 * that would express both is the wrong component. Each verdict gets its own shape, its own words, and
 * — for the third — the sentence that says what would make it measurable.
 *
 * # Why nothing here computes anything
 *
 * Every figure, verdict, interval and hash is rendered as received. The console derives NOTHING: a
 * client-side recomputation is a second source of truth for a statistical claim, and the day the two
 * disagree the reader has no way to tell which one is lying. This file therefore contains no
 * arithmetic on a metric, no comparison of two numbers, and no sorting by score.
 *
 * # Why the unverified label is a component and not a string
 *
 * An authored change may be applied without verification. It is then `unverified`, and that word has
 * to travel with it into every place a verified delta also appears — otherwise it reads as one. Making
 * it a component means the label cannot be forgotten at a call site that renders the change some other
 * way, and a test can assert that every authored-change render includes it.
 */

/** The three verdicts, exactly as the platform names them on the wire. */
export type PreflightVerdict = "admissible" | "refused" | "not_yet_measurable";

export type PreflightResult = {
  verdict: PreflightVerdict;
  cause?: string;
  node_id?: string;
  field?: string;
  shape?: string;
  missing_kind?: string;
  missing_subject?: string;
  config_hash?: string;
  dimensions?: string[];
  nodes?: string[];
  adapters?: string[];
};

/**
 * UnverifiedLabel is the word that travels with every authored change.
 *
 * 🚫 Deliberately NOT in the hazard palette. `--warn` and `--danger` are reserved for hazard — a
 * destructive control, an armed halt — because danger is only legible while it is rare. "Nobody has
 * measured this yet" is a normal, expected state of a perfectly good change; painting it red would
 * teach the reader to ignore red.
 */
export function UnverifiedLabel({ state }: { state: string }) {
  const unverified = state !== "verified";
  return (
    <Chip
      tone={unverified ? "neutral" : "ok"}
      title={
        unverified
          ? "This change was applied without a verification run. It is outside the verified-delta ledger and contributes nothing to any improvement or savings figure."
          : "A multi-seed evaluation produced a verdict for this change."
      }
    >
      {unverified ? "unverified" : "verified"}
    </Chip>
  );
}

/**
 * AuthoredChangeSummary renders one authored change. The unverified label is not optional here — it is
 * part of the summary, so a change cannot be rendered without it.
 */
export function AuthoredChangeSummary({
  changeId,
  configHash,
  axis,
  verificationState,
  actorId,
  forkedFrom,
}: {
  changeId: string;
  configHash: string;
  axis: string;
  verificationState: string;
  actorId?: string;
  forkedFrom?: string;
}) {
  return (
    <Card>
      <Row>
        <Chip variant="hash" title="the authored change">
          {changeId}
        </Chip>
        <Chip variant="hash" title="the configuration this change produced">
          {configHash}
        </Chip>
        <Chip title="the dimensions this change touches">{axis}</Chip>
        <UnverifiedLabel state={verificationState} />
      </Row>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {verificationState === "verified"
          ? "A verification run produced a verdict for this change."
          : "Applied, not verified. No quality or cost claim is attached to this change, and it contributes nothing to any improvement or savings figure until a multi-seed evaluation has run."}
      </p>
      {actorId ? (
        <p className="text-sm text-muted-foreground">
          Authored by <span className="mono">{actorId}</span>
          {forkedFrom ? (
            <>
              , from proposal <span className="mono">{forkedFrom}</span>. The originating operator is not
              credited with this change&rsquo;s outcome.
            </>
          ) : (
            "."
          )}
        </p>
      ) : null}
    </Card>
  );
}

/**
 * PreflightPanel renders the verdict for a draft, before anything is submitted.
 *
 * Each branch is a separate component rather than one component with a tone prop, because the three
 * cases do not differ only in colour: they differ in what they say, what they name, and what the
 * reader should do next. A single shape with three tones would invite a future edit to drop the parts
 * that differ and keep the colour, which is how three states become two.
 */
export function PreflightPanel({ result }: { result: PreflightResult }) {
  switch (result.verdict) {
    case "admissible":
      return <PreflightAdmissible result={result} />;
    case "refused":
      return <PreflightRefused result={result} />;
    case "not_yet_measurable":
      return <PreflightNotYetMeasurable result={result} />;
    default:
      // An unrecognised verdict is shown as itself rather than mapped onto one of the three. A console
      // that quietly renders an unknown answer as "refused" turns a protocol change into a wrong screen.
      return (
        <Banner tone="info" title="This verdict is not one this console recognises">
          <p>
            The platform answered <span className="mono">{String(result.verdict)}</span>. It is shown
            verbatim rather than mapped onto a verdict this console does understand.
          </p>
        </Banner>
      );
  }
}

function PreflightAdmissible({ result }: { result: PreflightResult }) {
  return (
    <Banner tone="info" title="This change can be applied">
      <Row>
        {result.config_hash ? (
          <Chip variant="hash" title="the configuration this change would produce">
            {result.config_hash}
          </Chip>
        ) : null}
        {(result.dimensions ?? []).map((d) => (
          <Chip key={d} title="a dimension this change touches">
            {d}
          </Chip>
        ))}
      </Row>
      <p>
        Every gate that applies to this change passed on evidence. Applying it produces a reviewable
        diff, and the change is recorded as <strong>unverified</strong> until a multi-seed evaluation
        runs.
      </p>
      {result.adapters?.length ? <AdapterPreview adapters={result.adapters} /> : null}
    </Banner>
  );
}

/**
 * AdapterPreview shows a component the platform would INSERT to make an ordering legal — before the
 * reader submits, never only in the diff afterwards.
 *
 * An indirection never hides a value from review. A reader who reorders two nodes and later receives a
 * diff containing a component they never saw proposed cannot meaningfully review it, and the fact that
 * the gate proved the adapter drops nothing required does not change that: they did not agree to it.
 */
function AdapterPreview({ adapters }: { adapters: string[] }) {
  return (
    <div>
      <p>
        <strong>This ordering is legal only because an adapter would be inserted.</strong> It ships as
        generated source in the same diff — it is not a hidden runtime coercion — and it is shown here so
        the change you submit is the change you saw.
      </p>
      <Row>
        {adapters.map((a) => (
          <Chip key={a} variant="hash" title="an adapter node this change would insert">
            {a}
          </Chip>
        ))}
      </Row>
    </div>
  );
}

function PreflightRefused({ result }: { result: PreflightResult }) {
  return (
    <Banner tone="warn" title="This change was declined, and nothing was submitted">
      <Row>
        {result.node_id ? <Chip tone="warn" title="the node the refusal names">{result.node_id}</Chip> : null}
        {result.field ? <Chip title="the field the refusal names">{result.field}</Chip> : null}
        {result.shape ? <Chip title="the kind of change that cannot be applied">{result.shape}</Chip> : null}
      </Row>
      <p>
        <strong>Declined, not attempted — and not a failure to retry.</strong> Nothing was published, no
        diff was written, and no evaluation budget was spent. Submitting the same draft is declined
        again.
      </p>
      {result.cause ? (
        <Disclosure summary="What the platform said, verbatim">
          <p className="mono break-words text-sm leading-relaxed">{result.cause}</p>
        </Disclosure>
      ) : null}
      <p>
        There is no override for this on any plan or role. A refusal exists because the change would be
        wrong in a way that is not visible at the moment of choosing it.
      </p>
    </Banner>
  );
}

/**
 * PreflightNotYetMeasurable is the third state, and the reason this file has a doc comment.
 *
 * The words matter more here than anywhere else on the surface. This is a statement about the
 * PLATFORM, not a judgment of the reader's change, and it must read that way — followed by what would
 * make it measurable, so the verdict is a next step rather than a dead end.
 */
function PreflightNotYetMeasurable({ result }: { result: PreflightResult }) {
  return (
    <Banner tone="info" title="We have not measured this yet">
      <Row>
        {result.missing_kind ? (
          <Chip title="the measurement that is missing">{result.missing_kind}</Chip>
        ) : null}
        {result.missing_subject ? (
          <Chip title="what the measurement would be about">{result.missing_subject}</Chip>
        ) : null}
        {result.node_id ? <Chip title="the node">{result.node_id}</Chip> : null}
      </Row>
      <p>
        This is a gap in <strong>our</strong> measurements, not a problem with your change. One of the
        gates that decides this change reads a number nobody has collected for this node yet, so it has
        no evidence to judge on — and it will not guess in either direction.
      </p>
      <p>
        It is <strong>not</strong> a refusal: nothing here says you may not make this change. It is also
        not an approval, because approving it would claim a check that never ran. Run an evaluation on
        this node to collect{" "}
        {result.missing_kind ? <span className="mono">{result.missing_kind}</span> : "the missing measurement"}
        , and this verdict resolves to a real answer.
      </p>
    </Banner>
  );
}

/**
 * ProviderBoundary states why a model list is shorter than the reader expects.
 *
 * A dropdown that silently omits every other provider's models reads as an incomplete catalogue, and
 * the reader's next move is to look for the missing entries — or to file a bug. The boundary is a
 * deliberate one, so it is stated where the choice is made rather than explained in documentation
 * nobody opens at that moment.
 */
export function ProviderBoundary({ provider }: { provider: string }) {
  return (
    <Banner tone="info" title={`Only ${provider} models are offered for this call site`}>
      <p>
        This call site is written against the <span className="mono">{provider}</span> SDK. Changing the
        model string does not change the SDK: the client, the parameter types and the response shape all
        differ, so a cross-provider swap would produce a diff that compiles and then calls the wrong
        provider in production.
      </p>
      <p>
        Models from other providers are therefore not offered here, and a cross-provider change submitted
        by any other route is declined by name. Routing across providers is a gateway concern, not a
        call-site edit.
      </p>
    </Banner>
  );
}

/**
 * ApplyModeNote explains, at the point of choice, why a parameter control is unavailable on a node.
 *
 * The apply mode is a structural property of the node — knowable before the reader types anything —
 * which is precisely why withholding it until after they have chosen a temperature is the wrong shape.
 */
export function ApplyModeNote({ mode }: { mode: "inline" | "bound" }) {
  if (mode === "bound") {
    return (
      <p className="text-sm leading-relaxed text-muted-foreground">
        This node applies in <strong>bound</strong> mode, so model parameters are data and can be carried
        as part of the change.
      </p>
    );
  }
  return (
    <Banner tone="info" title="This node cannot carry a parameter override">
      <p>
        It applies in <strong>inline</strong> mode, where parameters are code at the call site rather
        than data. A temperature or max-tokens change here is declined rather than silently dropped —
        a dropped parameter would mean the recorded configuration and the code that ran disagree.
      </p>
      <p>Switch this node to bound apply mode to carry parameter overrides.</p>
    </Banner>
  );
}

/** AuthoringSection is the shared frame for one axis's authoring controls. */
export function AuthoringSection({
  title,
  lede,
  children,
}: {
  title: string;
  lede: string;
  children: ReactNode;
}) {
  return (
    <Section title={title}>
      <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">{lede}</p>
      {children}
    </Section>
  );
}
