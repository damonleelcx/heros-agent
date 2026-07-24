import Link from "next/link";

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
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-4">
          <span className="font-display text-base font-light uppercase tracking-[0.15em] text-marketing-ink/30">
            Heros
          </span>
          <p className="font-mono text-xs text-marketing-ink/25">
            The console renders what the platform computed. It derives no score, no ranking and no
            confidence of its own.
          </p>
        </div>
      </footer>
    </div>
  );
}
