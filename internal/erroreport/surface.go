package erroreport

// surface.go holds the SERVER half of the closed surface enum.
//
// # Why the enum has two halves and one rule
//
// `surface` answers "where did this happen" without answering "for whom". A URL cannot do that job on
// this platform: a path under `/app` carries variant, run, node and tenant identifiers, and a denylist
// over paths that gain segments every phase fails the first time somebody adds a page. So the answer is
// an id from a closed set, and the complete set is readable in two files:
//
//	web/design-system/third-party-policy.ts  SURFACES — every BROWSER surface (public, tenant, operator)
//	this file                                Surfaces — every SERVER surface
//
// Two files rather than one because the two halves have different readers and different lifecycles: the
// browser half also drives the Content-Security-Policy and the consent categories, and the server half
// is a property of which processes exist. `TestTheSurfaceEnumIsClosedAcrossBothHalves` asserts they are
// disjoint and that nothing in the code names a surface outside their union, so "closed" means closed
// across the whole system rather than within one language.
//
// # What a surface id may never be
//
// A path, a URL, a tenant, a customer name, a hostname, or a value derived from a request. It is a
// compile-time constant naming a component of this system.

// Surfaces is the closed set of server-side surfaces.
var Surfaces = []string{
	"platform.api",      // the P2/P4/P11 platform HTTP API served by agentd
	"admin.api",         // the P8 operator API, on its own handler and its own credential
	"console.bff",       // the customer console's server half
	"admin-console.bff", // the operator console's server half
	"platform.worker",   // background work with no request: the optimizer loop, the run queue
}

var validSurface = func() map[string]bool {
	m := make(map[string]bool, len(Surfaces))
	for _, s := range Surfaces {
		m[s] = true
	}
	return m
}()

// ValidServerSurface reports whether s is a member of the server half.
//
// A surface that is not a member is replaced by "unknown" before transmission rather than passed
// through — the same discipline as an unrecognised error code, and for the same reason: an id typed at
// a call site is the shape a leaked path would take.
func ValidServerSurface(s string) bool { return validSurface[s] }
