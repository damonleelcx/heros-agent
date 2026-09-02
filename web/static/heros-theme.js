/* heros-theme.js — the site's one theme switch.
 *
 * 🔴 LOADED SYNCHRONOUSLY from <head>, NOT deferred. A deferred script sets the attribute after the
 * browser has already painted the page in the other theme, and that is the white flash every themed
 * site has to answer for. This file is under 3KB of same-origin script; blocking the first paint on
 * it costs far less than the flash.
 *
 * 🔴 "System" REMOVES the attribute rather than writing the resolved value. The OS preference is
 * answered in CSS — `:root:not([data-theme="dark"])` inside a `prefers-color-scheme:light` block in
 * heros.css — so somebody who never touches the switch gets the right theme with JavaScript
 * disabled entirely, and keeps following the OS when the OS changes mid-session. Writing "light" or
 * "dark" here on the visitor's behalf would freeze whichever answer was true at load and make this
 * file load-bearing for a default that does not need it.
 */
(function () {
  'use strict';

  var KEY = 'heros-theme';
  var MODES = ['system', 'light', 'dark'];
  var root = document.documentElement;

  /* localStorage throws in a Safari private window and in some embedded browsers. A theme switch is
     a convenience; it must never be the reason a page fails to load. */
  function stored() {
    try {
      var v = window.localStorage.getItem(KEY);
      return MODES.indexOf(v) > 0 ? v : 'system';
    } catch (e) { return 'system'; }
  }
  function remember(mode) {
    try {
      if (mode === 'system') window.localStorage.removeItem(KEY);
      else window.localStorage.setItem(KEY, mode);
    } catch (e) { /* the theme still applies for this page; it just will not be remembered */ }
  }

  function apply(mode) {
    if (mode === 'system') root.removeAttribute('data-theme');
    else root.setAttribute('data-theme', mode);
  }

  function resolved() {
    var mode = stored();
    if (mode !== 'system') return mode;
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches
      ? 'light' : 'dark';
  }

  /* First paint. Everything below this line waits for the document. */
  apply(stored());

  function announce() {
    document.dispatchEvent(new CustomEvent('heros:themechange', {
      detail: { mode: stored(), resolved: resolved() }
    }));
  }

  function set(mode) {
    if (MODES.indexOf(mode) < 0) return;
    remember(mode);
    apply(mode);
    paint();
    announce();
  }

  /* Following the OS means following it as it changes, not only as it was at load. */
  if (window.matchMedia) {
    var mq = window.matchMedia('(prefers-color-scheme: light)');
    var onOS = function () { if (stored() === 'system') { paint(); announce(); } };
    if (mq.addEventListener) mq.addEventListener('change', onOS);
    else if (mq.addListener) mq.addListener(onOS);
  }

  window.HerosTheme = {
    get: stored,
    resolved: resolved,
    set: set,
    cycle: function () { set(MODES[(MODES.indexOf(stored()) + 1) % MODES.length]); }
  };

  /* ── the control ──────────────────────────────────────────────────────────
   * Pages mark WHERE it goes with <span data-theme-slot></span> and this builds it, so the markup
   * for the switch exists once rather than once per page. Three states, cycled in one button:
   * a two-state toggle cannot express "follow my device", and silently dropping that state is how a
   * site ends up ignoring the setting the visitor actually made — in their OS.
   */
  var ICONS = {
    system: '<path d="M12 3a9 9 0 1 0 0 18Z" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="9"/>',
    light:  '<circle cx="12" cy="12" r="4.2"/><path d="M12 2.4v2.2M12 19.4v2.2M2.4 12h2.2M19.4 12h2.2M5.2 5.2l1.6 1.6M17.2 17.2l1.6 1.6M18.8 5.2l-1.6 1.6M6.8 17.2l-1.6 1.6"/>',
    dark:   '<path d="M20 13.4A8.4 8.4 0 0 1 10.6 4a8.4 8.4 0 1 0 9.4 9.4Z"/>'
  };
  var LABEL = { system: 'System', light: 'Light', dark: 'Dark' };
  var buttons = [];

  function paint() {
    var mode = stored(), next = MODES[(MODES.indexOf(mode) + 1) % MODES.length];
    for (var i = 0; i < buttons.length; i++) {
      var b = buttons[i];
      b.querySelector('svg').innerHTML = ICONS[mode];
      b.querySelector('.theme-name').textContent = LABEL[mode];
      /* The label names the CURRENT state, so the accessible name has to say what pressing it does —
         otherwise a screen reader hears "Dark" and cannot tell whether that is the state or the
         destination. */
      b.setAttribute('aria-label', 'Theme: ' + LABEL[mode] +
        (mode === 'system' ? ' (following your device)' : '') + '. Switch to ' + LABEL[next] + '.');
      b.setAttribute('title', 'Theme: ' + LABEL[mode] + ' — switch to ' + LABEL[next]);
    }
  }

  function build() {
    var slots = document.querySelectorAll('[data-theme-slot]');
    for (var i = 0; i < slots.length; i++) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'theme-toggle';
      b.innerHTML =
        '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" ' +
        'stroke-width="1.6" stroke-linecap="round" aria-hidden="true"></svg>' +
        '<span class="theme-name"></span>';
      b.addEventListener('click', function () { window.HerosTheme.cycle(); });
      slots[i].appendChild(b);
      buttons.push(b);
    }
    paint();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', build);
  else build();
})();
