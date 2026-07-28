import Link from "next/link";
import { FileCode, FlaskConical, GitBranch, GitPullRequest, Home, Layers, Play, Settings, Share2, User } from "lucide-react";
import { requireSession } from "@/lib/session";
import { visitedSubjects, routes, SUBJECT_LABELS } from "@/lib/subjects";
import { NavLink } from "@/components/nav";
import { CommandPath, type CommandEntry } from "@/components/commandPath";
import { ThemeControl } from "@/components/themeControl";

/**
 * ConsoleLayout is the shell every tenant-facing surface renders inside.
 *
 * # Why the navigation moved from a header row to a rail
 *
 * The design system puts the console's surfaces in a left rail and keeps the header for identity and
 * the two global controls. That is not a stylistic preference here: the header row this replaces held
 * seven links, a command path, a theme control, a tenant identifier and a sign-out, and at a narrow
 * width it wrapped into three lines of chrome above every page. The rail gives the navigation a fixed
 * home that does not compete with the page's own heading, and — the part that matters — it makes the
 * current surface visible while the reader is halfway down a long table, because it does not scroll
 * away.
 *
 * Below the `md` breakpoint the rail becomes a horizontal scroller under the header rather than
 * disappearing behind a menu button. FR9 requires every surface to be reachable by navigation; a
 * surface reachable only after opening a drawer is one most readers never learn exists.
 *
 * # What the shell deliberately does NOT do
 *
 * It renders no subject and no data. The subject strip belongs to the layouts that know one
 * (`workflows/[workflowId]`, `runs/[runId]`), and the shell holds no fetch at all — so a slow platform
 * cannot delay the chrome, and the reader always has somewhere to go.
 */
type Surface = { href: string; label: string; icon: React.ReactNode; exact?: boolean };

const SURFACES: Surface[] = [
  { href: "/app", label: "Overview", icon: <Home />, exact: true },
  { href: "/app/workflows", label: "Workflows", icon: <GitBranch /> },
  { href: "/app/runs", label: "Runs", icon: <Play /> },
  { href: "/app/variants", label: "Variants", icon: <Layers /> },
  { href: "/app/transforms", label: "Transforms", icon: <FileCode /> },
  { href: "/app/delivery", label: "Delivery", icon: <GitPullRequest /> },
  { href: "/app/studio", label: "Studio", icon: <FlaskConical /> },
  { href: "/app/wiring", label: "Wiring", icon: <Share2 /> },
];

const SETTINGS: Surface[] = [
  { href: "/app/configure", label: "Configure", icon: <Settings /> },
  { href: "/app/account", label: "Account", icon: <User /> },
];

export default async function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const session = await requireSession();
  const visited = visitedSubjects(session);

  // The command path reaches every surface AND every subject this session has opened. The surfaces are
  // static; the subjects are the console's only answer to the enumeration gap (subjects.ts).
  const entries: CommandEntry[] = [
    { id: "s:overview", group: "Surface", label: "Overview", href: routes.overview() },
    { id: "s:configure", group: "Surface", label: "Configure a variant", href: routes.configure() },
    { id: "s:workflows", group: "Surface", label: "Workflows", href: "/app/workflows" },
    { id: "s:runs", group: "Surface", label: "Runs", href: "/app/runs" },
    { id: "s:transforms", group: "Surface", label: "Transforms", href: "/app/transforms" },
    { id: "s:delivery", group: "Surface", label: "Delivery", href: "/app/delivery" },
    { id: "s:variants", group: "Surface", label: "Variants", href: "/app/variants" },
    { id: "s:studio", group: "Surface", label: "Prompt & Model Studio", href: "/app/studio" },
    { id: "s:wiring", group: "Surface", label: "Node wiring", href: "/app/wiring" },
    { id: "s:account", group: "Surface", label: "Plan and spend", href: routes.account() },
    ...visited.map((subject) => ({
      id: `v:${subject.kind}:${subject.id}`,
      group: SUBJECT_LABELS[subject.kind],
      label: subject.label,
      hint: subject.hint,
      href: subject.href,
    })),
  ];

  return (
    // Viewport-first (NFR17): on desktop the shell occupies exactly the viewport height and never
    // page-scrolls — the header and rail stay fixed and each page lays out inside a bounded main region.
    // Mobile keeps natural scroll (a phone legitimately scrolls).
    <div className="flex min-h-screen flex-col md:h-dvh md:min-h-0 md:overflow-hidden">
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <header className="sticky top-0 z-20 flex h-12 shrink-0 items-center gap-4 border-b border-border bg-background/90 px-4 backdrop-blur md:px-5">
        <Link
          className="font-display text-sm font-light uppercase tracking-[0.15em] text-foreground/70 transition-colors hover:text-foreground"
          href={routes.overview()}
        >
          Heros
        </Link>

        <span className="flex-1" />

        <CommandPath entries={entries} />
        <ThemeControl />

        {/*
          The tenant is stated in the chrome, permanently. An operator-adjacent surface that does not
          say whose data is on screen is one tab away from being read as somebody else's.
        */}
        <span
          className="mono hidden max-w-[12rem] truncate text-xs text-muted-foreground sm:inline"
          title="The tenant this session is bound to"
        >
          {session.tenantId}
        </span>

        {/*
          Sign-out is a POST, not a link. A GET that ends a session can be triggered by a browser
          prefetch, an <img> tag on a page the user happens to visit, or a link checker — each of which
          signs them out for a reason they will never work out.
        */}
        <form action="/api/session/end" method="post">
          <button className="button px-2.5 py-1 text-xs" type="submit">
            Sign out
          </button>
        </form>
      </header>

      <div className="flex min-h-0 flex-1">
        <nav
          className="sticky top-12 hidden h-[calc(100vh-3rem)] w-48 shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-border px-2 py-4 md:flex"
          aria-label="Console"
        >
          <div className="flex flex-1 flex-col gap-0.5">
            {SURFACES.map((item) => (
              <NavLink key={item.href} href={item.href} icon={item.icon} exact={item.exact}>
                {item.label}
              </NavLink>
            ))}
          </div>
          <div className="mt-2 flex flex-col gap-0.5 border-t border-border/60 pt-2">
            {SETTINGS.map((item) => (
              <NavLink key={item.href} href={item.href} icon={item.icon}>
                {item.label}
              </NavLink>
            ))}
          </div>
        </nav>

        {/* The same links, in flow, for viewports the rail does not fit on. */}
        <nav
          className="fixed inset-x-0 bottom-0 z-20 flex gap-1 overflow-x-auto border-t border-border bg-background/95 px-3 py-2 backdrop-blur md:hidden"
          aria-label="Console (compact)"
        >
          {[...SURFACES, ...SETTINGS].map((item) => (
            <NavLink key={item.href} href={item.href} icon={item.icon} exact={item.exact}>
              {item.label}
            </NavLink>
          ))}
        </nav>

        {/*
          `main` carries no frame of its own. Every page under it renders a `PageFrame`, which owns the
          measure, the gutter and the vertical rhythm — so a frame here as well meant the frame was
          applied twice: doubled top and bottom padding on every view, and a page that spent its first
          96px on nothing. One frame, owned by one component.

          The bottom padding clears the compact navigation, which is fixed, and is removed once the
          rail takes over.
        */}
        <main className="min-w-0 flex-1 pb-16 md:min-h-0 md:overflow-hidden md:pb-0" id="main">
          {children}
        </main>
      </div>
    </div>
  );
}
