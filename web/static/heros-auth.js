/* Shared by the sign-in and sign-up pages.
 *
 * 🔴 Error text comes from the SERVER, never from here. Every refusal in this product is written to
 * name a next action — "Sign in instead, or use 'Forgotten your password?'" — and a page that
 * substitutes its own "something went wrong" throws that away. The only messages composed on this side
 * are for fields the browser can see are empty before anything is sent.
 */
async function post(path, body){
  try{
    const res = await fetch(path, {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      /* same-origin: the session cookie is HttpOnly and set by the response; nothing here reads it. */
      credentials:'same-origin',
      body:JSON.stringify(body)
    });
    let parsed = {};
    try { parsed = await res.json(); } catch (_) { /* a proxy error page is not JSON */ }
    return {ok:res.ok, status:res.status, body:parsed};
  }catch(err){
    return {ok:false, status:0, body:{error:'Could not reach the server. Check your connection and try again.'}};
  }
}

function say(el, text, kind){
  el.textContent = text;
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

/* submit disables the button for the whole round trip.
 * 🔴 Both sign-in and sign-up cost a password hash on the server, which is deliberately slow. Without
 * this, an impatient second click spends a second hash and — on sign-in — a second attempt from a rate
 * limit the person did not know they were spending. */
async function submit(button, msg, path, body, pending){
  button.disabled = true;
  say(msg, pending, '');
  const r = await post(path, body);
  if (r.ok) {
    say(msg, 'Signed in. Taking you to the console…', 'ok');
    location.href = '/app/';
    return;
  }
  button.disabled = false;
  say(msg, r.body.error || 'That did not work.', 'bad');
}
