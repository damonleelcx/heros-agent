package config

import "testing"

// The secrets-management baseline (task 4.4): env vars override file-sourced secrets, so a secrets
// manager can inject keys into the process and they never need to live in a committed file.
func TestSecretEnvOverridesFileValues(t *testing.T) {
	c := Config{
		OpenAIAPIKey:    "from-file",
		QdrantAPIKey:    "from-file",
		Neo4jPassword:   "from-file",
		InboxSigningKey: "from-file",
	}
	t.Setenv("OPENAI_API_KEY", "from-secrets-manager")
	t.Setenv("QDRANT_API_KEY", "qdrant-secret")
	t.Setenv("NEO4J_PASSWORD", "neo4j-secret")
	t.Setenv("HEROS_INBOX_SIGNING_KEY", "hmac-secret")

	applySecretEnv(&c)

	for _, tc := range []struct{ name, got, want string }{
		{"OpenAIAPIKey", c.OpenAIAPIKey, "from-secrets-manager"},
		{"QdrantAPIKey", c.QdrantAPIKey, "qdrant-secret"},
		{"Neo4jPassword", c.Neo4jPassword, "neo4j-secret"},
		{"InboxSigningKey", c.InboxSigningKey, "hmac-secret"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// An empty/unset env var must NOT clobber a file-provided value (non-empty wins).
func TestEmptySecretEnvDoesNotClobber(t *testing.T) {
	c := Config{OpenAIAPIKey: "keep-me"}
	t.Setenv("OPENAI_API_KEY", "") // explicitly empty
	applySecretEnv(&c)
	if c.OpenAIAPIKey != "keep-me" {
		t.Fatalf("empty env clobbered file value: got %q", c.OpenAIAPIKey)
	}
}
