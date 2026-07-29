package proposal

// Origin records WHO initiated a change: the catalog, or a person (P13 13c, `authored-change` spec
// requirement "An authored change SHALL record its origin and actor").
//
// # Why this is a field on the candidate and not on the configuration
//
// `config_hash` is purely structural. A configuration a user authored and a byte-identical one an
// operator proposed denote the SAME configuration, and must therefore be the SAME measurement — scored
// once, cached once, comparable to each other. Putting Origin in the hashed shape would fork identity
// by authorship: the same configuration would hash two ways, P0's golden vectors would move for every
// spec that predates the field, and "have we already measured this?" would stop having an answer.
//
// So Origin travels on the candidate, the transform record, and the delivery record — everywhere the
// question "who did this, and may we say anything about it" is asked — and nowhere near the bytes that
// answer "which configuration is this". `TestOriginDoesNotAffectConfigHash` is the machine-enforced
// version of this paragraph.
type Origin string

const (
	// OriginOperator is the default: a catalog operator nominated this candidate from a diagnosis or a
	// structural signal. The zero value normalizes to this, so every pre-13c construction site keeps
	// meaning exactly what it meant.
	OriginOperator Origin = "operator"
	// OriginUser is a change a person authored directly.
	OriginUser Origin = "user"
)

// Normalized maps the zero value onto OriginOperator. Callers that predate 13c construct a Candidate
// without an Origin, and their candidates are operator-originated by construction — reading the zero
// value as "unknown" would invent a third state nothing produces.
func (o Origin) Normalized() Origin {
	if o == "" {
		return OriginOperator
	}
	return o
}

// IsUser reports whether a person authored this change.
func (o Origin) IsUser() bool { return o.Normalized() == OriginUser }

// Actor is the acting identity behind a user-originated change. It is recorded, never hashed.
//
// Empty for an operator-originated candidate: there is no person to name, and inventing a synthetic
// "system" actor would make an audit query for "changes people made" quietly wrong.
type Actor struct {
	// ID is the acting identity (a console session's subject, or a CLI-linked identity).
	ID string `json:"id,omitempty"`
	// TenantID scopes the actor. Request scope never comes from a client-supplied tenant id (P9's BFF
	// rule); this field carries what the server already resolved.
	TenantID string `json:"tenant_id,omitempty"`
}

// Zero reports whether no actor is recorded.
func (a Actor) Zero() bool { return a.ID == "" && a.TenantID == "" }
