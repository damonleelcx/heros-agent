// consent.test.mjs holds P24 wave 24d — the consent state machine and the control that drives it.
//
// # The property that costs the most to get wrong
//
// `not-asked` and `denied` are two states, not one falsy value. A system that cannot tell them apart
// re-asks somebody who already said no — which the person experiences as being ignored, and which is
// the single most common way a consent mechanism becomes theatre. Several assertions below exist only
// to keep those two states distinguishable.
//
// # What runs against a REAL console
//
// The state machine is a pure function and is tested as one. The behaviour that matters — a refusal
// surviving three navigations and a new session, the policy narrowing on the grant, and the banner
// disappearing — is tested against `next start` over real sockets, because all three are properties of
// a request/response cycle rather than of a function.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { startStubPlatform, startConsole } from "./support/harness.mjs";
import {
  CONSENT_COOKIE,
  CONSENT_POLICY_VERSION,
  DEFAULT_STATE,
  decode,
  encode,
  grantedCategories,
  hasAnswered,
  isGranted,
} from "../../design-system/consent.ts";
import { CATEGORY_TERMS, CATEGORY_ORDER } from "../../design-system/consent-terms.ts";
import { CONSENT_CATEGORIES, NON_ESSENTIAL_CATEGORIES } from "../../design-system/third-party-policy.ts";
import { buildContentSecurityPolicy } from "../../design-system/csp.ts";

const ROOT = new URL("..", import.meta.url).pathname;
const WEB = new URL("../..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const readWeb = (rel) => readFile(join(WEB, rel), "utf8");

/** granted builds a cookie value for a set of categories. */
function cookieFor(overrides) {
  return `${CONSENT_COOKIE}=${encode({
    policyVersion: CONSENT_POLICY_VERSION,
    decisions: { essential: "granted", product_analytics: "denied", session_replay: "denied", error_diagnostics: "denied", ...overrides },
  })}`;
}

// ── 4.1 · The state machine ──────────────────────────────────────────────────

test("4.1 four categories, three states each, and every non-essential one defaults to not-asked", () => {
  assert.deepEqual([...CONSENT_CATEGORIES], ["essential", "product_analytics", "session_replay", "error_diagnostics"]);
  assert.equal(DEFAULT_STATE.decisions.essential, "granted");
  for (const category of NON_ESSENTIAL_CATEGORIES) {
    assert.equal(DEFAULT_STATE.decisions[category], "not-asked", `${category} does not default to not-asked`);
  }
  // `not-asked` BEHAVES as denied — that is what default-denied means at the only place it can be
  // enforced — while remaining a different fact from a refusal.
  assert.deepEqual(grantedCategories(DEFAULT_STATE), ["essential"]);
  assert.equal(hasAnswered(DEFAULT_STATE), false);
});

test("4.1 nothing but an explicit action or a MATERIAL version can move a decision", async () => {
  // Asserted on the shape of the surface: there is no GET that grants, no query parameter that
  // decides, and no timer. A decision cannot be moved by a navigation, a scroll or by waiting.
  const route = await read("src/app/api/consent/route.ts");
  assert.doesNotMatch(route, /export async function GET/, "a GET can change a consent decision");
  assert.match(route, /export async function POST/);
  const banner = await read("src/components/consentBanner.tsx");
  assert.doesNotMatch(banner, /setTimeout|useEffect|onScroll/, "the banner has a time- or scroll-driven path");
  // The query parameter only OPENS the form.
  assert.match(banner, /consent=review/);
  assert.doesNotMatch(banner, /consent=granted|consent=accept/, "a query parameter grants something");
});

test("4.1 a malformed or absent cookie falls back to nothing-granted, never to assumed agreement", () => {
  for (const raw of [undefined, "", "not json", "%7Bbroken", encodeURIComponent("[]"), encodeURIComponent('{"v":1}')]) {
    const state = decode(raw);
    assert.deepEqual(grantedCategories(state), ["essential"], `a ${JSON.stringify(raw)} cookie granted something`);
  }
});

// ── 4.2 · The cookie, and why it is not the P23 ledger ───────────────────────

test("4.2 the record round-trips through the cookie", () => {
  const state = {
    policyVersion: CONSENT_POLICY_VERSION,
    decisions: { essential: "granted", product_analytics: "granted", session_replay: "denied", error_diagnostics: "granted" },
  };
  const back = decode(encode(state));
  assert.deepEqual(back.decisions, state.decisions);
  assert.equal(back.policyVersion, CONSENT_POLICY_VERSION);
});

test("4.2 🔴 the reason it is NOT the P23 consent-records ledger is written where the next reader will be", async () => {
  // This is a comment assertion on purpose. The failure it guards against is somebody "fixing" an
  // apparent inconsistency by moving this into the statutory ledger — where an append-only record that
  // survives identity erasure would mean a cookie choice outliving a deletion request.
  const consent = await readWeb("design-system/consent.ts");
  assert.match(consent, /consent-records/, "consent.ts does not name the ledger it is deliberately not");
  assert.match(consent, /append-only/);
  assert.match(consent, /erasure/);
  const prefs = await read("src/lib/consentPrefs.ts");
  assert.match(prefs, /consentGate\.ts/, "consentPrefs.ts does not distinguish itself from the legal gate");

  // And the two really are separate modules with separate cookies.
  const gate = await read("src/lib/consentGate.ts");
  assert.doesNotMatch(gate, new RegExp(CONSENT_COOKIE), "the legal gate reads the analytics cookie");
});

test("4.2 the consent cookie is not httpOnly, and says so explicitly", async () => {
  const prefs = await read("src/lib/consentPrefs.ts");
  assert.match(prefs, /httpOnly:\s*false/, "the consent cookie's flags are defaulted rather than stated");
  assert.match(prefs, /sameSite:\s*"lax"/);
  // A year, not a session: "a refusal is stored as a refusal" is worth nothing if it expires with the tab.
  assert.match(prefs, /maxAge:\s*60 \* 60 \* 24 \* 365/);
});

// ── 4.3 · The banner ─────────────────────────────────────────────────────────

test("4.3 🔴 decline carries the SAME visual weight as accept", async () => {
  const banner = await read("src/components/consentBanner.tsx");
  const accept = /<button className="([^"]*)" type="submit" name="action" value="accept-all">/.exec(banner);
  const decline = /<button className="([^"]*)" type="submit" name="action" value="decline-all">/.exec(banner);
  assert.ok(accept && decline, "the two controls could not be found");
  assert.equal(
    accept[1],
    decline[1],
    "accept and decline carry different classes. A banner whose accept control is large and coloured " +
      "and whose decline control is a grey line of text is not asking a question, it is applying pressure.",
  );
  assert.doesNotMatch(accept[1], /button--primary/, "accept is styled as the primary action");
  // And decline is reachable first by keyboard.
  assert.ok(
    banner.indexOf('value="decline-all"') < banner.indexOf('value="accept-all"'),
    "accept comes before decline in the tab order",
  );
});

test("4.3 the banner uses existing anchors only, and renders no raw markup", async () => {
  const banner = await read("src/components/consentBanner.tsx");
  const globals = await read("src/app/globals.css");
  for (const className of ["banner", "button", "hint", "caption"]) {
    assert.match(globals, new RegExp(`\\.${className}\\s*\\{`), `.${className} is not an existing anchor`);
    assert.match(banner, new RegExp(`"[^"]*\\b${className}\\b`), `the banner does not use .${className}`);
  }
  assert.doesNotMatch(banner, /dangerouslySetInnerHTML/);
  assert.doesNotMatch(banner, /style=\{\{/, "the banner carries an inline style");
});

// ── 4.4 · One vocabulary ─────────────────────────────────────────────────────

test("4.4 the three optional categories have term-dictionary entries, and every surface uses them", async () => {
  for (const category of NON_ESSENTIAL_CATEGORIES) {
    const entry = CATEGORY_TERMS[category];
    assert.ok(entry, `${category} has no term`);
    assert.ok(entry.term.length > 3, `${category}'s term is not a term`);
    assert.ok(entry.summary.length > 60, `${category}'s summary does not say what is collected and who receives it`);
    assert.equal(entry.optional, true);
  }
  assert.deepEqual(
    NON_ESSENTIAL_CATEGORIES.map((c) => CATEGORY_TERMS[c].term),
    ["Usage analytics", "Session recording", "Error diagnostics"],
  );
  assert.equal(CATEGORY_TERMS.essential.optional, false, "essential is offered as a choice it is not");
  assert.deepEqual([...CATEGORY_ORDER], [...CONSENT_CATEGORIES]);

  // The banner reads the dictionary rather than re-typing the words.
  const banner = await read("src/components/consentBanner.tsx");
  assert.match(banner, /CATEGORY_TERMS\[category\]\.term/);
  assert.match(banner, /CATEGORY_TERMS\[category\]\.summary/);
  for (const term of ["Usage analytics", "Session recording", "Error diagnostics"]) {
    assert.doesNotMatch(banner, new RegExp(`"${term}"`), `the banner hard-codes the term "${term}"`);
  }
});

test("4.4 the operator notice uses the same three terms", async () => {
  const notice = await readFile(join(WEB, "..", "docs", "decisions", "p24-operator-acceptable-use.md"), "utf8");
  for (const category of NON_ESSENTIAL_CATEGORIES) {
    assert.match(
      notice,
      new RegExp(CATEGORY_TERMS[category].term, "i"),
      `the operator notice does not use the term "${CATEGORY_TERMS[category].term}"`,
    );
  }
});

// ── 4.8 · Policy versions ────────────────────────────────────────────────────

test("4.8 a MATERIAL version resets every non-essential category to not-asked", () => {
  const old = encode({
    policyVersion: "1999-01-01.1",
    decisions: { essential: "granted", product_analytics: "granted", session_replay: "granted", error_diagnostics: "granted" },
  });
  const state = decode(old);
  for (const category of NON_ESSENTIAL_CATEGORIES) {
    assert.equal(state.decisions[category], "not-asked", `${category} carried a grant across a material version`);
  }
  assert.equal(hasAnswered(state), false, "a stale grant did not re-ask");
  assert.equal(state.policyVersion, CONSENT_POLICY_VERSION);
});

test("4.8 a non-material change asks nobody", () => {
  // The mechanism IS the version constant: a change that does not bump it invalidates nothing. This
  // asserts the other half — that the current version's decisions survive.
  const current = encode({
    policyVersion: CONSENT_POLICY_VERSION,
    decisions: { essential: "granted", product_analytics: "denied", session_replay: "denied", error_diagnostics: "granted" },
  });
  const state = decode(current);
  assert.equal(state.decisions.error_diagnostics, "granted");
  assert.equal(state.decisions.product_analytics, "denied");
  assert.equal(hasAnswered(state), true, "a current record was treated as unanswered");
});

// ── 7.2 · The material publication IS the consent version ────────────────────

test("7.2 🔴 the consent policy version is the sub-processor document's version", async () => {
  /*
   * The wiring, asserted rather than described. A grant is given against a statement of who receives
   * data; `content/legal/en/sub-processors/` is that statement and it is `material: true`. If the two
   * versions could drift, publishing a new material document would leave every existing grant in force
   * against a statement it was never given against — over-collection, silently, with a green build.
   */
  const { readdir } = await import("node:fs/promises");
  const dir = join(ROOT, "content", "legal", "en", "sub-processors");
  const versions = (await readdir(dir)).filter((f) => f.endsWith(".md")).map((f) => f.replace(/\.md$/, ""));
  assert.ok(versions.length >= 1, "no sub-processor document is published");
  const latest = versions.sort().at(-1);
  assert.equal(
    CONSENT_POLICY_VERSION,
    `sub-processors@${latest}`,
    `the consent policy version is ${CONSENT_POLICY_VERSION} but the published sub-processor document ` +
      `is ${latest}. Publishing a material version without bumping this leaves every existing grant in ` +
      `force against a statement it was never given against.`,
  );
  const front = await readFile(join(dir, `${latest}.md`), "utf8");
  assert.match(front, /^material: true$/m, "the sub-processor document is not marked material");
  assert.match(front, /^kind: sub-processors$/m);
});

test("7.2 a grant given against the previous sub-processor version is re-asked", () => {
  // The behaviour the wiring buys, exercised end to end through `decode`.
  const old = encode({
    policyVersion: "sub-processors@0.9.0",
    decisions: { essential: "granted", product_analytics: "granted", session_replay: "granted", error_diagnostics: "granted" },
  });
  const state = decode(old);
  assert.equal(hasAnswered(state), false, "a grant against a superseded sub-processor version survived");
  assert.deepEqual(grantedCategories(state), ["essential"]);
});

// ── 4.9 · No banner on the operator console ──────────────────────────────────

test("4.9 the operator console renders no banner, and the exception is STATED", async () => {
  const layout = await readWeb("admin-console/src/app/layout.tsx");
  assert.doesNotMatch(layout, /ConsentBanner/, "the operator console renders a consent banner");
  const files = await Promise.all([
    readWeb("admin-console/src/middleware.ts"),
    readWeb("admin-console/src/lib/reporting.ts"),
  ]);
  for (const src of files) {
    assert.match(src, /acceptable-use/i, "the operator console does not name the notice its exception lives in");
  }
  const notice = await readFile(join(WEB, "..", "docs", "decisions", "p24-operator-acceptable-use.md"), "utf8");
  assert.match(notice, /no consent banner/i, "the notice does not state the absence");
  assert.match(notice, /Refused/, "the notice does not state what is refused");
});

// ── 4.7 · Full function on decline (static half) ─────────────────────────────

test("4.7 no content, control or route is conditioned on a grant", async () => {
  // A grant may gate a third-party integration and NOTHING else. Any component that renders differently
  // because a category was granted would make declining cost the visitor something, which is the
  // definition of the pressure this requirement forbids.
  const offenders = [];
  for (const rel of ["src/app", "src/components", "src/lib"]) {
    const { readdir } = await import("node:fs/promises");
    const walk = async (dir) => {
      for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
        const child = join(dir, entry.name);
        if (entry.isDirectory()) {
          await walk(child);
          continue;
        }
        if (!/\.(ts|tsx)$/.test(entry.name)) continue;
        const src = (await read(child)).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
        if (!/isGranted|grantedCategories/.test(src)) continue;
        // The three permitted readers: the reporter's configuration, the banner's own checkbox state,
        // and the module that defines the question.
        // The four permitted readers: the two integration configurations, the banner's own checkbox
        // state, and the module that defines the question. A grant may gate a third-party integration
        // and NOTHING else.
        if ([
          "src/lib/reporting.ts",
          "src/lib/publicAnalyticsConfig.ts",
          "src/components/consentBanner.tsx",
          "src/lib/consentPrefs.ts",
        ].includes(child)) continue;
        offenders.push(child);
      }
    };
    await walk(rel);
  }
  assert.deepEqual(offenders, [], "a consent grant is read outside the reporter configuration and the banner");
});

// ── The live half ────────────────────────────────────────────────────────────

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

test("4.1 a first-time visitor is asked, and nothing is granted before they answer", async () => {
  const res = await fetch(`${console_.base}/`);
  const html = await res.text();
  assert.match(html, /Choose what this site may collect/, "a first-time visitor is not asked");
  const csp = res.headers.get("content-security-policy") ?? "";
  assert.doesNotMatch(csp, /https?:\/\//, "an origin was permitted before anybody answered");
});

test("4.5 🔴 a refusal is stored AS a refusal — no re-prompt across three navigations or a new session", async () => {
  const post = await fetch(`${console_.base}/api/consent`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ action: "decline-all", back: "/" }),
    redirect: "manual",
  });
  assert.equal(post.status, 303);
  const cookie = (post.headers.getSetCookie?.() ?? []).map((c) => c.split(";")[0]).find((c) => c.startsWith(CONSENT_COOKIE));
  assert.ok(cookie, "declining set no cookie — the refusal was not stored");

  for (const path of ["/", "/install", "/docs"]) {
    const res = await fetch(`${console_.base}${path}`, { headers: { cookie } });
    const html = await res.text();
    assert.doesNotMatch(html, /Choose what this site may collect/, `${path} re-asked somebody who declined`);
    assert.match(html, /Privacy choices/, `${path} offers no way back to the decision`);
  }

  // A "new session" for a visitor is a new connection carrying the same cookie: there is no server-side
  // session involved at all, which is the point — the refusal is not attached to an identity.
  const fresh = await fetch(`${console_.base}/`, { headers: { cookie, connection: "close" } });
  assert.doesNotMatch(await fresh.text(), /Choose what this site may collect/, "a new session re-asked");
});

test("4.6 withdrawal is reachable from every page carrying a gated integration, and takes effect next navigation", async () => {
  const granted = cookieFor({ error_diagnostics: "granted" });
  // Reachable: the control is in the ROOT layout, so it is on the public surface and on /app alike.
  for (const path of ["/", "/install", "/signin"]) {
    const html = await (await fetch(`${console_.base}${path}`, { headers: { cookie: granted } })).text();
    assert.match(html, /Privacy choices/, `${path} carries a gated integration with no way to withdraw`);
  }
  // The policy names the reporting origin while it is granted…
  const before = await fetch(`${console_.base}/`, { headers: { cookie: granted } });
  assert.match(before.headers.get("content-security-policy") ?? "", /ingest/, "a grant did not widen the policy");

  // …and does not on the NEXT navigation after withdrawal. No sign-out is involved: there is no session.
  const post = await fetch(`${console_.base}/api/consent`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded", cookie: granted },
    body: new URLSearchParams({ action: "decline-all", back: "/" }),
    redirect: "manual",
  });
  const withdrawn = (post.headers.getSetCookie?.() ?? []).map((c) => c.split(";")[0]).find((c) => c.startsWith(CONSENT_COOKIE));
  const after = await fetch(`${console_.base}/`, { headers: { cookie: withdrawn } });
  assert.doesNotMatch(after.headers.get("content-security-policy") ?? "", /https?:\/\//, "collection did not stop");
});

test("4.7 declining changes no content, no control and no route", async () => {
  /*
   * Compared as SETS of destinations and headings rather than as raw bytes.
   *
   * A byte comparison was the first version and it failed for a reason that is not the requirement:
   * the server-rendered payload carries the reporting configuration, including the boolean this test
   * is varying, so the documents differ by construction while every control on them is identical. A
   * test that cannot tell "a link disappeared" from "a boolean changed" would either be permanently
   * red or would be relaxed until it caught neither.
   *
   * What 4.7 is actually about is whether declining COSTS the visitor anything: a missing section, a
   * disabled control, a route that stops working. Those are destinations and headings, so those are
   * what is compared.
   */
  const declined = cookieFor({});
  const granted = cookieFor({ product_analytics: "granted", session_replay: "granted", error_diagnostics: "granted" });
  const surfaceOf = (html) => {
    // Only the CONSENT aside is removed, by its own label. Cutting at the first `<aside` was the
    // previous version and it silently discarded most of `/install` — the page opens with one. The
    // vacuity guard below is what caught it: a comparison over one link would have passed for years.
    const body = html.replace(/<aside aria-label="Privacy choices"[\s\S]*?<\/aside>/g, "");
    return {
      links: [...body.matchAll(/<a\b[^>]*href="([^"]+)"/g)].map((m) => m[1]).sort(),
      headings: [...body.matchAll(/<h([1-6])[^>]*>([^<]{0,120})/g)].map((m) => `h${m[1]}:${m[2].trim()}`).sort(),
      buttons: (body.match(/<button\b/g) ?? []).length,
    };
  };
  /*
   * `/install` is deliberately NOT in this list, and the vacuity guard is why it was noticed: without a
   * reachable platform the install page renders its degraded state, which is ONE anchor. Comparing two
   * identical degraded pages would have passed forever while proving nothing — so the routes here are
   * the ones that render a real surface, and the guard below keeps them that way.
   */
  for (const path of ["/", "/docs", "/signin"]) {
    const a = surfaceOf(await (await fetch(`${console_.base}${path}`, { headers: { cookie: declined } })).text());
    const b = surfaceOf(await (await fetch(`${console_.base}${path}`, { headers: { cookie: granted } })).text());
    assert.deepEqual(a.links, b.links, `${path} offers a visitor who declined a different set of destinations`);
    assert.deepEqual(a.headings, b.headings, `${path} renders different content for a visitor who declined`);
    assert.equal(a.buttons, b.buttons, `${path} offers a visitor who declined a different set of controls`);
    assert.ok(a.links.length >= 4, `${path} produced only ${a.links.length} links — the comparison is vacuous`);
  }
});

test("4.8 a stale policy version re-asks, live", async () => {
  const stale = `${CONSENT_COOKIE}=${encode({
    policyVersion: "1999-01-01.1",
    decisions: { essential: "granted", product_analytics: "granted", session_replay: "granted", error_diagnostics: "granted" },
  })}`;
  const res = await fetch(`${console_.base}/`, { headers: { cookie: stale } });
  assert.match(await res.text(), /Choose what this site may collect/, "a stale grant was not re-asked");
  assert.doesNotMatch(
    res.headers.get("content-security-policy") ?? "",
    /https?:\/\//,
    "a grant given against a superseded policy still widened the header",
  );
});

test("the policy narrows per category, not all-or-nothing", () => {
  // Granting analytics must not switch error reporting on, and vice versa. Asserted at the builder so
  // the failure names the category rather than the header.
  const analyticsOnly = buildContentSecurityPolicy({
    consoleId: "customer", pathname: "/", nonce: "N", dev: false,
    granted: ["essential", "product_analytics"],
  });
  assert.doesNotMatch(analyticsOnly, /ingest/, "granting analytics enabled error reporting");
  const diagnosticsOnly = buildContentSecurityPolicy({
    consoleId: "customer", pathname: "/", nonce: "N", dev: false,
    granted: ["essential", "error_diagnostics"],
  });
  assert.match(diagnosticsOnly, /ingest/);
  assert.equal(isGranted({ decisions: { error_diagnostics: "not-asked" } }, "error_diagnostics"), false);
});
