// demo-embed.test.mjs pins the public home page's demo recording to the ONE property that makes it
// servable at all: it is same-origin, and its bytes really are in the shipped tree.
//
// # Why this needs its own test
//
// `tests/public-surface.test.mjs` already fails any element carrying an external `src`, and that is the
// fence that matters. But it is a fence against a CLASS, and it would stay green the day somebody
// "improved" the demo by moving the file to a bucket and leaving a same-origin `<video>` pointing at
// nothing — a poster that 404s, a play button that does nothing, and a suite that never notices,
// because an element with a local `src` is exactly what that test wants to see.
//
// So this asserts the other half: the page references the recording, and the reference RESOLVES. The
// two together are what "the demo plays" decomposes into on a server-rendered page.
//
// The CSP is asserted here too, in the specific form the embed depends on. The policy in
// `web/design-system/third-party-policy.ts` has no `media-src` and its `CspDirective` union cannot
// express one, so media falls through to `default-src 'self'`. That is not an accident to route around
// later — it is why the file ships in `public/` — and a test that says so is how the next person finds
// that out before rather than after moving it.

import test, { after, before } from "node:test";
import assert from "node:assert/strict";
import { startConsole, startStubPlatform } from "./support/harness.mjs";

const MP4 = "/demo/heros-cli-openclaw.mp4";
const POSTER = "/demo/heros-cli-openclaw-poster.jpg";

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

test("the home page embeds the recording, same-origin, with a poster", async () => {
  const res = await fetch(`${console_.base}/`);
  assert.equal(res.status, 200);
  const html = await res.text();

  const video = /<video\b[^>]*>/.exec(html)?.[0];
  assert.ok(video, "the home page renders no <video> element");
  assert.match(video, new RegExp(`src="${MP4}"`), "the recording is not served from this origin");
  assert.match(video, new RegExp(`poster="${POSTER}"`), "the embed carries no poster");
  // The embed is an ambient loop with no chrome: the control bar covered the terminal output, which
  // is the only thing the panel exists to show. `muted` and `playsInline` are not decoration — a
  // browser refuses to autoplay without them, so dropping either turns the panel into a still frame.
  assert.doesNotMatch(video, /\bcontrols\b/, "the control bar is back, and it covers the terminal output");
  for (const attr of [/\bautoPlay\b|\bautoplay\b/, /\bloop\b/, /\bmuted\b/, /\bplaysInline\b|\bplaysinline\b/]) {
    assert.match(video, attr, `the embed lost ${attr} — without all four it does not play unattended`);
  }
  // The poster still has to be there. It covers the first paint, and it is the whole of what a
  // visitor sees if autoplay is refused (a data-saver mode, or a browser that has decided not to).
  assert.match(video, new RegExp(`poster="${POSTER}"`), "no poster — a refused autoplay shows an empty box");
});

test("both referenced assets are actually served, with the right content types", async () => {
  for (const [path, type] of [
    [MP4, "video/mp4"],
    [POSTER, "image/jpeg"],
  ]) {
    const res = await fetch(`${console_.base}${path}`);
    assert.equal(res.status, 200, `${path} is referenced by the home page and is not served`);
    assert.match(res.headers.get("content-type") ?? "", new RegExp(type));
    assert.ok(
      Number(res.headers.get("content-length")) > 1024,
      `${path} is served but is empty or a stub`,
    );
  }
});

test("🔴 media has no directive of its own, so an off-origin recording would be refused", async () => {
  const res = await fetch(`${console_.base}/`);
  const csp = res.headers.get("content-security-policy") ?? "";
  assert.match(csp, /default-src 'self'/);
  assert.ok(
    !/media-src/.test(csp),
    "a media-src appeared in the header — the embed's same-origin requirement is no longer enforced by default-src",
  );

  // And the vocabulary itself cannot express one, which is what makes the line above durable rather
  // than a snapshot of today's header.
  const policy = await import("../../design-system/third-party-policy.ts");
  for (const origin of policy.ALLOWED_ORIGINS) {
    assert.notEqual(
      origin.directive,
      "media-src",
      `${origin.origin} claims media-src, a directive the builder does not render`,
    );
  }
});
