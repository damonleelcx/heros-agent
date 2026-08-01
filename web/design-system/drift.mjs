// drift.mjs is the two-console drift check for the third-party policy (P24 task 1.7, design D11).
//
// # What can still drift once the builder is shared
//
// Both consoles construct their Content-Security-Policy through the same `csp.ts`, so they agree on
// SHAPE by construction. What they do not agree on by construction is CLASSIFICATION: each console
// passes its own id, and its own prefix→class map lives in `third-party-policy.ts`. A map that called
// the operator console's `/api` public would produce a perfectly well-formed header — correct
// directives, correct order, correct nonce — that names an analytics origin on an operator surface.
// Nothing about the header would look wrong. That is the failure this compares.
//
// # Why it is a function and not two assertions
//
// So it can be run against a MUTATED map and observed failing. A drift check that has only ever been
// run against a tree that does not drift is a drift check nobody knows is connected — which is the
// same argument every other fence in this phase is built on, applied to the one fence whose subject is
// the other fences agreeing with each other.
//
// # Its shape follows the existing web-drift checks
//
// One checker, invoked from both sides, reporting the RULE and BOTH VALUES rather than "these differ".
// A drift report that does not name what disagreed sends the reader to diff two files by eye, which is
// the activity the check exists to replace.

/**
 * driftFindings compares the two consoles' derived policy for every shared prefix.
 *
 * @param {object} input
 * @param {Record<string, {prefix: string, surface: string}[]>} input.prefixes  per-console prefix maps
 * @param {Record<string, {thirdPartyOrigins: string, categories: readonly string[]}>} input.rules
 * @param {readonly string[]} input.sharedPrefixes
 * @returns {string[]} one human-readable finding per disagreement; empty means they agree
 */
export function driftFindings({ prefixes, rules, sharedPrefixes }) {
  const findings = [];
  const consoles = Object.keys(prefixes);
  if (consoles.length < 2) {
    return ["the drift check has fewer than two consoles to compare — it is asserting nothing"];
  }
  if (sharedPrefixes.length === 0) {
    return ["no shared prefix is declared — the drift check compares nothing"];
  }

  for (const prefix of sharedPrefixes) {
    const derived = consoles.map((consoleId) => {
      const surface = resolve(prefixes[consoleId], `${prefix}/drift-probe`);
      return { consoleId, surface, rule: rules[surface] };
    });

    const [first, ...rest] = derived;
    for (const other of rest) {
      if (first.rule.thirdPartyOrigins !== other.rule.thirdPartyOrigins) {
        findings.push(
          `${prefix}: third-party rule differs — ${first.consoleId} says ` +
            `"${first.rule.thirdPartyOrigins}" (class ${first.surface}), ${other.consoleId} says ` +
            `"${other.rule.thirdPartyOrigins}" (class ${other.surface})`,
        );
      }
      const a = [...first.rule.categories].sort().join(",");
      const b = [...other.rule.categories].sort().join(",");
      if (a !== b) {
        findings.push(
          `${prefix}: permitted consent categories differ — ${first.consoleId} permits [${a}] ` +
            `(class ${first.surface}), ${other.consoleId} permits [${b}] (class ${other.surface})`,
        );
      }
    }

    // A shared prefix that either console treats as third-party-bearing is a finding on its own, not
    // only when they disagree: `/api` is a BFF on both sides, and a BFF is never a marketing surface.
    for (const { consoleId, surface, rule } of derived) {
      if (rule.thirdPartyOrigins !== "none") {
        findings.push(
          `${prefix}: ${consoleId} treats a shared data prefix as "${rule.thirdPartyOrigins}" ` +
            `(class ${surface}) — a BFF prefix carries tenant identifiers in its paths`,
        );
      }
    }
  }
  return findings;
}

/** resolve is `csp.ts`'s prefix resolution, over an arbitrary map so a mutated one can be tested. */
function resolve(policies, pathname) {
  for (const policy of policies) {
    if (policy.prefix === "/") return policy.surface;
    if (pathname === policy.prefix || pathname.startsWith(`${policy.prefix}/`)) return policy.surface;
  }
  throw new Error(`no prefix policy governs ${pathname}`);
}
