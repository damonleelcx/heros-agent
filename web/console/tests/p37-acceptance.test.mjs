// p37-acceptance.test.mjs — the RENDERED half of §6, over a real console and a real socket (task 6.11).
//
// # Why this exists beside `p37-surfaces.test.mjs`
//
// That file reads source. This one starts the console, signs in, and reads the bytes a browser would
// receive — because a green source assertion and a page that renders nothing are entirely compatible,
// and this console has paid for that lesson three times already.
//
// The properties here are the ones only rendering can settle:
//
//   · a CONNECTED reader sees their own node named, with its own current value;
//   · an UNCONNECTED reader sees `not_connected` and — this is the assertion that matters — the reader's
//     data position contains no sample node on ANY of the seven surfaces;
//   · the subject chosen on one surface is the subject on the next.
//
// # 🔴 What this is NOT
//
// It is not A1. Task 6.11 requires a person, in a browser, on a connected repository, changing their own
// node's memory strategy and reading their own diff — and a green build is explicitly not that. This
// file is the repeatable part; `openspec/changes/p37-source-bound-editors/acceptance.md` records the
// browser session, and neither substitutes for the other.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

let platform;
let console_;
let cookie;

import { WORKFLOW, NODES, connected, disconnected } from "./support/connected.mjs";

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
  cookie = await signIn(console_.base);
}, { timeout: 90_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

async function get(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie }, redirect: "manual" });
  return { status: res.status, html: await res.text() };
}

/** SURFACES is every route P37 rewrote, with the heading that proves it rendered rather than 200'd. */
const SURFACES = [
  ["/app/context", "Context strategy"],
  ["/app/memory", "What this node carries between turns"],
  ["/app/harness", "What this node is allowed to do"],
  ["/app/graph", "The shape of a workflow"],
  ["/app/studio", "Pick a model and a prompt for this node"],
  ["/app/authoring", "Make the change yourself"],
  ["/app/delivery", "How a change reaches your agent"],
];

// ── Connected ───────────────────────────────────────────────────────────────────────────────────

test("6.11 every rewritten surface renders, and NAMES the reader's own node", async () => {
  platform.set(connected);
  for (const [route, heading] of SURFACES) {
    // 🔴 With two candidates and no answer the resolver is `ambiguous` BY DESIGN — the shell asks once.
    // This test is about the surface AFTER that answer, so it carries one, exactly as the reader's next
    // navigation would. The asking itself is the next test.
    const { status, html } = await get(`${route}?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
    assert.equal(status, 200, `${route} did not render`);
    assert.ok(html.includes(heading), `${route} rendered without its heading`);
    assert.ok(html.length > 5000, `${route} rendered ${html.length} bytes — that is a shell, not a surface`);

    // 🔴 FR1 — the node is NAMED. A surface bound to a subject it does not name is a surface that has
    // defaulted the reader into something.
    assert.ok(
      html.includes("handleAnswer") || html.includes("answer"),
      `${route} does not name the node it is bound to`,
    );
    assert.match(html, />editing</, `${route} does not say which node it is editing`);
  }
});

test("6.11 the subject is ASKED ONCE and applies to every surface", async () => {
  platform.set(connected);
  // Two candidates and none chosen: the shell asks, in the shell, once.
  const { html } = await get("/app/context");
  assert.match(
    html,
    /Which node should these surfaces be about\?/,
    "with two candidates and no choice, the shell does not ask",
  );
  assert.match(html, /name="node_id"/, "the question is asked without a real form control");

  // An explicit selection resolves everywhere, through the URL a `finding` would carry (FR18).
  for (const [route] of SURFACES) {
    const picked = await get(`${route}?workflow=${encodeURIComponent(WORKFLOW)}&node=classify`);
    assert.equal(picked.status, 200, `${route} did not render with an explicit subject`);
    assert.ok(picked.html.includes("classify"), `${route} ignored the subject named in its URL`);
  }
});

test("🔴 6.4 not_measured renders with its NAMED missing input, on the axes that have none", async () => {
  platform.set(connected);
  const memory = await get(`/app/memory?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  assert.match(memory.html, /not measured/i, "the memory surface does not draw absence");
  assert.match(memory.html, /not_visible_in_static_ir/, "the missing input is not named on screen");
  assert.match(
    memory.html,
    /store read and written BETWEEN turns/,
    "the sentence saying WHY the input is missing did not render",
  );
  // 🚫 And absence is never drawn as zero or as an empty region.
  assert.doesNotMatch(memory.html, /Memory: 0\b/, "absence rendered as a zero");

  const harness = await get(`/app/harness?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  assert.match(harness.html, /not measured/i, "the harness surface does not draw absence");
  assert.match(harness.html, /not_visible_in_static_ir/, "the harness missing input is not named");
});

test("🔴 6.5 the boundary renders ABOVE the control the reader would use", async () => {
  platform.set(connected);
  const { html } = await get(`/app/memory?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  const boundaryAt = html.indexOf("Where a memory change reaches your source");
  const pickerAt = html.indexOf('type="radio"');
  assert.ok(boundaryAt > 0, "the memory boundary did not render at all");
  assert.ok(pickerAt > 0, "the memory picker did not render");
  assert.ok(
    boundaryAt < pickerAt,
    "the boundary rendered BELOW the picker. A reader who composes a change and only then meets a wall " +
      "has been given a technically honest bait-and-switch.",
  );
});

test("🔴 6.9 an option this call site cannot take renders DISABLED, naming what it needs", async () => {
  platform.set(connected);
  const { html } = await get(`/app/studio?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  assert.match(html, /gpt-4o/, "the foreign-provider model was HIDDEN — a hidden option is one nobody can ask for");
  assert.match(html, /disabled/, "the foreign-provider model is not disabled");
  assert.match(
    html,
    /a call site written against the openai SDK/,
    "the disabled option does not name what it would need",
  );
});

test("6.11 the params form's fields come from the entry's own schema", async () => {
  platform.set(connected);
  const { html } = await get(`/app/memory?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  // The strategy set the PLATFORM served, rendered as a picker — not a list in a TSX file.
  assert.match(html, /scratchpad/, "the served vocabulary did not reach the picker");
  assert.match(html, /No memory/, "the identity strategy is missing from the picker");
  // Its parameter is declared in the schema the registry validates against, so it must be offered.
  assert.match(html, /max_entries|How many notes to retain/, "the schema-derived parameter did not render");
});

test("6.8 one uncovered node among many stays VISIBLE, and is not averaged away", async () => {
  platform.set(connected);
  const { html } = await get(`/app/context?workflow=${encodeURIComponent(WORKFLOW)}&node=answer`);
  // Both nodes appear, and the uncovered one keeps its own state word.
  assert.match(html, /classify/, "the second node vanished from the projection");
  assert.match(html, /not reported/i, "the uncovered node's state was not rendered");
  // 🚫 No aggregate in its place.
  assert.doesNotMatch(html, /\d+% covered/i, "a percentage was rendered in place of the per-node states");
});

// ── Disconnected — 🔴 fence 6.2, the one that matters most ──────────────────────────────────────

test("🔴 6.2 an unconnected reader gets not_connected and NO sample node, on all seven", async () => {
  platform.set(disconnected);
  for (const [route] of SURFACES) {
    const { status, html } = await get(route);

    // A 200 carrying the word, never a 404 — a 404 sends the reader to look for a broken deployment
    // when the truth is that they have not opted in (design D6).
    assert.equal(status, 200, `${route} answered ${status}; not_connected is a business state`);
    assert.match(html, /This axis has nothing of yours to show yet/, `${route} does not render not_connected`);
    assert.match(html, /a connected repository/, `${route} does not NAME the missing input`);
    assert.match(html, /href="\/app\/connections"/, `${route} does not link to the connection flow`);
    assert.match(html, /href="\/docs\/concepts\//, `${route} does not link to the reading surface`);

    // 🔴 THE ASSERTION THIS WHOLE PHASE IS FOR. Not one fixture value in the reader's data position.
    for (const fixture of ["recall", "n_9f1c04ab", "turnThree", "9c4e21f0ab73", "ac_4f19c2ab"]) {
      assert.ok(
        !html.includes(fixture),
        `${route} rendered the fixture "${fixture}" to a reader who has connected nothing. A sample node ` +
          `dressed as theirs is worse than an empty screen, because they cannot tell which one they are ` +
          `looking at.`,
      );
    }
    // And no editor at all: the frame's body is a function of a subject that does not exist.
    assert.ok(!html.includes('type="radio"'), `${route} offered a picker with nothing to bind it to`);
  }
});

// 🔴 The other half of FR4, and the correction P12's own acceptance run forced.
//
// `not_connected` governs the position the reader's OWN NODE would occupy. It does not govern the rest
// of the page. A reader with pull requests and no reported IR structure still HAS pull requests, and a
// change they authored still exists — gating those behind a subject would take a capability away from
// exactly the reader who has least of it.
test("6.2 not_connected empties the AXIS position, and nothing else", async () => {
  platform.set(disconnected);

  const delivery = await get("/app/delivery");
  assert.match(delivery.html, /This axis has nothing of yours to show yet/, "the axis half did not say so");
  assert.match(delivery.html, /Pull requests/, "the reader's own deliveries were hidden behind a subject");
  assert.match(delivery.html, /Routes/, "the route ledger was hidden behind a subject");

  const authoring = await get("/app/authoring");
  assert.match(authoring.html, /This axis has nothing of yours to show yet/, "the axis half did not say so");
  assert.match(
    authoring.html,
    /Changes you have authored/,
    "the reader's own authored changes were hidden behind a subject",
  );
  assert.match(authoring.html, /Applied is not verified/, "the protected claim was hidden with them");
});

test("6.2 not_connected is distinguishable from a read failure", async () => {
  // Three transport treatments plus the business state, and they must not collapse: a reader whose
  // structure could not be READ has not lost anything, and a reader who connected nothing has a
  // different next action.
  platform.set((req, res) => {
    if ((req.url ?? "").startsWith("/api/v1/workflows")) {
      res.writeHead(502, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "upstream is unreachable" }));
      return;
    }
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  const { status, html } = await get("/app/memory");
  assert.equal(status, 200, "a read failure took the page down");
  assert.match(html, /could not be read/i, "a read failure did not say so");
  assert.doesNotMatch(
    html,
    /This axis has nothing of yours to show yet/,
    "a read failure was rendered as not_connected — that tells a reader their structure was never " +
      "received, on a day the platform was merely unreachable",
  );
});
