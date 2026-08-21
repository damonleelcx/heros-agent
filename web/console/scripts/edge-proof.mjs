// edge-proof.mjs asserts that a conversation stream arrives INCREMENTALLY through the real edge
// (P31 task 5.2).
//
// # Why this cannot be a unit test, and why the unit test is not enough
//
// `internal/api/conversation_edge_test.go` proves the PLATFORM writes incrementally: it timestamps the
// handler's own writes and fails if they all land at once. That is the half we own.
//
// The half we do not own is every hop between that handler and a browser — Caddy, Traefik, an ALB, the
// operator's nginx on an air-gapped install. A reverse proxy that buffers turns streaming into BATCHING,
// and **the failure is silent**: the stream still completes, nothing errors, the status is 200, every
// message arrives at the end in one burst. There is no status code for it and no log line. At the
// application layer it is indistinguishable from a slow platform.
//
// So the setting in each manifest is a REQUEST to one hop, and this is the ASSERTION about all of them.
//
// # What it measures
//
// It opens the stream, records the arrival time of each SSE frame, and fails if every frame shares ONE
// arrival moment. 🔴 The assertion is on TIMING and never on the bytes: a buffering proxy delivers exactly
// the same body as a correct one, so any check over content passes while the product is broken.
//
//   CONSOLE_URL=https://heros-agent.space \
//   CONSOLE_COOKIE='heros_session=…' \
//   CONVERSATION_ID=conv_… \
//     node scripts/edge-proof.mjs
//
// The cookie is a signed-in session's, taken from a browser. This measures the path a person actually
// takes — console origin, console BFF, platform — rather than a direct call to the platform, because the
// hops this exists to catch are the ones in front of the console.

import process from "node:process";

/**
 * MIN_DISTINCT_ARRIVALS is the real signal, and choosing it over elapsed time was a correction.
 *
 * The first version of this check asserted that the first and last frame were at least 250ms apart. That
 * is wrong, and a live run showed why: against a fast turn the whole conversation completed in 43ms, so a
 * PERFECTLY STREAMING edge failed the check. Absolute spread measures how long the WORK took; it says
 * nothing about the proxy.
 *
 * What a buffering hop actually does is release every byte at once — so all N frames share ONE arrival
 * time, whatever the turn's duration. Counting distinct arrival moments measures exactly that and nothing
 * else: two or more means bytes were released as they were produced; one means they were held.
 *
 * 🔴 The lesson is worth keeping beside the constant: a fence tuned to the durations of the runs somebody
 * happened to test is a fence that fails on a fast deployment and passes on a slow broken one.
 */
const MIN_DISTINCT_ARRIVALS = 2;

/** MIN_FRAMES is how many frames must arrive before a verdict is possible. */
const MIN_FRAMES = 3;

/** DEADLINE_MS bounds the whole measurement. A stream that never ends must not hang a release check. */
const DEADLINE_MS = 60_000;

function fail(message) {
  console.error(`edge-proof FAILED: ${message}`);
  process.exit(1);
}

const base = process.env.CONSOLE_URL;
const cookie = process.env.CONSOLE_COOKIE;
const conversationId = process.env.CONVERSATION_ID;

if (!base || !cookie || !conversationId) {
  // 🔴 It REFUSES rather than skipping. A gate that skips silently when its precondition is missing is a
  // gate that reports success for a run that measured nothing — the failure mode every fence in this
  // repository is written against.
  console.error(
    "edge-proof: needs CONSOLE_URL, CONSOLE_COOKIE and CONVERSATION_ID.\n" +
      "\n" +
      "  CONSOLE_URL       the console's public origin — the edge under test\n" +
      "  CONSOLE_COOKIE    a signed-in session cookie, copied from a browser\n" +
      "  CONVERSATION_ID   a conversation with a turn in flight, or one whose transcript replays\n" +
      "\n" +
      "It refuses rather than skipping: a check that passes when it measured nothing is worse than no check.",
  );
  process.exit(2);
}

const url = `${base.replace(/\/$/, "")}/api/stream/conversations?conversation_id=${encodeURIComponent(conversationId)}&after=0`;
const started = Date.now();
const arrivals = [];

const controller = new AbortController();
const deadline = setTimeout(() => controller.abort(), DEADLINE_MS);

let response;
try {
  response = await fetch(url, {
    headers: { accept: "text/event-stream", cookie },
    signal: controller.signal,
  });
} catch (cause) {
  fail(`the stream could not be opened: ${cause}`);
}

if (!response.ok) {
  fail(`the stream answered ${response.status}: ${(await response.text()).slice(0, 300)}`);
}

// 🔴 The header check is not the proof. It records that the response ASKED not to be buffered; the
// timing below is what says whether anything listened.
const buffering = response.headers.get("x-accel-buffering");
if (buffering !== "no") {
  console.warn(
    `edge-proof: the response carries x-accel-buffering=${buffering ?? "(absent)"}. ` +
      "Some hop is rewriting or dropping it — the timing assertion below is what decides.",
  );
}

const reader = response.body.getReader();
const decoder = new TextDecoder();
let buffer = "";
try {
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    // Every complete frame counts once, at the moment its chunk arrived.
    while (buffer.includes("\n\n")) {
      const at = buffer.indexOf("\n\n");
      const block = buffer.slice(0, at);
      buffer = buffer.slice(at + 2);
      if (block.startsWith(":")) continue; // a keep-alive comment is not a message
      arrivals.push(Date.now() - started);
    }
    if (arrivals.length >= 12) break; // enough to judge; do not hold the connection for a whole turn
  }
} catch (cause) {
  if (!controller.signal.aborted) fail(`reading the stream: ${cause}`);
} finally {
  clearTimeout(deadline);
  controller.abort();
}

if (arrivals.length < MIN_FRAMES) {
  fail(
    `only ${arrivals.length} frame(s) arrived in ${Date.now() - started}ms. ` +
      "Point CONVERSATION_ID at a conversation with a turn in flight — a transcript of one message " +
      "cannot distinguish streaming from batching.",
  );
}

const spread = arrivals[arrivals.length - 1] - arrivals[0];
const distinct = new Set(arrivals).size;
console.log(
  `edge-proof: ${arrivals.length} frames in ${distinct} distinct arrival(s), at ${arrivals.join("ms, ")}ms ` +
    `(spread ${spread}ms)`,
);

if (distinct < MIN_DISTINCT_ARRIVALS) {
  fail(
    `all ${arrivals.length} frames arrived in ONE chunk, ${spread}ms apart end to end.\n` +
      "That is BATCHING, not streaming: some hop between this process and the platform is buffering the " +
      "response and releasing it at the end. The bytes are identical to a working stream, the status is " +
      "200, and nothing logs — which is why this check measures time rather than content.\n" +
      "\n" +
      "  Caddy     `flush_interval -1` on the stream handler (deploy/scripts/bootstrap-vm.sh)\n" +
      "  Traefik   no `buffering` middleware on the router (deploy/k8s/overlays/prod/ingress.yaml)\n" +
      "  nginx     `proxy_buffering off;`\n" +
      "  HAProxy   `option http-no-delay`, no `http-buffer-response` on this backend",
  );
}

console.log(
  `edge-proof PASSED: ${arrivals.length} frames released in ${distinct} separate arrivals — ` +
    "the edge is forwarding this stream as it is produced.",
);
