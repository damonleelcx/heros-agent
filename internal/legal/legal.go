// Package legal is the consent-record domain: what a legal document's identity is, which version is in
// force, and whether an acceptance a client submitted refers to a document this deployment actually
// serves.
//
// # 🔴 The one rule this package exists to enforce
//
// **The submitted `content_hash` is validated server-side against the manifest.** Without that check the
// consent record says whatever the browser said, and its audit value is zero — an attacker, a stale tab,
// or a bug could record an acceptance of text that was never shown.
//
// Everything else here is in service of being able to make that check: reading the manifest the console
// publishes, knowing which version is in force, and deciding what a principal still owes.
//
// # Why the manifest and not the database
//
// A legal document's text lives in the console image (ADR-011). The platform does not have a copy and
// must not acquire one — a second copy is a second answer to "what does v1.0.0 say", and the first time
// they disagreed the record would cite one and the reader would see the other.
//
// So the platform reads `/legal/manifest.json` from the console it is deployed alongside, and validates
// against that. The manifest is derived from the same corpus load that renders the pages, so a document
// and its manifest entry cannot drift apart.
//
// # What this package deliberately does not do
//
//   - It does not RENDER a document, and holds no document text. It holds identities.
//   - It does not decide materiality. That is a declared field in the document's own front matter,
//     because no machine can judge it (Decision 3).
//   - It does not talk to a database. Storage is `Store`, injected, so the domain rules are testable
//     without one and the SQL lives in one place.
package legal

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind is a legal document kind. A closed set: adding one is a decision, not a new file.
type Kind string

const (
	KindTerms   Kind = "terms"
	KindPrivacy Kind = "privacy"
)

// Kinds is every published kind, in the order a customer meets them.
var Kinds = []Kind{KindTerms, KindPrivacy}

// ValidKind reports whether k is a published kind.
func ValidKind(k Kind) bool {
	for _, known := range Kinds {
		if known == k {
			return true
		}
	}
	return false
}

// Method is how an acceptance was made. Closed, for the same reason Kind is.
type Method string

const (
	MethodSignIn     Method = "signin"
	MethodCheckout   Method = "checkout"
	MethodPlanChange Method = "plan_change"
	MethodAPI        Method = "api"
)

// ValidMethod reports whether m is a recorded method.
func ValidMethod(m Method) bool {
	switch m {
	case MethodSignIn, MethodCheckout, MethodPlanChange, MethodAPI:
		return true
	}
	return false
}

// Version is one published version of one document, as the manifest describes it.
type Version struct {
	Version       string `json:"version"`
	EffectiveDate string `json:"effective_date"`
	Hash          string `json:"hash"`
	Route         string `json:"route"`
	// Material is the DECLARED materiality of this publication. It decides whether an existing
	// principal is asked to accept again — and nothing infers it.
	Material bool `json:"material"`
	// Supersedes is the version this one replaces, or "none" for the first.
	Supersedes string `json:"supersedes"`
	// Current is the manifest's own statement of which version is in force.
	Current bool `json:"current"`
}

// Manifest is `/legal/manifest.json`: every kind, every version, with hashes.
type Manifest struct {
	Schema        string             `json:"schema"`
	GeneratedFrom string             `json:"generated_from"`
	Kinds         map[Kind][]Version `json:"kinds"`
}

// Errors this package returns. Each names a distinct refusal, so a support engineer can answer "why was
// this acceptance rejected" without reading code.
var (
	// ErrUnknownKind: the submitted kind is not a published document kind.
	ErrUnknownKind = errors.New("legal: unknown document kind")
	// ErrUnknownVersion: the submitted version is not published for that kind.
	ErrUnknownVersion = errors.New("legal: no such published version")
	// ErrHashMismatch: 🔴 THE load-bearing refusal. The client submitted a hash for a version it was
	// not shown. Without this check the record says whatever the browser said.
	ErrHashMismatch = errors.New("legal: submitted content hash does not match the published document")
	// ErrInvalidMethod: the acceptance names a commitment moment that is not in the vocabulary.
	ErrInvalidMethod = errors.New("legal: unknown acceptance method")
	// ErrManifestUnavailable: the manifest could not be read. Deliberately distinct from a mismatch —
	// "we cannot check" and "the check failed" have different remedies, and conflating them would let an
	// outage look like an attack.
	ErrManifestUnavailable = errors.New("legal: the document manifest is unavailable")
	// ErrNoPublishedVersion: the kind exists but has no published version at all.
	ErrNoPublishedVersion = errors.New("legal: no published version for this document kind")
)

// Acceptance is one recorded consent. It carries no email, no name and no free text — see the migration
// header for why that is the schema rather than a habit.
type Acceptance struct {
	ID              string
	TenantID        string
	PrincipalID     string
	DocumentKind    Kind
	DocumentVersion string
	ContentHash     string
	AcceptedAt      time.Time
	Method          Method
	// SupersededBy is set when a MATERIAL later version is published.
	SupersededBy string
	// SubjectErasedAt is set when the subject was erased. The evidentiary fields above stay.
	SubjectErasedAt *time.Time
}

// Erased reports whether this record's subject has been tombstoned.
func (a Acceptance) Erased() bool { return a.SubjectErasedAt != nil }

// Current returns the version in force for a kind, as of `asOf` (YYYY-MM-DD).
//
// The manifest states which version is current, and that statement is preferred — it comes from the same
// render pass that produced the pages, so it cannot disagree with what a reader saw. The date comparison
// is the fallback for a manifest that predates the field, and it applies the same rule: the highest
// version whose effective date has arrived.
func (m Manifest) Current(kind Kind, asOf string) (Version, error) {
	versions, ok := m.Kinds[kind]
	if !ok || len(versions) == 0 {
		return Version{}, fmt.Errorf("%w: %s", ErrNoPublishedVersion, kind)
	}
	for _, v := range versions {
		if v.Current {
			return v, nil
		}
	}
	sorted := append([]Version(nil), versions...)
	sort.Slice(sorted, func(i, j int) bool { return compareVersions(sorted[i].Version, sorted[j].Version) < 0 })
	var live Version
	var found bool
	for _, v := range sorted {
		if v.EffectiveDate <= asOf {
			live, found = v, true
		}
	}
	if !found {
		// Every version is future-dated. Serve the earliest rather than nothing: a document published
		// ahead of its date is readable, and refusing here would make the gate unusable on day one.
		return sorted[0], nil
	}
	return live, nil
}

// Lookup finds a specific published version.
func (m Manifest) Lookup(kind Kind, version string) (Version, error) {
	if !ValidKind(kind) {
		return Version{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	for _, v := range m.Kinds[kind] {
		if v.Version == version {
			return v, nil
		}
	}
	return Version{}, fmt.Errorf("%w: %s %s", ErrUnknownVersion, kind, version)
}

// Validate is the server-side check every acceptance passes before it is written.
//
// 🔴 It is the reason this package exists. A client submits (kind, version, hash); this resolves the
// triple against the manifest the SERVER knows and refuses anything that does not match exactly.
//
// The three refusals are kept apart on purpose. "No such kind", "no such version" and "wrong hash for a
// real version" are three different situations: the first two are a malformed or stale client, and the
// third is the one that means somebody was shown different text from the one they are accepting.
func (m Manifest) Validate(kind Kind, version, hash string) (Version, error) {
	published, err := m.Lookup(kind, version)
	if err != nil {
		return Version{}, err
	}
	if !strings.EqualFold(published.Hash, hash) {
		return Version{}, fmt.Errorf(
			"%w: %s %s is published as %s, the client submitted %s",
			ErrHashMismatch, kind, version, short(published.Hash), short(hash),
		)
	}
	return published, nil
}

// Pending reports which kinds a principal must still accept, given what they have already accepted.
//
// The rule, and the two halves that matter:
//
//   - An acceptance of the CURRENT version, not superseded, satisfies that kind.
//   - Anything else — no acceptance, an acceptance of an older version, or an acceptance superseded by a
//     material publication — leaves the kind pending.
//
// A NON-MATERIAL publication does not appear here, because it never sets `superseded_by`. That is
// Decision 3 arriving at the place it actually bites: a typo fix must not push a consent interstitial at
// every customer.
func (m Manifest) Pending(accepted []Acceptance, asOf string) []Version {
	have := map[Kind]Acceptance{}
	for _, a := range accepted {
		if a.SupersededBy != "" {
			continue
		}
		// The most recent unsuperseded acceptance per kind wins. Records are append-only, so there can
		// be several; the latest is the one in force.
		if prior, ok := have[a.DocumentKind]; !ok || a.AcceptedAt.After(prior.AcceptedAt) {
			have[a.DocumentKind] = a
		}
	}

	var pending []Version
	for _, kind := range Kinds {
		current, err := m.Current(kind, asOf)
		if err != nil {
			// A kind with no published version cannot be pending. Nothing to accept is not the same as
			// something outstanding, and reporting it as pending would produce a gate nobody can clear.
			continue
		}
		a, ok := have[kind]
		if ok && a.DocumentVersion == current.Version && strings.EqualFold(a.ContentHash, current.Hash) {
			continue
		}
		pending = append(pending, current)
	}
	return pending
}

// short renders a hash for a message. The full value is never echoed: a refusal message travels into
// logs, and a log line that carries both hashes makes the comparison somebody else's to make.
func short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12] + "…"
}

// compareVersions orders MAJOR.MINOR.PATCH numerically. String order puts 1.10.0 before 1.9.0, which is
// the bug this function exists to not have.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}
