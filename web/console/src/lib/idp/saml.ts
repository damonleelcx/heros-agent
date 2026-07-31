import "server-only";
import { createHash, createPrivateKey, createSign, verify as cryptoVerify, X509Certificate } from "node:crypto";
import { deflateRawSync } from "node:zlib";
import { CONFIG, allowedRedirect } from "./config";
import { identitySecret, SECRET_SAML_SP_PRIVATE_KEY } from "./secrets";
import { emailClaim, checkFreshness, type FederatedClaims } from "./federation";
import { IdpUnreachableError } from "./oidc";
import {
  parseXml, canonicalize, childElements, descendants, textOf, attrValue,
  namespaceUri, type XmlElement,
} from "./xml";

/**
 * saml.ts is the SAML 2.0 Web Browser SSO mechanism (P22 Decision 2, task 2.4) — the enterprise
 * alternative, behind the same seam and deriving from the same `federation.ts` contract as OIDC.
 *
 * # The two attacks this file is really about
 *
 * **Signature wrapping.** The classic SAML break is not a broken signature; it is a *valid* signature
 * over an element the verifier then does not use. An attacker takes a legitimately signed assertion,
 * wraps it somewhere the verifier ignores, and puts a forged assertion where the verifier looks. Every
 * signature check passes and the claims are the attacker's. The defense is structural and it is the
 * rule this file is built around: **the element whose digest was verified is the ONLY element whose
 * claims are read.** `verifySignature` returns that element, and nothing here ever searches the
 * document for an assertion again afterwards.
 *
 * **Canonicalization drift.** A digest is over the canonical form, so a verifier whose serialiser
 * disagrees with the signer's by one namespace declaration checks bytes nobody signed. That is why
 * `xml.ts` keeps prefixes and implements exc-c14n rather than reaching for a convenience parser.
 *
 * # The profile, stated rather than assumed
 *
 * SP-initiated. AuthnRequest over HTTP-Redirect, **signed** with the SP key from the `Secrets` seam.
 * Response over HTTP-POST at an allowlisted ACS. Signature on the Assertion or on the Response
 * (both are deployed in the wild; both are accepted, and in either case only the verified element's
 * claims are read). RSA-SHA256 / ECDSA-SHA256 and their SHA-384/512 variants. **No encrypted
 * assertions** — a deployment that needs `EncryptedAssertion` is refused loudly rather than served by
 * a code path nobody exercises.
 */

const NS_PROTOCOL = "urn:oasis:names:tc:SAML:2.0:protocol";
const NS_ASSERTION = "urn:oasis:names:tc:SAML:2.0:assertion";
const NS_METADATA = "urn:oasis:names:tc:SAML:2.0:metadata";
const NS_DSIG = "http://www.w3.org/2000/09/xmldsig#";
const NS_XENC = "http://www.w3.org/2001/04/xmlenc#";
const EXC_C14N = "http://www.w3.org/2001/10/xml-exc-c14n#";
const ENVELOPED = "http://www.w3.org/2000/09/xmldsig#enveloped-signature";
const NAMEID_EMAIL = "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress";

/** SIGNATURE_ALGS is the allowlist. SHA-1 is absent: a collision-broken digest is not a signature. */
const SIGNATURE_ALGS: Record<string, { hash: string; options?: Record<string, unknown> }> = {
  "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256": { hash: "sha256" },
  "http://www.w3.org/2001/04/xmldsig-more#rsa-sha384": { hash: "sha384" },
  "http://www.w3.org/2001/04/xmldsig-more#rsa-sha512": { hash: "sha512" },
  "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256": { hash: "sha256", options: { dsaEncoding: "ieee-p1363" } },
  "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha384": { hash: "sha384", options: { dsaEncoding: "ieee-p1363" } },
  "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha512": { hash: "sha512", options: { dsaEncoding: "ieee-p1363" } },
};

const DIGEST_ALGS: Record<string, string> = {
  "http://www.w3.org/2001/04/xmlenc#sha256": "sha256",
  "http://www.w3.org/2001/04/xmldsig-more#sha384": "sha384",
  "http://www.w3.org/2001/04/xmlenc#sha512": "sha512",
};

/** SP_SIGN_ALG is the AuthnRequest signature algorithm. One, not a negotiation. */
const SP_SIGN_ALG = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256";

const SAML_TIMEOUT_MS = Number(process.env.CONSOLE_IDP_TIMEOUT_MS ?? 5000);
const METADATA_TTL_MS = 5 * 60 * 1000;

type Metadata = { entityId: string; ssoUrl: string; certificates: string[] };
const METADATA_CACHE = Symbol.for("heros.console.idp.saml.metadata");
type MetaGlobal = typeof globalThis & { [METADATA_CACHE]?: { at: number; value: Metadata } };

/**
 * metadata fetches and validates the IdP's SAML metadata.
 *
 * Same fail-closed rule as OIDC discovery: a failed refresh returns the error rather than serving the
 * stale copy, because verifying against a key set the IdP can no longer vouch for is the
 * cached-credential login Decision 8 refuses by name.
 */
export async function metadata(): Promise<Metadata> {
  const scope = globalThis as MetaGlobal;
  const cached = scope[METADATA_CACHE];
  if (cached && Date.now() - cached.at < METADATA_TTL_MS) return cached.value;

  let res: Response;
  try {
    res = await fetch(CONFIG.metadataUrl, { signal: AbortSignal.timeout(SAML_TIMEOUT_MS), redirect: "error", cache: "no-store" });
  } catch (err) {
    throw new IdpUnreachableError(err instanceof Error ? err.message : "metadata fetch failed");
  }
  if (!res.ok) throw new IdpUnreachableError(`metadata returned ${res.status}`);

  let root: XmlElement;
  try {
    root = parseXml(await res.text());
  } catch (err) {
    throw new IdpUnreachableError(err instanceof Error ? err.message : "metadata is not well-formed XML");
  }
  if (namespaceUri(root) !== NS_METADATA || root.local !== "EntityDescriptor") {
    throw new IdpUnreachableError("metadata is not a SAML EntityDescriptor");
  }
  const entityId = (attrValue(root, "entityID") ?? "").trim();
  if (entityId !== CONFIG.issuer) {
    // The same check discovery makes for OIDC, and for the same reason: without it a mis-typed or
    // hijacked metadata URL silently re-points this console's whole trust anchor.
    throw new IdpUnreachableError("metadata declares a different entityID than the configured one");
  }

  const certificates: string[] = [];
  let ssoUrl = "";
  for (const node of descendants(root)) {
    const uri = namespaceUri(node);
    if (uri === NS_DSIG && node.local === "X509Certificate") {
      const der = textOf(node).replace(/\s+/g, "");
      if (der) certificates.push(der);
    }
    if (uri === NS_METADATA && node.local === "SingleSignOnService") {
      if (attrValue(node, "Binding") === "urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect") {
        ssoUrl = ssoUrl || (attrValue(node, "Location") ?? "");
      }
    }
  }
  if (certificates.length === 0) throw new IdpUnreachableError("metadata carries no signing certificate");
  if (!ssoUrl) throw new IdpUnreachableError("metadata offers no HTTP-Redirect SSO endpoint");

  const value: Metadata = { entityId, ssoUrl, certificates };
  scope[METADATA_CACHE] = { at: Date.now(), value };
  return value;
}

/**
 * ensureMetadata confirms the IdP answers RIGHT NOW, and throws `IdpUnreachableError` if it does not.
 *
 * The OIDC side's `ensureReachable` carries the full reasoning: a five-minute metadata cache means a
 * dead IdP still yields a well-formed redirect, and the user lands on somebody else's error page while
 * our sign-in surface never learns anything is wrong.
 */
export async function ensureMetadata(): Promise<void> {
  delete (globalThis as MetaGlobal)[METADATA_CACHE];
  await metadata();
}

/** reachableMetadata probes the IdP for `/readyz`. Reachability, not traffic. */
export async function reachableMetadata(): Promise<{ reachable: boolean; detail?: string }> {
  try {
    await ensureMetadata();
    return { reachable: true };
  } catch (err) {
    return { reachable: false, detail: err instanceof Error ? err.message : "unreachable" };
  }
}

// ── SP-initiated request ────────────────────────────────────────────────────────────────────────

/** requestId turns a flow's state into a SAML `ID`, which must be an NCName (so: not digit-initial). */
export function requestId(state: string): string {
  return `_${createHash("sha256").update(state).digest("hex")}`;
}

/**
 * authnRequestUrl builds a SIGNED SP-initiated AuthnRequest over the HTTP-Redirect binding.
 *
 * # Why the request is signed even though the profile allows it not to be
 *
 * An unsigned AuthnRequest means anyone can start a flow that lands at our ACS, and it means the IdP
 * cannot tell our request from a crafted one. Signing costs a key we already have to source through
 * the `Secrets` seam (task 5.3) and closes that entirely. It also makes the seam's fail-closed
 * property visible on the SAML path rather than only on the OIDC one: no key ⇒ no sign-in, never a
 * silent downgrade to an unsigned request.
 *
 * The signature covers the query string in the order the binding specifies — `SAMLRequest`,
 * `RelayState`, `SigAlg` — because the binding signs the *serialisation*, and re-ordering after
 * signing is the standard way this check becomes decorative.
 */
export async function authnRequestUrl(flow: { state: string; redirectUri: string }): Promise<string> {
  const meta = await metadata();
  const acs = allowedRedirect(flow.redirectUri, CONFIG.acsAllowlist);
  if (!acs) throw new Error("the SAML ACS for this flow is not on the allowlist");

  const issued = new Date().toISOString();
  const request =
    `<samlp:AuthnRequest xmlns:samlp="${NS_PROTOCOL}" xmlns:saml="${NS_ASSERTION}"` +
    ` ID="${requestId(flow.state)}" Version="2.0" IssueInstant="${issued}"` +
    ` Destination="${meta.ssoUrl}" AssertionConsumerServiceURL="${acs}"` +
    ` ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST">` +
    `<saml:Issuer>${CONFIG.spEntityId}</saml:Issuer>` +
    `</samlp:AuthnRequest>`;

  const encoded = deflateRawSync(Buffer.from(request, "utf8")).toString("base64");
  const signable = `SAMLRequest=${encodeURIComponent(encoded)}&RelayState=${encodeURIComponent(flow.state)}&SigAlg=${encodeURIComponent(SP_SIGN_ALG)}`;

  const pem = await identitySecret(SECRET_SAML_SP_PRIVATE_KEY);
  const signer = createSign("sha256");
  signer.update(signable);
  const signature = signer.sign(createPrivateKey(pem)).toString("base64");

  const join = meta.ssoUrl.includes("?") ? "&" : "?";
  return `${meta.ssoUrl}${join}${signable}&Signature=${encodeURIComponent(signature)}`;
}

// ── Response verification ───────────────────────────────────────────────────────────────────────

export type SamlOutcome = { ok: true; claims: FederatedClaims } | { ok: false; cause: string };

/**
 * verifySignature verifies one `<ds:Signature>` and returns the element it actually covered.
 *
 * The return value is the point. A function that returned a boolean would leave the caller to decide
 * what was signed, and that decision is where signature wrapping lives.
 */
function verifySignature(doc: XmlElement, signature: XmlElement, certificates: string[]): { ok: true; covered: XmlElement } | { ok: false; cause: string } {
  const signedInfos = childElements(signature, NS_DSIG, "SignedInfo");
  if (signedInfos.length !== 1) return { ok: false, cause: "signature has no single SignedInfo" };
  const signedInfo = signedInfos[0];

  const c14n = childElements(signedInfo, NS_DSIG, "CanonicalizationMethod")[0];
  if (!c14n || attrValue(c14n, "Algorithm") !== EXC_C14N) {
    return { ok: false, cause: "unsupported canonicalization method" };
  }
  const method = childElements(signedInfo, NS_DSIG, "SignatureMethod")[0];
  const alg = SIGNATURE_ALGS[attrValue(method ?? signedInfo, "Algorithm") ?? ""];
  if (!alg) return { ok: false, cause: "unsupported signature algorithm" };

  const references = childElements(signedInfo, NS_DSIG, "Reference");
  // Exactly one reference. Several references let an attacker add a second one covering something
  // harmless and hope the verifier reports "signature valid" for the pair.
  if (references.length !== 1) return { ok: false, cause: "signature does not cover exactly one reference" };
  const reference = references[0];

  const uri = attrValue(reference, "URI") ?? "";
  if (!uri.startsWith("#") || uri.length < 2) return { ok: false, cause: "reference does not point at an element in this document" };
  const id = uri.slice(1);

  const matches = [...descendants(doc)].filter((el) => {
    const value = attrValue(el, "ID") ?? attrValue(el, "Id") ?? attrValue(el, "id");
    return value === id;
  });
  // Duplicate IDs are the raw material of a wrapping attack: two elements answer to one reference and
  // the verifier and the reader can pick different ones. Refused rather than resolved by a rule.
  if (matches.length !== 1) return { ok: false, cause: "reference resolves to no unique element" };
  const covered = matches[0];

  // The enveloped transform must be declared, and the signature must actually be inside the element
  // it covers — otherwise "enveloped" is a claim about a shape the document does not have.
  const transforms = childElements(reference, NS_DSIG, "Transforms")[0];
  const algorithms = transforms ? childElements(transforms, NS_DSIG, "Transform").map((t) => attrValue(t, "Algorithm") ?? "") : [];
  if (!algorithms.includes(ENVELOPED)) return { ok: false, cause: "reference is not an enveloped signature" };
  for (const a of algorithms) {
    if (a !== ENVELOPED && a !== EXC_C14N) return { ok: false, cause: "reference carries an unsupported transform" };
  }
  let inside = false;
  for (let node: XmlElement | null = signature.parent; node; node = node.parent) if (node === covered) inside = true;
  if (!inside) return { ok: false, cause: "the signature is not enveloped in the element it covers" };

  const digestMethod = childElements(reference, NS_DSIG, "DigestMethod")[0];
  const digestName = DIGEST_ALGS[attrValue(digestMethod ?? reference, "Algorithm") ?? ""];
  if (!digestName) return { ok: false, cause: "unsupported digest algorithm" };
  const digestValue = textOf(childElements(reference, NS_DSIG, "DigestValue")[0] ?? reference).trim();
  if (!digestValue) return { ok: false, cause: "reference carries no digest" };

  const canonical = canonicalize(covered, { omit: signature });
  const actual = createHash(digestName).update(Buffer.from(canonical, "utf8")).digest("base64");
  if (actual !== digestValue) return { ok: false, cause: "the signed digest does not match the element" };

  // SignedInfo is canonicalized with the namespace context it INHERITS — its `ds` prefix is declared
  // on the Signature element above it, and exc-c14n renders it on SignedInfo because that is where it
  // becomes visibly utilized in the extracted subtree.
  const signedInfoBytes = Buffer.from(canonicalize(signedInfo), "utf8");
  const signatureValue = textOf(childElements(signature, NS_DSIG, "SignatureValue")[0] ?? signature).replace(/\s+/g, "");
  if (!signatureValue) return { ok: false, cause: "signature carries no value" };

  const sig = Buffer.from(signatureValue, "base64");
  for (const cert of certificates) {
    let key;
    try {
      key = new X509Certificate(Buffer.from(cert, "base64")).publicKey;
    } catch {
      continue;
    }
    try {
      if (cryptoVerify(alg.hash, signedInfoBytes, { key, ...(alg.options ?? {}) }, sig)) return { ok: true, covered };
    } catch {
      continue;
    }
  }
  return { ok: false, cause: "signature did not verify against any metadata certificate" };
}

/**
 * verifySamlResponse validates a base64 `SAMLResponse` and returns the four contract claims.
 *
 * `expectedInResponseTo` binds the response to the flow this browser began — the SAML analogue of the
 * OIDC `nonce`, and the reason an unsolicited assertion (however validly signed) cannot complete a
 * sign-in here.
 */
export async function verifySamlResponse(encoded: string, input: { acsUrl: string; expectedInResponseTo: string }): Promise<SamlOutcome> {
  const meta = await metadata();

  let doc: XmlElement;
  try {
    doc = parseXml(Buffer.from(encoded, "base64").toString("utf8"));
  } catch (err) {
    return { ok: false, cause: err instanceof Error ? err.message : "response is not well-formed XML" };
  }
  if (namespaceUri(doc) !== NS_PROTOCOL || doc.local !== "Response") {
    return { ok: false, cause: "payload is not a SAML Response" };
  }

  // Refused loudly rather than half-handled: an encrypted assertion needs the SP decryption key and a
  // code path this deployment does not exercise, and a verifier that quietly ignored it would report
  // "no assertion" for a response that has one.
  for (const node of descendants(doc)) {
    if (node.local === "EncryptedAssertion" || namespaceUri(node) === NS_XENC) {
      return { ok: false, cause: "encrypted assertions are not accepted by this deployment" };
    }
  }

  const destination = attrValue(doc, "Destination");
  if (destination && !allowedRedirect(destination, CONFIG.acsAllowlist)) {
    return { ok: false, cause: "response Destination is not an allowlisted ACS" };
  }
  if (!allowedRedirect(input.acsUrl, CONFIG.acsAllowlist)) {
    return { ok: false, cause: "response arrived at an ACS that is not on the allowlist" };
  }

  const status = childElements(doc, NS_PROTOCOL, "Status")[0];
  const statusCode = status ? childElements(status, NS_PROTOCOL, "StatusCode")[0] : undefined;
  if (!statusCode || attrValue(statusCode, "Value") !== "urn:oasis:names:tc:SAML:2.0:status:Success") {
    return { ok: false, cause: "response status is not Success" };
  }

  // Verify EVERY signature present, and keep what each covered. An unverifiable signature anywhere is
  // a refusal, not something to skip past on the way to a good one.
  const covered: XmlElement[] = [];
  for (const node of descendants(doc)) {
    if (namespaceUri(node) !== NS_DSIG || node.local !== "Signature") continue;
    const outcome = verifySignature(doc, node, meta.certificates);
    if (!outcome.ok) return { ok: false, cause: outcome.cause };
    covered.push(outcome.covered);
  }
  if (covered.length === 0) return { ok: false, cause: "response carries no signature" };

  // THE anti-wrapping step. The assertion is taken from inside a verified element, and the only
  // assertions considered are the ones that element actually contains.
  const assertions: XmlElement[] = [];
  for (const element of covered) {
    if (namespaceUri(element) === NS_ASSERTION && element.local === "Assertion") assertions.push(element);
    else for (const node of descendants(element)) {
      if (namespaceUri(node) === NS_ASSERTION && node.local === "Assertion") assertions.push(node);
    }
  }
  const unique = [...new Set(assertions)];
  if (unique.length !== 1) return { ok: false, cause: "the signed content does not carry exactly one assertion" };
  const assertion = unique[0];

  const inResponseTo = attrValue(doc, "InResponseTo") ?? "";
  if (inResponseTo !== input.expectedInResponseTo) {
    return { ok: false, cause: "response does not answer the request this browser began" };
  }

  const issuer = textOf(childElements(assertion, NS_ASSERTION, "Issuer")[0] ?? assertion).trim();
  if (!issuer || issuer !== meta.entityId) return { ok: false, cause: "assertion issuer is not the configured IdP" };

  const conditions = childElements(assertion, NS_ASSERTION, "Conditions")[0];
  if (!conditions) return { ok: false, cause: "assertion carries no Conditions" };
  const audiences: string[] = [];
  for (const restriction of childElements(conditions, NS_ASSERTION, "AudienceRestriction")) {
    for (const audience of childElements(restriction, NS_ASSERTION, "Audience")) audiences.push(textOf(audience).trim());
  }
  if (!audiences.includes(CONFIG.spEntityId)) return { ok: false, cause: "assertion audience is not this service provider" };

  const subject = childElements(assertion, NS_ASSERTION, "Subject")[0];
  if (!subject) return { ok: false, cause: "assertion carries no Subject" };
  const nameIdEl = childElements(subject, NS_ASSERTION, "NameID")[0];
  const nameId = nameIdEl ? textOf(nameIdEl).trim() : "";
  if (!nameId) return { ok: false, cause: "assertion carries no NameID" };

  // The recipient must be this ACS. Without it a signed assertion minted for another SP is replayable
  // here, which is the SAML shape of an audience confusion.
  let recipientOk = false;
  for (const confirmation of childElements(subject, NS_ASSERTION, "SubjectConfirmation")) {
    for (const data of childElements(confirmation, NS_ASSERTION, "SubjectConfirmationData")) {
      const recipient = attrValue(data, "Recipient");
      const answers = attrValue(data, "InResponseTo");
      if (recipient && allowedRedirect(recipient, CONFIG.acsAllowlist) && (!answers || answers === input.expectedInResponseTo)) {
        recipientOk = true;
      }
    }
  }
  if (!recipientOk) return { ok: false, cause: "no SubjectConfirmationData names this ACS" };

  // `email` from the NameID when the IdP said it is one, otherwise from the conventional attribute.
  // Passed as VERIFIED, and the reason is worth stating plainly: SAML has no `email_verified`, so the
  // proof of ownership is not in the assertion at all — it is in `verified_domains` hanging off the
  // issuer's registration (`federation.ts`). An IdP asserting an address it does not own resolves to
  // nothing, because the registration that would have to vouch for that domain is somebody else's.
  let email = attrValue(nameIdEl!, "Format") === NAMEID_EMAIL ? nameId : "";
  if (!email) {
    for (const statement of childElements(assertion, NS_ASSERTION, "AttributeStatement")) {
      for (const attribute of childElements(statement, NS_ASSERTION, "Attribute")) {
        const name = (attrValue(attribute, "Name") ?? "").toLowerCase();
        if (name === "email" || name === "mail" || name.endsWith("/claims/emailaddress")) {
          const value = childElements(attribute, NS_ASSERTION, "AttributeValue")[0];
          if (value) email = email || textOf(value).trim();
        }
      }
    }
  }

  const assertionId = attrValue(assertion, "ID") ?? "";
  if (!assertionId) return { ok: false, cause: "assertion carries no ID" };

  const claims: FederatedClaims = {
    issuer,
    subject: nameId,
    ...emailClaim(email || undefined, Boolean(email)),
    assertionId,
    issuedAt: instant(attrValue(assertion, "IssueInstant")),
    notBefore: instant(attrValue(conditions, "NotBefore")),
    expiresAt: instant(attrValue(conditions, "NotOnOrAfter")),
  };
  const fresh = checkFreshness(claims, Date.now());
  if (!fresh.ok) return { ok: false, cause: fresh.cause };
  return { ok: true, claims };
}

/** instant parses an XSD dateTime into epoch seconds, or undefined — never into a default. */
function instant(value: string | undefined): number | undefined {
  if (!value) return undefined;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : undefined;
}

/** NAMESPACES is exported so a fixture IdP builds its assertion against the verifier's own constants. */
export const NAMESPACES = { NS_PROTOCOL, NS_ASSERTION, NS_METADATA, NS_DSIG, EXC_C14N, ENVELOPED, NAMEID_EMAIL };
