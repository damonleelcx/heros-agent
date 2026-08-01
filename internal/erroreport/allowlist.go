// Package erroreport is the P24 error-reporting boundary — the only path by which a failure inside the
// platform reaches a third-party incident inbox.
//
// Everything here exists to make one guarantee true by CONSTRUCTION rather than by review: the
// transmitted event is built field by field from an explicit allowlist, and a field added to any
// internal error representation is ABSENT from a transmitted event by default.
//
// The asymmetry is the whole argument, and it is the same one `internal/runlink` is built on:
//
//	Denylist (serialize + strip) — a new field is SENT. Silent. Discovered externally (by a customer).
//	Allowlist (construct)        — a new field is ABSENT. Visible as a missing feature. Discovered here.
//
// An error reporter is the strongest case for construction in the system, because it receives every
// field any engineer ever attaches to an error, from anywhere in the codebase, forever. A default
// event from a general-purpose SDK is close to a worst case: message, frames with source context,
// request URL / headers / body, environment, breadcrumbs carrying every fetch URL and console line, IP
// and hostname. And the most dangerous field is the most innocuous-looking —
// `fmt.Errorf("failed to resolve prompt %q", p)` is an ordinary Go error and an exfiltration path.
//
// # 🔴 Why this package speaks the ingest protocol itself instead of importing a vendor SDK
//
// This is a deliberate deviation worth stating, because "we used the official SDK" is the answer a
// reviewer expects.
//
// The design's requirement is that the constructed payload's JSON is the EXACT BYTES ON THE WIRE, and
// that a forbidden-shape fixture is asserted against those bytes. An SDK cannot give that: its event
// type carries fields we never set (server name — which is a hostname; loaded modules; contexts;
// request; user; breadcrumbs), its defaults populate several of them, and every version bump can add
// another. A `BeforeSend` that returns a freshly built event closes most of that, but the guarantee
// then reads "the SDK serialises only what we set, at the version we tested", which is precisely the
// denylist posture this package exists to refuse — one dependency upgrade from being false, silently,
// and discovered by a customer.
//
// Speaking the envelope directly costs about forty lines and buys the guarantee outright: `Envelope`
// below is the complete set of bytes, and `TestTransmittedBytesCarryNoForbiddenShape` reads them off a
// real socket. It also keeps an HTTP-carrying dependency out of the module graph the CLI's offline
// guarantee is asserted over, and it keeps the browser half (P24 wave 24c) able to make the same claim
// with a reporter small enough not to spend the public surface's transfer budget.
//
// # What is never expressible here
//
// Message bodies (except an `error.code` from the central enum), request bodies, request/response
// headers, query strings, breadcrumb / fetch / XHR / navigation URLs, console output, DOM breadcrumbs
// and click-target text, local variables, source context for a non-platform frame, environment values,
// credentials, prompt / completion / source / diff text, hostnames, server names, IP addresses, email
// addresses and tenant names. None of those is a field that is filtered — none of them is a field.
package erroreport

// AllowlistField is one permitted field, named and categorized so the allowlist is a readable
// security-review artifact rather than a set of struct tags a reviewer must reverse-engineer.
//
// It is deliberately the same shape as `runlink.AllowlistField`. Two boundaries in one system that
// describe their contracts differently give a reviewer two things to learn; the second one is the one
// that gets skimmed.
type AllowlistField struct {
	// Name is the wire key this field appears under in a transmitted event.
	Name string
	// Category groups fields for the review doc: classification, location, correlation, provenance.
	Category string
	// Why is the one-line justification a reviewer reads: why this is STRUCTURE, not CONTENT.
	Why string
}

// Allowlist is the complete, ratified set of fields permitted to cross the boundary (design D5).
//
// It is the SINGLE SOURCE OF TRUTH: `Event.Wire` writes exactly these keys, `cmd/erroreportdoc`
// renders `docs/decisions/error-event-allowlist.md` from this list, and the boundary test asserts
// a transmitted payload carries no key outside it AND that no key in it goes unpopulated. Change the
// boundary here, in one place, or not at all.
var Allowlist = []AllowlistField{
	// ── classification ─────────────────────────────────────────────────────────
	{"error.type", "classification", "The exception class (*runtime.Error, TypeError) — a type NAME, never a value. It is what distinguishes a nil map write from a failed type assertion without carrying either one's operands."},
	{"error.code", "classification", "A value from the central errorcode enum. The ONLY permitted message-shaped field, and it is closed: a string that is not a member does not reach the wire in any field."},
	{"level", "classification", "error or fatal. Two values, so an inbox can order by severity without being told what happened."},
	// ── location ───────────────────────────────────────────────────────────────
	{"frames.function", "location", "Our own symbol name. A stack is the whole of what makes a report actionable, and a function name is code we wrote, not data we were given."},
	{"frames.package", "location", "Our own package path — which subsystem, which is how a report is routed to an owner."},
	{"frames.file", "location", "Our own file path within the module. Not an absolute path: an absolute path carries the build host's directory layout and, on a developer machine, a user name."},
	{"frames.line", "location", "A line number in our own file. It is what turns a package-and-function report into one somebody can open, and it carries nothing that is not already in the checked-in source."},
	{"frames.in_app", "location", "Whether the frame is platform code. It is load-bearing rather than cosmetic: a non-platform frame carries no source context at all, and this is the flag that decides it."},
	// ── correlation ────────────────────────────────────────────────────────────
	{"trace_id", "correlation", "The identity ALREADY on the span, the structured log and the X-Trace-Id response header. No second correlation identity is minted — two systems each holding half an incident with no join key is the failure this avoids."},
	// ── provenance ─────────────────────────────────────────────────────────────
	{"release", "provenance", "The build the failure occurred in. Without it, a fixed defect and a live one are the same row."},
	{"edition", "provenance", "Which deployment shape — a label from a closed set, never a customer name or a hostname."},
	{"surface", "provenance", "Which surface — an id from the closed enum in web/design-system/third-party-policy.ts, NEVER a URL. A URL under /app carries variant, run, node and tenant identifiers."},
	{"runtime", "provenance", "go or browser, and its version. Which runtime failed, which is a property of the platform build rather than of the machine it ran on."},
}

// allowlistKeys is the flattened set of permitted wire keys, computed once from Allowlist.
var allowlistKeys = func() map[string]bool {
	m := make(map[string]bool, len(Allowlist))
	for _, f := range Allowlist {
		m[f.Name] = true
	}
	return m
}()

// Permitted reports whether a dotted wire key is on the allowlist. The boundary test walks a
// transmitted payload and asserts every leaf key is Permitted.
func Permitted(key string) bool { return allowlistKeys[key] }

// AllowlistKeys returns the permitted wire keys in declaration order. The test and the review-doc
// generator share this so neither can drift from the event builder.
func AllowlistKeys() []string {
	out := make([]string, 0, len(Allowlist))
	for _, f := range Allowlist {
		out = append(out, f.Name)
	}
	return out
}

// CategoryOf returns a field's category, or "" if the key is not on the allowlist.
func CategoryOf(key string) string {
	for _, f := range Allowlist {
		if f.Name == key {
			return f.Category
		}
	}
	return ""
}
