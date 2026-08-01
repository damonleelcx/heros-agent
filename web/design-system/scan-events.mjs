// scan-events.mjs is the BUILD-TIME fence on ANALYTICS EVENT NAMES (P24 task 5.6).
//
// # Why an event name needs a build gate
//
// Two reasons, and the second is the one that matters here.
//
// The ordinary one: every analytics implementation drifts the same way — `signup_start`,
// `signup-started`, `startSignup`, all three live, none comparable, and the funnel that was supposed to
// answer a question answers three questions badly.
//
// The one that matters: **an invented name is a free-text field on the far side of a boundary.**
// `track(\`install_step_${channel}\`)` is one plausible line of code and an exfiltration path — exactly
// the shape of `fmt.Errorf("failed to resolve prompt %q", p)` on the error side, and refused for the
// same reason. TypeScript catches a bad LITERAL; it does not catch a template literal, and a template
// literal is what an engineer reaches for when they want the name to vary.
//
// So this reads the call sites and requires every argument to be a quoted member of the enum. A
// template literal, a variable or a concatenation fails, naming the file and the expression.
//
//   node scan-events.mjs                 # the gate
//   node scan-events.mjs --self-test     # prove it goes red

import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import process from "node:process";
import { ANALYTICS_EVENTS } from "./analytics-events.ts";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB = join(HERE, "..");

/** The trees a `track(...)` call may appear in. */
const ROOTS = ["console/src", "admin-console/src", "design-system"];

/**
 * CALL matches `track("name"` and `track(\`name\`` and `track(name`.
 *
 * Deliberately loose on the ARGUMENT so the failure is informative: a tight pattern that only matched
 * valid calls would make an invalid one invisible, which is the opposite of a fence.
 */
const CALL = /\btrack\(\s*([^),]+)/g;

async function* walk(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.name === "node_modules" || entry.name === ".next") continue;
    // This file itself. Its self-test carries the three INVALID shapes on purpose, and a scanner that
    // flagged its own fixtures would be the "cries wolf on the prose that describes the rule" failure
    // this repository has already learned twice — the fix for which is never to loosen the pattern.
    if (entry.name === "scan-events.mjs") continue;
    if (entry.isDirectory()) yield* walk(full);
    else if (/\.(ts|tsx|mjs)$/.test(entry.name)) yield full;
  }
}

function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

/** findings inspects one file's `track(...)` call sites. */
export function findings(relative, source) {
  const out = [];
  const code = stripComments(source);
  for (const match of code.matchAll(CALL)) {
    const argument = match[1].trim();
    // The one legitimate shape: a double- or single-quoted member of the enum.
    const literal = /^["']([a-z0-9_]+)["']$/.exec(argument);
    if (literal && ANALYTICS_EVENTS.includes(literal[1])) continue;
    if (literal) {
      out.push({ file: relative, expression: argument, why: `"${literal[1]}" is not a member of the event enum` });
      continue;
    }
    // A parameter declaration inside the tracker itself is not a call site.
    if (/^event\s*:/.test(argument) || argument === "event") continue;
    out.push({
      file: relative,
      expression: argument,
      why: "an event name must be a quoted literal from the enum — a template literal or a variable can carry free text",
    });
  }
  return out;
}

function selfTest() {
  const bad = findings("<self-test>", "track(`install_step_${channel}`);\ntrack(planName);\ntrack(\"made_up\");");
  if (bad.length !== 3) {
    console.error(`event scan SELF-TEST FAILED: caught ${bad.length} of 3 invalid call sites`);
    process.exit(1);
  }
  const good = findings("<self-test>", `track("${ANALYTICS_EVENTS[0]}");\n// track("commented_out_and_invalid")\n`);
  if (good.length !== 0) {
    console.error(`event scan SELF-TEST FAILED: flagged a valid call or a comment: ${JSON.stringify(good)}`);
    process.exit(1);
  }
  console.log("event scan self-test passed: a template literal, a variable and an unknown name are caught; a valid call and a comment are not.");
}

async function main() {
  if (process.argv.includes("--self-test")) {
    selfTest();
    return;
  }
  const all = [];
  let scanned = 0;
  for (const root of ROOTS) {
    for await (const file of walk(join(WEB, root))) {
      scanned += 1;
      all.push(...findings(file.slice(WEB.length + 1), await readFile(file, "utf8")));
    }
  }
  if (all.length > 0) {
    console.error(`event scan FAILED — ${all.length} call site(s):`);
    for (const f of all) console.error(`  - ${f.file}: track(${f.expression}) — ${f.why}`);
    console.error(
      `\nEvent names come from the enum in web/design-system/analytics-events.ts. It is closed because an` +
        `\ninvented name is a free-text field on the far side of a boundary.\n` +
        `Members: ${ANALYTICS_EVENTS.join(", ")}`,
    );
    process.exit(1);
  }
  console.log(
    `event scan passed: ${scanned} file(s), every analytics event name is one of the ` +
      `${ANALYTICS_EVENTS.length} in the central enum.`,
  );
}

main().catch((error) => {
  console.error("event scan errored:", error);
  process.exit(2);
});
