import type { Metadata } from "next";
import { JetBrains_Mono, Newsreader, Plus_Jakarta_Sans } from "next/font/google";
import "./globals.css";
import { readTheme } from "@/lib/theme";
import { reportingConfig } from "@/lib/reporting";
import { recordSurfaceViewed } from "@/lib/consoleAnalytics";
import { ErrorReporting } from "@/components/errorReporting";
import { ConsentBanner } from "@/components/consentBanner";

/**
 * The root layout.
 *
 * `lang` is declared exactly once, here, and it is `en` — R4 makes the console English-only, and a
 * `lang` a component could override is how a page ends up announcing the wrong language to a screen
 * reader.
 *
 * The layout deliberately renders no chrome. The public surface and the console have different
 * chrome — the public surface has no session, so it cannot show a subject strip or a sign-out — and a
 * shared header that renders conditionally on a session is exactly how a public page ends up making a
 * session call. Each zone brings its own shell.
 */

/**
 * The three families the design system names, self-hosted.
 *
 * `next/font/google` downloads them at BUILD time and serves them from this origin. That is not a
 * performance preference: this console's CSP is `default-src 'self'` with `font-src 'self'`, and FR35
 * forbids the public surface from touching a third-party origin at all. A `<link>` to a font CDN would
 * be REFUSED by the browser, the page would fall back to a system stack, and the build would still be
 * green — a design that disappears silently in production is the failure mode this avoids.
 *
 * `display: swap` means text is readable while a face loads rather than invisible, and each family
 * names the fallback its metrics are closest to so the swap does not reflow the page.
 */
const jakarta = Plus_Jakarta_Sans({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-jakarta",
  fallback: ["system-ui", "-apple-system", "Segoe UI", "sans-serif"],
});

const newsreader = Newsreader({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-newsreader",
  fallback: ["Georgia", "Times New Roman", "serif"],
});

const jetbrains = JetBrains_Mono({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-jetbrains",
  fallback: ["SFMono-Regular", "Menlo", "monospace"],
});

export const metadata: Metadata = {
  title: "Heros — evidence for LLM workflow changes",
  description:
    "Discover an LLM workflow, change one call site as a reviewable diff, run it sandboxed, and score it with confidence intervals — so a change ships on evidence rather than on a hunch.",
};

/**
 * `data-theme` is written here, on the server, from the persisted choice (R17 / FR37).
 *
 * That placement is the requirement: the attribute is in the first byte of HTML the browser receives,
 * so the very first paint is already in the right theme. Nothing flashes, nothing reflows after
 * hydration, and no inline script is needed — which matters, because this console's CSP has no
 * `unsafe-inline` and buying a theme toggle with a CSP relaxation would be a bad trade.
 */
export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const theme = await readTheme();
  // P24 wave 24c. Renders nothing; attaches two listeners when `error_diagnostics` is granted and a DSN
  // is configured, and attaches NOTHING otherwise. It sits in the root layout because the failure it is
  // for — a chunk that did not load, a hydration mismatch, a script the policy refused — happens
  // BEFORE any page-level component mounts.
  const reporting = await reportingConfig();
  // P24 wave 24f. Emitted from the SERVER, with the surface resolved to an id from the closed enum —
  // never from the browser, and never as a URL. Fire and forget: no render path awaits it.
  recordSurfaceViewed();
  return (
    <html
      lang="en"
      data-theme={theme}
      className={`${jakarta.variable} ${newsreader.variable} ${jetbrains.variable}`}
    >
      <body className="min-h-screen bg-background font-sans text-foreground">
        <ErrorReporting {...reporting} />
        {children}
        {/*
         * Rendered in the ROOT layout so that "withdrawal is reachable from every page carrying a
         * gated integration" is true by construction rather than page by page — including under
         * `/app`, which carries error reporting. Renders the full form until every optional category
         * has a decision, then a single quiet control that re-opens it.
         */}
        <ConsentBanner />
      </body>
    </html>
  );
}
