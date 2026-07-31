// docs-version.mjs answers ONE question for every generator and every fence: **which platform version
// does this documentation describe?**
//
// # Why it is not simply `cli.ToolVersion`
//
// `internal/cli.ToolVersion` is `0.11.0-dev` in the source tree — a release stamps the real version in
// with `-ldflags` at build time. A reader has not installed the source tree; they have installed a
// published release. Documenting the dev string would state a version that exists on nobody's machine,
// which is a small lie with a large consequence: a reader comparing "documents platform 0.11.0-dev"
// against their own `heros version` output concludes the documentation is for something else.
//
// So the documented version is **the published release's**, and the source tree's dev version is the
// fallback used only when no release exists at all. Which one was used is reported, so a build log says
// what the pages will claim rather than leaving it to be discovered on the page.
//
// # What this deliberately does not do
//
// It does not check that the documentation is CORRECT for that version. It fixes what the pages state
// they document; whether a paragraph is still true after a release is a review responsibility, and no
// version string can carry it.

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";

const ROOT = process.cwd();

export async function documentedVersion() {
  const facts = JSON.parse(await readFile(join(ROOT, "src", "generated", "docs-facts.json"), "utf8"));
  let release = null;
  try {
    release = JSON.parse(await readFile(join(ROOT, "src", "generated", "release-assets.json"), "utf8"));
  } catch {
    release = null;
  }
  if (release?.version) {
    return { version: release.version, source: `published release ${release.release}`, facts, release };
  }
  return {
    version: facts.tool_version,
    source: "the source tree's tool version — no release is published yet",
    facts,
    release,
  };
}
