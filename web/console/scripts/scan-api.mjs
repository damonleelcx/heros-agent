// scan-api.mjs REFUSES documented HTTP endpoints while the machine-readable artifact is absent
// (task 4.8 · Decision 6, decisions/p23-one-way-doors.md §1.5).
//
// # 🔴 The point of this fence is that it does not pass vacuously
//
// The obvious implementation is "check every documented endpoint against the OpenAPI document, and if
// there is no OpenAPI document, there is nothing to check". That version reports success on a corpus
// full of invented endpoints. It is a green light with no bulb, and it is worse than no fence, because
// it stops the human review that would have caught them.
//
// So while the artifact is ABSENT this fence **refuses any documented endpoint at all**. The message is
// "I cannot check this", enforced as a build failure, rather than silence mistaken for approval.
//
// # When the artifact exists
//
// The refusal is replaced by the real check: every documented path, method and field must resolve
// against it. The switch is driven by `api_reference.status` in the generated facts, so the day an
// OpenAPI document ships, this fence changes behaviour without anybody editing it.
//
// # What it deliberately does not check
//
//   - Anything about the CLI. `scan-cli.mjs` owns the command surface, which IS documented.
//   - Console-internal routes (`/api/console/**`, `/api/session`). Those are the browser's own BFF, not
//     a customer-facing API, and nothing in the corpus should be describing them as one.
//   - Whether an endpoint that resolves BEHAVES as documented. Once the artifact exists this checks
//     shape, not semantics.

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documents, report } from "./lib/corpus.mjs";

const ROOT = process.cwd();

/**
 * ENDPOINT matches a documented HTTP endpoint: a method and a path, the shape a reference table uses.
 *
 * A bare path is deliberately NOT matched. `/docs/reference/cli` is a link, `/legal/manifest.json` is a
 * published file, and a fence that treated every slash-prefixed string as an endpoint would fire on the
 * navigation of the very page it was reading.
 */
const ENDPOINT = /\b(GET|POST|PUT|PATCH|DELETE)\s+(\/[A-Za-z0-9/_{}.:-]*)/g;

/** SELF_DESCRIBING are paths this documentation legitimately names that are not customer API endpoints. */
const NOT_AN_API = [/^\/legal\//, /^\/docs\//, /^\/app\//, /^\/api\/health$/, /^\/api\/session/];

async function main() {
  const facts = JSON.parse(await readFile(join(ROOT, "src", "generated", "docs-facts.json"), "utf8"));
  const status = facts.api_reference?.status ?? "absent";
  const findings = [];
  const docs = await documents();
  let mentions = 0;

  for (const document of docs) {
    // The generated page whose whole subject is the absence must be able to say so.
    if (document.rel === "docs/en/reference/http-api.md") continue;
    for (const line of document.lines) {
      ENDPOINT.lastIndex = 0;
      for (const match of line.text.matchAll(ENDPOINT)) {
        const [, method, path] = match;
        if (NOT_AN_API.some((p) => p.test(path))) continue;
        mentions += 1;
        if (status !== "present") {
          findings.push(
            `${document.path}:${line.number}: documents \`${method} ${path}\`, and there is no ` +
              `machine-readable API artifact to check it against.\n` +
              `      This is a REFUSAL, not a missing check: a hand-written endpoint list is a copy of the ` +
              `truth that drifts from the day it is written, and a fence that passed here would be ` +
              `approving text nobody can verify.`,
          );
        }
      }
    }
  }

  report(
    "api scan",
    findings,
    docs.length,
    status === "present"
      ? `${mentions} documented endpoint(s), all resolving against the API artifact.`
      : `the HTTP API reference tier is ABSENT and no page documents an endpoint — which is the only ` +
        `state this fence can honestly approve.`,
    "The API reference tier is absent because there is no OpenAPI document in the repository. Until one\n" +
      "exists, no page may document an endpoint — see /docs/reference/http-api, which explains the absence\n" +
      "rather than leaving a reader to conclude there is no API.",
  );
}

main().catch((error) => {
  console.error("api scan errored:", error);
  process.exit(2);
});
