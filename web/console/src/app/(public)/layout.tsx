import Link from "next/link";
import { REPOSITORY } from "@/content/repository";

/**
 * PublicLayout is the shell for the one surface that has no session.
 *
 * It is a **separate composition** from the console's shell rather than the same one with the
 * tenant-only parts hidden, and that separation is structural rather than cosmetic: a shared header
 * that renders a subject strip "when there is a session" is how a public page ends up making a session
 * call, and a public page that calls the session store is a public page that stops serving when the
 * platform does (FR32, NFR13).
 *
 * # Why the public surface is dark in both themes
 *
 * The console follows the reader's theme because it is a reading surface they sit in front of for an
 * hour. This is a poster: one composition, one contrast, seen once. Its tokens are fixed
 * (`--marketing-*`), so the page cannot half-invert into a state nobody designed — which is what a
 * light rendering of a composition built on glow, grid and glass would be.
 */
export default function PublicLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-screen bg-marketing-canvas text-marketing-ink">
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <header className="sticky top-0 z-50 flex items-center justify-between border-b border-marketing-ink/6 bg-marketing-canvas/90 px-6 py-4 backdrop-blur-xl md:px-12">
        <Link
          className="font-display text-lg font-light uppercase tracking-[0.15em] text-marketing-ink/90"
          href="/"
        >
          Heros
        </Link>
        <div className="flex items-center gap-6">
          <a
            className="hidden text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink/80 md:inline"
            href="#how"
          >
            How it works
          </a>
          <a
            className="hidden text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink/80 md:inline"
            href="#plans"
          >
            Plans
          </a>
          <Link
            className="hidden text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink/80 md:inline"
            href="/docs"
          >
            Docs
          </Link>
          {/*
            The repository link (P23 task 7.1). It is a plain ANCHOR and nothing else: no client
            component, no effect, no loading state, and — decisively — NO REQUEST UNTIL IT IS CLICKED.

            The three usual ways to dress this up are all refused at runtime by the CSP this console sets
            per request: a shields.io badge by `img-src 'self' data:`, a GitHub buttons widget by
            `default-src 'self'`, and a browser-side `api.github.com` fetch by `connect-src 'self'`. The
            capability declines to be the exception — see `src/content/repository.ts` for why a star
            count is opt-in, default off, and never rendered as `0`.

            `scripts/scan-repo-link.mjs` fails the build if this target is private or does not exist,
            under the same rule that forbids an install command that 404s.
          */}
          <a
            className="text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink/80"
            href={REPOSITORY.url}
            rel="noreferrer noopener"
            target="_blank"
            data-external="true"
          >
            GitHub<span className="visually-hidden"> (opens an external site)</span>
            <span aria-hidden="true"> ↗</span>
          </a>
          <Link
            className="text-sm text-marketing-ink/70 transition-colors hover:text-marketing-ink"
            href="/signin"
          >
            Sign in
          </Link>
          <Link
            className="rounded-lg bg-marketing-accent px-4 py-2 text-sm font-medium text-marketing-accent-ink transition-opacity hover:opacity-90"
            href="/signin"
          >
            Open the console
          </Link>
        </div>
      </header>

      <main id="main">{children}</main>

      <footer className="border-t border-marketing-ink/6 px-6 py-10 md:px-12">
        <div className="mx-auto flex max-w-7xl flex-col gap-6">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <span className="font-display text-base font-light uppercase tracking-[0.15em] text-marketing-ink/30">
              Heros
            </span>
            <p className="font-mono text-xs text-marketing-ink/25">
              The console renders what the platform computed. It derives no score, no ranking and no
              confidence of its own.
            </p>
          </div>

          {/*
            P23 task 8.7 — legal is linked from every place a commitment is made or reviewed, and the
            public footer is the first of them. A Terms of Service nobody can find from the page that
            sells the product is a document that exists for the archive rather than for the reader.
          */}
          <nav
            aria-label="Documentation and legal"
            className="flex flex-wrap gap-x-6 gap-y-2 border-t border-marketing-ink/6 pt-6"
          >
            <Link className="text-xs text-marketing-ink/40 transition-colors hover:text-marketing-ink/70" href="/docs">
              Documentation
            </Link>
            <Link
              className="text-xs text-marketing-ink/40 transition-colors hover:text-marketing-ink/70"
              href="/docs/start/install"
            >
              Install the CLI
            </Link>
            <Link
              className="text-xs text-marketing-ink/40 transition-colors hover:text-marketing-ink/70"
              href="/legal/terms"
            >
              Terms of Service
            </Link>
            <Link
              className="text-xs text-marketing-ink/40 transition-colors hover:text-marketing-ink/70"
              href="/legal/privacy"
            >
              Privacy Notice
            </Link>
            <Link className="text-xs text-marketing-ink/40 transition-colors hover:text-marketing-ink/70" href="/legal">
              Legal version history
            </Link>
            <a
              className="text-xs text-marketing-ink/40 transition-colors hover:text-marketing-ink/70"
              href={REPOSITORY.url}
              rel="noreferrer noopener"
              target="_blank"
              data-external="true"
            >
              Repository on GitHub<span className="visually-hidden"> (opens an external site)</span>
              <span aria-hidden="true"> ↗</span>
            </a>
          </nav>
        </div>
      </footer>
    </div>
  );
}
