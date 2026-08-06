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
	// MailConfigured reports whether this deployment can DELIVER a confirmation or password-reset link
	// (P28).
	//
	// 🔴 It is reported for the same reason `SelfServeSignup` is, and it matters more: a deployment with no
	// SMTP still signs people up and still accepts a `forgot password`, and the links go to an operator
	// surface instead of an inbox. That is a working configuration and it is emphatically not the one most
	// operators think they have. Held only in a log line, "why did nobody get their reset email" is a
	// question answered during an incident, from a terminal, by somebody who did not do the deploy.
	//
	// It is a VALUE and not a gate, like everything else here — but a `password` deployment with no mail is
	// the one combination where the honest word is *degraded*, because the front door has no recovery path.
	// `Describe` says so rather than leaving the reader to combine two fields.
	MailConfigured bool `json:"mail_configured"`
	// IdentityKind is the sign-in mechanism the CONSOLE is configured with, as the platform understands it.
	// Empty when this deployment does not declare one — the platform does not guess, because a guessed
	// answer here reads exactly like a checked one.
	IdentityKind string `json:"identity_kind,omitempty"`
}

// Describe returns the posture for a readiness surface. It never contains a credential: a `SeedResult`
// holds counts and the identifiers of entries it could not use, and no key material passes through it.
func (p Posture) Describe() map[string]any {
	out := map[string]any{
		"store":             string(p.Store),
		"self_serve_signup": p.SelfServeSignup,
		"mail_configured":   p.MailConfigured,
	}
	if p.IdentityKind != "" {
		out["identity_kind"] = p.IdentityKind
	}
	if !p.MailConfigured {
		// The consequence, spelled out, because "mail_configured: false" is a fact and this is what it
		// MEANS. An operator reading a health surface should not have to know which product features
		// depend on mail in order to understand what they are looking at.
		out["mail_detail"] = "confirmation and password-reset links cannot be delivered; they are held on " +
			"the operator surface. Set HEROS_SMTP_HOST and HEROS_SMTP_FROM to deliver them."
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
