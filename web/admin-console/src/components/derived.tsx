import type { CoverageView, DerivedFigure } from "@/lib/types";
import { percent } from "@/lib/format";

/**
 * derived.tsx is the ONLY way a SUM-derived figure reaches the screen (P26 tasks 2.2, 2.3).
 *
 * # Why a component and not a convention
 *
 * The rule — link coverage displayed wherever a derived figure is shown — was stated once in
 * `project.md` and honoured by the customer console. The operator billing surface, built before run
 * linking existed, did not honour it, and nothing noticed for fourteen phases. A convention that has
 * already failed once does not get a second chance as a convention.
 *
 * So the figure and its coverage are one component, rendered together, in the same view. Not behind a
 * link, not in a footnote: a footnote beside a figure reads as reassurance, and the reader who most
 * needs the caveat is the one least likely to open a disclosure to find it.
 *
 * # Why unknown coverage withholds the figure entirely
 *
 * A SUM figure whose coverage nobody can state is wrong by an unknown factor in a direction nobody can
 * quantify — and it is exactly the figure a credit gets issued against. A missing number prompts a
 * question; a wrong number gets acted on. `coverage === null` is therefore not a rendering variant of
 * the figure: it is the absence of one.
 */

/**
 * Derived renders a figure with its coverage, or renders the reason it is withheld.
 *
 * `withheld` is a real rendering rather than `null`, so an operator can tell "this period has no SUM"
 * from "the console failed to load" — the same distinction the nine states exist for.
 */
export function Derived({
  label,
  figure,
  coverage,
}: {
  label: string;
  figure?: DerivedFigure;
  /** The surface's coverage reading, used to explain a withheld figure in the figure's own place. */
  coverage: CoverageView;
}) {
  if (!figure || figure.coverage === null || figure.coverage === undefined) {
    return (
      <div className="derived derived--withheld">
        <span className="derived__label">{label}</span>
        <span className="derived__withheld">Not shown — link coverage is unknown</span>
        <span className="derived__basis">{coverage.statement}</span>
      </div>
    );
  }
  return (
    <div className="derived">
      <span className="derived__label">{label}</span>
      <span className="derived__value num">{figure.value}</span>
      {/* The coverage sits with the value, at the value's own scale group — not smaller, not later. */}
      <span className="derived__coverage">
        {percent(figure.coverage)} link coverage · {figure.runs_linked} of {figure.runs_reported} runs
        linked
      </span>
      <span className="derived__basis">
        Source: {figure.source}
        {figure.basis ? ` — ${figure.basis}` : ""}
      </span>
    </div>
  );
}

/**
 * CoverageStatement renders the surface's link-coverage sentence.
 *
 * It is shown whether or not any figure is, because "coverage is unknown" is itself the answer and a
 * reader who sees nothing at all cannot tell it apart from a page that failed to load.
 */
export function CoverageStatement({ coverage }: { coverage: CoverageView }) {
  return (
    <p className={coverage.known ? "state__body" : "state__body derived__basis"}>{coverage.statement}</p>
  );
}
