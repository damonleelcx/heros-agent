package adminidentity

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// xmlc14n.go is a namespace-PRESERVING XML reader and an exclusive-canonicalization writer, for the
// one consumer that needs one: SAML signature verification (`samlprovider.go`).
//
// # Why `encoding/xml` cannot do this
//
// An XML signature is computed over the CANONICAL FORM of an element, and exclusive canonicalization
// (`xml-exc-c14n`) is defined in terms of the document's PREFIXES — which namespace declarations an
// element visibly uses, rendered in a specified order. `encoding/xml` resolves prefixes to namespace
// URIs and discards the prefixes themselves, so a verifier built on it cannot reproduce the bytes the
// signer signed. It does not fail loudly: it computes a digest over a document nobody signed, and
// against a well-formed real assertion it "works" while having no idea what it checked.
//
// Everything here is unexported and has exactly one caller. It is deliberately not a general-purpose
// XML package: the subset below is the subset a SAML response uses, and widening it would be adding
// surface with no consumer.
//
// # The subset, stated rather than assumed
//
// Elements, attributes, text, CDATA, comments (discarded, matching `WithComments=false`), the XML
// declaration, and the five predefined entities plus numeric character references. **A DOCTYPE is
// refused, not ignored** — an assertion carrying one is an XXE / entity-expansion vector and no
// legitimate SAML response needs one. Refusing is the whole defense; "we do not expand entities" is a
// promise somebody has to keep forever.
//
// Its mirror is `web/console/src/lib/idp/xml.ts`, and both are checked against the W3C exclusive-c14n
// specification's own worked example — which is what makes two implementations a pair of independent
// checks rather than two chances to be wrong in the same way.

// xmlNS is the `xml` prefix's implicit binding. It is bound by the XML specification, is never
// declared, and must never be EMITTED as a declaration either — a canonicalizer that treats `xml:lang`
// like any other prefixed attribute writes `xmlns:xml=""` into its output, and every signature over an
// element carrying `xml:lang` then fails with nothing but "digest mismatch" to go on.
const xmlNS = "http://www.w3.org/XML/1998/namespace"

type xmlAttr struct {
	prefix string
	local  string
	value  string
}

type xmlNSDecl struct {
	prefix string
	uri    string
}

type xmlNode struct {
	// element is nil for a text node.
	element *xmlElement
	text    string
}

type xmlElement struct {
	prefix       string
	local        string
	declarations []xmlNSDecl
	attributes   []xmlAttr
	children     []xmlNode
	parent       *xmlElement
}

var errMalformedXML = errors.New("adminidentity: malformed XML")

// parseXML reads a document and returns its root element.
//
// Deliberately strict: an unbalanced tag, a stray `<`, or a DOCTYPE is an error rather than a
// best-effort recovery. Its only caller is deciding whether to trust an operator credential, and a
// recovering parser's whole value — "get something usable out of broken input" — is a liability there.
func parseXML(source string) (*xmlElement, error) {
	var root, current *xmlElement
	i, n := 0, len(source)

	for i < n {
		lt := strings.IndexByte(source[i:], '<')
		if lt < 0 {
			if current != nil && strings.TrimSpace(source[i:]) != "" {
				return nil, fmt.Errorf("%w: text after the root element", errMalformedXML)
			}
			break
		}
		if lt > 0 {
			text := source[i : i+lt]
			if current != nil {
				current.children = append(current.children, xmlNode{text: unescapeXML(text)})
			} else if strings.TrimSpace(text) != "" {
				return nil, fmt.Errorf("%w: text outside the root element", errMalformedXML)
			}
		}
		i += lt

		switch {
		case strings.HasPrefix(source[i:], "<?"):
			end := strings.Index(source[i:], "?>")
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated processing instruction", errMalformedXML)
			}
			i += end + 2
			continue
		case strings.HasPrefix(source[i:], "<!--"):
			end := strings.Index(source[i:], "-->")
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated comment", errMalformedXML)
			}
			i += end + 3
			continue
		case strings.HasPrefix(source[i:], "<![CDATA["):
			end := strings.Index(source[i:], "]]>")
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated CDATA", errMalformedXML)
			}
			if current != nil {
				current.children = append(current.children, xmlNode{text: source[i+9 : i+end]})
			}
			i += end + 3
			continue
		case strings.HasPrefix(source[i:], "<!"):
			// Includes `<!DOCTYPE`. Refused outright — see the file comment.
			return nil, fmt.Errorf("%w: a DOCTYPE or declaration is not accepted in a signed document", errMalformedXML)
		case strings.HasPrefix(source[i:], "</"):
			end := strings.IndexByte(source[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated end tag", errMalformedXML)
			}
			name := strings.TrimSpace(source[i+2 : i+end])
			if current == nil {
				return nil, fmt.Errorf("%w: end tag with no open element", errMalformedXML)
			}
			prefix, local := splitXMLName(name)
			if current.prefix != prefix || current.local != local {
				return nil, fmt.Errorf("%w: end tag </%s> does not close the open element", errMalformedXML, name)
			}
			current = current.parent
			i += end + 1
			continue
		}

		// A start tag. Scan to the matching `>`, honouring quoted values so a `>` inside one does not
		// truncate the tag.
		j := i + 1
		var quote byte
		for ; j < n; j++ {
			c := source[j]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '"' || c == '\'' {
				quote = c
			} else if c == '>' {
				break
			}
		}
		if j >= n {
			return nil, fmt.Errorf("%w: unterminated start tag", errMalformedXML)
		}
		body := source[i+1 : j]
		selfClosing := strings.HasSuffix(body, "/")
		if selfClosing {
			body = body[:len(body)-1]
		}
		nameEnd := strings.IndexAny(body, " \t\r\n/")
		if nameEnd < 0 {
			nameEnd = len(body)
		}
		if nameEnd == 0 {
			return nil, fmt.Errorf("%w: start tag with no name", errMalformedXML)
		}
		prefix, local := splitXMLName(body[:nameEnd])
		el := &xmlElement{prefix: prefix, local: local, parent: current}
		if err := parseXMLAttributes(body[nameEnd:], el); err != nil {
			return nil, err
		}

		if current != nil {
			current.children = append(current.children, xmlNode{element: el})
		} else if root != nil {
			return nil, fmt.Errorf("%w: more than one root element", errMalformedXML)
		} else {
			root = el
		}
		if !selfClosing {
			current = el
		}
		i = j + 1
	}

	if current != nil {
		return nil, fmt.Errorf("%w: unclosed element", errMalformedXML)
	}
	if root == nil {
		return nil, fmt.Errorf("%w: no root element", errMalformedXML)
	}
	return root, nil
}

// parseXMLAttributes reads `name="value"` pairs, separating namespace declarations from attributes.
func parseXMLAttributes(body string, el *xmlElement) error {
	seen := map[string]bool{}
	i := 0
	for i < len(body) {
		for i < len(body) && isXMLSpace(body[i]) {
			i++
		}
		if i >= len(body) {
			break
		}
		eq := strings.IndexByte(body[i:], '=')
		if eq < 0 {
			return fmt.Errorf("%w: attribute with no value", errMalformedXML)
		}
		name := strings.TrimSpace(body[i : i+eq])
		i += eq + 1
		for i < len(body) && isXMLSpace(body[i]) {
			i++
		}
		if i >= len(body) || (body[i] != '"' && body[i] != '\'') {
			return fmt.Errorf("%w: unquoted attribute value", errMalformedXML)
		}
		quote := body[i]
		i++
		end := strings.IndexByte(body[i:], quote)
		if end < 0 {
			return fmt.Errorf("%w: unterminated attribute value", errMalformedXML)
		}
		value := unescapeXML(body[i : i+end])
		i += end + 1

		if name == "" || seen[name] {
			return fmt.Errorf("%w: duplicate or empty attribute name", errMalformedXML)
		}
		seen[name] = true
		switch {
		case name == "xmlns":
			el.declarations = append(el.declarations, xmlNSDecl{prefix: "", uri: value})
		case strings.HasPrefix(name, "xmlns:"):
			el.declarations = append(el.declarations, xmlNSDecl{prefix: name[6:], uri: value})
		default:
			prefix, local := splitXMLName(name)
			el.attributes = append(el.attributes, xmlAttr{prefix: prefix, local: local, value: value})
		}
	}
	return nil
}

func isXMLSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func splitXMLName(name string) (string, string) {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

var xmlEntities = map[string]string{"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'"}

func unescapeXML(raw string) string {
	if !strings.ContainsRune(raw, '&') {
		return raw
	}
	var b strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] != '&' {
			b.WriteByte(raw[i])
			i++
			continue
		}
		end := strings.IndexByte(raw[i:], ';')
		if end < 0 || end > 12 {
			b.WriteByte(raw[i])
			i++
			continue
		}
		body := raw[i+1 : i+end]
		switch {
		case strings.HasPrefix(body, "#x"), strings.HasPrefix(body, "#X"):
			if v, err := strconv.ParseInt(body[2:], 16, 32); err == nil {
				b.WriteRune(rune(v))
				i += end + 1
				continue
			}
		case strings.HasPrefix(body, "#"):
			if v, err := strconv.ParseInt(body[1:], 10, 32); err == nil {
				b.WriteRune(rune(v))
				i += end + 1
				continue
			}
		default:
			if v, ok := xmlEntities[body]; ok {
				b.WriteString(v)
				i += end + 1
				continue
			}
		}
		// An unknown named entity is left alone rather than resolved: resolving would need an entity
		// table the DOCUMENT controls, which is what the DOCTYPE refusal exists to prevent.
		b.WriteByte(raw[i])
		i++
	}
	return b.String()
}

// ── Navigating ──────────────────────────────────────────────────────────────────────────────────

// namespaceScope resolves the in-scope prefix → URI map at an element.
func namespaceScope(el *xmlElement) map[string]string {
	var chain []*xmlElement
	for n := el; n != nil; n = n.parent {
		chain = append([]*xmlElement{n}, chain...)
	}
	scope := map[string]string{"xml": xmlNS}
	for _, node := range chain {
		for _, d := range node.declarations {
			scope[d.prefix] = d.uri
		}
	}
	return scope
}

// namespaceOf returns an element's namespace URI.
func namespaceOf(el *xmlElement) string { return namespaceScope(el)[el.prefix] }

// childrenNamed returns the direct child elements with this namespace URI and local name.
func childrenNamed(parent *xmlElement, uri, local string) []*xmlElement {
	var out []*xmlElement
	for _, c := range parent.children {
		if c.element != nil && c.element.local == local && namespaceOf(c.element) == uri {
			out = append(out, c.element)
		}
	}
	return out
}

// walkXML visits the subtree in document order.
func walkXML(el *xmlElement, visit func(*xmlElement)) {
	visit(el)
	for _, c := range el.children {
		if c.element != nil {
			walkXML(c.element, visit)
		}
	}
}

// textOfXML concatenates an element's direct text content.
func textOfXML(el *xmlElement) string {
	var b strings.Builder
	for _, c := range el.children {
		if c.element == nil {
			b.WriteString(c.text)
		}
	}
	return b.String()
}

// attrOf reads an unprefixed attribute.
func attrOf(el *xmlElement, local string) (string, bool) {
	for _, a := range el.attributes {
		if a.prefix == "" && a.local == local {
			return a.value, true
		}
	}
	return "", false
}

// ── Exclusive canonicalization (xml-exc-c14n, WithComments=false) ───────────────────────────────

// canonicalizeXML renders an element in exclusive canonical form, omitting `omit` if it is a
// descendant (the enveloped-signature transform).
//
// The three rules that make or break a signature check, and which a "serialise it back" writer gets
// wrong:
//
//  1. Only VISIBLY UTILIZED namespaces are rendered — the element's own prefix and its attributes'
//     prefixes, and nothing else. That is what "exclusive" means, and it is what lets an assertion
//     stay verifiable after being lifted out of the `<Response>` envelope it was signed inside.
//  2. A declaration is rendered only where it CHANGES; re-declaring an unchanged prefix produces bytes
//     the signer never produced.
//  3. Sort order is specified, not natural: namespace declarations first (default `xmlns` before
//     prefixed ones, then by prefix), then attributes by namespace URI then local name, with
//     unprefixed attributes — which have no namespace — sorting first.
func canonicalizeXML(el *xmlElement, omit *xmlElement) string {
	var b strings.Builder
	renderXML(&b, el, map[string]string{}, namespaceScope(el), omit)
	return b.String()
}

func renderXML(b *strings.Builder, el *xmlElement, rendered, inScope map[string]string, omit *xmlElement) {
	utilized := map[string]bool{el.prefix: true}
	for _, a := range el.attributes {
		if a.prefix != "" {
			utilized[a.prefix] = true
		}
	}
	prefixes := make([]string, 0, len(utilized))
	for p := range utilized {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes) // "" sorts first, so `xmlns` precedes every `xmlns:p`

	local := make(map[string]string, len(rendered)+len(prefixes))
	for k, v := range rendered {
		local[k] = v
	}
	name := el.local
	if el.prefix != "" {
		name = el.prefix + ":" + el.local
	}
	b.WriteString("<")
	b.WriteString(name)
	for _, p := range prefixes {
		if p == "xml" {
			continue // bound by the XML specification; never declared in canonical output
		}
		uri := inScope[p]
		if p == "" && uri == "" && local[""] == "" {
			continue // nothing to undo: no ancestor rendered a default namespace
		}
		if prior, ok := local[p]; ok && prior == uri {
			continue
		}
		local[p] = uri
		if p == "" {
			b.WriteString(` xmlns="` + escapeXMLAttr(uri) + `"`)
		} else {
			b.WriteString(` xmlns:` + p + `="` + escapeXMLAttr(uri) + `"`)
		}
	}

	attrs := make([]xmlAttr, len(el.attributes))
	copy(attrs, el.attributes)
	sort.SliceStable(attrs, func(i, j int) bool {
		ui, uj := "", ""
		if attrs[i].prefix != "" {
			ui = inScope[attrs[i].prefix]
		}
		if attrs[j].prefix != "" {
			uj = inScope[attrs[j].prefix]
		}
		if ui != uj {
			return ui < uj
		}
		return attrs[i].local < attrs[j].local
	})
	for _, a := range attrs {
		an := a.local
		if a.prefix != "" {
			an = a.prefix + ":" + a.local
		}
		b.WriteString(" " + an + `="` + escapeXMLAttr(a.value) + `"`)
	}
	b.WriteString(">")

	for _, c := range el.children {
		if c.element != nil {
			if c.element == omit {
				continue
			}
			renderXML(b, c.element, local, namespaceScope(c.element), omit)
		} else {
			b.WriteString(escapeXMLText(c.text))
		}
	}
	b.WriteString("</" + name + ">")
}

func escapeXMLText(v string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	return r.Replace(v)
}

func escapeXMLAttr(v string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
	return r.Replace(v)
}
