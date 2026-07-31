// gen-cli-reference.mjs generates the CLI reference from the `internal/cli` command registry
// (tasks 4.2, 6.1–6.5 · Decision 6, Decision 14).
//
// # Why generated, and why BOTH directions matter
//
// The failure people expect is documentation naming a command that does not exist. The failure that
// actually accumulates is the inverse — a command that exists and is undocumented — because adding a
// subcommand is a normal Tuesday and remembering the reference is not.
//
// Generating from the registry removes the second failure by construction: a new registry entry becomes
// a new section here on the next build. `scan-cli.mjs` then closes the loop in the other direction, by
// refusing any `heros …` invocation anywhere in the corpus that names a command or flag the registry does
// not have.
//
// # 🔴 This file is written, not edited
//
// The output carries `generated: true` and is overwritten on every build. A hand edit survives until the
// next `npm run build` and then vanishes — which is the intended behaviour, and is stated in the page
// itself so nobody spends an afternoon on a paragraph the build is about to delete.
//
// # What this deliberately does not check
//
//   - Whether the registry's summaries are ACCURATE. It transcribes them; it cannot run the command and
//     compare. `TestEveryCommandCarriesARunnableExampleAndASuccessCriterion` asserts each entry has an
//     example and a success criterion, not that the example produces that success.
//   - Whether a flag is actually READ by the command it is listed under. The registry declares that, and
//     `TestRegistryFlagsResolveToTheCatalogue` only checks the flag exists.
//
// Both are named review responsibilities, not machine-checked ones.

import { writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documentedVersion } from "./lib/docs-version.mjs";

const ROOT = process.cwd();
const OUT_DIR = join(ROOT, "content", "docs", "en", "reference");
const OUT = join(OUT_DIR, "cli.md");

const AVAILABILITY = {
  offline: "Runs offline, with no account.",
  "network-no-account": "Needs the network. Needs no account.",
  network: "Needs the network and a platform account.",
};

function table(head, rows) {
  const lines = [`| ${head.join(" | ")} |`, `|${head.map(() => "---").join("|")}|`];
  for (const row of rows) lines.push(`| ${row.join(" | ")} |`);
  return lines.join("\n");
}

/** cell escapes a value for a Markdown table cell — a raw pipe would silently split the row. */
function cell(value) {
  return String(value).replace(/\|/g, "\\|");
}

async function main() {
  const { version, source, facts } = await documentedVersion();

  const out = [];
  out.push("---");
  out.push("title: CLI reference");
  out.push("tier: reference");
  out.push(
    "summary: Every subcommand of the heros binary, its flags, its exit code, and whether it runs offline.",
  );
  out.push(`platform_version: ${version}`);
  out.push(
    "boundary: This reference is generated from the command registry. It describes what the binary accepts; it does not describe what the platform does with a linked run.",
  );
  out.push("generated: true");
  out.push("order: 1");
  out.push("---");
  out.push("");
  out.push(
    "This page is **generated** from the command registry in `internal/cli`, on every build. Editing it by hand has no lasting effect — the next build overwrites it. Change the registry instead, and the reference follows.",
  );
  out.push("");
  out.push(
    `Every command below exists in the binary today. A command in the registry with no entry here fails the build, and an invocation anywhere in this documentation naming a command or flag the registry does not have fails the build too. The reference and the binary cannot drift apart in either direction.`,
  );
  out.push("");

  // ── Exit codes, first ──────────────────────────────────────────────────────
  // Before the commands, because a CI author reads this page for exactly one thing.
  out.push("## Exit codes");
  out.push("");
  out.push(
    "The exit codes are a **public contract**: they are public the moment a customer's pipeline branches on them. Three remedies never share a code.",
  );
  out.push("");
  out.push(
    table(
      ["Code", "Name", "Means", "Your remedy"],
      facts.exit_codes.map((e) => [`\`${e.code}\``, cell(e.name), cell(e.meaning), cell(e.remedy)]),
    ),
  );
  out.push("");
  out.push(
    "The gap between `1` and `2` is the load-bearing one. **`1` is your gate failing** — the tool worked and told you something true about your workflow, and retrying will not change it. **`2` is our tool breaking** — retrying might. Collapsing them into one code is how a CI step that fails for an unclear reason ends up with `|| true` appended to it, after which a real regression is invisible too.",
  );
  out.push("");

  // ── Configuration ──────────────────────────────────────────────────────────
  out.push("## Configuration and which source wins");
  out.push("");
  out.push(
    `Every setting can come from four places. They resolve in this order, highest first: **${facts.config_precedence.join(" > ")}**.`,
  );
  out.push("");
  out.push(
    table(
      ["Source", "How you set it", "Notes"],
      [
        ["flag", "`--repo .`", "Highest. Only a flag you actually pass counts — a flag left at its default does not shadow the others."],
        ["env", `\`${facts.env_prefix}REPO=.\``, `Environment variables are namespaced \`${facts.env_prefix}\`.`],
        ["file", `\`${facts.project_file}\``, "A project file resolved relative to the repository."],
        ["default", "—", "The built-in value. `heros status` names the source that won for every setting."],
      ],
    ),
  );
  out.push("");
  out.push(
    "Run `heros status` to see the resolved value **and its provenance** for every setting, including which sources were overridden. That answers \"why is my config file being ignored\" without guessing.",
  );
  out.push("");
  out.push(
    "The three gate flags — `--min-quality`, `--max-cost-per-run`, `--latency-sla-ms` — deliberately have **no environment equivalent**. A quality gate that a stray environment variable can relax is a gate that silently stops gating, and the symptom is a green build.",
  );
  out.push("");

  // ── Commands ───────────────────────────────────────────────────────────────
  out.push("## Commands");
  out.push("");

  for (const command of facts.commands) {
    out.push(`### ${command.name}`);
    out.push("");
    out.push(cell(command.summary) + ".");
    out.push("");
    out.push(`**${AVAILABILITY[command.availability] ?? command.availability}**`);
    out.push("");
    if (command.prerequisite) {
      /*
       * 🔴 The prerequisite is printed ABOVE the command, not below it.
       *
       * A reader tests the first runnable line they see. Three of these examples exited non-zero when
       * typed cold — found by running the documentation against a real repository, not by any fence —
       * and a prerequisite noted underneath would have been read after the failure rather than before.
       */
      out.push(`**Before this runs:** ${cell(command.prerequisite)}`);
      out.push("");
    }
    out.push("```bash");
    out.push(command.example);
    out.push("```");
    out.push("");
    out.push(`**On success:** ${cell(command.success)} Exit code \`${command.success_exit}\`.`);
    out.push("");
    if (command.unavailable) {
      out.push(
        `**When this build does not include it:** the command exits \`2\` and prints \`${cell(command.unavailable)}\`. That is an operational outcome, not a malformed invocation — you have not typed anything wrong.`,
      );
      out.push("");
    }
    if (command.flags.length > 0) {
      out.push(
        table(
          ["Flag", "Type", "Default", "Environment", "Meaning"],
          command.flags.map((f) => [
            `\`--${f.name}\``,
            f.type,
            f.default === "" ? "*unset*" : `\`${cell(f.default)}\``,
            f.env === "" ? "*none*" : `\`${cell(f.env)}\``,
            cell(f.summary),
          ]),
        ),
      );
      out.push("");
    }
  }

  out.push("## What this reference does not cover");
  out.push("");
  out.push(
    "It describes the **command surface** — what the binary accepts and what it returns. It does not describe the platform's behaviour once a run is linked, and it does not explain when you would use each command; that is what the guides are for.",
  );
  out.push("");
  out.push(
    "It also does not promise that every summary here is a good description. The registry is the source, a test asserts each entry has a runnable example and a stated success criterion, and whether that example is the *right* example is a review judgement no generator can make.",
  );
  out.push("");

  await mkdir(OUT_DIR, { recursive: true });
  await writeFile(OUT, `${out.join("\n")}\n`, "utf8");
  console.log(
    `CLI reference generated: ${facts.commands.length} command(s), ${facts.exit_codes.length} exit code(s), ` +
      `documenting platform ${version} (from ${source}).`,
  );
}

main().catch((error) => {
  console.error("CLI reference generation FAILED:", error.message);
  process.exit(1);
});
