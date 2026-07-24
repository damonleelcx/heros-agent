// craft.test.mjs holds the assertions for the experience layer above the interface floor (§15).
//
// The floor is asserted next door in interface-floor.test.mjs: keyboard reach, four states, contrast,
// escaped values, the pinned locale. This file asserts the things that make the console the surface an
// operator PREFERS — and, more importantly, the invariant that keeps "preferred" from meaning "easier
// to destroy something with" (FR37).
//
// Every test here is written so it can actually go red. Where a guard is itself the thing under test
// (the token scanner), the test injects a violation and asserts the guard fails — a fence nobody has
// ever seen fail is a fence nobody knows is connected.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir, writeFile, unlink } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;

async function read(rel) {
  return readFile(join(ROOT, rel), "utf8");
}

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else yield rel;
  }
}

async function sources(ext = /\.tsx?$/) {
  const out = [];
  for await (const file of walk("src")) if (ext.test(file)) out.push(file);
  return out;
}

// ── 15.14 · Design language conformance ──────────────────────────────────────

test("the token scan passes on the current tree", async () => {
  const { stdout } = await exec("node", ["scripts/scan-tokens.mjs"], { cwd: ROOT });
  assert.match(stdout, /token scan passed/);
});

test("the token scan goes RED on an injected literal", async () => {
  // A guard that has never been seen to fail is a guard nobody knows is wired up. This injects the
  // exact violation the guard exists for and requires a non-zero exit.
  const probe = join(ROOT, "src", "__token_probe.css");
  await writeFile(probe, ".probe { color: #ff0000; padding: 13px; border-radius: 3px; }\n");
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-tokens.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1, "the token scan did not fail on a literal");
        assert.match(error.stderr, /colour literal/);
        assert.match(error.stderr, /spacing literal/);
        assert.match(error.stderr, /radius literal/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
  }
});

test("the shared token system is imported, not forked", async () => {
  const css = await read("src/app/globals.css");
  assert.match(css, /@import "\.\.\/\.\.\/\.\.\/design-system\/tokens\.css"/, "globals.css does not import the shared tokens");
  assert.match(css, /@import "\.\/tokens\.operator\.css"/, "globals.css does not import the operator identity layer");

  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  // The shared layer must NOT carry the operator's identity — that is the whole of Decision 12.
  assert.equal(/--chrome-bg/.test(shared), false, "the shared token system carries operator chrome");
  assert.equal(/--accent:/.test(shared), false, "the shared token system carries a surface accent");
});

test("density changes the rhythm and never the information", async () => {
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  const block = shared.match(/:root\[data-density="compact"\]\s*{([^}]*)}/);
  assert.ok(block, "no compact density block");

  // Every declaration in the compact block must be a spacing/rhythm token. A `display: none`, a
  // `visibility`, or an overridden font-size would mean compact hides or shrinks CONTENT, which is a
  // second information architecture wearing a preference's clothes.
  const declarations = block[1]
    .split(";")
    .map((d) => d.trim())
    .filter(Boolean)
    .map((d) => d.split(":")[0].trim());
  for (const property of declarations) {
    assert.match(
      property,
      /^--(pad|stack)/,
      `compact density changes \`${property}\` — density may only change padding/stack rhythm`,
    );
  }
});

test("numbers are rendered for comparison: tabular figures, right-aligned, unit once", async () => {
  const css = await read("src/app/globals.css");
  assert.match(css, /\.num[^{]*{[^}]*text-align:\s*right/s, "numeric cells are not right-aligned");
  assert.match(css, /\.num[^{]*{[^}]*font-variant-numeric:\s*tabular-nums/s, "numeric cells are not tabular");
  assert.match(css, /\.stat__value\s*{[^}]*font-variant-numeric:\s*tabular-nums/s, "stat values are not tabular");

  const primitives = await read("src/components/primitives.tsx");
  // The unit belongs to the COLUMN, not the cell: DataTable renders it in the header.
  assert.match(primitives, /c\.unit \?/, "DataTable does not render a column unit");
  assert.match(primitives, /className={c\.numeric \? "num" : undefined}/, "numeric columns are not marked");
});

test("the hazard palette is reserved for hazard", async () => {
  const css = await read("src/app/globals.css");

  // Any rule that paints with --danger/--warn must either sit under a `HAZARD:` annotation or name a
  // hazard in its selector. Everything else is decoration, which is what FR31 forbids.
  const hazardSelectors =
    /danger|warn|impersonation|alarm|degraded|unknown|stale|palette__danger|action--global/i;
  const blocks = [...css.matchAll(/([^{}]+){([^}]*)}/g)];
  const offenders = [];
  for (const [, rawSelector, body] of blocks) {
    if (!/var\(--(danger|warn)/.test(body)) continue;
    const selector = rawSelector.trim().split("\n").pop().trim();
    const index = css.indexOf(rawSelector);
    const preamble = css.slice(Math.max(0, index - 400), index);
    const annotated = /HAZARD:/.test(preamble.split("*/").slice(-2).join("*/"));
    if (!hazardSelectors.test(selector) && !annotated) offenders.push(selector);
  }
  assert.deepEqual(offenders, [], `these rules use the hazard palette without being a hazard: ${offenders.join(", ")}`);
});

test("the operator identity does not reuse a hazard hue", async () => {
  const operator = await read("src/app/tokens.operator.css");
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  const hex = (source, name) => (source.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`)) ?? [])[1];

  // The console's accent must not BE the warning colour. This is the concrete defect the operator
  // identity was changed to fix: an amber console spends the signal its kill switch depends on.
  const accent = hex(operator, "--accent");
  const warn = hex(shared, "--warn");
  const danger = hex(shared, "--danger");
  assert.ok(accent && warn && danger, "could not read the accent and hazard hues");
  assert.notEqual(accent, warn, "the operator accent is the warning colour");
  assert.notEqual(accent, danger, "the operator accent is the danger colour");

  // …and not merely a different shade of the same hue. A hue distance of at least 60° keeps them
  // distinguishable for the readers who most need them to be.
  assert.ok(hueDistance(accent, warn) > 60, "the operator accent is within 60° of the warning hue");
  assert.ok(hueDistance(accent, danger) > 60, "the operator accent is within 60° of the danger hue");
});

function hueDistance(a, b) {
  const h = (hexColour) => {
    const [r, g, bl] = [1, 3, 5].map((i) => parseInt(hexColour.slice(i, i + 2), 16) / 255);
    const max = Math.max(r, g, bl);
    const min = Math.min(r, g, bl);
    if (max === min) return 0;
    const d = max - min;
    let hue;
    if (max === r) hue = ((g - bl) / d) % 6;
    else if (max === g) hue = (bl - r) / d + 2;
    else hue = (r - g) / d + 4;
    return ((hue * 60) % 360 + 360) % 360;
  };
  const diff = Math.abs(h(a) - h(b));
  return Math.min(diff, 360 - diff);
}

test("every view composes from the closed primitive set", async () => {
  const pages = (await sources()).filter((f) => f.startsWith(join("src", "app")) && f.endsWith("page.tsx"));
  assert.ok(pages.length >= 8, "expected the console's pages to be found");

  for (const page of pages) {
    const src = await read(page);
    if (page.includes("signin")) continue; // the sign-in page renders before an identity exists
    assert.match(src, /from "@\/components\/primitives"/, `${page} does not compose from the primitive set`);
    // An inline style object is a bespoke layout by another name.
    assert.equal(/style={{/.test(src), false, `${page} carries an inline style — use a primitive`);
  }
});

test("every class a view uses is defined in the stylesheet", async () => {
  // This is the "adding a primitive is deliberate" guard: a class invented in a page and never
  // defined is either a typo (silently unstyled) or a shape that skipped the design language.
  const css = await read("src/app/globals.css");
  const defined = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map((m) => m[1]));
  const dynamic = new Set(["pill--accent", "stat--changed"]); // composed at runtime from props

  for (const file of await sources()) {
    const src = await read(file);
    for (const match of src.matchAll(/className=(?:"([^"]+)"|{`([^`]*)`})/g)) {
      const raw = (match[1] ?? match[2] ?? "").replace(/\$\{[^}]*\}/g, " ");
      for (const cls of raw.split(/\s+/).filter(Boolean)) {
        // `pill pill--${tone}` leaves a bare `pill--` once the interpolation is stripped. The base
        // class is checked; the modifier is a runtime value and is covered by the token/CSS pairing
        // rather than by this scan.
        if (cls.endsWith("-") || dynamic.has(cls)) continue;
        assert.ok(defined.has(cls), `${file} uses .${cls}, which the stylesheet does not define`);
      }
    }
  }
});

// ── 15.15 · Velocity: the palette, and URL-addressable views ─────────────────

test("the command palette opens on one keystroke from any view", async () => {
  const palette = await read("src/components/commandPalette.tsx");
  // The listener is on the document, not on a container: "from any view" must not mean "from any
  // view where the operator has already clicked something".
  assert.match(palette, /document\.addEventListener\("keydown"/, "the palette has no document-level shortcut");
  assert.match(palette, /metaKey \|\| event\.ctrlKey/, "the palette is not bound to ⌘K / Ctrl-K");
  assert.match(palette, /event\.key === "Escape"/, "the palette does not close on Escape");
  assert.match(palette, /inputRef\.current\?\.focus\(\)/, "the palette does not focus its input on open");

  const shell = await read("src/components/shell.tsx");
  assert.match(shell, /<CommandPalette/, "the palette is not mounted in the shell that wraps every view");
});

test("the palette offers exactly what the role grants — never offered-then-refused", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  // Both the nav and the palette filter the SAME map by the SAME capability list, which is the same
  // permission map the backend enforces. A capability the role lacks is ABSENT, not disabled.
  assert.match(surfaces, /export function navFor\(capabilities: string\[\]\)/);
  assert.match(surfaces, /export function commandsFor\(capabilities: string\[\]\)/);
  assert.match(surfaces, /capabilities\.includes\(s\.capability\)/);

  const shell = await read("src/components/shell.tsx");
  assert.match(shell, /navFor\(identity\.capabilities\)/, "the nav is not filtered by live capabilities");
  assert.match(shell, /commandsFor\(identity\.capabilities\)/, "the palette is not filtered by live capabilities");
});

test("a subject is reachable by name, never by recalling an identifier", async () => {
  const shell = await read("src/components/shell.tsx");
  assert.match(shell, /paletteSubjects/, "the palette is given no subjects to type-ahead over");
  // Subjects are listed with the OPERATOR's own session, so the palette cannot surface anything the
  // gate would not already have shown them.
  assert.match(shell, /adminFetch<\{ tenants: TenantView\[\] \}>\("\/admin\/api\/tenants", \{ sessionToken \}\)/);
});

test("every view's state lives in the URL, and nothing sensitive does", async () => {
  const addressable = {
    "src/app/tenants/page.tsx": /searchParams: Promise<\{ q\?: string \}>/,
    "src/app/audit/page.tsx": /seq\?: string; actor\?: string; action\?: string; result\?: string/,
    "src/app/fleet/page.tsx": /searchParams: Promise<\{ state\?: string \}>/,
    "src/app/registry/page.tsx": /searchParams: Promise<\{ q\?: string \}>/,
    "src/app/billing/page.tsx": /searchParams: Promise<\{ tenant\?: string \}>/,
    "src/app/crosstenant/page.tsx": /searchParams: Promise<\{ aggregate\?: string \}>/,
  };
  for (const [file, pattern] of Object.entries(addressable)) {
    const src = await read(file);
    assert.match(src, pattern, `${file} does not carry its view state in the URL`);
    // A GET form is what makes back/forward restore the view exactly, rather than resetting it.
    if (!file.includes("crosstenant")) {
      assert.match(src, /method="get"/, `${file} does not use a GET form, so back/forward would not restore it`);
    }
  }

  // Nothing may put session material or a subject's personal data in a URL.
  for (const file of await sources()) {
    const src = await read(file);
    assert.equal(
      /[?&](session|token|credential|assertion)=/.test(src),
      false,
      `${file} appears to put session material in a URL`,
    );
  }
});

// ── 15.16 · The operating picture ────────────────────────────────────────────

test("the operating picture answers halted/wrong without interaction", async () => {
  const page = await read("src/app/page.tsx");
  // The home is the picture itself, server-rendered — not a redirect to a list and not a skeleton.
  assert.match(page, /loadOperatingPicture/, "the home page does not load the operating picture");
  assert.equal(/redirect\(/.test(page), false, "the home page still redirects instead of showing state");

  const picture = await read("src/components/operatingPicture.tsx");
  for (const signal of ["Autonomous merges", "Worker fleet", "Tenants", "Active impersonations", "Anomalies"]) {
    assert.ok(picture.includes(signal), `the picture does not surface “${signal}”`);
  }
  // Halted state is a WORD as well as a colour.
  assert.match(picture, /ARMED — every tenant halted/, "the armed state is not stated in words");
});

test("a live figure carries its as-of time and announces staleness", async () => {
  const primitives = await read("src/components/primitives.tsx");
  assert.match(primitives, /Stale — could not refresh\. Last confirmed/, "staleness is not stated");
  assert.match(primitives, /As of \$\{asOf\}/, "no as-of time on live figures");

  const picture = await read("src/components/operatingPicture.tsx");
  assert.match(picture, /setStale\(true\)/, "a failed poll does not mark the picture stale");
  assert.match(picture, /STALE_AFTER_MS/, "an un-refreshed picture never becomes stale by age");

  const overview = await read("src/lib/overview.ts");
  assert.match(overview, /as_of: new Date\(\)\.toISOString\(\)/, "the picture is not stamped with its instant");
});

test("a refresh never blanks correct data and never reorders under the operator", async () => {
  const picture = await read("src/components/operatingPicture.tsx");
  // On failure the last-good picture is KEPT — the component must not clear its state.
  assert.equal(
    /setPicture\(null\)|setPicture\(undefined\)/.test(picture),
    false,
    "a failed poll clears the picture, replacing a known-old truth with none at all",
  );
  // No client-side sorting: row order is the server's, so an update cannot move a row under a cursor.
  assert.equal(/\.sort\(/.test(picture), false, "the picture sorts rows client-side, which can reorder under the operator");
  // A refresh in flight is a marker, not a loading state that replaces the numbers.
  assert.match(picture, /refreshing && !stale/, "a refresh in flight is not distinguished from a stale picture");
  assert.equal(/LoadingState/.test(picture), false, "a refresh renders a loading state over correct data");
});

test("each panel of the picture fails independently, and a denial is not an emptiness", async () => {
  const overview = await read("src/lib/overview.ts");
  assert.match(overview, /Promise\.allSettled/, "one unreachable subsystem would take the whole picture down");
  assert.match(overview, /state: "denied"/, "a panel cannot report a permission denial");
  assert.match(overview, /state: "degraded"/, "a panel cannot report a transport failure");

  const picture = await read("src/components/operatingPicture.tsx");
  assert.match(picture, /DeniedState/, "a denied panel does not name who holds the capability");
  assert.match(picture, /DegradedState/, "a degraded panel is not distinguished from an empty one");
});

// ── 15.17 · Truthful feedback ────────────────────────────────────────────────

test("a privileged command resolves to a receipt naming its audit entry", async () => {
  const form = await read("src/components/actionForm.tsx");
  assert.match(form, /Done — confirmed by the platform/, "success is not stated as confirmed by the platform");
  for (const field of ["Action", "Target", "Reason", "When", "Actor", "Result", "Audit"]) {
    assert.ok(form.includes(`<dt>${field}</dt>`), `the receipt does not state ${field}`);
  }
  // The audit reference must be REACHABLE, not merely printed.
  assert.match(form, /href={`\/audit\?seq=\$\{receipt\.outcome_seq\}`}/, "the audit reference is not a link");
  assert.match(form, /timestamp\(receipt\.at\)/, "the receipt's instant bypasses the locale swap point");
});

test("irreversibility is stated in words, not implied by a missing control", async () => {
  const form = await read("src/components/actionForm.tsx");
  assert.match(form, /There is no undo for this action/, "the receipt does not state when no undo exists");
  assert.match(form, /command\?\.undo \?/, "the receipt does not name the reversing action when one exists");

  const actions = await read("src/lib/actions.ts");
  // The one-way door must NOT carry an undo. If someone adds one, this fails.
  const gdpr = actions.slice(actions.indexOf("export async function executeGDPR"));
  const gdprCommand = gdpr.slice(gdpr.indexOf('action: "gdpr.execute"'), gdpr.indexOf('action: "gdpr.execute"') + 400);
  assert.equal(/undo:/.test(gdprCommand), false, "GDPR erasure claims a reversing action; it has none");
});

test("no optimistic success: in-flight, failed and unknown are three renderings", async () => {
  const form = await read("src/components/actionForm.tsx");
  assert.match(form, /pending \?[\s\S]{0,400}In flight/, "there is no in-flight rendering");
  assert.match(form, /state--unknown/, "there is no outcome-unknown rendering");
  assert.match(form, /Outcome unknown — do not retry yet/, "the unknown outcome does not warn against retrying");
  assert.match(form, /Open the audit log for this action/, "the unknown outcome offers no way to verify");
  // Failure states must SAY that nothing happened — the operator's next move depends on it.
  assert.match(form, /Denied — nothing happened/);
  assert.match(form, /One more step — nothing happened/);

  const actions = await read("src/lib/actions.ts");
  // Success is returned only after the platform answers: `ok: true` may not appear before the await.
  const post = actions.slice(actions.indexOf("async function post("), actions.indexOf("function form(fd: FormData)"));
  const awaitIndex = post.indexOf("await adminFetch");
  const okIndex = post.indexOf("ok: true");
  assert.ok(awaitIndex > 0 && okIndex > awaitIndex, "a command reports success before the platform confirms it");
});

test("a lost response on a COMMAND is unknown, while a lost read is degraded", async () => {
  const api = await read("src/lib/adminApi.ts");
  assert.match(
    api,
    /method === "GET" \? "degraded" : "unknown"/,
    "a transport failure does not distinguish a read from a command",
  );
});

// ── 15.10 / 14.8 · Friction survived the craft work ──────────────────────────

test("the palette navigates to a dangerous action and never performs one", async () => {
  const palette = await read("src/components/commandPalette.tsx");
  // Every selection is a navigation. A call to a server action here would be the FR37 defect.
  assert.match(palette, /router\.push\(entry\.href\)/, "the palette does not navigate on selection");
  assert.equal(/formAction|useActionState|fetch\(/.test(palette), false, "the palette can issue a request");
  for (const forbidden of ["armKillSwitch", "cancelJob", "executeGDPR", "suspendTenant", "issueRefund"]) {
    assert.equal(palette.includes(forbidden), false, `the palette imports ${forbidden} — it must only navigate`);
  }

  const surfaces = await read("src/lib/surfaces.ts");
  // Dangerous entries point at a confirmation's anchor, not at an endpoint.
  for (const entry of surfaces.matchAll(/href:\s*"([^"]+)"[\s\S]{0,200}?danger:\s*true/g)) {
    assert.equal(/\/api\//.test(entry[1]), false, `a dangerous palette entry points at an API path: ${entry[1]}`);
  }
});

test("nothing on the dangerous path is pre-filled", async () => {
  const form = await read("src/components/actionForm.tsx");
  const reason = form.slice(form.indexOf("name=\"reason\""), form.indexOf("name=\"reason\"") + 300);
  assert.equal(/defaultValue|value=/.test(reason), false, "the reason field is pre-filled");

  const typed = form.slice(form.indexOf('name="typed_target"'), form.indexOf('name="typed_target"') + 200);
  assert.equal(/defaultValue|value=/.test(typed), false, "the typed-target field is pre-filled");
});

test("the deliberate step count to a destructive effect is unchanged", async () => {
  const form = await read("src/components/actionForm.tsx");
  // Reason + explicit confirmation are both still REQUIRED, and the typed target still gates the
  // irreversible case. These are the steps FR24 defines; the craft work may not remove one.
  assert.match(form, /name="reason"[\s\S]{0,200}required/, "the reason is no longer required");
  assert.match(form, /name="confirmed" type="checkbox" required/, "the explicit confirmation is gone");
  assert.match(form, /name="typed_target"[\s\S]{0,120}required/, "the typed target is no longer required");
  // The global control stays visually heavier than its per-tenant counterpart.
  const css = await read("src/app/globals.css");
  const global = css.match(/form\.action--global\s*{([^}]*)}/)[1];
  const danger = css.match(/form\.action--danger\s*{([^}]*)}/)[1];
  assert.match(global, /--rule-heavy/, "the fleet-wide control is not visually heavier");
  assert.equal(/--rule-heavy/.test(danger), false, "the per-tenant control is as heavy as the fleet-wide one");
});

// ── 15.11 · Motion ───────────────────────────────────────────────────────────

test("motion is budgeted, meaningful, and never on the action path", async () => {
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  assert.match(shared, /--motion-fast:\s*120ms/);
  assert.match(shared, /--motion-base:\s*180ms/);
  assert.match(shared, /--motion-slow:\s*240ms/);

  const css = await read("src/app/globals.css");
  // Every duration in the stylesheet comes from the budget: no bespoke timing.
  for (const match of css.matchAll(/(?:transition|animation)[^;]*?(\d+)ms/g)) {
    assert.fail(`a literal duration (${match[1]}ms) bypasses the motion budget`);
  }
  // Nothing may animate longer than the slowest budgeted step.
  for (const match of css.matchAll(/animation:\s*[\w-]+\s+var\(--([\w-]+)\)/g)) {
    assert.match(match[1], /^motion-(fast|base|slow)$/, `animation uses ${match[1]}, which is not a motion token`);
  }
  // Reduced motion collapses everything, and is declared in the shared layer so both consoles get it.
  assert.match(shared, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(shared, /animation-duration:\s*0ms\s*!important/);
  assert.match(shared, /transition-duration:\s*0ms\s*!important/);
});

test("no information is carried by motion alone", async () => {
  const css = await read("src/app/globals.css");
  // The value-change mark is a background flash on a value that is ALREADY rendered — remove the
  // animation and the number is still there and still correct.
  const changed = css.match(/@keyframes value-changed\s*{([^}]*}[^}]*)}/s)[1];
  assert.equal(/content:|opacity:\s*0/.test(changed), false, "the value-change mark hides content when not animating");

  const picture = await read("src/components/operatingPicture.tsx");
  // Staleness is text, not a pulse.
  assert.match(picture, /stale=\{stale\}/, "staleness is not passed to a text rendering");
});
