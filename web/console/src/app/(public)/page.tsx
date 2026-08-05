import Link from "next/link";
import type { Metadata } from "next";
import { ArrowRight, Boxes, Check, ChevronRight, GitPullRequest, Minus, Server } from "lucide-react";
import { Claim } from "@/components/marketing/claim";
import { CAPABILITIES, PLAN_ORDER } from "@/lib/entitlements";
import { cx } from "@/lib/cx";

/**
 * The public home page.
 *
 * # What is new here, and what is unchanged
 *
 * The composition is the design system's: a hero with a worked example beside it, the demo recording,
 * four difference cards, the loop as four steps, the last-mile pair (how a change is delivered and
 * where the platform runs), the claims, the plans, a close. What did NOT change is the thing the page
 * is for — every capability sentence still renders through `<Claim>`, so it resolves against the
 * manifest in `src/content/capabilities.ts` or the build fails (FR33, R15). A prettier page that could
 * describe the roadmap in the present tense would be a worse page.
 *
 * The demo section is the one panel on the page that is NOT drawn, and it is labelled with the same
 * care for the opposite reason: a real recording earns nothing if a reader cannot tell it from the
 * illustrations around it. Its caption states how it was captured and the one way it was processed
 * (idle gaps capped at 3.2s), and its three cards quote strings the tool printed rather than making
 * capability claims the manifest has not been asked to carry. It is served from this origin, because
 * the CSP has no `media-src` and the public-surface test fails any external `src`.
 *
 * The last-mile section (delivery, P12; deployment, P19) follows the same rule: its two panels are
 * labelled illustrations, exactly like the hero's DemoBoard, and the hard claims underneath them —
 * `ci-mediated`, `deploy` — go through `<Claim>` in the claims grid where the build fence can see them.
 *
 * Two design-bundle details were deliberately dropped:
 *
 *   - **The per-plan feature lists.** They read well and they are unsourced. What each plan includes is
 *     resolved by the platform at runtime, so the cards below list the console capabilities this
 *     repository's own entitlement table says each plan unlocks — a claim with a source — rather than
 *     numbers written by whoever last touched the page.
 *   - **"Start free · no credit card required".** This product has no self-serve signup. A call to
 *     action that promises one is a claim the platform cannot honour, which is precisely what R15 is
 *     for. The call to action is to open the console.
 *
 * The page renders no session and makes no upstream call, which is what keeps it serving when the
 * platform is down (FR32, NFR13). A test asserts both.
 */
export const metadata: Metadata = {
  title: "Heros — evidence for changes to LLM workflows",
  description:
    "Change one call site, run it sandboxed, and score it over multiple seeds with confidence intervals — so a change ships because it was measured to help, not because it looked better.",
};

// ── The difference · the four properties the product is willing to be judged on ──

const DIFFERENCES = [
  {
    n: "01",
    title: "It says when there is no winner",
    body: "When two variants' confidence intervals overlap, the board says so and shows the ranks as an ordering rather than as evidence. It will not pick one for you when the measurement cannot.",
    limit: "Some eval sets will never produce a winner. That is the correct answer, not a failure of the tool.",
    accent: true,
  },
  {
    n: "02",
    title: "A gate is not a preference",
    body: "A variant that fails a gate is excluded from the ranked order — not ranked last, and not traded off against a good score somewhere else.",
    limit: "Which gates apply is yours to define. A gate nobody wrote cannot exclude anything.",
  },
  {
    n: "03",
    title: "Coverage keeps its own gaps",
    body: "What the eval set never exercised stays in the denominator. Dropping it would raise the percentage by deleting the evidence of failure, so the number you see is the honest one.",
    limit: "Coverage tells you what was never tested. It cannot test it for you.",
  },
  {
    n: "04",
    title: "An unverified proposal is labelled",
    body: "A proposed change that fails verification is withheld and said to be withheld. It is never shown beside a verified one in a form that looks like evidence.",
    limit: "Withheld work is still shown. Visible is not the same as recommended, and the page says which it is.",
  },
];

const STEPS = [
  {
    n: "1",
    tag: "Zero config",
    title: "Discover",
    body: "Point it at a repository. It finds the call sites, builds the graph, and names the patterns it can — saying so where it cannot.",
  },
  {
    n: "2",
    tag: "Reviewable",
    title: "Change one call site",
    body: "A model, a prompt, a skill set or a context policy, on one node. You get a diff before anything runs, and the platform tells you whether a compiler stood behind it.",
  },
  {
    n: "3",
    tag: "Isolated",
    title: "Run and score it",
    body: "Sandboxed, traced, multi-seed. Every score arrives with its interval, its seed count, and what the eval set did and did not cover.",
  },
  {
    n: "4",
    tag: "Human merges",
    title: "Decide",
    body: "A verified change arrives as a pull request. A person reads it and merges it — the platform does not, unless you have explicitly granted and budgeted for that.",
  },
];

// ── The recording · what to watch for in it ──────────────────────────────────

/**
 * DEMO_MOMENTS are three things the recording SHOWS, quoted from it.
 *
 * They are deliberately not written as capability sentences. A capability claim on this surface goes
 * through `<Claim>` and resolves against the manifest or the build fails (FR33, R15) — and the right
 * way to earn a new entry there is to ship the capability, not to describe a video. What these three
 * cards do instead is point at moments in a recording anybody can scrub to and check, with the strings
 * the tool actually printed. The distinction matters: the claims grid says what the platform does; this
 * says what this session did, and a reader can settle it themselves in sixty-six seconds.
 */
const DEMO_MOMENTS = [
  {
    step: "6 / 11",
    title: "A refusal, by name",
    body:
      "A model change is authored against a node the offline build has no registry entry for. It is not " +
      "silently skipped and not half-applied: the tool declines, names the node, the dimension and the " +
      "ref it could not resolve, and reports that nothing was written — on any plan or role.",
  },
  {
    step: "10 / 11",
    title: "A failed gate is an exit code",
    body:
      "The same eval is run under a minimum quality of 0.99. It scores 0.7456, and the gate failure is " +
      "the process exit status — 1 — not a warning in a log. The run also labels itself provisional " +
      "rather than reporting a single-seed number as a graded result.",
  },
  {
    step: "11 / 11",
    title: "The payload, before it is sent",
    body:
      "link --dry-run prints the exact bytes an opt-in upload would carry and transmits none of them: " +
      "metrics, IR structure, scores, and a commit sha. Grepped live on screen for file paths, prompt " +
      "text and provider keys — three times NO.",
  },
];

// ── The worked example beside the hero ───────────────────────────────────────

const DEMO_ROWS = [
  { rank: "—", variant: "gpt-4o / temp-0.3", score: "0.847", ci: "0.041", tied: true },
  { rank: "—", variant: "claude-3-5-sonnet", score: "0.831", ci: "0.038", tied: true },
  { rank: "3", variant: "gpt-4o-mini / temp-0.1", score: "0.769", ci: "0.019", tied: false },
];

/**
 * DemoBoard is an ILLUSTRATION and is labelled as one.
 *
 * It shows the behaviour the page is claiming — two variants tied, no winner declared, intervals
 * beside every figure — with invented numbers. Calling it an illustration in the caption is not
 * pedantry: a screenshot-shaped panel of numbers on a marketing page reads as a customer's real data
 * unless it says otherwise.
 */
function DemoBoard() {
  return (
    <figure className="relative m-0">
      <div className="overflow-hidden rounded-2xl border border-marketing-ink/10 bg-marketing-ink/5 shadow-2xl backdrop-blur-xl">
        <div className="flex items-center justify-between border-b border-marketing-ink/8 px-4 py-3">
          <div>
            <p className="font-mono text-[10px] uppercase tracking-widest text-marketing-ink/40">
              customer-support-v2
            </p>
            <p className="mt-0.5 font-mono text-xs text-marketing-ink/70">Eval board · weight: balanced</p>
          </div>
        </div>

        <div className="flex items-center gap-2 border-b border-marketing-ink/5 bg-marketing-ink/4 px-4 py-2">
          <Minus className="size-3 shrink-0 text-marketing-ink/50" aria-hidden="true" />
          <span className="text-xs text-marketing-ink/50">
            Top 2 are statistically tied — no winner can be declared
          </span>
        </div>

        <div className="divide-y divide-marketing-ink/5">
          <div className="grid grid-cols-[1.75rem_1fr_5rem_3.5rem] px-4 py-1.5">
            {["#", "Variant", "Score", "Gate"].map((head) => (
              <span
                key={head}
                className="font-mono text-[9px] uppercase tracking-widest text-marketing-ink/25"
              >
                {head}
              </span>
            ))}
          </div>
          {DEMO_ROWS.map((row) => (
            <div key={row.variant} className="grid grid-cols-[1.75rem_1fr_5rem_3.5rem] items-center px-4 py-3">
              <span
                className={cx("font-mono text-xs", row.tied ? "text-marketing-ink/30" : "text-marketing-ink/60")}
              >
                {row.rank}
              </span>
              <span className="min-w-0">
                <span
                  className={cx(
                    "block truncate font-mono text-xs",
                    row.tied ? "text-marketing-ink/45" : "text-marketing-ink/80",
                  )}
                >
                  {row.variant}
                </span>
              </span>
              <span className="flex flex-col">
                <span
                  className={cx(
                    "font-mono text-sm font-medium tabular-nums",
                    row.tied ? "text-marketing-ink/45" : "text-marketing-accent",
                  )}
                >
                  {row.score}
                </span>
                <span className="mt-0.5 flex items-center gap-1">
                  <span className="font-mono text-[9px] text-marketing-ink/25">±{row.ci}</span>
                  {row.tied ? (
                    <span className="rounded border border-marketing-ink/15 bg-marketing-ink/10 px-1 py-px font-mono text-[9px] text-marketing-ink/50">
                      tied
                    </span>
                  ) : null}
                </span>
              </span>
              <span className="inline-flex items-center gap-1 rounded bg-marketing-accent/10 px-1.5 py-0.5 font-mono text-[10px] text-marketing-accent">
                pass
              </span>
            </div>
          ))}
        </div>
      </div>

      <div className="absolute -right-3 -top-3 rounded-lg border border-marketing-ink/15 bg-marketing-panel/90 px-2.5 py-1.5 backdrop-blur">
        <p className="font-mono text-[9px] uppercase tracking-wider text-marketing-ink/40">No winner</p>
        <p className="font-mono text-[10px] font-medium text-marketing-ink/70">p = 0.31 › 0.05</p>
      </div>

      <figcaption className="mt-3 text-center font-mono text-[10px] text-marketing-ink/25">
        Illustration — the shape of a board, with invented numbers
      </figcaption>
    </figure>
  );
}

// ── The hero's graph strip ───────────────────────────────────────────────────

const HERO_NODES = [
  { id: "route", x: 40, y: 74, w: 148, h: 46, label: "route_request", file: "api/handler.ts", llm: false },
  { id: "classify", x: 246, y: 26, w: 156, h: 46, label: "classify_intent", file: "agents/intent.ts", llm: true },
  { id: "embed", x: 246, y: 132, w: 148, h: 46, label: "embed_query", file: "search/embed.ts", llm: false },
  { id: "rerank", x: 460, y: 74, w: 140, h: 46, label: "rerank_results", file: "search/rerank.ts", llm: false },
  { id: "generate", x: 460, y: 180, w: 152, h: 46, label: "generate_reply", file: "agents/respond.ts", llm: true },
  { id: "grade", x: 668, y: 128, w: 140, h: 46, label: "grade_output", file: "eval/grade.ts", llm: true },
];

const HERO_EDGES = [
  { x1: 188, y1: 94, x2: 246, y2: 49, dashed: false },
  { x1: 188, y1: 100, x2: 246, y2: 155, dashed: false },
  { x1: 394, y1: 155, x2: 460, y2: 100, dashed: false },
  { x1: 394, y1: 160, x2: 460, y2: 203, dashed: false },
  { x1: 402, y1: 49, x2: 460, y2: 92, dashed: true },
  { x1: 600, y1: 97, x2: 668, y2: 148, dashed: true },
  { x1: 612, y1: 203, x2: 668, y2: 157, dashed: false },
];

const HERO_REGIONS = [
  { x: 232, y: 8, w: 186, h: 176, label: "Retrieval" },
  { x: 444, y: 58, w: 184, h: 180, label: "Generation" },
];

function HeroGraph() {
  return (
    <svg viewBox="0 0 840 250" className="h-full w-full" aria-hidden="true">
      <defs>
        <marker id="hero-arrow" markerWidth="6" markerHeight="6" refX="5" refY="3" orient="auto">
          <path d="M0,0 L6,3 L0,6 Z" className="fill-marketing-accent/40" />
        </marker>
      </defs>

      {HERO_REGIONS.map((region) => (
        <g key={region.label}>
          <rect
            x={region.x}
            y={region.y}
            width={region.w}
            height={region.h}
            rx="8"
            className="fill-marketing-accent/3 stroke-marketing-accent/12"
            strokeWidth="1"
          />
          <text
            x={region.x + 10}
            y={region.y + 16}
            fontSize="9"
            letterSpacing="0.08em"
            className="fill-marketing-accent/40 font-mono"
          >
            {region.label.toUpperCase()}
          </text>
        </g>
      ))}

      {HERO_EDGES.map((edge, index) => (
        <line
          key={index}
          x1={edge.x1}
          y1={edge.y1}
          x2={edge.x2}
          y2={edge.y2}
          className={edge.dashed ? "stroke-marketing-accent/20" : "stroke-marketing-accent/35"}
          strokeWidth="1.5"
          strokeDasharray={edge.dashed ? "4 3" : undefined}
          markerEnd="url(#hero-arrow)"
        />
      ))}

      {HERO_NODES.map((node) => (
        <g key={node.id}>
          <rect
            x={node.x}
            y={node.y}
            width={node.w}
            height={node.h}
            rx="6"
            className={cx(
              "fill-marketing-panel/70",
              node.llm ? "stroke-marketing-accent/45" : "stroke-marketing-ink/10",
            )}
            strokeWidth="1"
          />
          {node.llm ? (
            <rect x={node.x} y={node.y} width={node.w} height="2" className="fill-marketing-accent/50" />
          ) : null}
          <text
            x={node.x + 12}
            y={node.y + 21}
            fontSize="10"
            className={cx("font-mono", node.llm ? "fill-marketing-accent/90" : "fill-marketing-ink/45")}
          >
            {node.label}
          </text>
          <text x={node.x + 12} y={node.y + 35} fontSize="8.5" className="fill-marketing-ink/30 font-mono">
            {node.file}
          </text>
        </g>
      ))}
    </svg>
  );
}

// ── The last mile · delivery (P12) and deployment (P19) ──────────────────────

/**
 * Both panels below are ILLUSTRATIONS and are labelled as such, on the same terms as DemoBoard: the
 * SHAPE of the thing with invented specifics. The checkable claims they dramatise — a PR opened by
 * your own CI, one image set on two substrates — render through `<Claim id="ci-mediated">` and
 * `<Claim id="deploy">` in the claims section, where the build fence can see them.
 */

const DELIVERY_CHECKS = [
  { label: "eval gate", value: "passed", tone: "accent" },
  { label: "verified", value: "−38% cost · quality held", tone: "accent" },
  { label: "diff", value: "1 file · 1 call site", tone: "ink" },
];

/** DeliveryCard — a delivered pull request, opened by the customer's CI (P12). */
function DeliveryCard() {
  return (
    <figure className="relative m-0">
      <div className="overflow-hidden rounded-2xl border border-marketing-ink/10 bg-marketing-ink/5 shadow-2xl backdrop-blur-xl">
        <div className="flex items-center gap-2 border-b border-marketing-ink/8 px-4 py-3">
          <GitPullRequest className="size-3.5 shrink-0 text-marketing-accent" aria-hidden="true" />
          <p className="min-w-0 truncate font-mono text-xs text-marketing-ink/70">
            heros/optimize <span className="text-marketing-ink/40">·</span>{" "}
            <span className="text-marketing-ink/45">generate_reply → gpt-4o-mini</span>
          </p>
        </div>

        <div className="flex items-center gap-2 border-b border-marketing-ink/5 bg-marketing-accent/6 px-4 py-2">
          <span className="size-1.5 shrink-0 rounded-full bg-marketing-accent" aria-hidden="true" />
          <span className="text-xs text-marketing-ink/60">
            Opened by <span className="font-mono text-marketing-ink/85">your CI</span> — the platform holds no
            repo token
          </span>
        </div>

        <ul className="m-0 list-none divide-y divide-marketing-ink/5 p-0">
          {DELIVERY_CHECKS.map((row) => (
            <li key={row.label} className="flex items-center justify-between px-4 py-3">
              <span className="flex items-center gap-2">
                <Check className="size-3.5 shrink-0 text-marketing-accent" aria-hidden="true" />
                <span className="font-mono text-[10px] uppercase tracking-widest text-marketing-ink/30">
                  {row.label}
                </span>
              </span>
              <span
                className={cx(
                  "font-mono text-xs tabular-nums",
                  row.tone === "accent" ? "text-marketing-accent" : "text-marketing-ink/60",
                )}
              >
                {row.value}
              </span>
            </li>
          ))}
        </ul>
      </div>

      <div className="absolute -right-3 -top-3 rounded-lg border border-marketing-ink/15 bg-marketing-panel/90 px-2.5 py-1.5 backdrop-blur">
        <p className="font-mono text-[9px] uppercase tracking-wider text-marketing-ink/40">CI-mediated</p>
        <p className="font-mono text-[10px] font-medium text-marketing-ink/70">no repo write access</p>
      </div>

      <figcaption className="mt-3 text-center font-mono text-[10px] text-marketing-ink/25">
        Illustration — a delivered pull request, opened by your CI
      </figcaption>
    </figure>
  );
}

const SUBSTRATES = [
  { icon: Server, name: "Docker Compose", detail: "single host · open-core" },
  { icon: Boxes, name: "Kubernetes", detail: "a cluster · dev / staging / prod / air-gapped" },
];

/** DeploymentDiagram — one digest-pinned image set fanning to two substrates (P19). */
function DeploymentDiagram() {
  return (
    <figure className="relative m-0">
      <div className="rounded-2xl border border-marketing-ink/10 bg-marketing-ink/5 p-6 shadow-2xl backdrop-blur-xl">
        <div className="mx-auto w-full max-w-[16rem] rounded-xl border border-marketing-accent/25 bg-marketing-accent/8 px-4 py-3 text-center">
          <p className="font-mono text-[10px] uppercase tracking-widest text-marketing-accent/60">images.env</p>
          <p className="mt-1 font-mono text-xs text-marketing-ink/75">one digest-pinned image set</p>
        </div>

        <svg viewBox="0 0 240 34" className="mx-auto h-8 w-full max-w-[16rem]" aria-hidden="true">
          <path
            d="M120 0 L120 12 M120 12 L58 12 L58 32 M120 12 L182 12 L182 32"
            className="fill-none stroke-marketing-accent/30"
            strokeWidth="1.25"
          />
        </svg>

        <div className="grid grid-cols-2 gap-3">
          {SUBSTRATES.map((substrate) => {
            const Icon = substrate.icon;
            return (
              <div
                key={substrate.name}
                className="rounded-xl border border-marketing-ink/10 bg-marketing-panel/60 p-4"
              >
                <Icon className="mb-2 size-4 text-marketing-accent/80" aria-hidden="true" />
                <p className="font-mono text-xs text-marketing-ink/80">{substrate.name}</p>
                <p className="mt-1 text-[11px] leading-relaxed text-marketing-ink/35">{substrate.detail}</p>
              </div>
            );
          })}
        </div>

        <p className="mt-5 flex items-center justify-center gap-2 rounded-lg border border-marketing-ink/8 bg-marketing-ink/3 px-3 py-2">
          <span className="size-1.5 rounded-full bg-marketing-accent/70" aria-hidden="true" />
          <span className="font-mono text-[10px] uppercase tracking-wider text-marketing-ink/45">
            one deployment = one tenant boundary
          </span>
        </p>
      </div>

      <figcaption className="mt-3 text-center font-mono text-[10px] text-marketing-ink/25">
        Illustration — the same image set, either substrate
      </figcaption>
    </figure>
  );
}

// ── Plans ───────────────────────────────────────────────────────────────────

/** unlocksAt lists the console capabilities this repository's entitlement table attributes to a plan. */
function unlocksAt(plan: (typeof PLAN_ORDER)[number]) {
  return CAPABILITIES.filter((capability) => capability.unlockedBy === plan);
}

export default function HomePage() {
  return (
    <>
      {/* ── Hero ──────────────────────────────────────────────────────────── */}
      <section className="relative flex min-h-[85vh] items-center overflow-hidden">
        <div
          className="absolute inset-0 bg-[length:48px_48px]"
          style={{ backgroundImage: "var(--marketing-grid)" }}
          aria-hidden="true"
        />
        <div
          className="absolute left-1/4 top-1/4 size-[38rem] rounded-full opacity-[0.07]"
          style={{ backgroundImage: "var(--marketing-glow-primary)" }}
          aria-hidden="true"
        />
        <div
          className="absolute bottom-1/4 right-1/4 size-[25rem] rounded-full opacity-[0.05]"
          style={{ backgroundImage: "var(--marketing-glow-secondary)" }}
          aria-hidden="true"
        />
        <div
          className="absolute inset-x-0 bottom-0 h-40"
          style={{ backgroundImage: "var(--marketing-fade)" }}
          aria-hidden="true"
        />

        <div className="relative z-10 mx-auto w-full max-w-7xl px-6 py-20 md:px-12">
          <div className="grid items-center gap-16 lg:grid-cols-2 xl:gap-24">
            <div>
              <p className="mb-8 inline-flex items-center gap-2 rounded-full border border-marketing-accent/20 bg-marketing-accent/10 px-3 py-1.5">
                <span className="size-1.5 rounded-full bg-marketing-accent" aria-hidden="true" />
                <span className="font-mono text-xs tracking-wider text-marketing-accent">
                  A scientific instrument for LLM evaluation
                </span>
              </p>

              <h1 className="mb-6 font-display text-5xl font-light leading-[1.08] tracking-tight text-marketing-ink md:text-6xl xl:text-7xl">
                Most LLM workflows are tuned on a hunch.
                <br />
                <span className="text-marketing-accent">This one ships on evidence.</span>
              </h1>

              <p className="mb-4 max-w-lg text-lg leading-relaxed text-marketing-ink/55">
                Heros maps every LLM call site in your repository into a graph you can tune — the model,
                the prompt, the context, the tools, the wiring — one reviewable change at a time. It runs
                each sandboxed, scores it over multiple seeds, and tells you plainly when the difference
                is too small to call.
              </p>

              <p className="mb-10 max-w-md text-sm text-marketing-ink/35">
                The eval platform that says <span className="font-mono text-marketing-ink/60">no winner</span>{" "}
                instead of picking one.
              </p>

              <div className="flex flex-wrap items-center gap-3">
                <Link
                  className="inline-flex items-center gap-2 rounded-xl bg-marketing-accent px-6 py-3 text-sm font-medium text-marketing-accent-ink transition-opacity hover:opacity-90"
                  href="/signin"
                >
                  Open the console
                  <ArrowRight className="size-4" aria-hidden="true" />
                </Link>
                <a
                  className="inline-flex items-center gap-2 rounded-xl border border-marketing-ink/10 px-6 py-3 text-sm text-marketing-ink/70 transition-colors hover:border-marketing-ink/20 hover:text-marketing-ink"
                  href="#how"
                >
                  See how it works
                  <ChevronRight className="size-4" aria-hidden="true" />
                </a>
              </div>

              <p className="mt-8 max-w-md text-xs leading-relaxed text-marketing-ink/25">
                The console holds no API key. Your browser gets a session it cannot read; the credential
                stays on the server.
              </p>
            </div>

            <div className="relative hidden lg:block">
              <DemoBoard />
            </div>
          </div>

          <figure className="relative m-0 mt-16 h-44 overflow-hidden rounded-2xl border border-marketing-ink/6 bg-marketing-panel/30">
            <HeroGraph />
            <figcaption className="absolute bottom-3 right-4 font-mono text-[10px] text-marketing-ink/20">
              Illustration — a pattern-classified graph, 6 call sites, 7 edges
            </figcaption>
          </figure>
        </div>
      </section>

      {/* ── The demo ──────────────────────────────────────────────────────── */}
      <section className="border-t border-marketing-ink/5 px-6 py-24 md:px-12" id="demo">
        <div className="mx-auto max-w-7xl">
          <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-accent">
            The demo
          </p>
          <h2 className="mb-4 max-w-xl font-display text-4xl font-light text-marketing-ink">
            Not an illustration. A recording.
          </h2>
          <p className="mb-12 max-w-2xl text-sm leading-relaxed text-marketing-ink/55">
            The panels elsewhere on this page are drawn, and each one says so. This is the tool itself,
            run against{" "}
            <span className="font-mono text-marketing-ink/75">openclaw/openclaw</span> — a public
            TypeScript monorepo of some 30,500 files, chosen because it is somebody else&rsquo;s code and
            not a fixture.
          </p>

          <div className="grid gap-10 lg:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)] lg:items-start">
            <figure className="m-0">
              {/*
                Same-origin, and that is a constraint rather than a convenience.
                `default-src 'self'` governs media — the policy in third-party-policy.ts has no
                `media-src` and cannot express one — so a video served from a bucket would be REFUSED
                by the browser, and `tests/public-surface.test.mjs` fails any element carrying an
                external `src` before it ever got that far. The file ships in `public/`.

                No `controls`: the chrome sat on top of the terminal output, which is the only thing
                the panel is for. So it plays as an ambient loop instead — `muted` and `playsInline`
                are what let a browser autoplay at all, and the poster still covers the first paint so
                nothing pops in.

                ⚠️ Two things this costs, stated rather than discovered later. Every visitor now
                fetches 1.7 MB on load, where `preload="none"` used to cost only the poster. And a
                looping video does not honour `prefers-reduced-motion` the way the motion budget in
                `tokens.customer.css` makes every CSS duration honour it (FR28) — this is the one
                moving thing on the page outside that budget. Fixing it properly means a client
                component, and the public surface deliberately ships none.

                The page still makes no upstream call (FR32, NFR13): a static asset on the console's
                own origin is not a request to the platform.
              */}
              <video
                className="w-full rounded-2xl border border-marketing-ink/10 bg-marketing-ink/5 shadow-2xl"
                autoPlay
                loop
                muted
                playsInline
                poster="/demo/heros-cli-openclaw-poster.jpg"
                src="/demo/heros-cli-openclaw.mp4"
                width={1280}
                height={720}
                aria-label="Terminal recording of the heros CLI run against the openclaw/openclaw repository"
              >
                <a href="/demo/heros-cli-openclaw.mp4">Download the recording — MP4, 1 minute 6 seconds</a>
              </video>
              <figcaption className="mt-4 text-xs leading-relaxed text-marketing-ink/35">
                A real session, captured under a PTY and replayed into a terminal — no output retyped
                and none re-simulated. Idle gaps between commands are capped at 3.2 seconds, so 565
                seconds of real work plays in 63; nothing else about the timing or the content is
                altered. No audio.
              </figcaption>
            </figure>

            <div>
              <p className="mb-5 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-ink/30">
                Three moments
              </p>
              <ol className="m-0 list-none space-y-4 p-0">
                {DEMO_MOMENTS.map((moment) => (
                  <li
                    key={moment.step}
                    className="rounded-xl border border-marketing-ink/8 bg-marketing-ink/3 p-5"
                  >
                    <span className="mb-2 block font-mono text-[10px] tracking-widest text-marketing-ink/25">
                      step {moment.step}
                    </span>
                    <h3 className="mb-2 font-display text-lg font-normal text-marketing-ink">
                      {moment.title}
                    </h3>
                    <p className="text-xs leading-relaxed text-marketing-ink/50">{moment.body}</p>
                  </li>
                ))}
              </ol>
            </div>
          </div>
        </div>
      </section>

      {/* ── The difference ────────────────────────────────────────────────── */}
      <section className="border-t border-marketing-ink/5 px-6 py-24 md:px-12">
        <div className="mx-auto max-w-7xl">
          <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-accent">
            The difference
          </p>
          <h2 className="mb-14 max-w-xl font-display text-4xl font-light text-marketing-ink">
            A tool that will tell you it does not know.
          </h2>
          <div className="grid gap-5 md:grid-cols-2">
            {DIFFERENCES.map((card) => (
              <article
                key={card.n}
                className={cx(
                  "rounded-2xl border p-8 transition-colors",
                  card.accent
                    ? "border-marketing-accent/25 bg-marketing-accent/5 hover:border-marketing-accent/40"
                    : "border-marketing-ink/8 bg-marketing-ink/3 hover:border-marketing-ink/15",
                )}
              >
                <span
                  className={cx(
                    "mb-4 block font-mono text-[10px] tracking-widest",
                    card.accent ? "text-marketing-accent/60" : "text-marketing-ink/25",
                  )}
                >
                  {card.n}
                </span>
                <h3
                  className={cx(
                    "mb-3 font-display text-2xl font-normal",
                    card.accent ? "text-marketing-accent" : "text-marketing-ink",
                  )}
                >
                  {card.title}
                </h3>
                <p className="mb-5 text-sm leading-relaxed text-marketing-ink/55">{card.body}</p>
                <p className="border-t border-marketing-ink/6 pt-4 text-xs leading-relaxed text-marketing-ink/30">
                  <span className="mr-1.5 font-mono text-marketing-ink/20">limit</span>
                  {card.limit}
                </p>
              </article>
            ))}
          </div>
        </div>
      </section>

      {/* ── How it works ──────────────────────────────────────────────────── */}
      <section className="border-t border-marketing-ink/5 px-6 py-24 md:px-12" id="how">
        <div className="mx-auto max-w-7xl">
          <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-accent">
            The loop
          </p>
          <h2 className="mb-16 font-display text-4xl font-light text-marketing-ink">
            Discover, change one thing, measure, decide.
          </h2>
          <ol className="relative grid list-none gap-10 p-0 md:grid-cols-4 md:gap-0">
            <span
              className="absolute left-[12.5%] right-[12.5%] top-8 hidden h-px bg-marketing-ink/8 md:block"
              aria-hidden="true"
            />
            {STEPS.map((step) => (
              <li key={step.n} className="relative md:px-6 md:first:pl-0 md:last:pr-0">
                <span className="relative z-10 mb-6 flex size-16 items-center justify-center rounded-full border border-marketing-ink/10 bg-marketing-panel">
                  <span className="font-display text-2xl font-light text-marketing-accent/80" aria-hidden="true">
                    {step.n}
                  </span>
                </span>
                <span className="mb-3 inline-flex items-center rounded border border-marketing-accent/15 bg-marketing-accent/8 px-2 py-0.5">
                  <span className="font-mono text-[10px] text-marketing-accent/70">{step.tag}</span>
                </span>
                <h3 className="mb-2 text-base font-medium text-marketing-ink">{step.title}</h3>
                <p className="text-sm leading-relaxed text-marketing-ink/45">{step.body}</p>
              </li>
            ))}
          </ol>
        </div>
      </section>

      {/* ── The last mile · delivery (P12) + deployment (P19) ─────────────── */}
      <section className="relative overflow-hidden border-t border-marketing-ink/5 px-6 py-24 md:px-12">
        <div
          className="absolute right-0 top-1/3 size-[30rem] rounded-full opacity-[0.05]"
          style={{ backgroundImage: "var(--marketing-glow-secondary)" }}
          aria-hidden="true"
        />
        <div className="relative mx-auto max-w-7xl">
          <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-accent">
            The last mile
          </p>
          <h2 className="mb-4 max-w-2xl font-display text-4xl font-light text-marketing-ink">
            It ends as a pull request — and it runs where you run.
          </h2>
          <p className="mb-14 max-w-xl text-sm leading-relaxed text-marketing-ink/40">
            A verified change is delivered the way your team already merges code, and the platform behind
            it stands up from one image set on a substrate you already operate. Two ends of the same
            promise: nothing lands, and nothing runs, outside your control.
          </p>

          <div className="grid gap-8 lg:grid-cols-2 lg:gap-12">
            <article className="flex flex-col">
              <span className="mb-4 inline-flex w-fit items-center rounded border border-marketing-accent/15 bg-marketing-accent/8 px-2 py-0.5">
                <span className="font-mono text-[10px] text-marketing-accent/70">P12 · Forge delivery</span>
              </span>
              <h3 className="mb-2 font-display text-2xl font-normal text-marketing-ink">
                Delivered by your own CI
              </h3>
              <p className="mb-8 max-w-md text-sm leading-relaxed text-marketing-ink/45">
                The optimization arrives as a pull request, opened from the CI you already run with the
                token it already holds — so the platform never needs write access to your repository. A
                hosted Git App is there if you want it, and off until you do.
              </p>
              <div className="mt-auto">
                <DeliveryCard />
              </div>
            </article>

            <article className="flex flex-col">
              <span className="mb-4 inline-flex w-fit items-center rounded border border-marketing-accent/15 bg-marketing-accent/8 px-2 py-0.5">
                <span className="font-mono text-[10px] text-marketing-accent/70">P19 · Deployment</span>
              </span>
              <h3 className="mb-2 font-display text-2xl font-normal text-marketing-ink">
                One image set, either substrate
              </h3>
              <p className="mb-8 max-w-md text-sm leading-relaxed text-marketing-ink/45">
                The whole platform stands up from one digest-pinned image set — a single host on Docker
                Compose, or a cluster on Kubernetes — from the same digests and the same environment
                contract, so a difference between them is a topology question, never an image one.
              </p>
              <div className="mt-auto">
                <DeploymentDiagram />
              </div>
            </article>
          </div>
        </div>
      </section>

      {/* ── Claims, each with its boundary ────────────────────────────────── */}
      <section className="border-t border-marketing-ink/5 bg-marketing-panel/40 px-6 py-24 md:px-12">
        <div className="mx-auto max-w-7xl">
          <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-accent">
            What it does
          </p>
          <h2 className="mb-4 font-display text-4xl font-light text-marketing-ink">
            Every claim here, with what it does not do.
          </h2>
          <p className="mb-14 max-w-xl text-sm leading-relaxed text-marketing-ink/40">
            Each line is a capability the platform ships today, and the boundary beside it is the part
            that usually gets left out. Nothing on this page can describe something that has not
            shipped — the build refuses it.
          </p>
          <ul className="grid list-none gap-4 p-0 md:grid-cols-2">
            <Claim id="discover" />
            <Claim id="classify" />
            <Claim id="variant" />
            <Claim id="sandbox" />
            <Claim id="evaluate" />
            <Claim id="gates" />
            <Claim id="coverage" />
            <Claim id="judge-calibration" />
            <Claim id="attribution" />
            <Claim id="proposals" />
            <Claim id="pull-request" />
            <Claim id="ci-mediated" />
            <Claim id="console" />
            <Claim id="deploy" />
            <Claim id="billing-evidence" />
          </ul>
        </div>
      </section>

      {/* ── Plans ─────────────────────────────────────────────────────────── */}
      <section className="border-t border-marketing-ink/5 px-6 py-24 md:px-12" id="plans">
        <div className="mx-auto max-w-7xl">
          <p className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-marketing-accent">Plans</p>
          <h2 className="mb-4 font-display text-4xl font-light text-marketing-ink">Four plans, by name.</h2>
          <p className="mb-14 max-w-xl text-sm leading-relaxed text-marketing-ink/40">
            What each includes is resolved by the platform against your account, and the console shows a
            capability you do not have as gated with the plan that unlocks it — never hidden, and never
            as an error. Pricing is not published here.
          </p>
          <ul className="grid list-none gap-4 p-0 md:grid-cols-4">
            {PLAN_ORDER.map((plan) => {
              const unlocks = unlocksAt(plan);
              return (
                <li
                  key={plan}
                  className="flex flex-col rounded-2xl border border-marketing-ink/8 bg-marketing-ink/2 p-6"
                >
                  <h3 className="mb-5 font-display text-2xl font-normal text-marketing-ink">{plan}</h3>
                  {unlocks.length === 0 ? (
                    <p className="flex-1 text-sm leading-relaxed text-marketing-ink/40">
                      Adds no console capability of its own — it changes limits the platform resolves for
                      your account.
                    </p>
                  ) : (
                    <ul className="flex flex-1 list-none flex-col gap-3 p-0">
                      {unlocks.map((capability) => (
                        <li key={capability.id} className="text-sm text-marketing-ink/55">
                          <span className="block">{capability.label}</span>
                          <span className="mt-0.5 block text-xs leading-relaxed text-marketing-ink/30">
                            {capability.description}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                  <p className="mt-6 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/25">
                    unlocks in this console
                  </p>
                </li>
              );
            })}
          </ul>
        </div>
      </section>

      {/* ── Close ─────────────────────────────────────────────────────────── */}
      <section className="border-t border-marketing-ink/5 px-6 py-24 text-center md:px-12">
        <div className="mx-auto max-w-2xl">
          <h2 className="mb-4 font-display text-4xl font-light text-marketing-ink">
            Your eval set has a winner — or it does not.
          </h2>
          <p className="mb-10 text-lg text-marketing-ink/45">
            Either way, you do not have to adopt a platform to find out. Change one node, measure it, and
            read what the measurement actually supports.
          </p>
          <Link
            className="inline-flex items-center gap-2 rounded-xl bg-marketing-accent px-8 py-4 font-medium text-marketing-accent-ink transition-opacity hover:opacity-90"
            href="/signin"
          >
            Open the console
            <ArrowRight className="size-5" aria-hidden="true" />
          </Link>
        </div>
      </section>
    </>
  );
}
