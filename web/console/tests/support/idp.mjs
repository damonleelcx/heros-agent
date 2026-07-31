// idp.mjs starts a REAL identity provider over a real socket — one that speaks OIDC discovery/JWKS
// and one that signs SAML assertions — so the console's verifiers are exercised as they ship.
//
// # Why a real IdP and not a stubbed verifier
//
// The properties under test are cryptographic and protocol-shaped: an ID token signed by a key
// published at a JWKS URI the discovery document named; a SAML assertion whose digest is over the
// exclusive-canonical form of the element it covers. None of those is exercised by a fake that returns
// `{ ok: true }` — and a fake written to match our own verifier is the specific failure this
// repository has already paid for once, on P21: a provider double built to agree with the code hid
// five shipped defects behind a green suite. So this signs with real keys, over real bytes, and every
// negative case below is produced by MUTATING a genuinely valid message rather than by hand-writing an
// invalid one.
//
// # Why the SAML signer uses the console's own canonicalizer
//
// It has to compute the digest an IdP would compute, and computing it a second way here would test
// our canonicalizer against itself. There is no third implementation available in-process, so the
// honest position is stated rather than hidden: `xml.ts` is checked against the W3C exclusive-c14n
// specification example in `sso-identity.test.mjs`, INDEPENDENTLY of any round trip. That test is what
// makes this signer's agreement meaningful; without it, a matched pair of wrong implementations would
// pass every case here.

import { createServer } from "node:http";
import { once } from "node:events";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash, createSign, generateKeyPairSync, randomUUID } from "node:crypto";
import { inflateRawSync } from "node:zlib";
// The console's OWN canonicalizer, imported rather than reimplemented — see the module comment in
// `src/lib/idp/xml.ts` for why a second implementation here would be a matched pair rather than a
// check, and which independent test makes this import trustworthy.
import { parseXml, canonicalize, descendants, attrValue } from "../../src/lib/idp/xml.ts";

const b64url = (value) => Buffer.from(JSON.stringify(value)).toString("base64url");

/**
 * startStubOidc starts an OIDC provider: discovery, JWKS, authorization and token endpoints.
 *
 * `control` lets a test bend one thing at a time — a wrong `nonce`, a stale `iat`, `alg: none`, a
 * refused token exchange — while everything else stays genuinely correct. That is what makes a
 * negative result attributable.
 */
export async function startStubOidc(options = {}) {
  const { publicKey, privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const jwk = { ...publicKey.export({ format: "jwk" }), kid: "stub-1", use: "sig", alg: "RS256" };

  const control = {
    /** Claims merged into every ID token. */
    claims: { email: "person@acme.test", email_verified: true, ...(options.claims ?? {}) },
    /** Overrides `nonce` with a fixed value, to exercise the binding check. */
    nonce: null,
    /** `iat`/`exp` offsets in seconds, to exercise the freshness bound. */
    iatOffset: 0,
    expOffset: 300,
    /** Signs with `alg: none`, to exercise the algorithm allowlist. */
    algNone: false,
    /** Refuses the token exchange, to exercise the reused-code path. */
    refuseToken: false,
    /** Reuses one `jti`, to exercise the one-time guard. */
    fixedJti: null,
    /** The authorization codes this provider has issued, and their pending nonces. */
    issued: new Map(),
    /** Every request the console made, for assertions about what it sent. */
    requests: [],
  };

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, `http://${req.headers.host}`);
    control.requests.push({ method: req.method, path: url.pathname, query: url.searchParams });
    const base = `http://127.0.0.1:${server.address().port}`;

    if (url.pathname === "/.well-known/openid-configuration") {
      return json(res, {
        issuer: base,
        authorization_endpoint: `${base}/authorize`,
        token_endpoint: `${base}/token`,
        jwks_uri: `${base}/jwks`,
      });
    }
    if (url.pathname === "/jwks") return json(res, { keys: [jwk] });

    if (url.pathname === "/authorize") {
      // A real IdP authenticates the user here. This one accepts immediately and redirects back with
      // a code — the console's checks are on the RETURN leg, which is what these tests are about.
      const code = randomUUID();
      control.issued.set(code, {
        nonce: url.searchParams.get("nonce"),
        challenge: url.searchParams.get("code_challenge"),
        redirectUri: url.searchParams.get("redirect_uri"),
      });
      const back = new URL(url.searchParams.get("redirect_uri"));
      back.searchParams.set("code", code);
      back.searchParams.set("state", url.searchParams.get("state") ?? "");
      res.writeHead(303, { location: back.toString() });
      return res.end();
    }

    if (url.pathname === "/token") {
      if (control.refuseToken) return json(res, { error: "invalid_grant" }, 400);
      const body = new URLSearchParams(await readBody(req));
      const record = control.issued.get(body.get("code"));
      if (!record) return json(res, { error: "invalid_grant" }, 400);
      // Single-use, exactly as a real IdP: this is what makes the console's "a reused code is refused"
      // assertion a test of a real behaviour rather than of our own bookkeeping.
      control.issued.delete(body.get("code"));
      const verifier = body.get("code_verifier") ?? "";
      const challenge = createHash("sha256").update(verifier).digest("base64url");
      if (!verifier || challenge !== record.challenge) return json(res, { error: "invalid_grant" }, 400);

      const now = Math.floor(Date.now() / 1000);
      const payload = {
        iss: base,
        aud: options.clientId ?? "console-client",
        sub: options.subject ?? "user-1",
        nonce: control.nonce ?? record.nonce,
        jti: control.fixedJti ?? randomUUID(),
        iat: now + control.iatOffset,
        exp: now + control.expOffset,
        ...control.claims,
      };
      return json(res, { token_type: "Bearer", id_token: mint(payload) });
    }

    res.writeHead(404).end();
  });

  function mint(payload) {
    const header = b64url({ alg: control.algNone ? "none" : "RS256", kid: "stub-1", typ: "JWT" });
    const body = b64url(payload);
    if (control.algNone) return `${header}.${body}.`;
    const signer = createSign("sha256");
    signer.update(`${header}.${body}`);
    return `${header}.${body}.${signer.sign(privateKey).toString("base64url")}`;
  }

  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return {
    base: `http://127.0.0.1:${server.address().port}`,
    control,
    mint,
    async close() {
      server.closeAllConnections?.();
      server.close();
    },
  };
}

/**
 * startStubSaml starts a SAML IdP: metadata over HTTP, and a signer that mints real signed responses.
 *
 * The certificate is minted per run with `openssl` and never committed — a private key in the
 * repository is a private key in the repository, whatever a comment beside it says.
 */
export async function startStubSaml(options) {
  const dir = mkdtempSync(join(tmpdir(), "heros-saml-"));
  execFileSync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes",
    "-keyout", join(dir, "idp.key"), "-out", join(dir, "idp.crt"),
    "-days", "1", "-subj", "/CN=heros-test-idp",
  ], { stdio: "ignore" });
  const idpKey = readFileSync(join(dir, "idp.key"), "utf8");
  const certificate = readFileSync(join(dir, "idp.crt"), "utf8").replace(/-----[^-]+-----/g, "").replace(/\s+/g, "");

  // The SP's own signing key, written where the console's `file` secrets source will look for it.
  const sp = generateKeyPairSync("rsa", { modulusLength: 2048 });
  writeFileSync(join(dir, "CONSOLE_SAML_SP_PRIVATE_KEY"), sp.privateKey.export({ type: "pkcs8", format: "pem" }));

  const control = {
    requests: [],
    /** Turning this off makes the IdP unreachable in the way an outage actually looks. */
    serveMetadata: true,
    /** The subject and email the IdP asserts. */
    nameId: options.nameId ?? "person@acme.test",
    /** Mutations a test applies to the signed document, to exercise one defense at a time. */
    mutate: null,
    /** Reuses one AssertionID, to exercise the one-time guard. */
    fixedAssertionId: null,
    /** Overrides `InResponseTo`, to exercise the unsolicited-assertion refusal. */
    inResponseTo: null,
  };
  const server = createServer((req, res) => {
    control.requests.push({ method: req.method, url: req.url });
    if (!control.serveMetadata) return res.writeHead(503).end();

    if (req.url.startsWith("/sso")) {
      // The IdP's side of the HTTP-Redirect binding: inflate the AuthnRequest, read its `ID` and its
      // ACS, mint a signed Response, and POST it back the way a real IdP's auto-submitting form does.
      const url = new URL(req.url, `http://${req.headers.host}`);
      const request = parseXml(
        inflateRawSync(Buffer.from(url.searchParams.get("SAMLRequest"), "base64")).toString("utf8"),
      );
      const acs = attrValue(request, "AssertionConsumerServiceURL");
      const relayState = url.searchParams.get("RelayState") ?? "";
      const assertionId = control.fixedAssertionId ?? `_${randomUUID().replace(/-/g, "")}`;
      let xml = samlResponseXml({
        entityId: options.entityId,
        spEntityId: options.spEntityId,
        acs,
        inResponseTo: control.inResponseTo ?? attrValue(request, "ID"),
        assertionId,
        nameId: control.nameId,
      });
      let signed = samlResponse({ xml, idpKey, signedId: assertionId });
      if (control.mutate) signed = control.mutate(signed, { assertionId, idpKey });
      const encoded = Buffer.from(signed, "utf8").toString("base64");
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      // A plain form with a submit button, not an auto-submitting script: the console serves a CSP
      // with no `unsafe-inline`, and this page is on the IdP's origin where a test driving a real
      // browser needs something clickable rather than something that may or may not have run.
      return res.end(
        `<!doctype html><html><body><form id="f" method="post" action="${acs}">` +
          `<input type="hidden" name="SAMLResponse" value="${encoded}">` +
          `<input type="hidden" name="RelayState" value="${relayState}">` +
          `<button type="submit">Continue</button></form></body></html>`,
      );
    }

    if (req.url.startsWith("/metadata")) {
      res.writeHead(200, { "content-type": "application/xml" });
      return res.end(
        `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="${options.entityId}">` +
          `<md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` +
          `<md:KeyDescriptor use="signing"><ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">` +
          `<ds:X509Data><ds:X509Certificate>${certificate}</ds:X509Certificate></ds:X509Data></ds:KeyInfo>` +
          `</md:KeyDescriptor>` +
          `<md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" ` +
          `Location="http://127.0.0.1:${server.address().port}/sso"/>` +
          `</md:IDPSSODescriptor></md:EntityDescriptor>`,
      );
    }
    res.writeHead(404).end();
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");

  return {
    secretsDir: dir,
    metadataUrl: `http://127.0.0.1:${server.address().port}/metadata`,
    control,
    idpKey,
    async close() {
      server.closeAllConnections?.();
      server.close();
    },
  };
}

/**
 * samlResponseXml builds an UNSIGNED SAML Response with a `<!--SIGNATURE-->` slot.
 *
 * Split from the signer so a test can mutate the document before or after signing and see which
 * defense catches it — mutating before signing produces a *validly signed* lie, which is the case that
 * matters for tenant mapping, and mutating after produces a broken digest, which is the case that
 * matters for the signature check.
 */
export function samlResponseXml({ entityId, spEntityId, acs, inResponseTo, assertionId, nameId, skewSeconds = 0 }) {
  const now = Date.now() + skewSeconds * 1000;
  const iso = (ms) => new Date(ms).toISOString();
  return (
    `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
    `xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r${assertionId}" Version="2.0" ` +
    `IssueInstant="${iso(now)}" Destination="${acs}" InResponseTo="${inResponseTo}">` +
    `<saml:Issuer>${entityId}</saml:Issuer>` +
    `<samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status>` +
    `<saml:Assertion ID="${assertionId}" Version="2.0" IssueInstant="${iso(now)}">` +
    `<saml:Issuer>${entityId}</saml:Issuer><!--SIGNATURE-->` +
    `<saml:Subject>` +
    `<saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">${nameId}</saml:NameID>` +
    `<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">` +
    `<saml:SubjectConfirmationData Recipient="${acs}" InResponseTo="${inResponseTo}" NotOnOrAfter="${iso(now + 60000)}"/>` +
    `</saml:SubjectConfirmation></saml:Subject>` +
    `<saml:Conditions NotBefore="${iso(now - 1000)}" NotOnOrAfter="${iso(now + 60000)}">` +
    `<saml:AudienceRestriction><saml:Audience>${spEntityId}</saml:Audience></saml:AudienceRestriction>` +
    `</saml:Conditions></saml:Assertion></samlp:Response>`
  );
}

/** samlResponse signs a document, computing the digest with the console's own canonicalizer. */
export function samlResponse({ xml, idpKey, signedId }) {
  const doc = parseXml(xml);
  const target = [...descendants(doc)].find(
    (el) => (el.attributes.find((a) => a.local === "ID") ?? {}).value === signedId,
  );
  if (!target) throw new Error(`no element with ID ${signedId} to sign`);
  const digest = createHash("sha256").update(Buffer.from(canonicalize(target), "utf8")).digest("base64");
  const signedInfo =
    `<ds:SignedInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">` +
    `<ds:CanonicalizationMethod Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"></ds:CanonicalizationMethod>` +
    `<ds:SignatureMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"></ds:SignatureMethod>` +
    `<ds:Reference URI="#${signedId}"><ds:Transforms>` +
    `<ds:Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"></ds:Transform>` +
    `<ds:Transform Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"></ds:Transform>` +
    `</ds:Transforms><ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"></ds:DigestMethod>` +
    `<ds:DigestValue>${digest}</ds:DigestValue></ds:Reference></ds:SignedInfo>`;
  const signer = createSign("sha256");
  signer.update(Buffer.from(signedInfo, "utf8"));
  const value = signer.sign(idpKey).toString("base64");
  return xml.replace(
    "<!--SIGNATURE-->",
    `<ds:Signature xmlns:ds="http://www.w3.org/2000/09/xmldsig#">${signedInfo}<ds:SignatureValue>${value}</ds:SignatureValue></ds:Signature>`,
  );
}

function json(res, body, status = 200) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
}

async function readBody(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}
