/**
 * xml.ts is a namespace-PRESERVING XML reader and an exclusive-canonicalization writer, and it exists
 * for exactly one consumer: SAML signature verification (`saml.ts`).
 *
 * # Why this file, alone in `lib/idp/`, is not `server-only`
 *
 * Every other module here reads a secret, an environment variable, or the network, and `server-only`
 * turns a bad import into a compile error. This one is pure computation over a string: no secrets, no
 * configuration, no I/O, nothing a client component could learn by importing it.
 *
 * The marker is dropped for a positive reason rather than an absent one. The test IdP that signs
 * fixture assertions (`tests/support/idp.mjs`) must compute the digest an IdP computes, which means
 * canonicalizing the same bytes. If it could not import THIS canonicalizer it would need its own — and
 * a second implementation written to agree with the first is not a check, it is a matched pair that
 * can be wrong together. So the signer imports this module, and the thing that makes that meaningful
 * is an independent one: `sso-identity.test.mjs` checks this canonicalizer against the W3C
 * exclusive-c14n specification's own worked example, which neither implementation authored.
 *
 * # Why this is not `encoding/xml`-shaped, and not a dependency
 *
 * XML signatures are computed over the *canonical form* of an element, and exclusive canonicalization
 * (`xml-exc-c14n`) is defined in terms of the document's PREFIXES — which namespace declarations an
 * element visibly uses, rendered in a specified order. A parser that resolves prefixes to URIs and
 * throws the prefixes away (Go's `encoding/xml` does; most convenience parsers do) cannot reproduce
 * that form, and a verifier built on one silently computes a digest over a document nobody signed. It
 * fails open in the worst way: against a well-formed real assertion it "works", and it has no idea
 * which bytes it checked.
 *
 * So this reader keeps prefixes, declaration order and the exact attribute set, and the writer below
 * implements exc-c14n against them.
 *
 * # The subset, stated rather than assumed
 *
 * Elements, attributes, text, CDATA, comments (discarded, matching `WithComments=false`), the XML
 * declaration and the five predefined entities plus numeric character references. **DTDs and internal
 * entity declarations are refused, not ignored** — an assertion carrying a DOCTYPE is an XXE/billion-
 * laughs vector and there is no legitimate SAML response that needs one. Refusing is the whole
 * defense; a parser that "does not expand entities" still has to be right about it forever.
 */

export type XmlAttr = { prefix: string; local: string; value: string };

export type XmlElement = {
  prefix: string;
  local: string;
  /** Namespace declarations made ON this element, in document order. `prefix: ""` is the default ns. */
  declarations: Array<{ prefix: string; uri: string }>;
  attributes: XmlAttr[];
  children: XmlNode[];
  parent: XmlElement | null;
};

export type XmlText = { text: string };
export type XmlNode = XmlElement | XmlText;

export function isElement(node: XmlNode): node is XmlElement {
  return (node as XmlElement).local !== undefined;
}

export class XmlError extends Error {}

// ── Reading ─────────────────────────────────────────────────────────────────────────────────────

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'" };

function unescape(raw: string): string {
  return raw.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (whole, body: string) => {
    if (body.startsWith("#x") || body.startsWith("#X")) {
      const code = Number.parseInt(body.slice(2), 16);
      return Number.isFinite(code) ? String.fromCodePoint(code) : whole;
    }
    if (body.startsWith("#")) {
      const code = Number.parseInt(body.slice(1), 10);
      return Number.isFinite(code) ? String.fromCodePoint(code) : whole;
    }
    // An unknown named entity is left alone rather than resolved: resolving would require an entity
    // table the document controls, which is the thing the DOCTYPE refusal exists to prevent.
    return ENTITIES[body] ?? whole;
  });
}

function splitName(name: string): { prefix: string; local: string } {
  const colon = name.indexOf(":");
  if (colon < 0) return { prefix: "", local: name };
  return { prefix: name.slice(0, colon), local: name.slice(colon + 1) };
}

/**
 * parseXml reads a document and returns its root element.
 *
 * Deliberately strict: an unbalanced tag, a stray `<`, or a DOCTYPE is an error rather than a
 * best-effort recovery. This parser's only caller is deciding whether to trust a credential, and a
 * recovering parser's whole value — "get something usable out of broken input" — is a liability there.
 */
export function parseXml(source: string): XmlElement {
  let i = 0;
  const n = source.length;
  let root: XmlElement | null = null;
  let current: XmlElement | null = null;

  const fail = (why: string): never => {
    throw new XmlError(`malformed XML: ${why}`);
  };

  while (i < n) {
    const lt = source.indexOf("<", i);
    if (lt < 0) {
      if (current && source.slice(i).trim()) fail("text after the root element");
      break;
    }
    if (lt > i) {
      const text = source.slice(i, lt);
      if (current) current.children.push({ text: unescape(text) });
      else if (text.trim()) fail("text outside the root element");
    }
    i = lt;

    if (source.startsWith("<?", i)) {
      const end = source.indexOf("?>", i);
      if (end < 0) fail("unterminated processing instruction");
      i = end + 2;
      continue;
    }
    if (source.startsWith("<!--", i)) {
      const end = source.indexOf("-->", i);
      if (end < 0) fail("unterminated comment");
      i = end + 3;
      continue;
    }
    if (source.startsWith("<![CDATA[", i)) {
      const end = source.indexOf("]]>", i);
      if (end < 0) fail("unterminated CDATA");
      if (current) current.children.push({ text: source.slice(i + 9, end) });
      i = end + 3;
      continue;
    }
    if (source.startsWith("<!", i)) {
      // Includes `<!DOCTYPE`. Refused outright — see the module comment.
      fail("a DOCTYPE or declaration is not accepted in a signed document");
    }
    if (source.startsWith("</", i)) {
      const end = source.indexOf(">", i);
      if (end < 0) fail("unterminated end tag");
      const name = source.slice(i + 2, end).trim();
      if (!current) fail("end tag with no open element");
      const { prefix, local } = splitName(name);
      if (current!.prefix !== prefix || current!.local !== local) fail(`end tag </${name}> does not close <${current!.prefix ? `${current!.prefix}:` : ""}${current!.local}>`);
      current = current!.parent;
      i = end + 1;
      continue;
    }

    // A start tag. Scan to the matching `>`, honouring quoted attribute values so a `>` inside one
    // does not truncate the tag.
    let j = i + 1;
    let quote = "";
    for (; j < n; j++) {
      const ch = source[j];
      if (quote) {
        if (ch === quote) quote = "";
        continue;
      }
      if (ch === '"' || ch === "'") quote = ch;
      else if (ch === ">") break;
    }
    if (j >= n) fail("unterminated start tag");
    let body = source.slice(i + 1, j);
    const selfClosing = body.endsWith("/");
    if (selfClosing) body = body.slice(0, -1);

    const nameMatch = /^([^\s/>]+)/.exec(body);
    if (!nameMatch) fail("start tag with no name");
    const { prefix, local } = splitName(nameMatch![1]);
    const element: XmlElement = { prefix, local, declarations: [], attributes: [], children: [], parent: current };

    const attrPattern = /([^\s=/]+)\s*=\s*("([^"]*)"|'([^']*)')/g;
    attrPattern.lastIndex = nameMatch![1].length;
    let attr: RegExpExecArray | null;
    const seen = new Set<string>();
    while ((attr = attrPattern.exec(body)) !== null) {
      const rawName = attr[1];
      if (seen.has(rawName)) fail("duplicate attribute");
      seen.add(rawName);
      const value = unescape(attr[3] ?? attr[4] ?? "");
      if (rawName === "xmlns") element.declarations.push({ prefix: "", uri: value });
      else if (rawName.startsWith("xmlns:")) element.declarations.push({ prefix: rawName.slice(6), uri: value });
      else {
        const parts = splitName(rawName);
        element.attributes.push({ prefix: parts.prefix, local: parts.local, value });
      }
    }

    if (current) current.children.push(element);
    else if (root) fail("more than one root element");
    else root = element;
    if (!selfClosing) current = element;
    i = j + 1;
  }

  if (current) throw new XmlError("malformed XML: unclosed element");
  if (!root) throw new XmlError("malformed XML: no root element");
  return root;
}

// ── Navigating ──────────────────────────────────────────────────────────────────────────────────

/**
 * XML_NS is the `xml` prefix's implicit binding.
 *
 * It is bound by the XML specification itself, is never declared, and — crucially for c14n — must
 * never be EMITTED as a declaration either. A canonicalizer that treats `xml:lang` like any other
 * prefixed attribute writes `xmlns:xml=""` into the output and every signature over an element
 * carrying `xml:lang` or `xml:space` fails, with a message that says only "digest mismatch".
 */
export const XML_NS = "http://www.w3.org/XML/1998/namespace";

/** namespaceContext resolves the in-scope prefix → URI map at an element. */
export function namespaceContext(element: XmlElement): Map<string, string> {
  const chain: XmlElement[] = [];
  for (let node: XmlElement | null = element; node; node = node.parent) chain.unshift(node);
  const scope = new Map<string, string>([["xml", XML_NS]]);
  for (const node of chain) for (const d of node.declarations) scope.set(d.prefix, d.uri);
  return scope;
}

/** namespaceUri returns an element's namespace URI. */
export function namespaceUri(element: XmlElement): string {
  return namespaceContext(element).get(element.prefix) ?? "";
}

/** children returns the direct child elements matching a namespace URI and local name. */
export function childElements(parent: XmlElement, uri: string, local: string): XmlElement[] {
  return parent.children.filter(
    (c): c is XmlElement => isElement(c) && c.local === local && namespaceUri(c) === uri,
  );
}

/** descendants walks the whole subtree, in document order. */
export function* descendants(element: XmlElement): Generator<XmlElement> {
  yield element;
  for (const child of element.children) if (isElement(child)) yield* descendants(child);
}

/** textOf concatenates an element's direct text content. */
export function textOf(element: XmlElement): string {
  return element.children.filter((c): c is XmlText => !isElement(c)).map((c) => c.text).join("");
}

/** attrValue reads an unprefixed attribute. */
export function attrValue(element: XmlElement, local: string): string | undefined {
  return element.attributes.find((a) => a.prefix === "" && a.local === local)?.value;
}

// ── Exclusive canonicalization (xml-exc-c14n, WithComments=false) ───────────────────────────────

function escapeText(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/\r/g, "&#xD;");
}

function escapeAttr(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/"/g, "&quot;")
    .replace(/\t/g, "&#x9;")
    .replace(/\n/g, "&#xA;")
    .replace(/\r/g, "&#xD;");
}

/**
 * canonicalize renders an element in exclusive canonical form.
 *
 * The three rules that make or break a signature check, and which a "just serialise it back" writer
 * gets wrong:
 *
 *  1. **Only VISIBLY UTILIZED namespaces are rendered.** Exclusive c14n emits a declaration for the
 *     element's own prefix and for the prefixes of its attributes, and nothing else — which is the
 *     entire point of "exclusive": an assertion stays verifiable after being lifted out of the
 *     `<Response>` envelope it was signed inside.
 *  2. **A declaration is rendered only where it CHANGES.** `rendered` carries what an ancestor already
 *     emitted; re-declaring an unchanged prefix produces bytes the signer never produced.
 *  3. **Sort order is specified, not natural.** Namespace declarations first (default `xmlns` before
 *     prefixed ones, then by prefix); then attributes by namespace URI, then by local name, with
 *     unprefixed attributes — which have no namespace — sorting first.
 *
 * `omit` is the enveloped-signature transform: the `<ds:Signature>` element is excluded from the
 * digest of the very thing it signs, because it cannot contain its own digest.
 */
export function canonicalize(
  element: XmlElement,
  options: { rendered?: Map<string, string>; omit?: XmlElement | null } = {},
): string {
  const inherited = namespaceContext(element);
  return render(element, options.rendered ?? new Map<string, string>(), inherited, options.omit ?? null);
}

function render(
  element: XmlElement,
  rendered: Map<string, string>,
  inScope: Map<string, string>,
  omit: XmlElement | null,
): string {
  const utilized = new Set<string>([element.prefix]);
  for (const a of element.attributes) if (a.prefix) utilized.add(a.prefix);

  const localRendered = new Map(rendered);
  const declarations: string[] = [];
  for (const prefix of [...utilized].sort()) {
    // The `xml` prefix is bound by the XML specification and is never declared in canonical output.
    if (prefix === "xml") continue;
    const uri = inScope.get(prefix) ?? "";
    // An unprefixed name in no namespace needs `xmlns=""` only if an ancestor rendered a default
    // namespace — otherwise there is nothing to undo.
    if (prefix === "" && uri === "" && (localRendered.get("") ?? "") === "") continue;
    if (localRendered.get(prefix) === uri) continue;
    localRendered.set(prefix, uri);
    declarations.push(prefix === "" ? ` xmlns="${escapeAttr(uri)}"` : ` xmlns:${prefix}="${escapeAttr(uri)}"`);
  }
  // `xmlns` sorts before `xmlns:a` because "" < any prefix, which the sort above already gives us.

  const attrs = [...element.attributes]
    .sort((a, b) => {
      const ua = a.prefix ? inScope.get(a.prefix) ?? "" : "";
      const ub = b.prefix ? inScope.get(b.prefix) ?? "" : "";
      if (ua !== ub) return ua < ub ? -1 : 1;
      return a.local < b.local ? -1 : a.local > b.local ? 1 : 0;
    })
    .map((a) => ` ${a.prefix ? `${a.prefix}:` : ""}${a.local}="${escapeAttr(a.value)}"`);

  const name = `${element.prefix ? `${element.prefix}:` : ""}${element.local}`;
  let out = `<${name}${declarations.join("")}${attrs.join("")}>`;
  for (const child of element.children) {
    if (isElement(child)) {
      if (child === omit) continue;
      const childScope = namespaceContext(child);
      out += render(child, localRendered, childScope, omit);
    } else {
      out += escapeText(child.text);
    }
  }
  return `${out}</${name}>`;
}
