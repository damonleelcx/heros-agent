/* heros-arc.js — the predictive arch that sits behind the page.
 *
 * WHAT IT IS. A lattice of square pixels over an arch-shaped field of light. The field is the sum of
 * two things: a broad ridge that follows the arch itself, and a handful of bright cores that travel
 * along it, each stretched along the arch's tangent so it reads as something moving rather than a
 * blob parked on a curve. Every pixel's colour and alpha come from the field sampled at its centre.
 *
 * 🔴 WRITTEN HERE, not lifted. The reference damon supplied (threeui.com "Predictive Arc") is a paid
 * component and publishes no source. This is our own implementation of the same idea, which also
 * means the colours are ours to move — and they have to be, because the theme moves them.
 *
 * 🔴 HOW IT DRAWS, and why it is not one fillRect per dot. A viewport-sized lattice is 20–30k cells;
 * setting fillStyle and filling a rect for each one is tens of thousands of state changes a frame and
 * it does not hold 60fps on a laptop. Instead the field is written into an ImageData ONE PIXEL PER
 * CELL, blown up with image smoothing off so each pixel becomes a solid block, and then a repeating
 * dot pattern is composited with `destination-in` to punch the gaps between dots. Three draw calls a
 * frame, whatever the lattice size.
 *
 * 🔴 EVERYTHING IS IN DEVICE PIXELS. The lattice pitch and the dot are integers, so the squares land
 * on pixel boundaries and stay crisp; scaling the context by devicePixelRatio instead puts the dot
 * pattern on fractional coordinates and it renders soft and unevenly spaced at dpr 1.5 and 2.5.
 *
 * Colours come from CSS custom properties on :root — see heros.css — so the palette has ONE home and
 * the theme switch moves this with everything else.
 */
(function () {
  'use strict';

  /* Well under Chrome's own limits (65535 per side, ~2^28 px of area): the arch never legitimately
     needs more than a viewport at dpr 2, so anything approaching these is a fault, not a big screen. */
  var MAX_SIDE = 8192, MAX_PIXELS = 24000000;

  function warn(message) {
    if (window.console && console.warn) console.warn('heros-arc: ' + message);
  }

  var reduceMQ = window.matchMedia ? window.matchMedia('(prefers-reduced-motion: reduce)') : null;
  var instances = [];

  /* ── field parameters ─────────────────────────────────────────────────────
   * Named rather than inlined so the shape can be read off in one place. All lengths are fractions
   * of the canvas, so the arch is the same arch at any size instead of a different one per window.
   */
  var P = {
    pitch: 7,          // css px between dot centres
    dot: 3,            // css px of the dot itself; the rest is gap
    peak: 0.26,        // height of the arch's crown, as a fraction of the canvas
    halfWidth: 0.60,   // the arch's half-span; > 0.5 so its feet leave the canvas at the sides
    drop: 0.82,        // how far the arch falls from crown to foot
    ridgeSigma: 0.115, // thickness of the haze that follows the arch
    ridgeGain: 0.44,
    ridgeTaper: 0.30,  // how much dimmer the feet are than the crown
    coreLong: 0.10,    // travelling core, along the tangent (fraction of width)
    coreCross: 0.055,  // …and across it (fraction of height)
    span: 0.95         // cores travel over u ∈ [-span, span]
  };

  /* Five cores, each with its own start, speed, and breathing rate. Two run backwards so the arch
     never reads as a single conveyor belt. Fixed rather than random: a background that is different
     on every load cannot be compared against a screenshot when something looks wrong. */
  var CORES = [
    { u: -0.86, v:  0.052, w: 0.9, p: 0.0 },
    { u: -0.30, v:  0.038, w: 1.3, p: 1.7 },
    { u:  0.12, v: -0.031, w: 0.7, p: 3.1 },
    { u:  0.58, v:  0.045, w: 1.1, p: 4.4 },
    { u:  0.91, v: -0.058, w: 1.5, p: 5.6 }
  ];

  function readPalette() {
    var s = getComputedStyle(document.documentElement);
    function rgb(name, fallback) {
      var raw = s.getPropertyValue(name).trim();
      var n = raw.split(/[\s,]+/).map(Number).filter(function (x) { return !isNaN(x); });
      return n.length === 3 ? n : fallback;
    }
    var alpha = parseFloat(s.getPropertyValue('--arc-alpha'));
    return {
      dim: rgb('--arc-dim-rgb', [38, 190, 140]),
      core: rgb('--arc-core-rgb', [214, 255, 236]),
      alpha: isNaN(alpha) ? 1 : alpha
    };
  }

  /* 🔴 THE STYLESHEET IS A HARD DEPENDENCY, AND IT USED TO BE AN UNCHECKED ONE.
   *
   * A canvas's width/height ATTRIBUTES are also its intrinsic size, so they decide layout whenever
   * CSS does not. measure() reads getBoundingClientRect() and writes those attributes — which is
   * correct only while `.arc-bg` pins the box. With the rule missing the write feeds the next read
   * and the canvas DOUBLES every frame: 1200x600, 2400x1200, … 38400x19200. Chrome abandons a canvas
   * that large and draws a broken-image placeholder in an enormous in-flow box, which is the whole
   * page gone. Measured live on eval on 2026-09-02, where a browser was holding a pre-arch heros.css
   * from cache (59 rules, no .arc-bg) — but a 404 on the stylesheet, a CSP rule, or any load-order
   * hiccup does exactly the same thing.
   *
   * `.arc-bg` is absolute or fixed in every rule we ship, so `static` means our CSS is not applied.
   * There is then no correct way to draw this and no reason to try: it is decoration. Hide it and say
   * so once, rather than returning silently — a background that is missing with no explanation is the
   * kind of thing nobody can diagnose from a screenshot.
   */
  function stylesheetIsApplied(canvas) {
    return getComputedStyle(canvas).position !== 'static';
  }

  function Arc(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.grid = document.createElement('canvas');
    this.gctx = this.grid.getContext('2d');
    this.pal = readPalette();
    this.started = performance.now();
    this.lastFrame = 0;
    this.faults = 0;
    this.rafId = 0;
    this.fallback = 0;
    this.stopped = false;
    this.measure();
  }

  Arc.prototype.measure = function () {
    var r = this.canvas.getBoundingClientRect();
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var w = Math.max(1, Math.round(r.width * dpr));
    var h = Math.max(1, Math.round(r.height * dpr));
    /* 🔴 An independent ceiling on the backing store, deliberately NOT relying on the check above.
     * This is the guard that holds if some future path inflates the box for a reason nobody has
     * thought of yet: a canvas is capped by area as well as by side, and past the cap the browser
     * does not clip or scale — it discards the canvas and renders a broken image. Refusing one
     * oversized frame costs a background; not refusing costs the page. */
    if (w * h > MAX_PIXELS || w > MAX_SIDE || h > MAX_SIDE) {
      warn('refusing a ' + w + 'x' + h + ' backing store (cap ' + MAX_PIXELS + ' px): ' +
           'the canvas is being sized from its own output. The arch is off on this page.');
      this.stopped = true;
      this.canvas.style.display = 'none';
      return false;
    }

    if (w === this.w && h === this.h) return false;

    this.w = w; this.h = h;
    this.canvas.width = w; this.canvas.height = h;

    this.pitch = Math.max(3, Math.round(P.pitch * dpr));
    this.dotPx = Math.max(1, Math.min(this.pitch - 1, Math.round(P.dot * dpr)));
    this.cols = Math.ceil(w / this.pitch);
    this.rows = Math.ceil(h / this.pitch);

    this.grid.width = this.cols;
    this.grid.height = this.rows;
    this.img = this.gctx.createImageData(this.cols, this.rows);
    this.field = new Float32Array(this.cols * this.rows);

    /* The mask that turns solid blocks into a lattice of dots. One period square, dot centred. */
    var m = document.createElement('canvas');
    m.width = m.height = this.pitch;
    var mc = m.getContext('2d');
    mc.fillStyle = '#000';
    var off = Math.floor((this.pitch - this.dotPx) / 2);
    mc.fillRect(off, off, this.dotPx, this.dotPx);
    this.mask = this.ctx.createPattern(m, 'repeat');

    this.ctx.imageSmoothingEnabled = false;
    return true;
  };

  /* One frame's field, then one frame's pixels. */
  Arc.prototype.draw = function (now) {
    var w = this.w, h = this.h, cols = this.cols, rows = this.rows, pitch = this.pitch;
    var t = (now - this.started) / 1000;
    var field = this.field;

    var cx = w * 0.5, peak = h * P.peak, hw = w * P.halfWidth, drop = h * P.drop;
    var sigma = h * P.ridgeSigma, invSigma2 = 1 / (sigma * sigma);

    /* Pass 1 — the ridge. A Lorentzian rather than a gaussian: the fat tails are what make the arch
       a haze the page sits inside, instead of a hard stripe with nothing around it. */
    var i, j, k, x, y, u, yArc, m, inv, dp, base;
    for (i = 0; i < cols; i++) {
      x = i * pitch + pitch * 0.5;
      u = (x - cx) / hw;
      yArc = peak + drop * u * u;
      m = 2 * drop * u / hw;
      inv = 1 / Math.sqrt(1 + m * m);
      base = P.ridgeGain * (1 - P.ridgeTaper * u * u);
      for (j = 0; j < rows; j++) {
        y = j * pitch + pitch * 0.5;
        dp = (y - yArc) * inv;
        field[j * cols + i] = base / (1 + dp * dp * invSigma2);
      }
    }

    /* Pass 2 — the travelling cores. Each one only touches the cells inside its own bounding box,
       which is why the count of cores costs almost nothing next to the size of the lattice. */
    var aL = w * P.coreLong, aP = h * P.coreCross;
    var invL2 = 1 / (aL * aL), invP2 = 1 / (aP * aP);
    for (k = 0; k < CORES.length; k++) {
      var c = CORES[k];
      var cu = c.u + t * c.v;
      cu = ((cu + P.span) % (2 * P.span) + 2 * P.span) % (2 * P.span) - P.span;
      var bx = cx + cu * hw, by = peak + drop * cu * cu;
      var slope = 2 * drop * cu / hw, tn = 1 / Math.sqrt(1 + slope * slope);
      var tx = tn, ty = slope * tn;
      var amp = 0.72 + 0.38 * Math.sin(t * c.w + c.p);

      var exX = Math.sqrt(aL * aL * tx * tx + aP * aP * ty * ty);
      var exY = Math.sqrt(aL * aL * ty * ty + aP * aP * tx * tx);
      var i0 = Math.max(0, Math.floor((bx - exX) / pitch)), i1 = Math.min(cols - 1, Math.ceil((bx + exX) / pitch));
      var j0 = Math.max(0, Math.floor((by - exY) / pitch)), j1 = Math.min(rows - 1, Math.ceil((by + exY) / pitch));

      for (i = i0; i <= i1; i++) {
        var ddx = i * pitch + pitch * 0.5 - bx;
        for (j = j0; j <= j1; j++) {
          var ddy = j * pitch + pitch * 0.5 - by;
          var along = ddx * tx + ddy * ty;
          var perp = -ddx * ty + ddy * tx;
          var q = along * along * invL2 + perp * perp * invP2;
          if (q >= 1) continue;
          var g = (1 - q); g *= g;
          field[j * cols + i] += amp * g;
        }
      }
    }

    /* Pass 3 — colour. Alpha carries how much light (or, on paper, how much ink) is here; the hue
       walks from the dim end of the ramp to the core as the field rises. */
    var data = this.img.data;
    var d0 = this.pal.dim[0], d1 = this.pal.dim[1], d2 = this.pal.dim[2];
    var c0 = this.pal.core[0], c1 = this.pal.core[1], c2 = this.pal.core[2];
    var ga = this.pal.alpha;
    for (k = 0; k < field.length; k++) {
      var I = field[k];
      var o = k << 2;
      if (I < 0.01) { data[o + 3] = 0; continue; }
      var a = I * 1.15; if (a > 1) a = 1;
      var mix = I > 1 ? 1 : I * I * 0.75 + I * 0.25;
      data[o]     = d0 + (c0 - d0) * mix;
      data[o + 1] = d1 + (c1 - d1) * mix;
      data[o + 2] = d2 + (c2 - d2) * mix;
      data[o + 3] = a * ga * 255;
    }
    this.gctx.putImageData(this.img, 0, 0);

    var ctx = this.ctx;
    ctx.globalCompositeOperation = 'source-over';
    ctx.clearRect(0, 0, w, h);
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(this.grid, 0, 0, cols, rows, 0, 0, cols * pitch, rows * pitch);
    ctx.globalCompositeOperation = 'destination-in';
    ctx.fillStyle = this.mask;
    ctx.fillRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'source-over';
  };

  /* ── the clock ────────────────────────────────────────────────────────────
   * 🔴 The loop is re-armed BEFORE the frame is drawn, never after. With `raf(frame)` as the last
   * statement of the frame body, a single exception anywhere in the draw ends the animation forever
   * — and it ends it by holding a good-looking still image, which is indistinguishable from a
   * finished render. Faults are counted instead, and only a persistent fault stops it.
   *
   * 🔴 rAF is not the only clock. It is not serviced in a background tab, and it can come back
   * un-rearmed from bfcache or an app switch. A one-second watchdog notices a VISIBLE canvas that
   * has not drawn and drives frames from setTimeout until a real rAF callback lands again.
   */
  Arc.prototype.frame = function (now, fromRAF) {
    if (fromRAF && this.fallback) { clearTimeout(this.fallback); this.fallback = 0; }
    this.rafId = 0;
    this.arm();
    this.lastFrame = now;
    try {
      this.measure();
      this.draw(now);
      this.faults = 0;
    } catch (e) {
      if (++this.faults >= 12) { this.stopped = true; }
    }
  };

  Arc.prototype.arm = function () {
    if (this.stopped || this.rafId) return;
    if (reduceMQ && reduceMQ.matches) return;   // one still frame, and nothing after it
    var self = this;
    this.rafId = requestAnimationFrame(function (t) { self.frame(t, true); });
  };

  Arc.prototype.kick = function () {
    if (this.stopped) return;
    if (reduceMQ && reduceMQ.matches) { this.frame(performance.now(), false); return; }
    this.arm();
  };

  Arc.prototype.watchdog = function () {
    if (this.stopped || document.visibilityState !== 'visible') return;
    if (reduceMQ && reduceMQ.matches) return;
    if (performance.now() - this.lastFrame < 1000) return;
    var self = this;
    (function loop() {
      if (self.stopped || document.visibilityState !== 'visible') { self.fallback = 0; return; }
      self.frame(performance.now(), false);
      self.fallback = setTimeout(loop, 33);
    })();
  };

  Arc.prototype.repaint = function () {
    this.pal = readPalette();
    /* A theme change must land even when nothing is animating — reduced motion, or a tab that has
       just been switched to. Draw it here rather than waiting for a frame that may not come. */
    try { this.draw(performance.now()); } catch (e) { /* the next frame will try again */ }
  };

  function boot() {
    var nodes = document.querySelectorAll('canvas[data-heros-arc]');
    for (var i = 0; i < nodes.length; i++) {
      if (!stylesheetIsApplied(nodes[i])) {
        nodes[i].style.display = 'none';
        warn('heros.css is not applied to this canvas (its position computes to static), so the ' +
             'arch cannot be sized and is off on this page. A stale cached stylesheet does this.');
        continue;
      }
      var a = new Arc(nodes[i]);
      instances.push(a);
      a.frame(performance.now(), false);
      a.kick();
    }
    if (!instances.length) return;

    function each(fn) { for (var i = 0; i < instances.length; i++) fn(instances[i]); }

    window.addEventListener('resize', function () { each(function (a) { a.measure(); a.kick(); }); });
    window.addEventListener('pageshow', function () { each(function (a) { a.kick(); }); });
    window.addEventListener('focus', function () { each(function (a) { a.kick(); }); });
    document.addEventListener('visibilitychange', function () { each(function (a) { a.kick(); }); });
    document.addEventListener('heros:themechange', function () { each(function (a) { a.repaint(); }); });
    setInterval(function () { each(function (a) { a.watchdog(); }); }, 1000);

    if (reduceMQ) {
      var onMotion = function () { each(function (a) { a.kick(); }); };
      if (reduceMQ.addEventListener) reduceMQ.addEventListener('change', onMotion);
      else if (reduceMQ.addListener) reduceMQ.addListener(onMotion);
    }
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
