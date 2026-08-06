import Link from "next/link";
import { ArrowLeft } from "lucide-react";

/**
 * The shell the four unauthenticated identity pages share.
 *
 * # Why a route group and not the public or console layout
 *
 * `/signin` already carries its own shell, for a reason stated in its header: the console's shell requires
 * a session and the public shell advertises the product, and a page whose whole job is that you are not
 * signed in is neither. `/forgot-password`, `/reset-password`, `/verify-email` and `/signup` are in exactly
 * the same position, and giving each its own copy of the shell is how four pages end up looking like three
 * different products.
 *
 * It shares `/signin`'s tokens rather than the console's theme, for `/signin`'s reason: these pages render
 * before any tenant preference is known, and a surface that flips appearance depending on a cookie the
 * reader may not have yet is a surface that looks broken half the time.
 *
 * 🔴 Every colour, radius and spacing value below is a design-system token or a value already used by
 * `/signin`. Nothing here is improvised — `npm run scan:tokens` fails the build on a literal, and the
 * ui-consistency rule forbids inventing a style when an anchor exists.
 */
export default function IdentityLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="relative min-h-screen bg-marketing-canvas text-marketing-ink">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <div
        className="absolute inset-0 bg-[length:48px_48px]"
        style={{ backgroundImage: "var(--marketing-grid)" }}
        aria-hidden="true"
      />
      <Link
        className="absolute left-6 top-6 z-20 inline-flex items-center gap-2 text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink"
        href="/signin"
      >
        <ArrowLeft className="size-4" aria-hidden="true" />
        Sign in
      </Link>
      <main
        className="relative z-10 mx-auto flex min-h-screen flex-col items-center justify-center px-6 py-24"
        id="main"
      >
        {/* Measured to the FORM, not to the viewport — `/signin`'s note applies unchanged: a field
            stretched across a 1440px display reads as a text area and invites a paragraph. */}
        <section className="w-full" style={{ maxInlineSize: "var(--measure-form)" }}>
          <div className="w-full rounded-2xl border border-marketing-ink/10 bg-marketing-ink/4 p-8 shadow-2xl backdrop-blur">
            {children}
          </div>
        </section>
      </main>
    </div>
  );
}
