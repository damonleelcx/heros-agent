// upload-sourcemaps.mjs makes error frames readable for the PLATFORM'S OWN HOSTED DEPLOYMENT — and for
// nothing else (P24 task 3.6, design D1).
//
// # The problem it solves, and the one it must not create
//
// A minified frame reads `t.a` in `4bd1b696-…js:1`. A source map turns that into a function and a file
// somebody can open, which is most of what makes an error report actionable.
//
// A source map is also a READABLE COPY OF OUR OWN SOURCE. Serving one from a customer-facing origin
// publishes the application to anybody who opens dev tools, and shipping one inside an installable
// package hands it to every customer who installs. So the map has to reach the reporting service
// WITHOUT ever becoming an asset:
//
//   1. build with maps into a scratch tree that is not the shipped one;
//   2. upload them, out of band, with a release-scoped token that exists only in CI;
//   3. delete them;
//   4. assert they are gone before anything downstream can package the tree.
//
// Step 4 is not decoration. A build that uploads and then crashes leaves maps on disk, and the next
// step packages them — which is exactly how "we never ship source maps" becomes false without anybody
// changing their mind about it.
//
// # 🔴 The token, and why this script refuses rather than defaulting
//
// `HEROS_SOURCEMAP_UPLOAD_TOKEN` is a RELEASE-SCOPED credential: it may create a release and attach
// artefacts, and it can do nothing else. It exists in the release pipeline's secret store and appears
// in no image, no manifest and no runtime environment — the running platform never holds it, and
// nothing at runtime has any use for it.
//
// With no token this script does NOTHING and says so. It does not fall back to a broader credential,
// it does not read a file, and it does not skip silently: a release that meant to upload and did not
// must be visible in the log, and a build that never meant to must not print a warning that teaches
// people to ignore warnings.
//
//   HEROS_SOURCEMAP_UPLOAD_TOKEN=… HEROS_VERSION=v0.24.0 node scripts/upload-sourcemaps.mjs
//
// # Status, stated because a written pipeline step that has never run is a claim
//
// 🔴 This has NEVER EXECUTED. There is no hosted deployment of this platform yet and no release-scoped
// token exists, so the upload half is code that is correct by construction and unproven by execution.
// What IS proven is the fence: `scan-bundle.mjs` fails the build on a `.map` in the shipped tree or on
// a `sourceMappingURL` pointing at one, and that has been demonstrated red.

import { readdir, readFile, rm, stat } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";

const TOKEN_ENV = "HEROS_SOURCEMAP_UPLOAD_TOKEN";
const STATIC_DIR = join(process.cwd(), ".next", "static");

/** mapsUnder returns every source map in a tree, so both the upload and the assertion read one list. */
async function mapsUnder(dir) {
  const out = [];
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...(await mapsUnder(full)));
    else if (entry.name.endsWith(".map")) out.push(full);
  }
  return out;
}

async function main() {
  const token = (process.env[TOKEN_ENV] ?? "").trim();
  const release = (process.env.HEROS_VERSION ?? "").trim();

  const maps = await mapsUnder(STATIC_DIR);

  if (!token) {
    // The ordinary case, on every build that is not a hosted release. Absent, and stated once —
    // not warned about, because a warning on every correct build is a warning people stop reading.
    console.log(
      `source maps: ${TOKEN_ENV} is unset, so nothing is uploaded. ` +
        `${maps.length} map(s) in the shipped tree; the bundle scan fails the build if that is not zero.`,
    );
    // Whether or not we uploaded, maps must not remain. A build that produced them and has no token is
    // a build that would otherwise ship them.
    await removeAll(maps);
    return;
  }

  if (!release) {
    console.error(`source maps: ${TOKEN_ENV} is set but HEROS_VERSION is not — a map uploaded against no
release cannot be matched to the build it came from, which makes it worse than no map at all.`);
    process.exit(1);
  }
  if (maps.length === 0) {
    console.error(
      "source maps: an upload was requested but the build produced none.\n" +
        "  Build with `productionBrowserSourceMaps: true` in the release job only, then run this.\n" +
        "  Refusing rather than reporting a successful upload of nothing.",
    );
    process.exit(1);
  }

  console.log(`source maps: uploading ${maps.length} map(s) for release ${release}`);
  let uploaded = 0;
  for (const map of maps) {
    const body = await readFile(map);
    const name = map.slice(map.indexOf(join(".next", "static")));
    const res = await fetch(
      `https://sentry.io/api/0/organizations/heros-agent/releases/${encodeURIComponent(release)}/files/`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/octet-stream", "X-Name": name },
        body,
      },
    );
    if (!res.ok) {
      console.error(`source maps: upload of ${name} failed with ${res.status}`);
      // The maps are removed even on failure. A partial upload is recoverable by re-running the
      // release; a map left on disk is not recoverable at all once the image is built from it.
      await removeAll(maps);
      process.exit(1);
    }
    uploaded += 1;
  }

  await removeAll(maps);
  console.log(`source maps: uploaded ${uploaded} map(s) and removed every one from the shipped tree`);
}

/** removeAll deletes the maps and ASSERTS they are gone. */
async function removeAll(maps) {
  for (const map of maps) await rm(map, { force: true });
  const remaining = [];
  for (const map of maps) {
    try {
      await stat(map);
      remaining.push(map);
    } catch {
      // gone, which is the expected outcome
    }
  }
  if (remaining.length > 0) {
    console.error(
      `source maps: ${remaining.length} map(s) could not be removed:\n  ${remaining.join("\n  ")}\n` +
        "Refusing to leave a readable copy of the application in a tree something downstream will package.",
    );
    process.exit(1);
  }
}

main().catch((error) => {
  console.error("source maps: errored:", error);
  process.exit(1);
});
