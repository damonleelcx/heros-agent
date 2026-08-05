package tenancy

// posture.go is what a deployment REPORTS about its account system, as values rather than as the
// absence of an error.
//
// # Why this exists at all
//
// Three things about P27 differ per deployment shape, and all three used to be inferable only by reading
// the process's environment: whether identity is durable or in-memory, whether an unmapped verified
// identity may create an organization, and what the boot seed actually did. A capability that is
// silently on in one shape and off in another is the failure P19's capability ledger exists to prevent,
// and a configured value visible only in a log is a value nobody checks before an incident — they check
// it during one, from a terminal, at the worst possible time.
//
// So the posture is a value on `/readyz`. It is deliberately NOT a gate: a deployment with self-serve
// off is not degraded, it is configured. Reporting it beside `secrets_source` and `billing_provider` —
// which are the same kind of fact — keeps that distinction.

// StoreKind names which implementation of Store this deployment is running.
type StoreKind string

const (
	// StoreMemory is the in-process store. Honest for a single-container deployment and stated so:
	// a restart empties it, which is exactly what the console's session map already does today.
	StoreMemory StoreKind = "memory"
	// StorePostgres is the durable store.
	StorePostgres StoreKind = "postgres"
)

// Posture is the account system's reported configuration.
type Posture struct {
	// Store is which implementation is live. A deployment that believes it is durable and reads
	// "memory" here has found its bug before a customer did.
	Store StoreKind `json:"store"`
	// SelfServeSignup reports whether a verified identity that maps to no organization may create one.
	//
	// 🔴 It is a DECLARED value, never inferred from whether an identity provider is configured.
	// Inferred security posture is the class of decision that is wrong quietly — and the answer differs
	// across the three deployment shapes this product ships in, so no single inference is right.
	SelfServeSignup bool `json:"self_serve_signup"`
	// Seed is what the boot seed did. Absent when this deployment seeds nothing.
	Seed *SeedResult `json:"seed,omitempty"`
}

// Describe returns the posture for a readiness surface. It never contains a credential: a `SeedResult`
// holds counts and the identifiers of entries it could not use, and no key material passes through it.
func (p Posture) Describe() map[string]any {
	out := map[string]any{
		"store":             string(p.Store),
		"self_serve_signup": p.SelfServeSignup,
	}
	if p.Seed != nil {
		seed := map[string]any{
			"tenants_created":     p.Seed.TenantsCreated,
			"tenants_existing":    p.Seed.TenantsExisting,
			"credentials_created": p.Seed.CredentialsCreated,
			"credentials_present": p.Seed.CredentialsPresent,
		}
		if len(p.Seed.Skipped) > 0 {
			// Named, not counted. A configured credential somebody believes works and that silently
			// does not is a support call with no evidence attached to it.
			seed["skipped"] = p.Seed.Skipped
		}
		out["seed"] = seed
	}
	return out
}
