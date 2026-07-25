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
type Credential struct {
	Identity string `json:"identity"`
	Token    string `json:"token"`
	Endpoint string `json:"endpoint"`
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
