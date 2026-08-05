package launch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

var bootAt = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func configWith(creds ...config.TenantCredential) config.Config {
	return config.Config{AuthMode: "required", TenantCredentials: creds}
}

// TestAConfiguredCredentialStillAuthenticatesAfterTheSeed is the upgrade guarantee, at the boot level.
//
// This is the one that would break every existing deployment if it were wrong: the keys customers
// already hold are in a configuration file, and after P27 they must resolve through the durable store
// without anybody rotating anything.
func TestAConfiguredCredentialStillAuthenticatesAfterTheSeed(t *testing.T) {
	cfg := configWith(
		config.TenantCredential{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"},
		config.TenantCredential{TenantID: "globex", APIKey: "globex-key", Role: "member", KeyID: "k2"},
	)
	sys, err := buildAccountSystem(cfg, nil, bootAt)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	handler := api.New(nil, cfg)
	handler.AuthRegistry().WithSource(sys.Store)

	p, ok := handler.AuthRegistry().Lookup("acme-key")
	if !ok {
		t.Fatal("a credential that worked before the upgrade stopped working after it")
	}
	if p.TenantID != "acme" || p.Role != "admin" {
		t.Fatalf("the seeded principal is wrong: %+v", p)
	}
	if p.UserID != "" {
		t.Error("a configured credential belongs to the organization, not to a person")
	}
	if _, ok := handler.AuthRegistry().Lookup("globex-key"); !ok {
		t.Error("the second configured credential did not seed")
	}
	if _, ok := handler.AuthRegistry().Lookup("not-a-key"); ok {
		t.Error("an unknown key authenticated")
	}
}

// TestTheSeedIsIdempotentAcrossTwoBoots: the second boot must create nothing and break nothing.
func TestTheSeedIsIdempotentAcrossTwoBoots(t *testing.T) {
	cfg := configWith(config.TenantCredential{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"})

	first, err := buildAccountSystem(cfg, nil, bootAt)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	if first.Posture.Seed.TenantsCreated != 1 || first.Posture.Seed.CredentialsCreated != 1 {
		t.Fatalf("first boot seeded %+v", first.Posture.Seed)
	}

	// A second boot against the SAME store — which is what a restart is on a durable deployment.
	second, err := tenancy.Seed(first.Store, []tenancy.SeedEntry{
		{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"},
	}, bootAt)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if second.TenantsCreated != 0 || second.CredentialsCreated != 0 {
		t.Errorf("the second boot wrote something: %+v", second)
	}
	if second.TenantsExisting != 1 || second.CredentialsPresent != 1 {
		t.Errorf("the second boot did not recognise what was there: %+v", second)
	}
}

// TestTheReadinessSurfaceReportsThePostureAsValues is 3.5.
//
// 🔴 The assertion is on the VALUES, not on the absence of an error. A posture visible only in a log is
// a posture nobody checks before an incident — they check it during one, from a terminal, at the worst
// possible time.
func TestTheReadinessSurfaceReportsThePostureAsValues(t *testing.T) {
	cfg := configWith(config.TenantCredential{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"})
	sys, err := buildAccountSystem(cfg, nil, bootAt)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	handler := api.New(nil, config.Config{})
	handler.SetAccountSystem(sys.Posture)

	rec := httptest.NewRecorder()
	handler.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz: %d — %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccountSystem struct {
			Store           string `json:"store"`
			SelfServeSignup bool   `json:"self_serve_signup"`
			Seed            struct {
				TenantsCreated     int `json:"tenants_created"`
				CredentialsCreated int `json:"credentials_created"`
			} `json:"seed"`
		} `json:"account_system"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, rec.Body.String())
	}
	if body.AccountSystem.Store != string(tenancy.StoreMemory) {
		t.Errorf("store posture is %q, want %q — a deployment that believes it is durable must find "+
			"out here rather than after a restart", body.AccountSystem.Store, tenancy.StoreMemory)
	}
	if body.AccountSystem.SelfServeSignup {
		t.Error("self-serve sign-up defaulted ON; an air-gapped install must not grow a registration " +
			"form by upgrading")
	}
	if body.AccountSystem.Seed.TenantsCreated != 1 || body.AccountSystem.Seed.CredentialsCreated != 1 {
		t.Errorf("the seed outcome is not reported: %+v", body.AccountSystem.Seed)
	}
}

// TestSelfServeIsDeclaredNeverInferred.
func TestSelfServeIsDeclaredNeverInferred(t *testing.T) {
	if SelfServeEnabled() {
		t.Fatal("unset must mean OFF")
	}
	for _, on := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(SelfServeSignupEnv, on)
		if !SelfServeEnabled() {
			t.Errorf("%q should enable self-serve", on)
		}
	}
	for _, off := range []string{"", "0", "false", "no", "maybe", "please"} {
		t.Setenv(SelfServeSignupEnv, off)
		if SelfServeEnabled() {
			t.Errorf("%q must NOT enable self-serve — only an explicit affirmative does", off)
		}
	}
}

// TestAnUnusableConfiguredCredentialFailsTheBoot: a role nobody can honour is not silently downgraded.
func TestAnUnusableConfiguredCredentialFailsTheBoot(t *testing.T) {
	cfg := configWith(config.TenantCredential{TenantID: "acme", APIKey: "acme-key", Role: "superadmin", KeyID: "k1"})
	if _, err := buildAccountSystem(cfg, nil, bootAt); err == nil {
		t.Fatal("a configured credential with an unknown role was seeded anyway; seeding it as `member` " +
			"would grant less authority than the operator believes they configured")
	}
}

// TestABlankConfiguredEntryIsNamedRatherThanDropped.
func TestABlankConfiguredEntryIsNamedRatherThanDropped(t *testing.T) {
	cfg := configWith(
		config.TenantCredential{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"},
		config.TenantCredential{TenantID: "", APIKey: "orphan", KeyID: "k2"},
	)
	sys, err := buildAccountSystem(cfg, nil, bootAt)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(sys.Posture.Seed.Skipped) != 1 {
		t.Fatalf("the unusable entry was not named: %+v", sys.Posture.Seed)
	}
	// And it reaches the readiness surface, where somebody will actually see it.
	described := sys.Posture.Describe()
	seed, _ := described["seed"].(map[string]any)
	if _, ok := seed["skipped"]; !ok {
		t.Error("a skipped credential is not on the readiness surface — a credential somebody believes " +
			"they configured and that silently does not work is a support call with no evidence")
	}
}
