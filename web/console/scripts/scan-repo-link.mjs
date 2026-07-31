// scan-repo-link.mjs fences the repository link's TARGET (task 7.2).
//
// # The rule, and why it is the same rule as the install command
//
// A link to a private or non-existent repository fails the build, under the same rule that forbids an
// install command that 404s. The failure is worse than a dead link: the marketing page says the project
// is open, and clicking through proves it is not. A reader does not conclude "broken link" — they
// conclude the claim was untrue.
//
// # What it checks, and what it records
//
// It resolves the repository named in `src/content/repository.ts` against the forge and requires
// `private: false`. The outcome is written to `src/generated/repo-check.json` with its date, so:
//
//   - an air-gapped or offline build has a prior answer to stand on rather than failing on a firewall
//   - the answer's AGE is visible in review, instead of being an invisible assumption
//
// A build with no network AND no prior answer fails. That combination means nobody has ever checked, and
// passing there would make the fence decorative on exactly the first build that needed it.
//
// # The star count is deliberately not fetched for display
//
// The count is recorded here because we are already asking, and it is free. It is **not** rendered:
// `SHOW_STAR_COUNT` is off (task 7.5, escalated and answered), the repository has 0 stars, and "★ 0" on
// a marketing page is worse than nothing. When it is turned on, this measurement — captured at build
// time on one machine we control, stamped with its date — is what would be rendered. Never a reader's
// browser, which the CSP refuses anyway.
//
// # What it deliberately does not check
//
//   - Whether the repository has a README, a licence or any content. It checks that a reader who clicks
//     arrives somewhere they can read.
//   - Whether other external links resolve. `scan-links` allow-lists origins without making network
//     requests, deliberately, so the build works air-gapped.

import { readFile, writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";

const ROOT = process.cwd();
const OUT = join(ROOT, "src", "generated", "repo-check.json");

async function main() {
  const source = await readFile(join(ROOT, "src", "content", "repository.ts"), "utf8");
  const owner = /owner:\s*"([^"]+)"/.exec(source)?.[1];
  const name = /name:\s*"([^"]+)"/.exec(source)?.[1];
  const url = /url:\s*"([^"]+)"/.exec(source)?.[1];
  if (!owner || !name || !url) {
    console.error("repo link scan FAILED — src/content/repository.ts does not declare owner, name and url.");
    process.exit(1);
  }
  if (url !== `https://github.com/${owner}/${name}`) {
    console.error(
      `repo link scan FAILED — the rendered url ${url} does not match the declared repository ${owner}/${name}. ` +
        `The link a reader clicks and the repository this fence checks must be the same one.`,
    );
    process.exit(1);
  }

  let prior = null;
  try {
    prior = JSON.parse(await readFile(OUT, "utf8"));
  } catch {
    prior = null;
  }

  let result = null;
  try {
    const res = await fetch(`https://api.github.com/repos/${owner}/${name}`, {
      headers: { accept: "application/vnd.github+json", "user-agent": "heros-console-repo-fence" },
      signal: AbortSignal.timeout(15_000),
    });
    if (res.status === 404) {
      console.error(
        `repo link scan FAILED — ${owner}/${name} does not exist or is not public. The public header and ` +
          `footer link to it, and a link that 404s says the project is open and proves otherwise.`,
      );
      process.exit(1);
    }
    if (res.ok) {
      const body = await res.json();
      if (body.private) {
        console.error(
          `repo link scan FAILED — ${owner}/${name} is PRIVATE. Make it public, or remove the link; ` +
            `there is no third option that is honest.`,
        );
        process.exit(1);
      }
      result = {
        repository: `${owner}/${name}`,
        public: true,
        stars: body.stargazers_count ?? 0,
        measured_on: new Date().toISOString().slice(0, 10),
      };
    }
  } catch {
    result = null;
  }

  if (!result) {
    if (!prior) {
      console.error(
        `repo link scan FAILED — the repository could not be reached and there is no prior check to stand on. ` +
          `Nobody has ever verified that ${owner}/${name} is public, and passing here would make this fence ` +
          `decorative on the one build that needed it.`,
      );
      process.exit(1);
    }
    console.log(
      `repo link scan: offline — standing on the check from ${prior.measured_on} ` +
        `(${prior.repository} was public, ${prior.stars} star(s)).`,
    );
    return;
  }

  await mkdir(join(ROOT, "src", "generated"), { recursive: true });
  await writeFile(OUT, `${JSON.stringify(result, null, 2)}\n`, "utf8");
  console.log(
    `repo link scan passed: ${result.repository} is public (${result.stars} star(s), measured ${result.measured_on}). ` +
      `The count is recorded, not rendered — see src/content/repository.ts.`,
  );
}

main().catch((error) => {
  console.error("repo link scan errored:", error);
  process.exit(2);
});
