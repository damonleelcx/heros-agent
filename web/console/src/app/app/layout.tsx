import Link from "next/link";
import {
  Brain,
  CreditCard,
  FileCode,
  FlaskConical,
  GitBranch,
  GitPullRequest,
  Grid3x3,
  BookOpen,
  HardDriveDownload,
  Home,
  Layers,
  MessageSquareText,
  PenLine,
  Play,
  Repeat,
  Settings,
  Share2,
  SlidersHorizontal,
  User,
  UserCircle,
  Users,
} from "lucide-react";
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
  // P31. FIRST after Overview, and that position is the argument: the whole premise of this surface is
  // that a person who has installed nothing can type a sentence instead of learning fifty routes. A rail
  // entry buried under Coverage would be reached only by somebody who already knows their way around,
  // which is the reader who needs it least.
  { href: "/app/ask", label: "Ask", icon: <MessageSquareText /> },
  { href: "/app/workflows", label: "Workflows", icon: <GitBranch /> },
  { href: "/app/runs", label: "Runs", icon: <Play /> },
  { href: "/app/variants", label: "Variants", icon: <Layers /> },
  { href: "/app/transforms", label: "Transforms", icon: <FileCode /> },
  { href: "/app/delivery", label: "Delivery", icon: <GitPullRequest /> },
  { href: "/app/studio", label: "Studio", icon: <FlaskConical /> },
  { href: "/app/authoring", label: "Author", icon: <PenLine /> },
  { href: "/app/wiring", label: "Wiring", icon: <Share2 /> },
  { href: "/app/context", label: "Context", icon: <SlidersHorizontal /> },
  // Memory sits beside Context because a reader confuses the two more often than any other pair, and
  // adjacency is where the distinction is cheapest to make: within one call vs across invocations.
  { href: "/app/memory", label: "Memory", icon: <Brain /> },
  // Harness sits after Memory because the three are the axes a reader most often conflates, and the
  // order teaches the distinction: within one call (context), across calls (memory), around the call
  // (harness). It is also the only one that can multiply what a node costs, so it is the last one a
  // reader reaches rather than the first.
  { href: "/app/harness", label: "Harness", icon: <Repeat /> },
  // Coverage sits last in the primary group because it is the surface a reader reaches WHEN an axis
  // declines — it explains the boundary the other surfaces enforce, rather than being a place to work.
  { href: "/app/coverage", label: "Coverage", icon: <Grid3x3 /> },
];

const SETTINGS: Surface[] = [
  // Install is the one rail entry that leaves the console: the page is PUBLIC (/install), because its readers
  // are people who do not have the CLI and therefore have no account. It is linked rather than duplicated —
  // two copies of an install command is exactly the drift the distribution contract exists to prevent.
  //
  // It sits in the settings group rather than the primary rail because it is a surface a reader visits ONCE —
  // to get the binary, or to answer a security review — not a place to work.
  { href: "/install", label: "Install", icon: <HardDriveDownload /> },
  // Documentation is the same shape as Install and linked for the same reason: it is PUBLIC (/docs), it
  // needs no session, and a second copy of a command inside the console is the drift the whole accuracy
  // fence set exists to prevent. A console whose only route to the documentation is a search engine is a
  // console that ships its documentation to strangers and hides it from customers.
  { href: "/docs", label: "Documentation", icon: <BookOpen /> },
  { href: "/app/configure", label: "Configure", icon: <Settings /> },
  { href: "/app/billing", label: "Billing", icon: <CreditCard /> },
  // 🔴 "Organization", not "Account". This renders the ORGANIZATION's plan, entitlements and usage
  // (its heading is the organization id); P28 added a personal `/app/settings/account` below, and for
  // a while the rail carried TWO items labelled "Account" pointing at different pages. The noun
  // dictionary calls a word that means two things a defect rather than a synonym, and a rail is where
  // that costs the most: the reader cannot tell which one holds what they came for, and picks wrong
  // half the time.
  { href: "/app/account", label: "Organization", icon: <User /> },
  // P27. It sits beside Account and Billing because it answers the same class of question — who and
  // what this organization is — rather than being a subject you open.
  { href: "/app/settings/members", label: "Members", icon: <Users /> },
  // P28. Under Settings, beside Members, because the left rail holds task DOMAINS: "other people" and
  // "me" are two of them, and merging them is how an admin changes their own password from a row that
  // looks like somebody else's.
  { href: "/app/settings/account", label: "Account", icon: <UserCircle /> },
];

export default async function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const session = await requireSession();
  const visited = visitedSubjects(session);

  // The command path reaches every surface AND every subject this session has opened. The surfaces are
  // static; the subjects are the console's only answer to the enumeration gap (subjects.ts).
  const entries: CommandEntry[] = [
    { id: "s:overview", group: "Surface", label: "Overview", href: routes.overview() },
    { id: "s:ask", group: "Surface", label: "Ask a question", href: routes.ask() },
    { id: "s:configure", group: "Surface", label: "Configure a variant", href: routes.configure() },
    { id: "s:workflows", group: "Surface", label: "Workflows", href: "/app/workflows" },
    { id: "s:runs", group: "Surface", label: "Runs", href: "/app/runs" },
    { id: "s:transforms", group: "Surface", label: "Transforms", href: "/app/transforms" },
    { id: "s:delivery", group: "Surface", label: "Delivery", href: "/app/delivery" },
    { id: "s:variants", group: "Surface", label: "Variants", href: "/app/variants" },
    { id: "s:studio", group: "Surface", label: "Prompt & Model Studio", href: "/app/studio" },
    { id: "s:authoring", group: "Surface", label: "Author a change", href: "/app/authoring" },
    { id: "s:wiring", group: "Surface", label: "Node wiring", href: "/app/wiring" },
    { id: "s:context", group: "Surface", label: "Context strategy", href: "/app/context" },
    { id: "s:memory", group: "Surface", label: "Memory strategy", href: "/app/memory" },
    { id: "s:harness", group: "Surface", label: "Harness strategy", href: "/app/harness" },
    { id: "s:coverage", group: "Surface", label: "Coverage — what applies where", href: "/app/coverage" },
    { id: "s:install", group: "Surface", label: "Install the CLI — channels, platforms, trust", href: "/install" },
    { id: "s:account", group: "Surface", label: "Plan and spend", href: routes.account() },
    { id: "s:billing", group: "Surface", label: "Billing and payment method", href: routes.billing() },
    { id: "s:members", group: "Surface", label: "Members, invitations and API keys", href: "/app/settings/members" },
    // 🔴 `s:my-account`, not a second `s:account`. Two entries carried the SAME id — a duplicate React
    // key, and a duplicate identifier in a palette that is keyed by it, so which of the two the reader
    // reaches is a rendering detail rather than a decision. Same root as the two "Account" items in the
    // rail above: P28's personal page was added beside an existing organization page and took its name
    // and its id with it.
    { id: "s:my-account", group: "Surface", label: "Your account, password and email", href: "/app/settings/account" },
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
