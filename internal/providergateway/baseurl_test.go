package providergateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/registry"
)

// baseurl_test.go fences the endpoint override.
//
// The property that matters most is the NEGATIVE one: a deployment that sets nothing must reach the
// same vendor endpoints it reached before this feature existed. Everything else here is about refusing
// a configuration that would send this deployment's provider credential somewhere it should not go.

// 🔴 THE NO-CHANGE FENCE. With no variables set, every provider keeps its vendor endpoint.
//
// This is the assertion that makes the feature safe to ship: the override is opt-in, and an
// unconfigured deployment must be byte-identical to one built before the option existed.
func TestWithNoEnvironmentSetEveryProviderKeepsItsVendorEndpoint(t *testing.T) {
	overrides, err := BaseURLOverridesFromEnv()
	if err != nil {
		t.Fatalf("BaseURLOverridesFromEnv on a clean environment: %v", err)
	}
	if overrides.Len() != 0 {
		t.Fatalf("a clean environment produced %d override(s): %v", overrides.Len(), overrides.Describe())
	}

	g := New(EnvSecrets{}, overrides.Options()...)
	for provider, want := range map[string]string{
		ProviderOpenAI:    "https://api.openai.com/v1",
		ProviderAnthropic: "https://api.anthropic.com/v1",
		ProviderBedrock:   "", // per-region; the adapter builds it from the credential
	} {
		if got := g.endpoint(provider, registry.ModelSpec{}); got != want {
			t.Errorf("%s endpoint = %q, want %q — an unconfigured deployment must reach the vendor",
				provider, got, want)
		}
	}
}

// 🔴 THE REGRESSION THIS NEARLY SHIPPED WITH. `HEROS_RELEASE_BASE_URL` already configures the release
// channel that clilink/upgrade reads, and it is NOT a provider.
//
// Under the obvious env scheme (`HEROS_<NAME>_BASE_URL`) the unknown-name check would have read
// `RELEASE` as a misspelled provider and refused to boot a deployment that had merely configured its
// release channel — a fence breaking the thing it exists to protect. The `HEROS_PROVIDER_` prefix is
// what makes the check safe, and this test is why the prefix cannot be "simplified" away later.
func TestTheReleaseChannelVariableIsNotMistakenForAProvider(t *testing.T) {
	t.Setenv("HEROS_RELEASE_BASE_URL", "https://releases.example.com")

	overrides, err := BaseURLOverridesFromEnv()
	if err != nil {
		t.Fatalf("HEROS_RELEASE_BASE_URL was read as a provider endpoint: %v\n"+
			"It configures the release channel. Refusing it here would fail the boot of any deployment "+
			"that sets it.", err)
	}
	if overrides.Len() != 0 {
		t.Fatalf("HEROS_RELEASE_BASE_URL produced an override: %v", overrides.Describe())
	}
}

func TestAnOverrideRedirectsOnlyItsOwnProvider(t *testing.T) {
	t.Setenv(BaseURLEnvName(ProviderOpenAI), "https://relay.example.com/v1")

	overrides, err := BaseURLOverridesFromEnv()
	if err != nil {
		t.Fatalf("BaseURLOverridesFromEnv: %v", err)
	}
	g := New(EnvSecrets{}, overrides.Options()...)

	if got := g.endpoint(ProviderOpenAI, registry.ModelSpec{}); got != "https://relay.example.com/v1" {
		t.Errorf("openai endpoint = %q, want the relay", got)
	}
	// 🔴 And nothing else moved. One provider's redirect must not drag its neighbours along.
	if got := g.endpoint(ProviderAnthropic, registry.ModelSpec{}); got != "https://api.anthropic.com/v1" {
		t.Errorf("anthropic endpoint = %q, want its untouched vendor default", got)
	}
}

// An explicitly empty value is "declared but not redirecting" — which is how both deploy substrates can
// declare the same env contract without either of them pointing anywhere.
func TestAnEmptyValueDeclaresTheVariableWithoutRedirecting(t *testing.T) {
	t.Setenv(BaseURLEnvName(ProviderOpenAI), "")

	overrides, err := BaseURLOverridesFromEnv()
	if err != nil {
		t.Fatalf("an empty value was treated as an error: %v", err)
	}
	if overrides.Len() != 0 {
		t.Fatalf("an empty value produced an override: %v", overrides.Describe())
	}
}

// 🔴 Every refusal below fails the BOOT rather than falling back to the vendor. Each names a way a
// credential could reach somewhere unintended.
func TestAMisconfiguredEndpointIsRefusedRatherThanIgnored(t *testing.T) {
	cases := map[string]struct {
		value    string
		mustName string
	}{
		"plaintext http to a remote host": {
			value:    "http://relay.example.com/v1",
			mustName: "https",
		},
		"no scheme": {
			value:    "relay.example.com/v1",
			mustName: "absolute URL",
		},
		"credentials in the URL": {
			value:    "https://user:secret@relay.example.com/v1",
			mustName: "credentials",
		},
		"a query the adapters would concatenate a path onto": {
			value:    "https://relay.example.com/v1?key=abc",
			mustName: "query",
		},
		"a scheme that is not http": {
			value:    "ftp://relay.example.com/v1",
			mustName: "http",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(BaseURLEnvName(ProviderOpenAI), tc.value)
			_, err := BaseURLOverridesFromEnv()
			if err == nil {
				t.Fatalf("%q was accepted. It must fail the boot: an endpoint this deployment's provider "+
					"credential is sent to cannot be wrong quietly", tc.value)
			}
			if !strings.Contains(err.Error(), tc.mustName) {
				t.Errorf("the refusal does not mention %q, so an operator cannot tell what to fix.\n"+
					"error: %v", tc.mustName, err)
			}
			if !strings.Contains(err.Error(), BaseURLEnvName(ProviderOpenAI)) {
				t.Errorf("the refusal does not name the variable.\nerror: %v", err)
			}
		})
	}
}

// http IS accepted on loopback — a relay running on the same host, or a test server.
func TestLoopbackMayUsePlaintext(t *testing.T) {
	for _, v := range []string{"http://127.0.0.1:8080/v1", "http://localhost:8080/v1", "http://[::1]:8080/v1"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(BaseURLEnvName(ProviderOpenAI), v)
			if _, err := BaseURLOverridesFromEnv(); err != nil {
				t.Errorf("%s was refused: %v — loopback plaintext puts nothing on a network", v, err)
			}
		})
	}
}

// 🔴 A variable naming something that is not a provider is REFUSED, not ignored. An ignored override
// leaves an operator certain they redirected traffic they did not redirect.
func TestAnUnknownProviderNameIsRefused(t *testing.T) {
	t.Setenv("HEROS_PROVIDER_OPENAI2_BASE_URL", "https://relay.example.com/v1")

	_, err := BaseURLOverridesFromEnv()
	if err == nil {
		t.Fatal("a variable naming a provider that does not exist was ignored. The operator believes " +
			"their traffic is redirected; the evidence that it is not is a bill from the vendor.")
	}
	// It must name the valid set, or the operator is left guessing at the spelling.
	for _, p := range Providers() {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("the refusal does not list %q among the valid providers.\nerror: %v", p, err)
		}
	}
}

// Providers is derived from the adapters, so a provider that can be reached can also be configured.
func TestEveryReachableProviderIsConfigurable(t *testing.T) {
	got := Providers()
	if len(got) != len(allAdapters()) {
		t.Fatalf("Providers() = %v but there are %d adapters — a provider with an adapter and no entry "+
			"here cannot be pointed anywhere", got, len(allAdapters()))
	}
	// One SUBTEST per provider: `t.Setenv` unsets at the end of the test that called it, so setting
	// several inside one test accumulates them and the second provider sees the first still set.
	for _, a := range allAdapters() {
		t.Run(a.name(), func(t *testing.T) {
			t.Setenv(BaseURLEnvName(a.name()), "https://relay.example.com/v1")
			o, err := BaseURLOverridesFromEnv()
			if err != nil {
				t.Fatalf("%s could not be configured: %v", a.name(), err)
			}
			if o.Len() != 1 {
				t.Fatalf("%s produced %d overrides, want 1", a.name(), o.Len())
			}
		})
	}
}

// The boot log has to name the address, because nothing else does — the console shows a provider NAME
// and never its endpoint.
func TestDescribeNamesTheProviderTheAddressAndTheVariable(t *testing.T) {
	t.Setenv(BaseURLEnvName(ProviderOpenAI), "https://relay.example.com/v1")
	o, err := BaseURLOverridesFromEnv()
	if err != nil {
		t.Fatalf("BaseURLOverridesFromEnv: %v", err)
	}
	lines := o.Describe()
	if len(lines) != 1 {
		t.Fatalf("Describe() = %v, want one line", lines)
	}
	for _, want := range []string{ProviderOpenAI, "https://relay.example.com/v1", BaseURLEnvName(ProviderOpenAI)} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the boot line omits %q — it is the only place an endpoint is ever stated.\nline: %s",
				want, lines[0])
		}
	}
}

// 🔴 THE END-TO-END FENCE. The tests above assert what `endpoint()` returns; this asserts that a real
// request built from the ENVIRONMENT actually lands on the redirected host.
//
// It is the difference between "the option was computed" and "the traffic moved", and it is the whole
// claim of this feature: a deployment holding a relay key must reach the relay. The httptest server is
// loopback, which is exactly why plaintext loopback is permitted rather than banned outright.
func TestARequestBuiltFromTheEnvironmentReachesTheRedirectedHost(t *testing.T) {
	var gotPath string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIBody)
	}))
	t.Cleanup(srv.Close)

	// The relay's address, exactly as a deployment would set it.
	t.Setenv(BaseURLEnvName(ProviderOpenAI), srv.URL+"/v1")

	overrides, err := BaseURLOverridesFromEnv()
	if err != nil {
		t.Fatalf("BaseURLOverridesFromEnv: %v", err)
	}
	creds := StaticSecrets{ProviderOpenAI: {APIKey: "sk-test-openai-secret-value"}}
	g := New(creds, append(overrides.Options(),
		withClock(func(context.Context, time.Duration) error { return nil }, func() float64 { return 1 }))...)

	entry := &registry.ModelEntry{Spec: registry.ModelSpec{Provider: ProviderOpenAI, ModelID: "gpt-5"}}
	resp, err := g.Complete(context.Background(), entry, simpleReq(), nil)
	if err != nil {
		t.Fatalf("Complete against the redirected endpoint: %v", err)
	}
	if resp == nil || resp.Content == "" {
		t.Fatalf("no response from the redirected endpoint: %+v", resp)
	}
	// It reached THIS server — the handler ran — and at the path the adapter appends to the base.
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions — the adapter must append its path to the "+
			"configured base, not to the vendor's", gotPath)
	}
	// And the credential travelled with it. A redirect that dropped the key would fail at the relay
	// for a reason that looks like a bad key.
	if !strings.Contains(gotAuth, "sk-test-openai-secret-value") {
		t.Errorf("the provider credential did not reach the redirected host (auth header %q)", gotAuth)
	}
}
