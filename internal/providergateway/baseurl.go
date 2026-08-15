package providergateway

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
)

// baseurl.go lets a deployment point a provider at a different endpoint — an API relay, a regional
// gateway, a corporate egress proxy — without a code change.
//
// # Why this exists
//
// `WithBaseURL` has been on the Gateway since it was written, and until now its ONLY caller anywhere
// in the tree was `cmd/demo/runmonitor` pointing at an httptest stub. `internal/launch` never called
// it, `ModelSpec` carries no endpoint field, and `defaultBaseURL` hardcodes the vendor URLs — so a
// deployment holding a relay key had no way to use it. The key would be sent to `api.openai.com`,
// which rejects it, and the operator learns nothing about their relay.
//
// # 🔴 Why the env name is namespaced under HEROS_PROVIDER_
//
// The obvious spelling is `HEROS_<PROVIDER>_BASE_URL`. It is taken: `HEROS_RELEASE_BASE_URL` already
// configures the release channel that `clilink/upgrade` reads. Under the obvious scheme the unknown-name
// check below would read `RELEASE` as a misspelled provider and REFUSE TO BOOT a deployment that had
// merely configured its release channel — a fence breaking the thing it was added to protect.
//
// `HEROS_PROVIDER_<NAME>_BASE_URL` cannot collide, which is what makes the unknown-name check safe to
// have at all.
//
// # What it refuses, and why refusing beats defaulting
//
// Every rejection below fails the BOOT rather than falling back to the vendor default. A base URL is
// where this deployment's provider credential gets sent: silently ignoring a malformed one means the
// operator believes traffic goes to their relay while the key goes to the vendor, and silently
// accepting a plaintext one puts that key on the wire in clear. Neither is a state to discover later
// from a bill or a leak.

// Providers returns the closed set this gateway can reach, derived from the adapters themselves.
//
// From `allAdapters` rather than a second list of literals: a provider whose adapter exists but which
// this function forgot would be un-configurable, and one listed here without an adapter would accept
// configuration that can never take effect.
func Providers() []string {
	as := allAdapters()
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.name())
	}
	sort.Strings(out)
	return out
}

const (
	baseURLEnvPrefix = "HEROS_PROVIDER_"
	baseURLEnvSuffix = "_BASE_URL"
)

// BaseURLEnvName is the variable that overrides one provider's endpoint. Exported so a deployment
// manifest, a document and an error message cannot disagree about the spelling.
func BaseURLEnvName(provider string) string {
	return baseURLEnvPrefix + strings.ToUpper(provider) + baseURLEnvSuffix
}

// BaseURLOverrides is the set of endpoints a deployment has redirected.
//
// Empty is the normal case and means every provider keeps its vendor default — the zero value is
// therefore the behaviour this deployment had before the file existed.
type BaseURLOverrides struct {
	byProvider map[string]string
}

// BaseURLOverridesFromEnv reads one variable per known provider.
//
// 🔴 It also refuses a variable that matches the pattern and names something that is NOT a provider.
// An ignored `HEROS_PROVIDER_OPENAI2_BASE_URL` leaves an operator certain they redirected traffic they
// did not redirect, and the evidence that they did not is a bill from the vendor they were trying to
// stop using.
func BaseURLOverridesFromEnv() (BaseURLOverrides, error) {
	known := map[string]bool{}
	for _, p := range Providers() {
		known[p] = true
	}

	out := BaseURLOverrides{byProvider: map[string]string{}}
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, baseURLEnvPrefix) || !strings.HasSuffix(name, baseURLEnvSuffix) {
			continue
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(name, baseURLEnvPrefix), baseURLEnvSuffix)
		provider := strings.ToLower(middle)
		if !known[provider] {
			return BaseURLOverrides{}, fmt.Errorf(
				"%s names %q, which is not a provider this gateway can reach. It is one of %s. "+
					"Refusing rather than ignoring it: an ignored endpoint override leaves the credential "+
					"going to the vendor while the operator believes it goes somewhere else",
				name, provider, strings.Join(Providers(), ", "))
		}
		// An explicitly EMPTY value is "no override", not an error. That is how a manifest declares the
		// variable in its env contract without redirecting anything — which the k8s and Compose
		// substrates both need in order to declare the same set.
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := validateBaseURL(name, value); err != nil {
			return BaseURLOverrides{}, err
		}
		out.byProvider[provider] = strings.TrimSpace(value)
	}
	return out, nil
}

// validateBaseURL refuses everything that would send a credential somewhere unintended.
func validateBaseURL(envName, raw string) error {
	v := strings.TrimSpace(raw)
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("%s is not a URL: %w", envName, err)
	}
	switch {
	case u.Scheme == "" || u.Host == "":
		return fmt.Errorf("%s must be an absolute URL including scheme and host, such as "+
			"https://relay.example.com/v1 — got %q", envName, v)

	case u.User != nil:
		// Credentials in the URL would be logged by anything that logs the endpoint, and this package
		// logs it at boot.
		return fmt.Errorf("%s must not carry credentials in the URL. The provider key comes from the "+
			"secrets source; an endpoint carrying userinfo puts it anywhere the endpoint is printed",
			envName)

	case u.RawQuery != "" || u.Fragment != "":
		// The adapters append a path (`/chat/completions`, `/messages`) to this value, so a query or
		// fragment does not survive concatenation — it would silently produce a URL nobody wrote.
		return fmt.Errorf("%s must be a base URL with no query or fragment: the adapters append a path "+
			"to it, so %q would become a URL nobody intended", envName, v)

	case u.Scheme == "http" && !isLoopbackHost(u.Host):
		// The one rule that is about secrecy rather than correctness.
		return fmt.Errorf("%s must use https. %q would send this deployment's provider credential over "+
			"the network in clear text. http is accepted only for loopback, which is for a local relay "+
			"or a test server", envName, v)

	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%s must be http or https, got scheme %q", envName, u.Scheme)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// Options renders the overrides as Gateway options. Empty when nothing is overridden, so a caller
// passes them unconditionally and an unconfigured deployment builds the gateway it always built.
func (o BaseURLOverrides) Options() []Option {
	opts := make([]Option, 0, len(o.byProvider))
	for _, p := range sortedKeys(o.byProvider) {
		opts = append(opts, WithBaseURL(p, o.byProvider[p]))
	}
	return opts
}

// Describe renders one line per override for the boot log.
//
// 🔴 A redirected endpoint is a security-relevant fact about where this deployment's credentials go,
// and it is invisible everywhere else — the console shows a provider NAME, never its address. If it is
// not said at boot it is not said anywhere.
func (o BaseURLOverrides) Describe() []string {
	out := make([]string, 0, len(o.byProvider))
	for _, p := range sortedKeys(o.byProvider) {
		out = append(out, fmt.Sprintf("%s → %s (overridden by %s)", p, o.byProvider[p], BaseURLEnvName(p)))
	}
	return out
}

// Len is how many providers are redirected.
func (o BaseURLOverrides) Len() int { return len(o.byProvider) }

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
