package providergateway

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestSecretsFromEnv_DefaultsToEnvWhenUnset(t *testing.T) {
	t.Setenv(EnvSecretsSource, "")
	s, err := NewSecretsFromEnv(context.Background())
	if err != nil {
		t.Fatalf("NewSecretsFromEnv: %v", err)
	}
	if _, ok := s.(EnvSecrets); !ok {
		t.Fatalf("got %T, want EnvSecrets — the no-configuration default must stay the laptop path", s)
	}
	if s.Describe().Kind != SourceKindEnv {
		t.Errorf("Describe().Kind = %q, want %q", s.Describe().Kind, SourceKindEnv)
	}
}

// The one that matters. A typo must not quietly become "read the env vars" — a deployment that
// believes it is on a secrets manager and is not is a level-1 regression bought with the convenience
// of not writing an error branch.
func TestSecretsFromEnv_AnUnknownSourceFailsClosedRatherThanFallingBackToEnv(t *testing.T) {
	for _, typo := range []string{"aws-secretsmanager", "vault", "AWS-SECRETS-MANAGER", "aws"} {
		t.Run(typo, func(t *testing.T) {
			t.Setenv(EnvSecretsSource, typo)
			s, err := NewSecretsFromEnv(context.Background())
			if err == nil {
				t.Fatalf("%s=%q was accepted and resolved to %T; it must fail closed", EnvSecretsSource, typo, s)
			}
			if s != nil {
				t.Errorf("a source was returned alongside the error: %T", s)
			}
			// The error must say what the valid values are — this is a deployment-time mistake, and the
			// person hitting it is holding a Helm chart, not the source.
			if !strings.Contains(err.Error(), SourceKindAWSSecretsManager) || !strings.Contains(err.Error(), SourceKindEnv) {
				t.Errorf("error does not name the valid sources: %v", err)
			}
		})
	}
}

func TestSecretsFromEnv_AWSRequiresSomewhereToLookForTheSecrets(t *testing.T) {
	t.Setenv(EnvSecretsSource, SourceKindAWSSecretsManager)
	t.Setenv(EnvSecretsAWSPrefix, "")
	t.Setenv(EnvSecretsAWSIDs, "")
	_, err := NewSecretsFromEnv(context.Background())
	if err == nil {
		t.Fatal("the AWS source was accepted with no prefix and no IDs; it has nothing to fetch")
	}
	if !strings.Contains(err.Error(), EnvSecretsAWSIDs) || !strings.Contains(err.Error(), EnvSecretsAWSPrefix) {
		t.Errorf("error does not tell the operator which variable to set: %v", err)
	}
}

func TestSecretsFromEnv_ExplicitIDsAreParsedAndRejectedLoudly(t *testing.T) {
	t.Setenv(EnvSecretsSource, SourceKindAWSSecretsManager)
	t.Setenv(EnvSecretsAWSRegion, "us-west-2")
	t.Setenv(EnvSecretsAWSPrefix, "")

	t.Run("valid", func(t *testing.T) {
		t.Setenv(EnvSecretsAWSIDs, " openai = heros/openai , anthropic=arn:aws:secretsmanager:us-west-2:1:secret:x ")
		s, err := NewSecretsFromEnv(context.Background())
		if err != nil {
			t.Fatalf("NewSecretsFromEnv: %v", err)
		}
		sm, ok := s.(*AWSSecretsManager)
		if !ok {
			t.Fatalf("got %T, want *AWSSecretsManager", s)
		}
		if sm.ids[ProviderOpenAI] != "heros/openai" {
			t.Errorf("openai -> %q, want the trimmed explicit ID", sm.ids[ProviderOpenAI])
		}
		if sm.ids[ProviderAnthropic] != "arn:aws:secretsmanager:us-west-2:1:secret:x" {
			t.Errorf("anthropic -> %q, want the full ARN", sm.ids[ProviderAnthropic])
		}
		if _, ok := sm.ids[ProviderBedrock]; ok {
			t.Error("bedrock was mapped; an explicit list must be exactly what was listed")
		}
	})

	t.Run("malformed", func(t *testing.T) {
		t.Setenv(EnvSecretsAWSIDs, "openai")
		if _, err := NewSecretsFromEnv(context.Background()); err == nil {
			t.Error("a malformed provider=id entry was accepted")
		}
	})
}

// The prefix path must cover every provider the gateway can dispatch to. If these two lists drift,
// the symptom is a provider that resolves and dispatches and then fails at the last step with "no
// credential" — which reads like an IAM problem and is not.
func TestSecretsFromEnv_PrefixCoversExactlyTheGatewaysProviders(t *testing.T) {
	t.Setenv(EnvSecretsSource, SourceKindAWSSecretsManager)
	t.Setenv(EnvSecretsAWSRegion, "eu-central-1")
	t.Setenv(EnvSecretsAWSIDs, "")
	t.Setenv(EnvSecretsAWSPrefix, "heros/prod/")

	s, err := NewSecretsFromEnv(context.Background())
	if err != nil {
		t.Fatalf("NewSecretsFromEnv: %v", err)
	}
	sm := s.(*AWSSecretsManager)

	got := make([]string, 0, len(sm.ids))
	for p := range sm.ids {
		got = append(got, p)
	}
	slices.Sort(got)

	// Every gateway provider is mapped — the original claim, and the one that keeps a provider from
	// dispatching and then failing at the last step with "no credential".
	for _, want := range SupportedProviders() {
		if sm.ids[want] == "" {
			t.Errorf("prefix mapped %v — provider %q is uncredentialable", got, want)
		}
	}
	// And the only names beyond them are the RESERVED ones. This is an equality, not a floor: an
	// unexpected key means the prefix expands to a secret nobody provisioned, and the operator finds out
	// when the fetch 404s. Reserved names were missing entirely until a live deployment resolved zero
	// billing credentials through this exact branch.
	want := append(SupportedProviders(), ReservedSecretNames()...)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("prefix mapped %v, want the gateway's providers plus the reserved names %v", got, want)
	}
	if sm.ids[ProviderBedrock] != "heros/prod/bedrock" {
		t.Errorf("bedrock -> %q, want prefix+provider", sm.ids[ProviderBedrock])
	}
	// The reserved names expand the same way — prefix + name, no special case.
	if sm.ids["billing_provider"] != "heros/prod/billing_provider" {
		t.Errorf("billing_provider -> %q, want prefix+name", sm.ids["billing_provider"])
	}
}

// SupportedProviders must be derived from the gateway's adapter list, not a second copy of it.
func TestSupportedProviders_MatchesTheAdaptersTheGatewayActuallyRegisters(t *testing.T) {
	g := New(StaticSecrets{})
	got := SupportedProviders()
	if len(got) != len(g.adapters) {
		t.Fatalf("SupportedProviders() = %v (%d) but the gateway registered %d adapters",
			got, len(got), len(g.adapters))
	}
	for _, p := range got {
		if _, ok := g.adapters[p]; !ok {
			t.Errorf("SupportedProviders() lists %q, which the gateway cannot dispatch to", p)
		}
	}
}

// Every source must be able to say what it is. This is the compiler's job at build time, but the
// assertion is here too because "implements the interface" and "returns a usable answer" are
// different claims — a Describe returning a zero SourceInfo would compile and report nothing.
func TestDescribe_EverySourceNamesItselfUsefully(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) { return 200, okSecret("arn:x", `{"api_key":"sk-x"}`) })
	awsSrc, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderOpenAI: "id"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	for _, s := range []Secrets{EnvSecrets{}, StaticSecrets{}, awsSrc} {
		info := s.Describe()
		if info.Kind == "" {
			t.Errorf("%T.Describe() returned no Kind; /readyz would report an anonymous source", s)
		}
		if !slices.Contains([]string{SourceKindEnv, SourceKindStatic, SourceKindAWSSecretsManager}, info.Kind) {
			t.Errorf("%T.Describe().Kind = %q is not one of the central enum's values", s, info.Kind)
		}
	}
}

// TestEveryReservedNameBelongsToANamespace is the outer half of the two-place drift fence.
//
// Each consuming package fences its OWN namespace — `internal/billing` the `billing_` names,
// `internal/adminidentity` the `admin_` ones — because only that package knows which of its constants
// resolve. What no consumer can see is a reserved name belonging to NO namespace: a typo, or a name
// added here for a consumer that was never written. Under the prefix form that name expands to a secret
// ID an operator is told to provision and nothing ever fetches.
//
// This package cannot import its consumers (the dependency runs one way), so it checks the property it
// can: every reserved name is in a namespace that has a fence.
func TestEveryReservedNameBelongsToANamespace(t *testing.T) {
	// Each prefix here has a matching per-namespace fence in the package that consumes it.
	fenced := []string{"billing_", "admin_"}
	for _, n := range ReservedSecretNames() {
		ok := false
		for _, p := range fenced {
			if strings.HasPrefix(n, p) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("reserved name %q is in no fenced namespace %v — no package's drift fence covers it, "+
				"so an operator would be told to provision a secret nothing resolves", n, fenced)
		}
	}
}
