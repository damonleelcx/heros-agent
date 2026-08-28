import "server-only";
import type { ReactNode } from "react";
import { Banner, Row } from "./primitives";
import { NotConnected, SubjectName } from "./editorKit";
import { SubjectSwitcher } from "./subjectSwitcher";
import type { AxisSubject, SubjectOutcome } from "@/lib/axisSubject";

/**
 * axisFrame.tsx resolves the subject ONCE and renders it on every axis surface (P37 FR1–FR4, D-37.2).
 *
 * # 🔴 Why this is a shared component and not `app/app/layout.tsx`
 *
 * Design D1 says the subject is resolved "in the shell". The console's shell says something else, in its
 * own words: *"It renders no subject and no data ... the shell holds no fetch at all — so a slow platform
 * cannot delay the chrome, and the reader always has somewhere to go."*
 *
 * Both rules are right, and they are about different things. D1's requirement is that the question is
 * asked ONCE and answered by ONE resolver — not that the answer is computed in `layout.tsx`. P9's rule is
 * that the navigation cannot be held hostage by a platform read. Putting `resolveSubject()` in the
 * console layout would satisfy D1's letter and break P9's rule for all thirty routes, including the
 * twenty-three that have no subject.
 *
 * So the resolution lives here, in the frame the seven axis surfaces share. One resolver, one call site
 * per request, the answer displayed on every surface — and the chrome still renders while the platform
 * is slow. Recorded as D-37.6 in `decisions.md` rather than left as an undocumented divergence, because
 * a reader comparing the design to the tree deserves to know which way it went.
 *
 * 🚫 It is NOT a route group. Moving `context/`, `memory/`, `harness/`, `graph/`, `studio/`, `authoring/`
 * and `delivery/` under `(axis)/` would give a real nested layout and would move seven directories that
 * a dozen tests address by path — a large blast radius for a property this component already has.
 */

/** AxisFrameProps is what a surface hands the frame. */
export type AxisFrameProps = {
  /** axis is the member of the shared vocabulary this surface edits — it selects the reading-surface link. */
  axis: string;
  /** outcome is the resolver's answer, read once per request by the page. */
  outcome: SubjectOutcome;
  /** returnTo is this surface's own route, so switching the node does not also change the page. */
  returnTo: string;
  /** children receives the resolved subject. It is never rendered in any other state. */
  children: (subject: AxisSubject, candidates: AxisSubject[]) => ReactNode;
};

/**
 * AxisFrame renders the subject strip and then the surface's own body.
 *
 * 🔴 The body is a FUNCTION of the resolved subject rather than a node. That is the type system carrying
 * FR4: there is no way to render the surface's editor without a subject, so there is no path on which a
 * fixture could occupy the reader's data position.
 */
export function AxisFrame({ axis, outcome, returnTo, children }: AxisFrameProps) {
  if (outcome.state === "not_mounted") {
    return (
      <p className="hint">
        This deployment does not accept workflow structure, so there is no node of yours to bind this axis
        to. Nothing failed — the capability is not served here.
      </p>
    );
  }
  if (outcome.state === "read_failed") {
    return (
      <p className="hint">
        Your reported structure could not be read. This is <strong>not</strong> the same as having sent
        none: nothing has been lost, and retrying is safe.
        {outcome.detail ? <span className="mono block">{outcome.detail}</span> : null}
      </p>
    );
  }
  if (outcome.state === "not_connected") {
    // 🔴 FR4 — the reader's data position contains NOTHING. No sample node, no fixture value, no
    // demonstration diff. Fence 6.2 renders every axis surface in this state and asserts it.
    return <NotConnected axis={axis} />;
  }
  if (outcome.state === "ambiguous") {
    return (
      <div className="flex flex-col gap-3">
        <Banner tone="info" title="Which node should these surfaces be about?">
          <p>
            This workflow reports {outcome.candidates.length} nodes and none has been chosen. Pick one and
            every axis surface stays on it — you are asked once, not on each page.
          </p>
        </Banner>
        <SubjectSwitcher candidates={outcome.candidates} returnTo={returnTo} />
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <Row>
        <SubjectName subject={outcome.subject} candidates={outcome.candidates} sole={outcome.sole} />
        {/*
          🔴 Always changeable (FR2). The switcher renders only when there is a choice to make: with one
          candidate the name alone is the honest control, because a picker with one option asks a
          question that has no second answer.
        */}
        {outcome.candidates.length > 1 ? (
          <SubjectSwitcher
            candidates={outcome.candidates}
            selected={outcome.subject.node_id}
            returnTo={returnTo}
          />
        ) : null}
      </Row>
      {children(outcome.subject, outcome.candidates)}
    </div>
  );
}
