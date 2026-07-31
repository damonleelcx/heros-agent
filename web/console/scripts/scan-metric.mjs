// scan-metric.mjs checks every documented metric against the harness on NAME, UNIT and COMPUTATION, and
// requires it to cite where it is computed (task 4.9).
//
// # Why the unit and the computation are separate checks
//
// A metric name is not a definition. "Latency" can mean total wall time, time to first token, or time
// excluding retries — three different numbers that all pass a name check and all pass a unit check,
// because all three are milliseconds. The one that catches a real disagreement is the computation, and
// the only way to make a computation checkable is to require the page to cite the site that performs it.
//
// The failure this prevents is not a typo. It is a customer comparing our number to their own,
// concluding one of the two systems is wrong, and being unable to find out which.
//
// # What "cites where it is computed" means
//
// A page that names a metric must, somewhere on that page, name the site the catalogue gives for it —
// `internal/telemetry/metrics.go:derive`, for example. Not a link, not a paragraph: the literal path, so
// a reader who doubts the sentence can open the file.
//
// # What it deliberately does not check
//
//   - That the catalogue's own sentence describes the code at the cited site. The catalogue's test
//     asserts the citation points somewhere real, not that the description is faithful. That is a named
//     review responsibility, and the citation exists to make it a cheap one.
//   - Metric values. Nothing here knows what a good latency is, and a benchmark stated in documentation
//     would be a number about somebody else's system.
//   - Metrics mentioned inside a code fence. A sample showing `latency_total_ms` in JSON output is data,
//     not a definition, and requiring a citation beside it would make every example unwritable.

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documents, fencedLines, report } from "./lib/corpus.mjs";

const ROOT = process.cwd();

async function main() {
  const facts = JSON.parse(await readFile(join(ROOT, "src", "generated", "docs-facts.json"), "utf8"));
  const catalogue = new Map(facts.metrics.map((m) => [m.name, m]));
  const findings = [];
  const docs = await documents();
  let mentions = 0;

  // Every metric name in the catalogue is snake_case and distinctive enough to match on directly. A
  // metric-shaped word that is NOT in the catalogue is the more interesting finding: it is either a typo
  // or a number nobody emits.
  const METRIC_SHAPED = /\b(latency|cost|tokens?|throughput|reliability|context|concurrency|revenue|sandbox|tool)_[a-z_]+\b/g;

  for (const document of docs) {
    if (document.kind !== "docs") continue;
    const fenced = fencedLines(document);
    const named = new Set();

    for (const line of document.lines) {
      if (fenced.has(line.number)) continue;
      for (const match of line.text.matchAll(METRIC_SHAPED)) {
        const name = match[0];
        mentions += 1;
        const definition = catalogue.get(name);
        if (!definition) {
          findings.push(
            `${document.path}:${line.number}: names \`${name}\`, which the harness does not emit. ` +
              `The catalogue has: ${[...catalogue.keys()].slice(0, 6).join(", ")}…`,
          );
          continue;
        }
        named.add(name);

        // Unit parity, when the page states one. A page may mention a metric without restating its unit;
        // it may not state a DIFFERENT one.
        const unitClaim = new RegExp(`${name}[^\\n]{0,80}?\\bin (milliseconds|ms|seconds|usd|dollars|tokens|calls|a ratio|count)\\b`, "i");
        const stated = unitClaim.exec(line.text)?.[1]?.toLowerCase();
        if (stated) {
          const normalised = { milliseconds: "ms", seconds: "s", dollars: "usd", "a ratio": "ratio" }[stated] ?? stated;
          if (normalised !== definition.unit) {
            findings.push(
              `${document.path}:${line.number}: says \`${name}\` is in ${stated}; the harness emits it in ` +
                `${definition.unit} (${definition.computed_in}).`,
            );
          }
        }
      }
    }

    // Citation, per page rather than per mention: a reference table cites once for a column.
    for (const name of named) {
      const definition = catalogue.get(name);
      const site = definition.computed_in.split(",")[0].trim();
      if (!document.body.includes(site)) {
        findings.push(
          `${document.path}: defines \`${name}\` without citing where it is computed. ` +
            `Add \`${site}\` — a metric definition a reader cannot check is a sentence they have to trust.`,
        );
      }
    }
  }

  report(
    "metric scan",
    findings,
    docs.length,
    `${mentions} metric mention(s) across ${catalogue.size} catalogued metric(s), each matching the harness on name and unit and citing its computation site.`,
    "The metric catalogue is internal/telemetry/catalog.go. Run `make docs-facts` after changing it.\n" +
      "Every documented metric must match on name and unit, and must cite the site that computes it.",
  );
}

main().catch((error) => {
  console.error("metric scan errored:", error);
  process.exit(2);
});
