// accept.mjs is the console's ACCEPTANCE run: the checks that are only true in a real browser.
//
// # Why this exists as a separate gate from `scan-bundle.mjs`
//
// `scan-bundle.mjs` measures `.next/static` — the JavaScript OUR BUILD produces. A script served from
// a third-party host is not in that directory and never will be. So on its own, the payload ceiling
// would stop a small 3D library and not notice three trackers, and after P24 installs three
// integrations it would mean LESS than it did before (design D9). A phase that relaxes a guard has to
// leave a stronger one behind, or it is just a relaxation.
//
// The measurement therefore has to be taken where the bytes actually arrive: in a browser, over the
// wire, on the real surface. That is what this script does.
//
// # Why per origin and not a total
//
// A total lets one integration grow into another's headroom silently — a decision made by nobody,
// discovered by nobody. Each allowlisted origin carries its own ceiling in
// `web/design-system/third-party-policy.ts`, and the failure names the origin, the budget and the
// overage. The total is kept as well, because three integrations each inside their own budget can
// still add up to a public page that costs more than the product.
//
// # Both directions
//
// A surface may not load an origin the allowlist does not carry — and the allowlist may not carry an
// origin no surface loads. A stale entry is a permission nobody asked for, and it is the kind of thing
// that survives for years because nothing fails when it is wrong.
//
// # It refuses rather than skipping
//
// With no browser available this exits non-zero and says what to install. A gate that skips silently
// when its precondition is missing is a gate that reports success for a run that measured nothing,
// which is the failure mode every fence in this repository is written against.
//
//   npm run accept                       # start a console and measure it
//   CONSOLE_URL=http://… npm run accept  # measure a console that is already running

import { readdir, stat } from "node:fs/promises";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import process from "node:process";
import {
  ALLOWED_ORIGINS,
  THIRD_PARTY_TOTAL_BUDGET_BYTES,
} from "../../design-system/third-party-policy.ts";

/**
 * PUBLIC_ROUTES is what an anonymous visitor actually walks, and it is deliberately not "every route".
 *
 * The budget is a property of the PUBLIC surface — the only surface P24 permits a third-party tag on.
 * Tenant and operator routes are covered by a different assertion with a different threshold (zero),
 * because there the question is not "how many bytes" but "any at all".
 */
const PUBLIC_ROUTES = ["/", "/install", "/docs", "/legal", "/signin"];

/**
 * chromeExecutable finds a browser without downloading one mid-run.
 *
 * Three sources, in order of how explicit they are. A download inside an acceptance run would make the
 * gate's result depend on network weather, which is the one thing a gate must not do.
 */
async function chromeExecutable() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;

  const cache = join(homedir(), ".cache", "puppeteer");
  for (const flavour of ["chrome-headless-shell", "chrome"]) {
    const dir = join(cache, flavour);
    let versions = [];
    try {
      versions = await readdir(dir);
    } catch {
      continue;
    }
    for (const version of versions.sort().reverse()) {
      for (const candidate of [
        join(dir, version, `${flavour}-mac-arm64`, flavour),
        join(dir, version, `${flavour}-mac-x64`, flavour),
        join(dir, version, `${flavour}-linux64`, flavour),
        join(dir, version, "chrome-mac-arm64", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"),
        join(dir, version, "chrome-mac-x64", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"),
        join(dir, version, "chrome-linux64", "chrome"),
      ]) {
        if (existsSync(candidate) && (await stat(candidate)).isFile()) return candidate;
      }
    }
  }

  for (const installed of [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
  ]) {
    if (existsSync(installed)) return installed;
  }
  return null;
}

/** originOf reduces a URL to its scheme+host+port, which is the unit a budget and a CSP are stated in. */
function originOf(url) {
  try {
    return new URL(url).origin;
  } catch {
    return null;
  }
}

/**
 * measure drives a real browser over `routes` and returns transferred bytes per origin.
 *
 * The number comes from CDP's `Network.loadingFinished.encodedDataLength`, which is bytes ON THE WIRE
 * after content encoding — the same thing a budget stated in "gzip on the wire" means. Measuring
 * decoded length would report a number three times too large and turn every budget conversation into
 * an argument about which number is real.
 */
async function measure(browser, base, routes, consentCookie) {
  const bytesByOrigin = new Map();
  const urlsByOrigin = new Map();

  const page = await browser.newPage();
  if (consentCookie) await page.setCookie({ ...consentCookie, url: base });
  const cdp = await page.createCDPSession();
  await cdp.send("Network.enable");

  const urlByRequest = new Map();
  cdp.on("Network.requestWillBeSent", (event) => urlByRequest.set(event.requestId, event.request.url));
  cdp.on("Network.loadingFinished", (event) => {
    const url = urlByRequest.get(event.requestId);
    if (!url) return;
    const origin = originOf(url);
    if (!origin) return;
    bytesByOrigin.set(origin, (bytesByOrigin.get(origin) ?? 0) + (event.encodedDataLength ?? 0));
    if (!urlsByOrigin.has(origin)) urlsByOrigin.set(origin, new Set());
    urlsByOrigin.get(origin).add(url);
  });

  for (const route of routes) {
    await page.goto(`${base}${route}`, { waitUntil: "networkidle2", timeout: 30_000 });
    // A tag that loads after first paint is still a tag, and this wait has to cover the LONGEST path a
    // deferred loader can take. Ours is `requestAnimationFrame` then `requestIdleCallback` with a
    // 3-second timeout, so 1,500 ms — the first value here — let the whole Clarity load escape the
    // measurement and reported it as an unloaded allowlist entry. 4,500 ms covers the timeout with
    // room; a budget that measures a tag which has not finished loading is not a budget.
    await new Promise((resolve) => setTimeout(resolve, 4500));
  }

  await page.close();
  return { bytesByOrigin, urlsByOrigin };
}

/**
 * evaluate turns a measurement into findings. Pure, exported, and separate from the browser on purpose.
 *
 * The browser half can only be made to go red by arranging a page that really loads a third party,
 * which proves the MEASUREMENT works but exercises exactly one of the four rules. The other three —
 * an over-budget origin, a stale allowlist entry, an over-budget total — would each need a real
 * integration to exist before they could be demonstrated, which is the wrong order: the fence has to be
 * red-demonstrated before the tool arrives, or it ends up shaped around the tool.
 *
 * So the decision is a function over a measurement, and `tests/third-party-fence.test.mjs` feeds it
 * synthetic measurements for all four. The measurement path is demonstrated end to end, in a real
 * browser, against a page that really does load a second origin.
 */
export function evaluate({ bytesByOrigin, urlsByOrigin, own, allowlist, totalBudget, exercised }) {
  const failures = [];
  const thirdParty = [...bytesByOrigin.entries()].filter(([origin]) => origin !== own);
  /**
   * entryFor resolves an observed origin to its allowlist row, honouring a bounded wildcard.
   *
   * A wildcard entry matches a single leftmost label under its own registrable suffix and nothing else,
   * so `https://*.clarity.ms` covers `https://e.clarity.ms` and does NOT cover `https://clarity.ms.evil`
   * — the check is a suffix match after the dot, not a substring.
   */
  const entryFor = (origin) =>
    allowlist.find((o) => {
      if (o.origin === origin) return true;
      if (!o.origin.includes("*")) return false;
      const suffix = o.origin.replace("https://*", "");
      return origin.startsWith("https://") && origin.endsWith(suffix) && origin.length > `https://${suffix}`.length;
    });
  const budgets = { has: (o) => Boolean(entryFor(o)), get: (o) => entryFor(o) };

  // Direction 1 — a surface may not load an origin the allowlist does not carry.
  for (const [origin, bytes] of thirdParty) {
    if (!budgets.has(origin)) {
      const sample = [...(urlsByOrigin?.get(origin) ?? [])].slice(0, 3).join(", ");
      failures.push(
        `UNLISTED ORIGIN: the public surface transferred ${bytes} bytes from ${origin}, which the ` +
          `allowlist does not carry${sample ? ` (e.g. ${sample})` : ""}. An origin is added to ` +
          `third-party-policy.ts with the integration that needs it, the consent category that gates ` +
          `it and a budget.`,
      );
    }
  }

  // Direction 2 — the allowlist may not carry an origin no surface loads.
  //
  // Applied only to `page-load` origins. An `on-event` origin (an error-reporting ingest endpoint) is
  // contacted only when something happens, which no clean page walk can produce — so requiring it to
  // appear here would report a permanent false failure, and the first person to hit that would relax
  // the check for every origin. `on-event` entries are exercised by their own deliberate trigger
  // instead; see `proveTheReportingOriginIsReachedOnAnError`.
  for (const entry of allowlist) {
    if (entry.contactedOn !== "page-load") continue;
    // An entry whose integration is not CONFIGURED for this run cannot be observed: the tag is absent
    // by design on every substrate but the platform's own hosted deployment, so requiring it to appear
    // would report a permanent false failure — and the first person to hit that would relax the check
    // for every origin. `exercised` names the integrations this run actually loaded.
    if (exercised && !exercised.has(entry.integration)) continue;
    // Wildcard-aware. `bytesByOrigin.has("https://*.clarity.ms")` is always false — the browser
    // contacts `scripts.clarity.ms`, never the pattern — so an exact lookup reported a live entry as
    // stale. Found by the measured run; the same `entryFor` the budget check uses resolves it.
    const loaded = [...bytesByOrigin.keys()].some((o) => entryFor(o) === entry);
    if (!loaded) {
      failures.push(
        `STALE ALLOWLIST ENTRY: ${entry.origin} (${entry.integration}) is permitted but was not ` +
          `loaded by any public route in this run. A permission nobody asked for is removed, not kept.`,
      );
    }
  }

  // The budgets themselves, per origin.
  for (const [origin, bytes] of thirdParty) {
    const entry = budgets.get(origin);
    if (!entry) continue;
    if (bytes > entry.budgetBytes) {
      failures.push(
        `BUDGET: ${origin} (${entry.integration}) transferred ${bytes} bytes, over its ` +
          `${entry.budgetBytes}-byte budget by ${bytes - entry.budgetBytes}. Per-origin on purpose: ` +
          `another origin shrinking does not buy this one headroom.`,
      );
    }
  }

  const total = thirdParty.reduce((sum, [, bytes]) => sum + bytes, 0);
  if (total > totalBudget) {
    failures.push(
      `BUDGET: third-party total is ${total} bytes, over the ${totalBudget}-byte ceiling by ` +
        `${total - totalBudget}.`,
    );
  }

  return { failures, thirdParty, total, budgets };
}

/**
 * proveTheMeasurementIsConnected runs the whole browser path against a page that really does load a
 * second origin, and requires it to go RED.
 *
 * # Why this runs on every acceptance run rather than once
 *
 * "0 third-party bytes from 0 origins" is the expected result and it is also what a broken measurement
 * prints. A CDP listener attached to the wrong session, a `waitUntil` that returns before the tag
 * loads, a browser flag that blocks third-party requests outright — every one of those produces a
 * confident green line for a run that saw nothing. The distinction between "measured zero" and
 * "measured nothing" cannot be made from the passing output, so it is made by requiring the same code
 * path to report a request it was given one to find.
 *
 * The fixture's second origin is another loopback port. From the console's perspective that IS a third
 * party — the rule is stated in origins, and the check is `origin !== own` — so this exercises the real
 * comparison rather than a mock of it.
 */
async function proveTheMeasurementIsConnected(browser) {
  const { createServer } = await import("node:http");
  const { once } = await import("node:events");

  const listen = async (handler) => {
    const server = createServer(handler);
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    return { server, base: `http://127.0.0.1:${server.address().port}` };
  };

  const third = await listen((_req, res) => {
    res.writeHead(200, { "content-type": "application/javascript" });
    res.end(`/* ${"x".repeat(4096)} */\n`);
  });
  const first = await listen((_req, res) => {
    res.writeHead(200, { "content-type": "text/html" });
    res.end(`<!doctype html><html><body><script src="${third.base}/tag.js"></script></body></html>`);
  });

  try {
    const measurement = await measure(browser, first.base, ["/"]);
    const { failures } = evaluate({
      ...measurement,
      own: originOf(first.base),
      allowlist: [],
      totalBudget: THIRD_PARTY_TOTAL_BUDGET_BYTES,
    });
    const caught = failures.some((f) => f.startsWith("UNLISTED ORIGIN") && f.includes(third.base));
    if (!caught) {
      console.error(
        "acceptance SELF-CHECK FAILED: a page that really loads a second origin produced no finding.\n" +
          "The measurement is not connected, so '0 third-party bytes from 0 origins' below would mean\n" +
          "'we looked at nothing' rather than 'there was nothing'. Refusing to report a green run.",
      );
      return false;
    }
    return true;
  } finally {
    first.server.close();
    third.server.close();
  }
}

/**
 * proveTheReportingOriginIsReachedOnAnError is the `on-event` half of the both-directions check, and
 * the browser verification for P24 task 3.7.
 *
 * # What it establishes, in one run
 *
 *  1. **Decline is real.** With no consent cookie the tenant route's policy names no third-party origin
 *     and a deliberate throw produces NO request to the reporting host.
 *  2. **Accept reaches exactly one origin.** With `error_diagnostics` granted, the same throw produces a
 *     request to the reporting origin and to nothing else.
 *  3. **The payload is what the allowlist says.** The request BODY is inspected: no breadcrumb array, no
 *     `location.href`, no message body, and a surface id rather than a URL.
 *
 * # Why the request is intercepted rather than allowed to leave
 *
 * An acceptance run must not deposit fixture events in a real incident inbox on every execution. The
 * request is intercepted at the browser and answered locally, which means the browser really made it —
 * so the policy really permitted it, which is the thing under test — and nothing left the machine.
 */
async function proveTheReportingOriginIsReachedOnAnError(browser, base, reportingOrigin) {
  const findings = [];
  const captured = [];
  const probeNotes = [];

  // A FRESH browser context, not just a fresh page.
  //
  // Cookies and storage are per CONTEXT. Reusing the default context meant the granted measurement's
  // `_ga` cookies were still present here, and this phase reported "a visitor who declined was left an
  // identifier" about a visitor who had granted one two steps earlier. The finding was real and the
  // subject was wrong, which is the worst shape a security finding can take.
  const context = await browser.createBrowserContext();
  const page = await context.newPage();

  // The BODY is read from CDP rather than from the interception hook: a `fetch` with `keepalive` does
  // not expose its post data through the high-level request object, and an assertion that silently read
  // an empty string would pass for every payload including a bad one. `Network.requestWillBeSent`
  // carries it, with `getRequestPostData` as the fallback for a body the event truncated.
  const cdp = await page.createCDPSession();
  await cdp.send("Network.enable");
  const pending = [];
  cdp.on("Network.requestWillBeSent", (event) => {
    if (originOf(event.request.url) !== reportingOrigin) return;
    pending.push(
      (async () => {
        let body = event.request.postData ?? "";
        if (!body && event.request.hasPostData) {
          try {
            const got = await cdp.send("Network.getRequestPostData", { requestId: event.requestId });
            body = got.postData ?? "";
          } catch {
            body = "";
          }
        }
        captured.push({ url: event.request.url, body });
      })(),
    );
  });

  // Interception blocks the egress: the browser really makes the request — which is what proves the
  // policy permitted it — and nothing leaves this machine.
  await page.setRequestInterception(true);
  page.on("request", (request) => {
    if (originOf(request.url()) === reportingOrigin) {
      void request.respond({ status: 200, contentType: "application/json", body: "{}" });
      return;
    }
    void request.continue();
  });

  const throwOnce = async () => {
    // A real unhandled error, raised out of band so the page's own script does not catch it. This is
    // the exact path a chunk-load failure or a hydration mismatch takes.
    await page.evaluate(() => {
      setTimeout(() => {
        throw new Error("p24 acceptance probe: tenant Nous Research Ltd key sk-ant-api03-acceptance-probe");
      }, 0);
    });
    await new Promise((resolve) => setTimeout(resolve, 1200));
  };

  // ── 1. Declined (the default): nothing is requested ────────────────────────
  //
  // This runs in its own browser context (see above), so the cookie jar is empty: the granted
  // measurement's consent cookie AND the identifiers GA4 wrote under it are both absent, and "a visitor
  // who has answered nothing" means exactly that.
  await page.goto(`${base}/`, { waitUntil: "networkidle2", timeout: 30_000 });
  await throwOnce();
  await Promise.all(pending);
  if (captured.length > 0) {
    findings.push(
      `CONSENT: a deliberate error with NO consent cookie produced ${captured.length} request(s) to ` +
        `${reportingOrigin}. Default-denied means nothing is transmitted before an explicit grant.`,
    );
  }

  // ── 1b. Declined: no non-essential cookie or storage entry exists ──────────
  //
  // "Zero third-party requests" is only half of what declining has to mean. A tag that wrote a client
  // id into localStorage and then failed to send it has still identified the visitor, and the identifier
  // is still there the next time they arrive.
  const storage = await page.evaluate(() => ({
    cookies: document.cookie.split(";").map((c) => c.split("=")[0].trim()).filter(Boolean),
    local: Object.keys(localStorage),
    session: Object.keys(sessionStorage),
  }));
  // The essential set, named. Anything else is a finding — an allowlist here for the same reason the
  // event payload is built from one: a denylist of known tracker keys would pass the first tracker
  // nobody had heard of.
  const ESSENTIAL_KEYS = ["heros_consent", "heros_console_theme", "heros_console_session"];
  for (const [where, keys] of [["cookie", storage.cookies], ["localStorage", storage.local], ["sessionStorage", storage.session]]) {
    for (const key of keys) {
      if (ESSENTIAL_KEYS.includes(key)) continue;
      findings.push(
        `CONSENT: a non-essential ${where} entry "${key}" exists with nothing granted. Declining must ` +
          `leave no identifier behind, not merely stop a request.`,
      );
    }
  }

  // ── 2. Granted: exactly one origin is reached ──────────────────────────────
  const { CONSENT_COOKIE, encode, CONSENT_POLICY_VERSION } = await import("../../design-system/consent.ts");
  await page.setCookie({
    name: CONSENT_COOKIE,
    value: encode({
      policyVersion: CONSENT_POLICY_VERSION,
      decisions: {
        essential: "granted",
        product_analytics: "denied",
        session_replay: "denied",
        error_diagnostics: "granted",
      },
    }),
    url: base,
  });
  captured.length = 0;
  pending.length = 0;
  await page.goto(`${base}/`, { waitUntil: "networkidle2", timeout: 30_000 });
  await throwOnce();

  if (captured.length === 0) {
    findings.push(
      `REPORTING: with error_diagnostics granted, a deliberate unhandled error produced no request to ` +
        `${reportingOrigin}. Either the reporter is not installed, or the policy refused it — both are ` +
        `the failure this integration exists to prevent, and both look like a working page.`,
    );
  }

  // ── 2b. A script without the nonce does not run ────────────────────────────
  //
  // The reporter runs as first-party bundled code reached through `'strict-dynamic'` from the nonced
  // bootstrap — which is only a guarantee if the nonce regime is real. This injects a script the way an
  // XSS payload does and requires the browser to refuse it.
  //
  // 🔴 The obvious version of this test PASSES WRONGLY, and the reason is worth writing down. Creating
  // a `<script>` from `page.evaluate` and appending it does NOT get blocked: `'strict-dynamic'` exists
  // precisely to propagate trust from an already-trusted script to the scripts it creates, and CDP
  // evaluation counts as trusted. Written that way, the assertion reports that the CSP is broken on a
  // console whose CSP is fine — which is worse than not testing it.
  //
  // What an XSS payload actually is, is a PARSER-INSERTED script in a document governed by this policy.
  // An `iframe srcdoc` inherits the parent's policy and parses its own markup, so the script below is
  // parser-inserted under the real header — the case the nonce is for.
  const nonceProbe = await page.evaluate(() => {
    const nonce = document.querySelector("script[nonce]")?.nonce ?? "";
    const run = (markup, flag) =>
      new Promise((resolve) => {
        const frame = document.createElement("iframe");
        frame.srcdoc = markup;
        document.body.appendChild(frame);
        setTimeout(() => {
          const ran = Boolean(window[flag]);
          frame.remove();
          resolve(ran);
        }, 300);
      });
    return (async () => ({
      nonce,
      withoutNonce: await run(
        "<script>parent.__p24_unnonced = true;<\/script>",
        "__p24_unnonced",
      ),
      withNonce: await run(
        `<script nonce="${nonce}">parent.__p24_nonced = true;<\/script>`,
        "__p24_nonced",
      ),
    }))();
  });

  if (nonceProbe.withoutNonce) {
    findings.push(
      "CSP: a parser-inserted inline script with no nonce EXECUTED. `'strict-dynamic'` and the " +
        "per-request nonce are what make the reporter's first-party-only claim mean anything; without " +
        "them any injected script runs on the same page.",
    );
  }
  // The POSITIVE control. Without it, "the unnonced script did not run" is indistinguishable from "no
  // script in that frame could have run" — an iframe blocked for an unrelated reason would report the
  // CSP as working on a page where it is not.
  if (!nonceProbe.withNonce) {
    findings.push(
      "CSP SELF-CHECK: the NONCED script in the same frame did not run either, so the negative result " +
        "above proves nothing about the nonce. Something other than the policy is blocking the frame.",
    );
  }

  // ── 3. The payload is what the allowlist says ──────────────────────────────
  //
  // Only the bodies that were actually captured are inspected, and at least one must have been: a
  // browser can emit the same request twice through CDP (once with the post data, once without), and a
  // loop that treated an empty body as a payload would report the transmitted event as carrying no
  // surface id — a confident, wrong finding.
  const bodies = captured.filter((c) => c.body.length > 0);
  if (captured.length > 0 && bodies.length === 0) {
    findings.push("BROWSER PAYLOAD: a request was made but no body could be read — the payload was not inspected.");
  }
  for (const { body } of bodies) {
    for (const [what, needle] of [
      ["a breadcrumb collection", "breadcrumb"],
      ["the page URL", base],
      ["a request block", '"request"'],
      ["a user block", '"user"'],
      ["the error's message body", "acceptance probe"],
      ["a tenant name", "Nous Research"],
      ["an API key", "sk-ant-api03"],
    ]) {
      if (body.includes(needle)) {
        findings.push(`BROWSER PAYLOAD: the transmitted event contains ${what} (${JSON.stringify(needle)}).`);
      }
    }
    if (!body.includes('"surface"')) {
      findings.push("BROWSER PAYLOAD: the transmitted event carries no surface id.");
    }
  }

  await Promise.all(pending);
  probeNotes.push(
    nonceProbe.withNonce && !nonceProbe.withoutNonce
      ? "a parser-inserted inline script was REFUSED without the nonce and RAN with it"
      : "the nonce probe did not establish the policy",
  );
  if (process.env.P24_DEBUG) {
    for (const c of captured) console.error("[debug] captured", c.url, JSON.stringify(c.body).slice(0, 400));
  }
  await page.close();
  await context.close();
  return { findings, captured, probeNotes };
}

async function main() {
  const executablePath = await chromeExecutable();
  if (!executablePath) {
    console.error(
      "acceptance: no browser found, and this measurement is only meaningful in one.\n" +
        "  npx @puppeteer/browsers install chrome-headless-shell@stable\n" +
        "  # or set CHROME_PATH to an existing Chrome/Chromium binary\n" +
        "\nRefusing rather than skipping: a gate that passes without measuring anything is worse than\n" +
        "no gate, because the passing line gets believed.",
    );
    process.exit(2);
  }

  let puppeteer;
  try {
    puppeteer = (await import("puppeteer-core")).default;
  } catch {
    console.error("acceptance: puppeteer-core is not installed — run `npm install` in web/console");
    process.exit(2);
  }

  // Either measure a console somebody is already running, or start one exactly as the security suite
  // does — `next start` under production settings, because the shipped policy and the development
  // policy differ and the acceptance question is about what ships.
  let started;
  let base = process.env.CONSOLE_URL;
  if (!base) {
    const { startStubPlatform, startConsole } = await import("../tests/support/harness.mjs");
    const platform = await startStubPlatform();
    // The console is started WITH a reporting DSN, so the on-event probe below exercises the real
    // reporter. The origin is the allowlisted one — the browser reporter refuses a DSN whose origin the
    // artefact does not carry — and the key is a fixture; the probe intercepts the request at the
    // browser, so nothing leaves this machine.
    const reporting = ALLOWED_ORIGINS.find((o) => o.category === "error_diagnostics");
    const console_ = await startConsole(platform.base, {
      HEROS_ERROR_REPORTING_DSN: reporting ? `${reporting.origin.replace("://", "://p24acceptancefixturekey@")}/4242` : "",
      HEROS_VERSION: "acceptance",
      HEROS_EDITION: "dev",
      // Forwarded, not defaulted. With these unset the tags are ABSENT — which is the state on every
      // substrate but the platform's own hosted deployment, and the state this gate runs in by default.
      // A run that wants to WEIGH them supplies them, and the output says which were exercised.
      HEROS_GA4_MEASUREMENT_ID: process.env.HEROS_GA4_MEASUREMENT_ID ?? "",
      HEROS_CLARITY_PROJECT_ID: process.env.HEROS_CLARITY_PROJECT_ID ?? "",
    });
    started = { platform, console_ };
    base = console_.base;
  }

  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });

  const failures = [];
  try {
    if (!(await proveTheMeasurementIsConnected(browser))) {
      process.exitCode = 2;
      return;
    }

    const own = originOf(base);
    const { CONSENT_COOKIE, encode, CONSENT_POLICY_VERSION } = await import("../../design-system/consent.ts");

    /*
     * TWO measurements, and the pair is the point.
     *
     * DECLINED is the state a first-time visitor is in, and the only correct answer there is zero. It is
     * measured first because a run that only measured the granted state would report budgets while
     * saying nothing about the thing that matters more — that a visitor who has not answered is not
     * being counted.
     *
     * GRANTED is where the per-origin budgets live. An origin can only be weighed when it is actually
     * loaded, so the budget half of this gate is meaningless without it.
     */
    const declinedMeasurement = await measure(browser, base, PUBLIC_ROUTES);
    const declinedThirdParty = [...declinedMeasurement.bytesByOrigin.entries()].filter(([o]) => o !== own);
    if (declinedThirdParty.length > 0) {
      failures.push(
        `CONSENT: a visitor who has answered nothing caused ${declinedThirdParty.length} third-party ` +
          `origin(s) to be contacted: ${declinedThirdParty.map(([o, b]) => `${o} (${b} bytes)`).join(", ")}. ` +
          `Default-denied means nothing loads before an explicit grant.`,
      );
    }

    const grantAll = {
      name: CONSENT_COOKIE,
      value: encode({
        policyVersion: CONSENT_POLICY_VERSION,
        decisions: {
          essential: "granted",
          product_analytics: "granted",
          session_replay: "granted",
          error_diagnostics: "granted",
        },
      }),
    };
    const measurement = await measure(browser, base, PUBLIC_ROUTES, grantAll);

    // Which integrations this run could actually load. An id that is absent means the tag is absent by
    // design, and an absent tag cannot be weighed or observed — so its allowlist row is neither
    // confirmed nor reported stale, and the run SAYS so rather than passing quietly.
    const exercised = new Set();
    if (process.env.HEROS_GA4_MEASUREMENT_ID) exercised.add("Google Analytics 4");
    if (process.env.HEROS_CLARITY_PROJECT_ID) exercised.add("Microsoft Clarity");
    if (process.env.HEROS_ERROR_REPORTING_DSN || true) exercised.add("Sentry (error reporting)");
    const notExercised = ALLOWED_ORIGINS.filter(
      (o) => o.contactedOn === "page-load" && !exercised.has(o.integration),
    );

    if (process.env.P24_DEBUG) {
      console.error("[debug] origins observed with all granted:",
        JSON.stringify([...measurement.bytesByOrigin.entries()]));
    }
    const evaluated = evaluate({
      ...measurement,
      own,
      allowlist: ALLOWED_ORIGINS,
      totalBudget: THIRD_PARTY_TOTAL_BUDGET_BYTES,
      exercised,
    });
    failures.push(...evaluated.failures);
    const { thirdParty, total, budgets } = evaluated;

    /*
     * 🔴 TASK 6.4 — the tenant prefix, measured rather than argued.
     *
     * Every assertion about `/app` up to this point is about the POLICY: the header names no analytics
     * origin, the class does not permit the category, the runtime is absent from the chunks. This is
     * the outcome: a real browser, signed in, walking real tenant routes, with EVERY category granted —
     * and the only third-party origin it may contact is the error-reporting one.
     *
     * It is the measurement that would catch what none of the others can: a request from a dependency's
     * code, from a font the design system pulled in, from an image somebody hot-linked. Those are not
     * policy failures, they are page failures, and only a browser sees them.
     */
    let tenantMeasured = 0;
    if (!process.env.CONSOLE_URL && started) {
      const { signIn } = await import("../tests/support/harness.mjs");
      const cookie = await signIn(base);
      const session = cookie.split("=");
      const context = await browser.createBrowserContext();
      const page = await context.newPage();
      await page.setCookie(
        { name: session[0], value: session.slice(1).join("="), url: base },
        { name: grantAll.name, value: grantAll.value, url: base },
      );
      const seen = new Map();
      const cdp = await page.createCDPSession();
      await cdp.send("Network.enable");
      const urls = new Map();
      cdp.on("Network.requestWillBeSent", (e) => urls.set(e.requestId, e.request.url));
      cdp.on("Network.loadingFinished", (e) => {
        const url = urls.get(e.requestId);
        if (!url) return;
        const origin = originOf(url);
        if (origin && origin !== own) seen.set(origin, (seen.get(origin) ?? 0) + (e.encodedDataLength ?? 0));
      });
      const TENANT_ROUTES = ["/app", "/app/studio", "/app/account", "/app/coverage"];
      for (const route of TENANT_ROUTES) {
        await page.goto(`${base}${route}`, { waitUntil: "networkidle2", timeout: 30_000 });
        await new Promise((r) => setTimeout(r, 1500));
      }
      tenantMeasured = TENANT_ROUTES.length;
      const permittedOnTenant = new Set(
        ALLOWED_ORIGINS.filter((o) => o.category === "error_diagnostics").map((o) => o.origin),
      );
      for (const [origin, bytes] of seen) {
        if (permittedOnTenant.has(origin)) continue;
        failures.push(
          `TENANT EGRESS: a signed-in browser on a tenant route transferred ${bytes} bytes from ` +
            `${origin}. The only third-party origin a tenant prefix may contact is the error-reporting ` +
            `one, under connect-src — /app renders prompt text, generated diffs and run output.`,
        );
      }
      await page.close();
      await context.close();
    }

    // The `on-event` origins, exercised by their own deliberate trigger.
    const reporting = ALLOWED_ORIGINS.find((o) => o.contactedOn === "on-event");
    let reported = [];
    let probeNotes = [];
    if (reporting && !process.env.CONSOLE_URL) {
      const probe = await proveTheReportingOriginIsReachedOnAnError(browser, base, reporting.origin);
      failures.push(...probe.findings);
      reported = probe.captured;
      probeNotes = probe.probeNotes;
    }

    if (failures.length > 0) {
      console.error(`acceptance FAILED — ${failures.length} finding(s):`);
      for (const failure of failures) console.error(`  - ${failure}`);
      process.exitCode = 1;
      return;
    }

    // The measurement is printed whether or not anything was found. With an empty allowlist the
    // expected line is "0 bytes from 0 origins", and printing it is what distinguishes a run that
    // measured zero from a run that measured nothing.
    console.log(
      "acceptance self-check passed: a fixture page loading a second origin was detected and named, " +
        "so the measurement below is a measurement.",
    );
    console.log(
      `acceptance passed: ${PUBLIC_ROUTES.length} public route(s) walked in a real browser; ` +
        `0 third-party bytes with consent DECLINED; ${total} bytes from ${thirdParty.length} origin(s) ` +
        `with every category GRANTED; ${ALLOWED_ORIGINS.length} allowlisted origin(s), all inside budget.`,
    );
    if (notExercised.length > 0) {
      console.log(
        `  NOT EXERCISED (absent by configuration, so neither confirmed nor reported stale): ` +
          notExercised.map((o) => `${o.origin} [${o.integration}]`).join(", "),
      );
    }
    if (reported.length > 0) {
      console.log(
        `  on-event: a deliberate unhandled error produced 0 request(s) declined and ` +
          `${reported.length} granted, to ${reporting.origin} and nowhere else; the transmitted body ` +
          `carries no breadcrumb collection, no page URL, no request block and no message body.`,
      );
    }
    for (const note of probeNotes) console.log(`  csp: ${note}`);
    if (tenantMeasured > 0) {
      console.log(
        `  tenant: ${tenantMeasured} signed-in /app route(s) walked with EVERY category granted; no ` +
          `third-party origin contacted except the error-reporting one.`,
      );
    }
    if (reporting && !process.env.CONSOLE_URL) {
      console.log("  consent: declining left no non-essential cookie, localStorage or sessionStorage entry.");
    }
    for (const [origin, bytes] of thirdParty.sort((a, b) => b[1] - a[1])) {
      const entry = budgets.get(origin);
      console.log(`  ${origin}: ${bytes} bytes / ${entry?.budgetBytes ?? "?"} budget (${entry?.integration ?? "?"})`);
    }
  } finally {
    await browser.close();
    await started?.console_.close();
    await started?.platform.close();
  }
}

// Importing this module for `evaluate` must not start a browser. `import.meta.main` is true only when
// this file is the entry point.
if (import.meta.main) {
  main().catch((error) => {
    console.error("acceptance errored:", error);
    process.exit(2);
  });
}
