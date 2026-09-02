// arc_runaway_test.js — does heros-arc.js survive its own stylesheet going missing?
//
// 🔴 Run by TestTheArchCannotSizeItselfFromItsOwnOutput in cmd/herosd. This is a BEHAVIOURAL test,
// not a grep: it reproduces the exact condition that took eval down on 2026-09-02 — a canvas whose
// layout size comes from its own width/height attributes because `.arc-bg` is not applied — and
// asserts the module refuses instead of doubling.
//
// The DOM here is a stub, deliberately minimal. It only has to model the one property that caused
// the bug: for a canvas with no CSS box, getBoundingClientRect() reports the ATTRIBUTE size, so
// anything that writes those attributes from that measurement feeds itself.

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const src = fs.readFileSync(path.join(__dirname, '..', 'heros-arc.js'), 'utf8');
const failures = [];

function makeCanvas(position) {
  const el = {
    tagName: 'CANVAS',
    width: 300, height: 150,          // a canvas's intrinsic default
    style: {},
    _position: position,
    getBoundingClientRect() {
      // The bug in one line: with no CSS box, layout follows the attributes.
      return { width: this.width, height: this.height, top: 0, left: 0 };
    },
    getContext() { throw new Error('getContext must not be reached when the arch refuses to mount'); },
    addEventListener() {},
  };
  return el;
}

function run(position, label) {
  const canvas = makeCanvas(position);
  const warnings = [];
  const listeners = {};
  const sandbox = {
    console: { warn: (m) => warnings.push(String(m)), log() {}, error() {} },
    performance: { now: () => 0 },
    setTimeout: () => 0, clearTimeout() {}, setInterval: () => 0,
    requestAnimationFrame: () => 0,
    document: {
      readyState: 'complete',
      documentElement: {},
      querySelectorAll: (sel) => (sel.indexOf('data-heros-arc') >= 0 ? [canvas] : []),
      addEventListener(type, fn) { listeners[type] = fn; },
      createElement: () => ({ getContext: () => ({}), width: 0, height: 0 }),
    },
    getComputedStyle: (el) => ({
      position: el._position,
      getPropertyValue: () => '',
    }),
  };
  sandbox.window = sandbox;
  sandbox.window.devicePixelRatio = 2;
  sandbox.window.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
  sandbox.window.addEventListener = () => {};

  vm.createContext(sandbox);
  vm.runInContext(src, sandbox, { filename: 'heros-arc.js' });

  return { canvas, warnings, label };
}

// ── the case that broke production ───────────────────────────────────────────────────────────────
const stale = run('static', 'stylesheet missing');
if (stale.canvas.width !== 300 || stale.canvas.height !== 150) {
  failures.push(`stylesheet missing: the canvas was resized to ${stale.canvas.width}x${stale.canvas.height}. ` +
    `It must be left alone — sizing it from its own rect is what doubles it every frame until the ` +
    `browser discards it and draws a broken image over the page.`);
}
if (stale.canvas.style.display !== 'none') {
  failures.push(`stylesheet missing: the canvas is still displayed (display=${JSON.stringify(stale.canvas.style.display)}). ` +
    `Unstyled it is an in-flow 300x150 box in the middle of the page.`);
}
if (!stale.warnings.some((w) => /not applied|static/.test(w))) {
  failures.push(`stylesheet missing: nothing was logged. A background that vanishes with no ` +
    `explanation cannot be diagnosed from a screenshot. Got: ${JSON.stringify(stale.warnings)}`);
}

if (failures.length) {
  console.error(failures.map((f) => '  - ' + f).join('\n'));
  process.exit(1);
}
console.log('ok - the arch refuses to mount without its stylesheet, and says so');
