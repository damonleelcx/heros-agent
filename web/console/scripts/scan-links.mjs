// scan-links.mjs keeps the anchor contract (task 4.10 · Decision 8).
//
// # Why a heading rename is a shipped-binary problem
//
// CLI error messages, console empty states and API error bodies deep-link into this documentation. A
// renamed heading therefore breaks a link that ships **inside a binary the customer already installed**.
// There is no deploy that fixes it for them, and no way to know how many people follow it.
//
// So four things fail the build:
//
//   1. an internal link that does not resolve to a page
//   2. an anchor that does not resolve to a heading on the page it points at
//   3. a slug that EXISTED IN THE LAST COMMIT and is gone now, with no redirect added in the same change
//   4. an external link that is not allow-listed, or is not marked as external
//
// (3) is the one that needs explaining. The comparison is against `git show HEAD:` of the checked-in slug
// manifest — the anchors as last published — so "you removed something readers may be pointing at" is a
// question with a real answer rather than a reviewer's memory. In a build with no git history (a
// container build from a tarball), the check is SKIPPED AND SAYS SO, because silently passing a check
// you did not run is how a fence becomes decorative.
//
// # The reserved `/docs/v/` segment (decisions/p23-one-way-doors.md §1.6)
//
// Versioned documentation is deferred, and its URL shape is reserved now because reserving it later
// means moving published URLs. No documentation section may be named `v`.
//
// # What it deliberately does not check
//
//   - Whether an external link's TARGET is alive. This build must work air-gapped, and a fence that made
//     network requests would fail in exactly the deployment the whole design protects. Reachability of
//     the ONE link that matters — the repository — is `scan-repo-link.mjs`, which is explicit about it.
//   - Whether a link is USEFUL, or points at the best page. That is review.
//   - Links in the console's own React source. `link-coverage.test.mjs` owns navigation.

import { readFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import process from "node:process";
import { documents, report } from "./lib/corpus.mjs";

const exec = promisify(execFile);
const ROOT = process.cwd();
const MANIFEST = process.env.HEROS_SLUG_MANIFEST ?? join(ROOT, "docs", "slug-manifest.json");
const REDIRECTS = join(process.env.HEROS_CONTENT_ROOT ?? join(ROOT, "content"), "redirects.json");

/** ALLOWED_EXTERNAL is the closed set of origins the corpus may link to. */
const ALLOWED_EXTERNAL = ["https://github.com/", "https://raw.githubusercontent.com/"];

/** KNOWN_ROUTES are console routes the corpus may link to that are not content pages. */
const KNOWN_ROUTES = new Set(["/", "/docs", "/legal", "/signin", "/install", "/app", "/legal/manifest.json"]);

async function readJSON(path, fallback) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch {
    return fallback;
  }
}

/** publishedAtHead reads the slug manifest as it was in the last commit. */
async function publishedAtHead() {
  try {
    const { stdout } = await exec("git", ["show", "HEAD:web/console/docs/slug-manifest.json"], { cwd: ROOT });
    return { available: true, manifest: JSON.parse(stdout) };
  } catch {
    return { available: false, manifest: null };
  }
}

async function main() {
  const manifest = await readJSON(MANIFEST, null);
  if (!manifest) {
    console.error("link scan FAILED — there is no docs/slug-manifest.json. Run `npm run gen:slug-manifest`.");
    process.exit(1);
  }

  const anchors = new Map(manifest.pages.map((page) => [page.route, new Set(page.anchors)]));
  const routes = new Set([...anchors.keys(), ...KNOWN_ROUTES]);
  // Legal kinds resolve at their current-version route as well as their permanent one.
  for (const page of manifest.pages) {
    const legal = /^\/legal\/([a-z]+)\/v\/[\d.]+$/.exec(page.route);
    if (legal) {
      routes.add(`/legal/${legal[1]}`);
      const current = anchors.get(`/legal/${legal[1]}`) ?? new Set();
      for (const anchor of page.anchors) current.add(anchor);
      anchors.set(`/legal/${legal[1]}`, current);
    }
  }

  const redirects = await readJSON(REDIRECTS, { redirects: [] });
  const redirected = new Set((redirects.redirects ?? []).map((r) => r.from));

  const findings = [];
  const docs = await documents();
  let links = 0;

  for (const document of docs) {
    const own = anchors.get(document.route) ?? new Set();
    for (const line of document.lines) {
      for (const match of line.text.matchAll(/\[[^\]]*\]\(([^)\s]+)\)/g)) {
        const target = match[1];
        links += 1;

        if (/^https?:\/\//.test(target)) {
          if (!ALLOWED_EXTERNAL.some((origin) => target.startsWith(origin))) {
            findings.push(
              `${document.path}:${line.number}: external link to ${target} is not allow-listed. ` +
                `Allowed origins: ${ALLOWED_EXTERNAL.join(", ")}`,
            );
          }
          continue;
        }

        if (target.startsWith("#")) {
          const anchor = target.slice(1);
          const pageAnchors = anchors.get(routeOf(document)) ?? own;
          if (!pageAnchors.has(anchor)) {
            findings.push(`${document.path}:${line.number}: anchor #${anchor} does not resolve on this page`);
          }
          continue;
        }

        if (!target.startsWith("/")) {
          findings.push(
            `${document.path}:${line.number}: relative link \`${target}\` — links are absolute so a page can ` +
              `move without every link inside it changing`,
          );
          continue;
        }

        const [path, fragment] = target.split("#");
        if (path.startsWith("/docs/v/")) {
          findings.push(
            `${document.path}:${line.number}: \`${path}\` uses the RESERVED /docs/v/ segment. Versioned ` +
              `documentation is deferred and this shape is held for it.`,
          );
          continue;
        }
        if (!routes.has(path)) {
          findings.push(`${document.path}:${line.number}: \`${path}\` does not resolve to a page`);
          continue;
        }
        if (fragment && !(anchors.get(path) ?? new Set()).has(fragment)) {
          findings.push(`${document.path}:${line.number}: \`${path}#${fragment}\` — no such anchor on that page`);
        }
      }
    }
  }

  // ── No documentation section may be named `v` ─────────────────────────────
  for (const page of manifest.pages) {
    if (page.route.startsWith("/docs/v/") || page.route === "/docs/v") {
      findings.push(
        `${page.source}: publishes at ${page.route}, inside the reserved /docs/v/ segment. ` +
          `Rename the section — this shape is held for versioned documentation.`,
      );
    }
  }

  // ── A removed or renamed slug, against what was last published ────────────
  const head = await publishedAtHead();
  let removalNote;
  if (!head.available) {
    removalNote =
      "the removed-slug check was SKIPPED — no git history here (this is expected in a container build " +
      "from a tarball, and it means a rename would not be caught in THIS build)";
  } else {
    let removed = 0;
    const nowByRoute = new Map(manifest.pages.map((p) => [p.route, new Set(p.anchors)]));
    for (const page of head.manifest.pages ?? []) {
      const now = nowByRoute.get(page.route);
      if (!now) {
        if (!redirected.has(page.route)) {
          findings.push(
            `${page.route} was published in the last commit and no longer resolves, and no redirect was ` +
              `added in this change. A CLI error message pointing there ships inside binaries customers ` +
              `already installed — add an entry to content/redirects.json, or restore the page.`,
          );
          removed += 1;
        }
        continue;
      }
      for (const anchor of page.anchors) {
        if (now.has(anchor)) continue;
        if (redirected.has(`${page.route}#${anchor}`)) continue;
        findings.push(
          `${page.route}#${anchor} was published in the last commit and the heading is gone. ` +
            `Restore the heading, or add \`${page.route}#${anchor}\` to content/redirects.json in this change.`,
        );
        removed += 1;
      }
    }
    removalNote = `${removed === 0 ? "no" : String(removed)} anchor(s) removed without a redirect`;
  }

  report(
    "link scan",
    findings,
    docs.length,
    `${links} link(s) resolve, ${anchors.size} page(s) of anchors published, ${removalNote}.`,
    "Anchors are a published contract: CLI messages and console empty states deep-link into them, and a\n" +
      "renamed heading breaks a link inside a binary the customer already has. Removing one needs a\n" +
      "redirect in content/redirects.json, in the same change.",
  );
}

/** routeOf derives a document's own route, so a `#fragment` link is checked against its own headings. */
function routeOf(document) {
  if (document.kind === "legal") {
    const match = /^legal\/en\/([a-z]+)\/([\d.]+)\.md$/.exec(document.rel);
    return match ? `/legal/${match[1]}/v/${match[2]}` : "";
  }
  const slug = document.rel.replace(/^docs\/en\//, "").replace(/\.md$/, "");
  return slug === "index" ? "/docs" : `/docs/${slug}`;
}

main().catch((error) => {
  console.error("link scan errored:", error);
  process.exit(2);
});
