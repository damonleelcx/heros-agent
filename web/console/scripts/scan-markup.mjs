// scan-markup.mjs bans raw-markup rendering (R7, FR24, task 5.7).
//
// # The defect
//
// `p25monitor.html` builds table rows by string concatenation — `'<td>' + n.node_id + '</td>'` — and
// the page has **no escaping helper defined at all**; the other four pages each define one, this one
// was missed. `node_id` is derived from customer source code. The three pages that do escape are also
// inconsistent: two escape five characters, two escape four.
//
// React's default escaping removes the whole class of defect rather than fixing one instance — but
// only if the port never reaches for `dangerouslySetInnerHTML`. This is the fence that makes "never"
// mean never.
//
// # Why an allowlist rather than a flat ban
//
// A flat ban is the right default and a lie as a policy: the day something genuinely needs it — a
// pre-sanitised SVG sprite, say — a flat ban gets deleted rather than amended, and the fence goes with
// it. So the allowlist exists, it is EMPTY today, and adding to it is a reviewed decision with a
// written reason rather than a line somebody slipped past.
//
// Every entry must carry a `reason`. An allowlist without reasons is a list of things nobody
// remembers agreeing to.

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";

const ROOT = process.cwd();

/**
 * ALLOWLIST is empty, deliberately.
 *
 * Format: { file: "src/…/thing.tsx", reason: "why raw markup is correct here and what sanitises it" }
 */
const ALLOWLIST = [];

const BANNED = [
  [/dangerouslySetInnerHTML/, "dangerouslySetInnerHTML"],
  [/\.innerHTML\s*=/, "assignment to innerHTML"],
  [/\.outerHTML\s*=/, "assignment to outerHTML"],
  [/insertAdjacentHTML\s*\(/, "insertAdjacentHTML"],
  [/document\.write\s*\(/, "document.write"],
];

async function* walk(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(full);
    else if (/\.(tsx|ts)$/.test(entry.name)) yield full;
  }
}

function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

async function main() {
  const findings = [];
  let scanned = 0;

  for await (const file of walk(join(ROOT, "src"))) {
    scanned += 1;
    const rel = relative(ROOT, file);
    const allowed = ALLOWLIST.find((entry) => entry.file === rel);
    const source = stripComments(await readFile(file, "utf8"));
    for (const [pattern, name] of BANNED) {
      if (!pattern.test(source)) continue;
      if (allowed) {
        console.log(`markup scan: ${rel} uses ${name} under an explicit allowlist entry — ${allowed.reason}`);
        continue;
      }
      findings.push(`${rel}: uses ${name} — platform values are rendered as TEXT, never as markup`);
    }
  }

  if (scanned === 0) {
    console.error("markup scan: no source files found — is the working directory the console root?");
    process.exit(2);
  }

  if (findings.length > 0) {
    console.error(`markup scan FAILED — ${findings.length} finding(s):`);
    for (const f of findings) console.error(`  - ${f}`);
    console.error(
      "\nEvery value the console renders comes from a customer's source code, prompts or diffs.\n" +
        "React escapes by default; keep it that way, or add a reviewed entry to ALLOWLIST with a reason.",
    );
    process.exit(1);
  }

  console.log(`markup scan passed: ${scanned} file(s), no raw-markup rendering.`);
}

main().catch((error) => {
  console.error("markup scan errored:", error);
  process.exit(2);
});
