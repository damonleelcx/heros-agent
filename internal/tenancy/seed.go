package tenancy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// seed.go demotes `cfg.TenantCredentials` from THE TRUTH to A SEED.
//
// # The decision, and the failure the other reading causes
//
// Two things now describe tenants: a configuration file and a database. Exactly one can win, and the
// database does. Configuration creates what is missing and never updates, never deletes.
//
// The opposite reading — configuration is authoritative and reconciles the database — has a concrete
// failure, not a theoretical one: *a customer signs up at 02:00; the 03:00 rolling restart reconciles
// against a file that does not mention them; their organization, their members and their credentials are
// deleted.* That is precisely the property this phase exists to create, destroyed by the mechanism meant
// to preserve it. Last-writer-wins is no better — it makes the outcome depend on deployment timing,
// which is the class of bug that only reproduces in production.
//
// # 🔴 A partial seed refuses to serve
//
// If three of ten configured entries fail, the platform does not start. A platform that starts and then
// rejects seven customers looks broken to seven customers; a platform that will not start says exactly
// what is wrong, once, to the person doing the deploy. `identity.ts` already takes this posture for a
// development identity provider in production, and it is the same argument.
//
// # What the seed does with the configured key
//
// It hashes it. The plaintext in the configuration file keeps working because verification hashes what
// it receives and compares stored hashes — so an existing deployment's keys authenticate after the
// upgrade without anybody rotating anything. The seed is the only code that ever reads a configured
// plaintext, and it does not keep one.

// SeedEntry is one configured credential, in the shape the seed needs.
type SeedEntry struct {
	TenantID string
	APIKey   string
	Role     string
	KeyID    string
}

// SeedResult reports what the seed did, so a deployment can show the difference between "brought the
// tenants up" and "found them already there" instead of logging the same line either way.
//
// It is a VALUE the readiness surface renders, not a log line. A configured outcome that is only visible
// in a log is an outcome nobody checks before an incident.
type SeedResult struct {
	TenantsCreated     int      `json:"tenants_created"`
	TenantsExisting    int      `json:"tenants_existing"`
	CredentialsCreated int      `json:"credentials_created"`
	CredentialsPresent int      `json:"credentials_present"`
	Skipped            []string `json:"skipped,omitempty"`
}

// Total is the number of configured entries the seed considered.
func (r SeedResult) Total() int { return r.CredentialsCreated + r.CredentialsPresent + len(r.Skipped) }

// Summary is one human line for a boot log, beside the machine-readable value.
func (r SeedResult) Summary() string {
	return fmt.Sprintf("tenants: %d created, %d already present; credentials: %d created, %d already present",
		r.TenantsCreated, r.TenantsExisting, r.CredentialsCreated, r.CredentialsPresent)
}

// Seed applies configured tenant credentials to the store, create-if-absent.
//
// It is idempotent by construction rather than by a ledger: every write is guarded by a read, and an
// entry that already exists is left untouched — including its name, its status and its role. Running it
// twice changes nothing, which the suite asserts by comparing the store before and after.
func Seed(store Store, entries []SeedEntry, at time.Time) (SeedResult, error) {
	var res SeedResult
	if store == nil {
		return res, errors.New("tenancy: cannot seed a nil store")
	}

	// Deterministic order, so a failure names the same entry on every run and two deployments produce
	// the same log.
	sorted := make([]SeedEntry, 0, len(entries))
	sorted = append(sorted, entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].TenantID != sorted[j].TenantID {
			return sorted[i].TenantID < sorted[j].TenantID
		}
		return sorted[i].KeyID < sorted[j].KeyID
	})

	for _, e := range sorted {
		tenantID := strings.TrimSpace(e.TenantID)
		key := strings.TrimSpace(e.APIKey)
		if tenantID == "" || key == "" {
			// A configured entry with no tenant or no key cannot become a row. It is SKIPPED and
			// NAMED — not silently dropped, because a credential somebody believes they configured and
			// that does not work is a support call with no evidence attached to it.
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s/%s: missing tenant_id or api_key", e.TenantID, e.KeyID))
			continue
		}

		if _, err := store.GetTenant(tenantID); err != nil {
			if !errors.Is(err, ErrNotFound) {
				return res, fmt.Errorf("tenancy: seed could not read organization %q: %w", tenantID, err)
			}
			// The name defaults to the id. The configuration file has nowhere to put a human name, and
			// inventing one ("Acme Inc") from an id would be a guess a support conversation then repeats.
			if _, err := store.CreateTenant(Tenant{TenantID: tenantID, Name: tenantID, CreatedAt: at}); err != nil {
				if errors.Is(err, ErrExists) {
					// Another replica seeded it between the read and the write. That is the expected
					// outcome of a concurrent boot, not a failure.
					res.TenantsExisting++
				} else {
					return res, fmt.Errorf("tenancy: seed could not create organization %q: %w", tenantID, err)
				}
			} else {
				res.TenantsCreated++
			}
		} else {
			// 🔴 Present, so untouched. Not renamed, not reactivated, not re-roled. A seed that
			// "corrected" an existing row would reintroduce exactly the reconciliation this file rejects.
			res.TenantsExisting++
		}

		hash := HashSecret(key)
		if _, err := store.ResolveCredential(hash); err == nil {
			res.CredentialsPresent++
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return res, fmt.Errorf("tenancy: seed could not read the credential for %q: %w", tenantID, err)
		}

		role := Role(strings.TrimSpace(e.Role))
		if role == "" {
			role = RoleMember
		}
		if !KnownRole(role) {
			// The pre-P27 configuration vocabulary was "admin" | "member". Anything else is a
			// configuration mistake, and a seed that quietly downgraded it would grant less authority
			// than the operator believes they configured.
			return res, fmt.Errorf("tenancy: configured credential %s/%s has unknown role %q", tenantID, e.KeyID, e.Role)
		}

		label := strings.TrimSpace(e.KeyID)
		if label == "" {
			label = "configured credential"
		}
		// No user id: a credential from a configuration file belongs to the organization, not to a
		// person. It is a MACHINE credential, it is listed as one, and member removal does not touch it.
		if _, err := store.CreateCredential(Credential{
			CredentialID: newID("cred"),
			TenantID:     tenantID,
			Label:        label,
			Role:         role,
			Hash:         hash,
			CreatedAt:    at,
		}); err != nil {
			if errors.Is(err, ErrExists) {
				res.CredentialsPresent++
			} else {
				return res, fmt.Errorf("tenancy: seed could not create the credential for %q: %w", tenantID, err)
			}
		} else {
			res.CredentialsCreated++
		}
	}
	return res, nil
}
