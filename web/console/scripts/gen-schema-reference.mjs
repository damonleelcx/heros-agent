// gen-schema-reference.mjs generates the schema and metric reference from the shipped artifacts
// (tasks 4.3, 4.9's input · Decision 6).
//
// It writes three pages, and the third is the interesting one:
//
//   reference/schemas.md    from `schemas/*.schema.json` — the JSON Schemas that ship
//   reference/metrics.md    from the metric catalogue in `internal/telemetry` — name, unit, computation
//                           AND the site that computes it
//   reference/http-api.md   the ABSENT tier, with the reason
//
// # Why an absent tier gets a generated page rather than no page
//
// A reader looking for the HTTP API reference and finding nothing concludes one of two things: that they
// failed to find it, or that the product has no API. The first wastes their time; the second is wrong.
// A page that says "this tier is absent, here is exactly why, and here is what to use instead" costs one
// paragraph and answers the question.
//
// It is generated rather than written because the STATUS is a fact about the repository, and the day an
// OpenAPI document exists this page must stop saying it does not. A hand-written absence outlives its
// own truth — which is the same drift the tier's absence exists to avoid.
//
// # What this deliberately does not check
//
//   - Whether the JSON Schemas are correct, or that anything validates against them. `make schema` does
//     that; this transcribes their identity and their top-level shape.
//   - Whether a metric's stated computation MATCHES the code at the cited site. It carries the citation
//     so a reader can check; the catalogue's own test asserts the citation points somewhere real, not
//     that the sentence describes what is there. That gap is named on the page.

import { readdir, readFile, writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documentedVersion } from "./lib/docs-version.mjs";

const ROOT = process.cwd();
const SCHEMA_DIR = join(ROOT, "..", "..", "schemas");
const OUT_DIR = join(ROOT, "content", "docs", "en", "reference");

function table(head, rows) {
  return [`| ${head.join(" | ")} |`, `|${head.map(() => "---").join("|")}|`, ...rows.map((r) => `| ${r.join(" | ")} |`)].join("\n");
}

const cell = (value) => String(value).replace(/\|/g, "\\|");

function frontMatter({ title, summary, version, boundary, order }) {
  return [
    "---",
    `title: ${title}`,
    "tier: reference",
    `summary: ${summary}`,
    `platform_version: ${version}`,
    `boundary: ${boundary}`,
    "generated: true",
    `order: ${order}`,
    "---",
    "",
  ];
}

async function schemaPage(version) {
  let files = [];
  try {
    files = (await readdir(SCHEMA_DIR)).filter((name) => name.endsWith(".schema.json")).sort();
  } catch {
    files = [];
  }

  const out = frontMatter({
    title: "Schema reference",
    summary: "The JSON Schemas that ship with the platform, and what each one is the contract for.",
    version,
    boundary:
      "These are the schemas in the repository. A document that validates against one is well-formed; whether it is CORRECT for your workflow is not something a schema can say.",
    order: 2,
  });

  out.push("This page is **generated** from `schemas/` on every build.");
  out.push("");

  if (files.length === 0) {
    out.push("No JSON Schemas were found in the repository at build time. This page will list them when there are any.");
    out.push("");
  } else {
    const rows = [];
    for (const file of files) {
      const parsed = JSON.parse(await readFile(join(SCHEMA_DIR, file), "utf8"));
      const required = Array.isArray(parsed.required) ? parsed.required.join(", ") : "—";
      rows.push([`\`${file}\``, cell(parsed.title ?? "—"), cell(parsed.type ?? "—"), cell(required || "—")]);
    }
    out.push(table(["File", "Title", "Type", "Required at the top level"], rows));
    out.push("");

    for (const file of files) {
      const parsed = JSON.parse(await readFile(join(SCHEMA_DIR, file), "utf8"));
      out.push(`## ${file.replace(/\.schema\.json$/, "")}`);
      out.push("");
      if (parsed.description) {
        out.push(cell(parsed.description));
        out.push("");
      }
      const properties = parsed.properties ?? {};
      const names = Object.keys(properties).sort();
      if (names.length > 0) {
        const required = new Set(Array.isArray(parsed.required) ? parsed.required : []);
        out.push(
          table(
            ["Field", "Type", "Required", "Meaning"],
            names.map((name) => [
              `\`${name}\``,
              cell(properties[name].type ?? "—"),
              required.has(name) ? "yes" : "no",
              cell((properties[name].description ?? "—").split(". ")[0]),
            ]),
          ),
        );
        out.push("");
      }
    }
  }

  await writeFile(join(OUT_DIR, "schemas.md"), `${out.join("\n")}\n`, "utf8");
  return files.length;
}

async function metricPage(version, facts) {
  const out = frontMatter({
    title: "Metric reference",
    summary: "Every metric the platform emits, with its unit, its exact computation, and the code that computes it.",
    version,
    boundary:
      "It states what each number measures and where it is computed. It does not tell you what a good value is — that depends on your workflow, and a benchmark stated here would be a number about somebody else's system.",
    order: 3,
  });

  out.push("This page is **generated** from the metric catalogue in `internal/telemetry` on every build.");
  out.push("");
  out.push(
    'A metric name is not a definition. "Latency" can mean total wall time, time to first token, or time excluding retries — three different numbers — so every row below states the **computation** and **cites the site that performs it**. If you are comparing one of these figures against your own measurement, the computation column is the one that matters.',
  );
  out.push("");

  const scopes = [
    ["call", "Per provider call", "Emitted once for every call to a model provider."],
    ["run", "Per run", "Emitted once per run window. These carry a reserved `node_id` sentinel, because concurrency across a run is not attributable to one node."],
  ];

  for (const [scope, heading, blurb] of scopes) {
    const rows = facts.metrics.filter((m) => m.scope === scope);
    if (rows.length === 0) continue;
    out.push(`## ${heading}`);
    out.push("");
    out.push(blurb);
    out.push("");
    out.push(
      table(
        ["Metric", "Unit", "Computation", "Computed in"],
        rows.map((m) => [`\`${m.name}\``, `\`${m.unit}\``, cell(m.computation), `\`${cell(m.computed_in)}\``]),
      ),
    );
    out.push("");
  }

  out.push("## What is not measured, and why that is stated");
  out.push("");
  out.push(
    "Two absences are deliberate rather than pending. **A model with no pricing entry emits no cost at all**, rather than a zero — a zero cost is a claim, and an absent one is a gap you can see. **A non-streaming call emits no time-to-first-token**, because there is no first token to time. In both cases the metric is missing rather than wrong, which is the only version of the choice that survives being aggregated.",
  );
  out.push("");
  out.push(
    "The build checks that every metric named on any documentation page exists with this name, this unit and this computation. It cannot check that the sentence in the computation column still describes the code at the cited site — that is a review responsibility, and the citation is there so it is a cheap one.",
  );
  out.push("");

  await writeFile(join(OUT_DIR, "metrics.md"), `${out.join("\n")}\n`, "utf8");
  return facts.metrics.length;
}

async function apiPage(version, facts) {
  const out = frontMatter({
    title: "HTTP API reference",
    summary: "This tier is absent, and this page says why rather than leaving you to conclude there is no API.",
    version,
    boundary:
      "There is no HTTP API reference. This page documents that absence and its reason; it is not a partial reference and it does not list endpoints.",
    order: 4,
  });

  out.push("## Status: absent");
  out.push("");
  out.push(cell(facts.api_reference.reason));
  out.push("");
  out.push("## Why you are reading this instead of an endpoint list");
  out.push("");
  out.push(
    "Reference documentation is **generated from a shipped artifact**, or it is not published. The CLI reference is generated from the command registry; the schema reference from the JSON Schemas; the metric reference from the metric catalogue. Each of those has an artifact a fence can check documentation against.",
  );
  out.push("");
  out.push(
    "The HTTP API has no such artifact. A hand-written endpoint list would be a **copy of the truth that begins drifting the day it is written**, and — worse — it would defeat the fence, because a fence can only compare a page against an artifact. A wrong endpoint list that passes a check is more dangerous than no endpoint list, because a reader trusts it.",
  );
  out.push("");
  out.push(
    "So this tier is marked absent and the API fence **refuses** any documented endpoint, method or field anywhere in this documentation rather than passing vacuously. That refusal is the honest behaviour: the fence says \"I cannot check this\", instead of saying nothing and being mistaken for approval.",
  );
  out.push("");
  out.push("## What to use instead, today");
  out.push("");
  out.push(
    "The `heros` CLI is the supported programmatic surface, and it **is** fully documented — see the [CLI reference](/docs/reference/cli). It covers discovery, applying a change, evaluation and linking a run, and its exit codes are a public contract your pipeline can branch on.",
  );
  out.push("");
  out.push(
    "When an OpenAPI document ships, this page is replaced by a generated reference and the fence starts checking pages against it. Until then, nothing here describes an endpoint.",
  );
  out.push("");

  await writeFile(join(OUT_DIR, "http-api.md"), `${out.join("\n")}\n`, "utf8");
}

async function main() {
  const { version, facts } = await documentedVersion();
  await mkdir(OUT_DIR, { recursive: true });
  const schemas = await schemaPage(version);
  const metrics = await metricPage(version, facts);
  await apiPage(version, facts);
  console.log(
    `schema reference generated: ${schemas} schema(s), ${metrics} metric(s), and the HTTP API tier rendered ABSENT with its reason.`,
  );
}

main().catch((error) => {
  console.error("schema reference generation FAILED:", error.message);
  process.exit(1);
});
