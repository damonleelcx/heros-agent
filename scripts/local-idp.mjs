/**
 * A real, standalone OpenID Connect provider on localhost — so the FEDERATED sign-in flow can be walked
 * by hand without an Okta tenant.
 *
 * Real discovery, a real JWKS, real PKCE verification, a real RS256-signed ID token, single-use codes.
 * Nothing about the console's return leg is stubbed: the issuer check, the audience check, the nonce
 * binding and the signature verification are the ones it runs in production.
 *
 * # Why this is self-contained rather than a proxy in front of the test stub
 *
 * The first version wrapped `web/console/tests/support/idp.mjs` to add a consent screen, rewriting the
 * discovery document to advertise the wrapper. The console refused the result — correctly — with
 * *"ID token issuer is not the configured issuer"*: `iss` lives INSIDE the signed token, so a proxy can
 * rewrite what it advertises and can never rewrite what it signs. The two would have to disagree, and
 * the console is right not to accept that. Serving it directly is the only shape where they agree.
 *
 * # The consent screen
 *
 * A real IdP shows its own login page at `/authorize`. The test stub accepts immediately, which is right
 * for a test and useless for a person: the point of walking this by hand is to SEE the browser leave for
 * another origin, choose an identity there, and come back.
 *
 *   node scripts/local-idp.mjs
 */
import { createServer } from "node:http";
import { createHash, createSign, generateKeyPairSync, randomUUID } from "node:crypto";

const CLIENT_ID = process.env.LOCAL_IDP_CLIENT_ID ?? "heros-console-local";
const SUBJECT = process.env.LOCAL_IDP_SUBJECT ?? "dana-northwind";
const EMAIL = process.env.LOCAL_IDP_EMAIL ?? "dana@northwind.test";

const { publicKey, privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const jwk = { ...publicKey.export({ format: "jwk" }), kid: "local-1", use: "sig", alg: "RS256" };

/** issued maps an authorization code to the nonce and PKCE challenge it was minted against. */
const issued = new Map();

const b64 = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
function mint(payload) {
  const signing = `${b64({ alg: "RS256", typ: "JWT", kid: jwk.kid })}.${b64(payload)}`;
  const sig = createSign("RSA-SHA256").update(signing).end().sign(privateKey).toString("base64url");
  return `${signing}.${sig}`;
}
const json = (res, body, status = 200) => {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
};
const readBody = (req) =>
  new Promise((r) => { let b = ""; req.on("data", (c) => (b += c)); req.on("end", () => r(b)); });

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  const base = `http://127.0.0.1:${server.address().port}`;

  if (url.pathname === "/.well-known/openid-configuration") {
    return json(res, {
      issuer: base,
      authorization_endpoint: `${base}/authorize`,
      token_endpoint: `${base}/token`,
      jwks_uri: `${base}/jwks`,
      response_types_supported: ["code"],
      id_token_signing_alg_values_supported: ["RS256"],
      code_challenge_methods_supported: ["S256"],
    });
  }
  if (url.pathname === "/jwks") return json(res, { keys: [jwk] });

  if (url.pathname === "/authorize") {
    // The consent screen. A real provider asks for a password and a second factor here; the console
    // never sees any of it, which is the property being demonstrated.
    if (url.searchParams.get("approved") !== "1") {
      const onward = new URL(base + "/authorize" + url.search);
      onward.searchParams.set("approved", "1");
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      return res.end(`<!doctype html><meta charset="utf-8"><title>Sign in — Northwind Identity</title>
<style>
 body{font:16px/1.6 system-ui;background:#0b0f14;color:#e6edf3;display:grid;place-items:center;min-height:100vh;margin:0}
 .card{max-width:32rem;padding:2.25rem;border:1px solid #1e2b3a;border-radius:14px;background:#0f141b}
 .who{font-family:ui-monospace,monospace;color:#7ee2b8}
 a.btn{display:block;margin-top:1.75rem;padding:.85rem;text-align:center;background:#2ee6a8;color:#04140d;
       border-radius:9px;text-decoration:none;font-weight:600}
 small{color:#7d8b99} h2{margin:.2rem 0 1rem}
</style>
<div class=card>
  <p><small>NORTHWIND IDENTITY · a different origin from the console</small></p>
  <h2>Sign in to Heros</h2>
  <p>Continuing signs you in as <span class=who>${EMAIL}</span>.</p>
  <p><small>This is where your own provider — Okta, Entra, Google — asks for a password and a second
  factor. Heros never sees either. What returns to the console is a signed assertion it verifies against
  the keys published here.</small></p>
  <a class=btn href="${onward.pathname}${onward.search}">Continue as ${EMAIL}</a>
</div>`);
    }
    const code = randomUUID();
    issued.set(code, {
      nonce: url.searchParams.get("nonce"),
      challenge: url.searchParams.get("code_challenge"),
    });
    const back = new URL(url.searchParams.get("redirect_uri"));
    back.searchParams.set("code", code);
    back.searchParams.set("state", url.searchParams.get("state") ?? "");
    res.writeHead(303, { location: back.toString() });
    return res.end();
  }

  if (url.pathname === "/token") {
    const body = new URLSearchParams(await readBody(req));
    const record = issued.get(body.get("code"));
    if (!record) return json(res, { error: "invalid_grant" }, 400);
    // Single-use, exactly as a real provider: a replayed code buys nothing.
    issued.delete(body.get("code"));
    const verifier = body.get("code_verifier") ?? "";
    const challenge = createHash("sha256").update(verifier).digest("base64url");
    if (!verifier || challenge !== record.challenge) return json(res, { error: "invalid_grant" }, 400);

    const now = Math.floor(Date.now() / 1000);
    return json(res, {
      token_type: "Bearer",
      id_token: mint({
        iss: base, aud: CLIENT_ID, sub: SUBJECT, nonce: record.nonce,
        jti: randomUUID(), iat: now, exp: now + 300,
        email: EMAIL, email_verified: true,
      }),
    });
  }

  res.writeHead(404).end();
});

await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
console.log(JSON.stringify({ issuer: base, client_id: CLIENT_ID, email: EMAIL }));
console.log(`\nOIDC provider on ${base}`);
console.log(`  discovery : ${base}/.well-known/openid-configuration`);
console.log(`  signs in as: ${EMAIL}`);
console.log(`\nleave this running; ctrl-c to stop\n`);
