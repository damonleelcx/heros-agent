import type { Metadata } from "next";
import "./globals.css";
import { readDensity, readTheme } from "@/lib/prefs";
import { reportingConfig } from "@/lib/reporting";
import { recordSurfaceViewed } from "@/lib/consoleAnalytics";
import { ErrorReporting } from "@/components/errorReporting";

/**
 * layout.tsx is the console's shell.
 *
 * It deliberately renders NO operator chrome and NO navigation of its own: the chrome names the acting
 * admin principal, and the root layout has no session (the sign-in page uses this layout too). Every
 * authenticated page composes `OperatorShell`, which resolves the identity first and therefore cannot
 * render a shell that then fails each of its requests (FR20).
 *
 * What it DOES carry is the operator's density preference, stamped on the root element during server
 * rendering (FR29). Applying it here rather than after hydration is why changing density does not make
 * the page jump.
 */

export const metadata: Metadata = {
  title: "Operator Console — Heros Platform",
  description: "Internal platform operations. Every action is permission-gated and audited.",
  robots: { index: false, follow: false },
};

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const density = await readDensity();
  const theme = await readTheme();
  // P24 wave 24c. Renders nothing. Error reporting is the ONE third-party integration this console
  // takes: an analytics tag and a session recorder are refused structurally, because this screen renders
  // cross-tenant aggregates, tenant names, active impersonation state and audit rows.
  const reporting = await reportingConfig();
  // P24 wave 24f. Emitted from the SERVER, with the surface resolved to an id from the closed enum —
  // never from the browser, and never as a URL. Fire and forget: no render path awaits it.
  recordSurfaceViewed();
  return (
    <html lang="en-US" data-density={density} data-theme={theme}>
      <body>
        <ErrorReporting {...reporting} />
        <a className="skip-link" href="#main">
          Skip to main content
        </a>
        {children}
      </body>
    </html>
  );
}
