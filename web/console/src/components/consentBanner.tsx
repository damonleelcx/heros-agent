import { headers } from "next/headers";
import { CATEGORY_TERMS } from "../../../design-system/consent-terms.ts";
import { hasAnswered, isGranted, OPTIONAL_CATEGORIES, readConsent } from "@/lib/consentPrefs";

/**
 * consentBanner.tsx is the visitor's control over the three optional categories.
 *
 * # 🔴 Decline carries the SAME visual weight as accept
 *
 * Both are a plain `.button`. Neither is `.button--primary`.
 *
 * That is the single most load-bearing decision in this file, and it is a decision rather than an
 * oversight, so it is written down: a banner whose accept control is large and coloured and whose
 * decline control is a grey line of text is not asking a question, it is applying pressure. The
 * requirement is equal weight, and equal weight means the same class — not "similar", not "also
 * clearly visible".
 *
 * # Why it renders nothing once answered — and why "answered" includes "declined"
 *
 * A refusal is a STORED FACT. Once every optional category has a decision, the banner is gone and stays
 * gone across navigations, sessions and browser restarts, until the visitor opens it themselves or a
 * MATERIAL policy version invalidates the grant. Re-asking somebody who said no is the behaviour a
 * visitor experiences as being ignored, and it is the reason `not-asked` and `denied` are two states
 * rather than one falsy value.
 *
 * # Why the withdrawal control is in the root layout
 *
 * "Withdrawal is reachable from every page carrying a gated integration" is a rule that fails the first
 * time somebody adds a page, if it is enforced page by page. Rendering the control in the ROOT LAYOUT
 * makes it true by construction on the public surface and on every `/app` route — and `/app` carries a
 * gated integration too, because error reporting runs there.
 *
 * # Why there is no JavaScript here at all
 *
 * It is a `<form>` and a `<details>`. Declining works with scripting disabled, on a failed hydration,
 * and behind a proxy that stripped the bundle — the conditions under which a visitor is most likely to
 * be somebody who cares. A consent control that needs JavaScript fails open for exactly that person.
 */
export async function ConsentBanner() {
  const state = await readConsent();
  const h = await headers();
  const path = h.get("x-pathname") ?? "/";
  const answered = hasAnswered(state);
  // `?consent=review` is how the withdrawal control re-opens the form. It only OPENS it — a query
  // parameter can never grant or deny anything, which is why the decision lives behind a POST.
  const reviewing = path.includes("consent=review");
  const back = path.split("?")[0] || "/";

  if (answered && !reviewing) {
    return (
      <div className="border-t border-border px-4 py-2">
        <a className="caption underline-offset-4 hover:underline" href={`${back}?consent=review`}>
          Privacy choices
        </a>
      </div>
    );
  }

  return (
    /*
     * `bg-card` and NOT `banner--info`.
     *
     * The first version used the info variant and it was wrong twice over, which a browser showed
     * immediately: the variant paints an 8%-alpha tint over whatever is behind it, so a control that is
     * `sticky` at the bottom of a scrolling page rendered TRANSPARENT — the hero type read straight
     * through it — and it tinted the whole control the info hue, which says "this is a notice" about a
     * thing that is actually a question. `bg-card` is the surface anchor 17 other components already
     * use, so this is the same card every other panel on the console is.
     */
    <aside
      className="banner sticky bottom-0 z-10 mx-auto mb-3 w-full max-w-7xl border-border bg-card shadow-2xl"
      aria-label="Privacy choices"
    >
      <form action="/api/consent" method="post" className="flex flex-col gap-3">
        <input type="hidden" name="back" value={back} />
        <p className="banner__title">
          Choose what this site may collect
        </p>
        <p className="hint">
          Nothing optional is collected until you choose. You can change this at any time from
          &ldquo;Privacy choices&rdquo; at the bottom of any page, and declining leaves every part of
          this product working.
        </p>

        <details className="flex flex-col gap-2">
          <summary className="caption cursor-pointer">What each one means</summary>
          {/*
            * Bounded and scrollable, found by expanding it in a real browser: unbounded, the four
            * summaries made the panel 532px of a 720px viewport — a control that asks a question while
            * covering most of the page it is asking about. `max-h-80` is the anchor the docs search
            * results already use; it is not a new number.
            */}
          <ul className="flex max-h-80 flex-col gap-2 overflow-y-auto pt-2">
            {(["essential", ...OPTIONAL_CATEGORIES] as const).map((category) => (
              <li key={category} className="flex flex-col gap-1">
                <label className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    name={category}
                    value="granted"
                    defaultChecked={category === "essential" ? true : isGranted(state, category)}
                    disabled={category === "essential"}
                  />
                  <span className="font-medium">{CATEGORY_TERMS[category].term}</span>
                </label>
                <span className="hint">{CATEGORY_TERMS[category].summary}</span>
              </li>
            ))}
          </ul>
          <p className="pt-2">
            <button className="button" type="submit" name="action" value="save">
              Save these choices
            </button>
          </p>
        </details>

        {/*
         * Two controls, one class each. The order is decline-first on purpose: the control a reader
         * reaches by tabbing once should not be the one that agrees to everything.
         */}
        <p className="flex flex-wrap gap-2">
          <button className="button" type="submit" name="action" value="decline-all">
            Decline optional
          </button>
          <button className="button" type="submit" name="action" value="accept-all">
            Accept optional
          </button>
          <a className="button" href="/legal/privacy">
            Read the privacy statement
          </a>
        </p>
      </form>
    </aside>
  );
}
