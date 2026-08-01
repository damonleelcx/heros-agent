// social-proof.test.mjs holds §12.7 — the home page's one statement about the world rather than about
// the product.
//
// # Why a link needs a test suite
//
// Because every dishonest version of it is easier to write than the honest one. A badge is one line. A
// browser-side fetch is three. A hand-typed "★ 1.2k" is zero. Each of them is refused here, and the
// refusals are separate tests because they fail for different reasons and the messages have to say
// which.
//
// The rule generalises past this feature, deliberately: it is written for ANY number the marketing
// surface states about the world — downloads, members, "used by" — because this will not be the last one
// asked for.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const stripComments = (source) => source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

async function runScan(script, env = {}) {
  try {
    const { stdout, stderr } = await exec("node", [join(ROOT, "scripts", script)], {
      cwd: ROOT,
      env: { ...process.env, ...env },
    });
    return { code: 0, output: stdout + stderr };
  } catch (error) {
    return { code: error.code ?? 1, output: `${error.stdout ?? ""}${error.stderr ?? ""}` };
  }
}

// ── 7.1 · The link is an anchor and nothing else ─────────────────────────────

test("7.1 — the repository link is a plain anchor: no client component, no effect, no loading state", async () => {
  const layout = await read("src/app/(public)/layout.tsx");
  assert.doesNotMatch(layout, /^"use client"/m, "the public layout became a client component");

  // Comments are stripped first: this module's own header DOCUMENTS the three refused approaches,
  // including a `fetch('https://api.github.com')` example. A fence that punished the explanation would
  // make the explanation the thing people delete.
  const repository = stripComments(await read("src/content/repository.ts"));
  assert.doesNotMatch(repository, /useState|useEffect|fetch\(/, "the repository module acquired browser behaviour");

  // The link is present in both the header and the footer, and marked as external.
  const header = layout.slice(layout.indexOf("<header"), layout.indexOf("</header>"));
  const footer = layout.slice(layout.indexOf("<footer"), layout.indexOf("</footer>"));
  for (const [name, section] of [["header", header], ["footer", footer]]) {
    assert.match(section, /REPOSITORY\.url/, `the ${name} does not link to the repository`);
    assert.match(section, /data-external="true"/, `the ${name}'s repository link is not marked external`);
    assert.match(section, /rel="noreferrer noopener"/, `the ${name}'s repository link leaks a referrer`);
  }
});

// ── 12.7 · Every dishonest way to render a count ─────────────────────────────

test("12.7 — a shields.io badge fails the build on the external-origin check", async () => {
  const file = join(ROOT, "src/app/(public)/layout.tsx");
  const original = await readFile(file, "utf8");
  try {
    await writeProbe(
      file,
      original,
      `<img src="https://img.shields.io/github/stars/damonleelcx/heros-agent" alt="stars" />`,
    );
    const { code, output } = await runScan("scan-content.mjs");
    assert.equal(code, 1, "a shields.io badge did not fail the build");
    assert.match(output, /shields\.io badge|third-party origin/, `the refusal did not name the problem:\n${output}`);
  } finally {
    await writeFile(file, original);
  }
});

test("12.7 — a browser-side api.github.com fetch fails the build", async () => {
  const file = join(ROOT, "src/components/reading/toc.tsx");
  const original = await readFile(file, "utf8");
  try {
    await writeProbe(file, original, `fetch("https://api.github.com/repos/x/y");`, "// PROBE");
    const { code, output } = await runScan("scan-content.mjs");
    assert.equal(code, 1, "a browser-side api.github.com call did not fail the build");
    assert.match(output, /api\.github\.com|third-party origin/, `the refusal did not name the problem:\n${output}`);
  } finally {
    await writeFile(file, original);
  }
});

test("12.7 — the runtime CSP refuses all three independently of the build check", async () => {
  /*
   * Asserted separately, because a build check and a runtime control are two different guarantees and
   * "we have a fence" is not the same claim as "the browser would refuse it". Both are stated in the
   * design; both are checked here.
   */
  /*
   * 🔴 This used to read three literals out of `src/middleware.ts`. P24 moved the header out of that
   * file — it is now CONSTRUCTED from `web/design-system/third-party-policy.ts`, so both consoles read
   * one table and a hard-coded origin in either middleware fails the build.
   *
   * The right correction was NOT to point the same three `assert.match` calls at the new file. That
   * would still be asserting that a source file contains a string, which is a proxy for the claim; the
   * claim is "the browser would refuse it". So this now builds the policy the public surface actually
   * serves — the prefix a badge or a browser-side fetch would live on — and checks the DIRECTIVES.
   * The test got closer to its own sentence as a result of the refactor rather than further from it.
   */
  const { buildContentSecurityPolicy } = await import("../../design-system/csp.ts");
  const { ALLOWED_ORIGINS } = await import("../../design-system/third-party-policy.ts");
  const csp = buildContentSecurityPolicy({ consoleId: "customer", pathname: "/", nonce: "N", dev: false });
  assert.match(csp, /default-src 'self'/, "the CSP would permit a widget or an iframe");
  assert.match(csp, /connect-src 'self'(;|$)/, "the CSP would permit a browser-side api.github.com fetch");
  assert.match(csp, /img-src 'self' data:(;|$)/, "the CSP would permit a shields.io badge");

  // And the tenant prefix, which the original could not distinguish at all.
  //
  // 🔴 The assertion here is NOT "no origin". P24 wave 24c permits exactly one — the error-reporting
  // ingest host, under `connect-src`, on every prefix — because the event that reaches it is
  // constructed from an allowlist and carries no content by construction. Writing this as "no https
  // origin" would have been the easy version and it would have had to be deleted the day that landed,
  // taking with it the thing it was really for: that a WIDGET, an IFRAME, a BADGE or a browser-side
  // fetch to a forge cannot appear on a tenant page, no matter what anybody consented to.
  const tenant = buildContentSecurityPolicy({
    consoleId: "customer",
    pathname: "/app",
    nonce: "N",
    dev: false,
    granted: ["product_analytics", "session_replay", "error_diagnostics"],
  });
  const permitted = new Set(
    ALLOWED_ORIGINS.filter((o) => o.category === "error_diagnostics").map((o) => o.origin),
  );
  for (const named of [...tenant.matchAll(/https?:\/\/[^\s;]+/g)].map((m) => m[0])) {
    assert.ok(
      permitted.has(named),
      `a granted consent category put ${named} on a tenant prefix — only the error-reporting origin is permitted there`,
    );
  }
  assert.doesNotMatch(tenant, /img-src[^;]*https?:\/\//, "a badge origin reached a tenant prefix");
  assert.doesNotMatch(tenant, /script-src[^;]*https?:\/\//, "a script host reached a tenant prefix");
  assert.doesNotMatch(tenant, /clarity|googletagmanager|google-analytics|shields\.io|api\.github\.com/i,
    "an analytics, replay or forge origin reached a tenant prefix");
});

test("12.7 — the count is never hand-typed: the module holds no number", async () => {
  const repository = await read("src/content/repository.ts");
  // A star count literal would be a number that is wrong the day after it is written, and nobody
  // notices a stale number the way they notice a broken image.
  const inCode = repository.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  const numbers = [...inCode.matchAll(/\b\d{2,}\b/g)].map((m) => m[0]);
  assert.deepEqual(numbers, [], `the repository module carries a hand-typed number: ${numbers.join(", ")}`);
});

test("12.7 — the count is opt-in and OFF, and the link does not depend on it", async () => {
  const repository = await read("src/content/repository.ts");
  assert.match(
    repository,
    /export const SHOW_STAR_COUNT = false;/,
    "the star count is not explicitly off — the repository has 0 stars, and '★ 0' is worse than nothing",
  );

  // The link must not be conditional on the count. A reader can always reach the repository.
  const layout = await read("src/app/(public)/layout.tsx");
  const header = layout.slice(layout.indexOf("<header"), layout.indexOf("</header>"));
  assert.doesNotMatch(
    header,
    /SHOW_STAR_COUNT[\s\S]{0,200}REPOSITORY\.url/,
    "the repository link is gated on the star-count flag — the link is unconditional",
  );
});

test("12.7 — an unavailable measurement degrades to the plain link, never to 0", async () => {
  const fence = await read("scripts/scan-repo-link.mjs");
  /*
   * 🔴 The rule in one sentence: an unavailable measurement rendered as zero is a false statement, and
   * it is the one a reader will believe.
   *
   * The fence records the measurement; nothing renders it while SHOW_STAR_COUNT is false. What must be
   * true today is that no default of 0 exists anywhere to be rendered.
   */
  assert.doesNotMatch(fence, /stars:\s*0\b/, "the measurement defaults to 0 rather than being absent");
  assert.match(
    fence,
    /measured_on/,
    "the measurement is not stamped with its date — a measurement with no date is a claim",
  );

  const repository = await read("src/content/repository.ts");
  assert.match(
    repository,
    /Degrades to the plain link/,
    "the degradation rule is not stated where the next person will read it",
  );
});

// ── 7.2 / 12.7 · The link target is fenced ───────────────────────────────────

test("12.7 — pointing the link at a private or missing repository fails the build", async () => {
  const file = join(ROOT, "src/content/repository.ts");
  const original = await readFile(file, "utf8");
  try {
    // A repository that does not exist. The fence resolves it against the forge and must refuse.
    const probe = original
      .replace(/owner: "[^"]+"/, 'owner: "damonleelcx"')
      .replace(/name: "[^"]+"/, 'name: "heros-agent-does-not-exist-p23-probe"')
      .replace(/url: "[^"]+"/, 'url: "https://github.com/damonleelcx/heros-agent-does-not-exist-p23-probe"');
    await writeFile(file, probe);

    const { code, output } = await runScan("scan-repo-link.mjs", { NODE_USE_ENV_PROXY: "1" });
    if (/offline — standing on the check/.test(output)) {
      // No network. Say so rather than passing: a fence that could not run has not run.
      assert.fail(
        "the repository fence could not reach the forge, so this assertion did not run. " +
          "It is reported rather than skipped, because a check that silently passes offline is decorative.",
      );
    }
    assert.equal(code, 1, `a non-existent repository did not fail the build:\n${output}`);
    assert.match(output, /does not exist or is not public/, `the refusal did not name the problem:\n${output}`);
  } finally {
    await writeFile(file, original);
  }
});

// ── 7.6 · The page's existing posture is unchanged ───────────────────────────

test("7.6 — the home page still sets no cookie and makes no third-party request at render", async () => {
  const layout = await read("src/app/(public)/layout.tsx");
  const page = await read("src/app/(public)/page.tsx");
  for (const [name, source] of [["layout", layout], ["page", page]]) {
    assert.doesNotMatch(source, /cookies\(\)|document\.cookie/, `the public ${name} touches cookies`);
    assert.doesNotMatch(source, /await fetch\(|platformFetch/, `the public ${name} makes a request at render`);
  }

  // And the CSP is untouched — this capability declined to be the exception.
  const middleware = await read("src/middleware.ts");
  assert.doesNotMatch(middleware, /unsafe-inline'[^;]*script-src|'unsafe-eval'.*production/, "the CSP was relaxed");
});

// ── helpers ──────────────────────────────────────────────────────────────────

async function writeFile(path, contents) {
  const { writeFile: write } = await import("node:fs/promises");
  await write(path, contents, "utf8");
}

/** writeProbe injects a line into a file so a fence can be watched failing, then the caller restores it. */
async function writeProbe(path, original, injection, marker = "{/* PROBE */}") {
  const anchor = original.indexOf("return (");
  const at = original.indexOf("\n", anchor) + 1;
  await writeFile(path, original.slice(0, at) + `    ${marker}\n    ${injection}\n` + original.slice(at));
}
