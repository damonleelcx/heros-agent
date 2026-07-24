import type { ReactNode } from "react";
import type { AdminIdentity, TenantView } from "@/lib/types";
import { ROLE_LABELS } from "@/lib/session";
import { minutes } from "@/lib/format";
import { readDensity, readTheme } from "@/lib/prefs";
import { adminFetch } from "@/lib/adminApi";
import { commandsFor, navFor } from "@/lib/surfaces";
import { EndImpersonationButton } from "./impersonationBanner";
import { CommandPalette, type PaletteSubject } from "./commandPalette";
import { ConsoleClock, DensityToggle, PrimaryNav, ThemeToggle } from "./chromeControls";

/**
 * shell.tsx is the operator chrome that wraps every authenticated view (FR23).
 *
 * It carries four things on EVERY page, not just on entry:
 *   1. the console's identity — a distinct chrome band and accent, so the operator console cannot be
 *      mistaken for the customer console at a glance, without reading content and without relying on
 *      the address bar;
 *   2. persistent identification of the acting admin principal and its live roles;
 *   3. the impersonation banner, when a session is active — named tenant, scope, expiry, "every
 *      action logged", and an always-visible End control (FR25);
 *   4. the command palette, one keystroke away from anywhere (FR32).
 *
 * The nav and the palette are both built from `surfaces.ts` filtered by the operator's live
 * capabilities — the SAME permission map the backend enforces — so a surface the role cannot use is
 * absent from both, and the screen and the gate never disagree (FR22).
 *
 * # Why the chrome is not amber any more
 *
 * It used to be, and amber is `--warn`. Painting the chrome, the links and the primary buttons in the
 * hazard hue spent the signal the kill switch depends on: by the time an operator reached a control
 * that really was dangerous, red and amber had been ordinary for the entire session. The console's
 * identity is now a cool accent that is nowhere near the hazard palette (FR31) — still unmistakable
 * beside the customer console, no longer borrowing a colour that has a job.
 */

export async function OperatorShell({
  identity,
  sessionToken,
  children,
}: {
  identity: AdminIdentity;
  /** The acting admin's session. The palette lists subjects with it, so it can surface nothing the
   * gate would not already have shown this operator. */
  sessionToken: string;
  children: ReactNode;
}) {
  const density = await readDensity();
  const theme = await readTheme();
  const nav = navFor(identity.capabilities);
  const commands = commandsFor(identity.capabilities);
  const subjects = await paletteSubjects(identity, sessionToken);
  const imp = identity.active_impersonation;

  return (
    <>
      {/* ── Distinct operator chrome, on every view ──
       *
       * One band, not two rows. The identity, the navigation and the acting principal share a single
       * horizon line, so an operator's eye finds it once and everything below it is content. Its
       * darkness survives the light theme, which is what keeps FR23 true in both.
       */}
      <header className="chrome">
        <div className="chrome__bar">
          <span className="chrome__mark">
            {identity.console}
            <span className="chrome__glyph">Internal</span>
          </span>

          <nav className="nav" aria-label="Console sections">
            <PrimaryNav items={nav} />
          </nav>

          <span className="chrome__right">
            <ConsoleClock />
            <CommandPalette commands={commands} subjects={subjects} />
            <DensityToggle density={density} />
            <ThemeToggle theme={theme} />
            <span className="chrome__principal">
              Acting as <strong>{identity.admin_id}</strong>
              <span aria-label="Your roles">
                {identity.roles.map((role) => (
                  <span key={role} className="role-chip">
                    {ROLE_LABELS[role]}
                  </span>
                ))}
              </span>
            </span>
            <span className="nav__end">
              <form action="/api/session/logout" method="post">
                <button type="submit" className="quiet">
                  Sign out
                </button>
              </form>
            </span>
          </span>
        </div>
      </header>

      {/* ── Impersonation banner (persistent, with End control) ── */}
      {imp ? (
        <div className="impersonation-banner" role="alert">
          <span>
            Impersonating <strong>{imp.tenant_id}</strong> ·{" "}
            {imp.scope === "elevated_write" ? "WRITE ENABLED" : "read-only"} · expires in{" "}
            {minutes(imp.remaining_seconds)} min · every action is logged
          </span>
          <EndImpersonationButton impersonationId={imp.id} />
        </div>
      ) : null}

      <main id="main">{children}</main>
    </>
  );
}

/**
 * paletteSubjects resolves the subjects an operator can reach by NAME (FR32).
 *
 * Tenants are the console's primary subject, so they are what the palette type-aheads over. The list
 * is fetched with the operator's own session, which means the palette can only ever surface what the
 * gate would already have shown them — there is no privileged listing here.
 *
 * A failure to fetch is swallowed on purpose: the palette degrades to its command entries, and the
 * page the operator actually asked for still renders. A convenience that can take a view down is not
 * a convenience.
 */
async function paletteSubjects(identity: AdminIdentity, sessionToken: string): Promise<PaletteSubject[]> {
  if (!identity.capabilities.includes("tenant.read")) return [];
  try {
    const res = await adminFetch<{ tenants: TenantView[] }>("/admin/api/tenants", { sessionToken });
    return (res.tenants ?? []).map((t) => ({
      id: t.tenant_id,
      label: t.tenant_id,
      hint: `${t.plan_name} · ${t.status}${t.autonomous_merges_halted ? " · merges halted" : ""}`,
      href: `/tenants/${encodeURIComponent(t.tenant_id)}`,
      kind: "tenant",
    }));
  } catch {
    return [];
  }
}
