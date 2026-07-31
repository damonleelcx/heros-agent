import Link from "next/link";
import { REPOSITORY } from "@/content/repository";

/**
 * ReadingLayout is the THIRD composition, beside the marketing poster and the console shell.
 *
 * |          | `(public)`              | `/app`                          | `(reading)` — this one       |
 * |----------|-------------------------|---------------------------------|------------------------------|
 * | Session  | none                    | required                        | **none**                     |
 * | Theme    | dark-fixed              | follows the reader              | **follows the reader**       |
 * | Scroll   | page                    | **viewport-first, bounded**     | **document scroll**          |
 * | Data     | none                    | tenant read-model               | **none**                     |
 * | Read for | seconds                 | an hour, interactively          | **an hour, linearly; printed**|
 *
 * # 🔴 The viewport-first exemption is a DECISION, not an omission
 *
 * `/app` is bounded (`md:overflow-hidden`, a `main` that owns its own scroll) because the console is a
 * dashboard: a reader glances at it, and a section that has fallen below the fold is a section they will
 * not find. NFR17 is right for that surface.
 *
 * It is wrong for this one, and stating why here is the entire reason this comment exists — so that the
 * next person to read this file sees a deliberate exemption rather than a page somebody forgot to bound:
 *
 *   1. A 9,000-word Terms of Service inside a bounded inner scroll region **breaks the browser's own
 *      find-and-scroll**. `Ctrl+F` scrolls the document, not the reader's chosen sub-region, so a match
 *      inside an inner scroller is found and not shown. On the document a customer is agreeing to, that
 *      is not a papercut.
 *   2. It **prints as one page of clipped text**. The print stylesheet (task 3.4) is not decoration
 *      here — a buyer's counsel prints the Terms — and an inner scroller has no pages to paginate.
 *   3. Scroll position is the reader's place in an hour-long linear read. A nested scroller loses it on
 *      every resize and does not restore it on back-navigation.
 *
 * Priority law: level 3 (user complexity) and level 2 (stability) — a document that cannot be read or
 * printed is a document that does not serve — over level 7 (maintenance), which is one composition fewer. The exemption is bounded to this route group;
 * `/app` is untouched.
 *
 * # No session, no fetch
 *
 * This surface makes no upstream call and reads no session — it is a **separate composition** for exactly
 * the reason `(public)`'s own header gives: a shared shell that renders a subject strip "when there is a
 * session" is how a public page acquires a session call, and a legal document that stops serving during a
 * platform incident is unavailable at the precise moment a customer goes looking for it (NFR1).
 *
 * The harness asserts this rather than trusting it: the upstream-request counter does not move while
 * every route here returns 200 (task 12.1).
 *
 * # Two client islands, and no third
 *
 * The TOC's scroll-spy and the search box. Everything else is server-rendered Markdown, which is what
 * keeps the `scan-bundle` payload budget unchanged and what makes the surface work with JavaScript
 * disabled.
 */
export default function ReadingLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="reading">
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <header className="reading__header">
        <Link className="reading__wordmark" href="/">
          Heros
        </Link>
        <nav className="reading__nav" aria-label="Reading surface">
          <Link className="reading__nav-link" href="/docs">
            Documentation
          </Link>
          <Link className="reading__nav-link" href="/legal">
            Legal
          </Link>
          <a
            className="reading__nav-link"
            href={REPOSITORY.url}
            rel="noreferrer noopener"
            target="_blank"
            data-external="true"
          >
            Repository<span className="visually-hidden"> (opens an external site)</span>
            <span aria-hidden="true"> ↗</span>
          </a>
          <Link className="reading__nav-link reading__nav-link--strong" href="/signin">
            Sign in
          </Link>
        </nav>
      </header>

      {/*
        `main` does NOT own a scroll region and does NOT set `overflow`. The document scrolls. See the
        header comment — this is the exemption, and it is the whole point of the composition.
      */}
      <main className="reading__main" id="main">
        {children}
      </main>

      <footer className="reading__footer">
        <p className="reading__footer-note">
          Served by the console from its own container. This page makes no request to the platform and no
          request to any third party — so it keeps serving when the platform does not.
        </p>
        <nav className="reading__footer-links" aria-label="Legal">
          <Link className="reading__nav-link" href="/legal/terms">
            Terms of Service
          </Link>
          <Link className="reading__nav-link" href="/legal/privacy">
            Privacy Notice
          </Link>
          <Link className="reading__nav-link" href="/legal">
            Version history
          </Link>
        </nav>
      </footer>
    </div>
  );
}
