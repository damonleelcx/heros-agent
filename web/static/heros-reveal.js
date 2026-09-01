/* heros-reveal.js — the show/hide control on a password field.
 *
 * 🔴 ITS OWN FILE, and that is not tidiness. It began inside heros-auth.js, which the console would
 * then have had to load to get this control — and heros-auth.js defines a global `say(el, text, kind)`
 * while the console defines its own `say(t, html)` with a different signature. A deferred script runs
 * AFTER inline scripts, so loading it in the console would have silently replaced the console's `say`
 * and broken every message it renders. Nothing would have thrown; the console would just have started
 * drawing wrong.
 *
 * So the shared control lives alone, with names nothing else uses, and all three pages load it.
 */
/* ── password reveal ─────────────────────────────────────────────────────────
 *
 * Progressive enhancement: the markup ships a plain <input type="password">, and this wraps it and
 * adds the toggle. With JavaScript unavailable the field is exactly what it was — a working password
 * box — rather than a broken control.
 *
 * 🔴 type="button". A <button> inside a <form> defaults to type="submit", so without this, revealing
 * your password submits the form — which on the sign-in page spends a rate-limit token and an argon2id
 * hash on an attempt nobody meant to make.
 *
 * 🔴 The state is announced, not just drawn. aria-pressed carries it and the label says what the
 * button will DO next; an icon alone tells a screen-reader user nothing about whether their password is
 * currently on screen, which is the one thing they cannot check by looking.
 */
const EYE_SHOW =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">' +
  '<path d="M2 12s3.6-6.5 10-6.5S22 12 22 12s-3.6 6.5-10 6.5S2 12 2 12Z" stroke-linecap="round" stroke-linejoin="round"/>' +
  '<circle cx="12" cy="12" r="2.6"/></svg>';
const EYE_HIDE =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">' +
  '<path d="M2 12s3.6-6.5 10-6.5c2 0 3.7.6 5.1 1.4M22 12s-3.6 6.5-10 6.5c-2 0-3.7-.6-5.1-1.4" stroke-linecap="round" stroke-linejoin="round"/>' +
  '<path d="M4 4l16 16" stroke-linecap="round"/></svg>';

function addPasswordReveal(input){
  if (!input || input.dataset.revealWired) return;
  input.dataset.revealWired = '1';

  const wrap = document.createElement('span');
  wrap.className = 'field-wrap';
  input.parentNode.insertBefore(wrap, input);
  wrap.appendChild(input);

  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'reveal';
  const paint = shown => {
    btn.innerHTML = shown ? EYE_HIDE : EYE_SHOW;
    btn.setAttribute('aria-pressed', shown ? 'true' : 'false');
    btn.setAttribute('aria-label', shown ? 'Hide password' : 'Show password');
    btn.title = shown ? 'Hide password' : 'Show password';
  };
  paint(false);

  btn.addEventListener('click', () => {
    const shown = input.type === 'text';
    input.type = shown ? 'password' : 'text';
    paint(!shown);
    /* Focus returns to the field with the caret where it was. Without this the caret jumps to the end,
       so revealing a password mid-edit moves your cursor — which is worst precisely when you are
       checking a character you just typed in the middle. */
    const at = input.selectionStart, to = input.selectionEnd;
    input.focus();
    try { input.setSelectionRange(at, to); } catch (_) { /* not all types allow it */ }
  });

  wrap.appendChild(btn);
}

/* Wire every password box on the page, including any added later by a re-render. */
function wirePasswordReveals(root){
  (root || document).querySelectorAll('input[type="password"]').forEach(addPasswordReveal);
}
document.addEventListener('DOMContentLoaded', () => wirePasswordReveals());
