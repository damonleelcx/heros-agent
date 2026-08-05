package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// credential.go stores the platform token on disk under the user's config dir, 0600. Writing and
// reading the file is net-free (it lives in the cli package so `status` can report identity without
// pulling in the network); the token is VALIDATED over the network by `login`, which lives in the net
// package and calls SaveCredential here after a successful validation.
//
// 🔴 The token value is written to disk but never emitted: no command prints it, no payload carries it,
// and `status` reports only the non-secret identity. The endpoint is pinned to https://heros-agent.space
// and stored alongside so a stale credential can be recognized if the pin ever changes.

// Credential is the stored platform identity.
//
// 🔴 The three P27 fields are ADDITIVE and every one is `omitempty` (task 13.6). A credential written by
// an older binary has none of them and still loads — `Identity`, `Token` and `Endpoint` keep their names,
// meanings and values, which is the same discipline the `whoami` response follows for the same reason:
// somebody has this file on disk already and an upgrade must not log them out.
type Credential struct {
	Identity string `json:"identity"`
	Token    string `json:"token"`
	Endpoint string `json:"endpoint"`

	// OrganizationName is what the customer calls their organization. A DISPLAY value: `status` shows it
	// so a person with two of them can tell which terminal is which, and nothing routes on it.
	OrganizationName string `json:"organization_name,omitempty"`
	// UserID is the acting person. ABSENT for a machine credential, never a placeholder — a placeholder
	// in an attribution field reads as a person whose id we failed to record.
	UserID string `json:"user_id,omitempty"`
	// Kind is "personal" or "machine". Stated rather than inferred from UserID being empty, because the
	// difference decides whether removing somebody ends this login and a reader must not have to derive
	// it from a blank field.
	Kind string `json:"credential_kind,omitempty"`
}

// credentialPath returns the on-disk credential location, honoring $HEROS_CONFIG_DIR for tests.
func credentialPath() string {
	if d := os.Getenv("HEROS_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "credential.json")
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "heros", "credential.json")
}

// SaveCredential writes the credential 0600. Called by `login` after the token validates.
func SaveCredential(c Credential) error {
	path := credentialPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return operational("save credential: mkdir", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return operational("save credential: marshal", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return operational("save credential: write", err)
	}
	return nil
}

// LoadCredential reads the stored credential. ok is false when none exists (not authenticated).
func LoadCredential() (Credential, bool) {
	b, err := os.ReadFile(credentialPath())
	if err != nil {
		return Credential{}, false
	}
	var c Credential
	if err := json.Unmarshal(b, &c); err != nil {
		return Credential{}, false
	}
	if c.Token == "" || c.Endpoint != runlink.PlatformBaseURL {
		// A credential for a different endpoint is not valid for this pinned build.
		return Credential{}, false
	}
	return c, true
}

// storedIdentity returns the non-secret identity of the stored credential, if any.
func storedIdentity() (string, bool) {
	c, ok := LoadCredential()
	if !ok {
		return "", false
	}
	return c.Identity, true
}
