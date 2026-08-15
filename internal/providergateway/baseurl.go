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
// `WithBaseURL` has been on the Gateway since it was written, and its only caller anywhere in the tree
// was `cmd/demo/runmonitor` pointing at an httptest stub. `internal/launch` never called it, `ModelSpec`
// carries no endpoint field, and `defaultBaseURL` hardcodes the vendor URLs — so a deployment holding a
// relay key had no way to use it.
//
// # 🔴 TWO SCOPES, and they do not inherit from one another
//
// The platform makes provider calls on two paths that carry completely different data:
//
//   - the ACTIVATION GATE's calibration runs, which send the pinned fixtures — this repository's own
//     test trees, and nothing of any customer's;
//   - CUSTOMER ANALYSES under a `platform` placement, which send that customer's source.
//
// One variable covering both means redirecting the cheap path silently redirects the other, and the
// operator who wanted to measure a definition through a relay has posted a customer's source code to a
// third party without deciding to. So each path has its OWN variable, neither falls back to the other,
// and an unset scope is the vendor:
//
//	HEROS_REHEARSAL_PROVIDER_OPENAI_BASE_URL=https://relay.example.com/v1   # the gate only
//	HEROS_ANALYSIS_PROVIDER_OPENAI_BASE_URL=https://relay.example.com/v1    # customers' source
//
// Inheritance was the first design and is rejected deliberately. A default that silently widens the
// blast radius of the safer setting is the wrong direction for a rule about where customer code goes:
// redirecting the dangerous path should cost a deliberate second act, not come free with the first.
//
// # 🔴 Why the names are namespaced the way they are
//
// `HEROS_<NAME>_BASE_URL` is taken — `HEROS_RELEASE_BASE_URL` already configures the release channel
// `clilink/upgrade` reads — so the unknown-name check below would read `RELEASE` as a misspelled
// provider and refuse to boot a deployment that had merely configured its release channel.
//
// `HEROS_PROVIDER_<NAME>_BASE_URL` is the RETIRED spelling from this feature's first revision, when one
// variable covered both paths. It is refused rather than ignored — see `errRetiredEnvName`.
//
// # What it refuses, and why refusing beats defaulting
//
// Every rejection fails the BOOT rather than falling back to the vendor. A base URL is where this
// deployment's provider credential gets sent: silently ignoring a malformed one means the operator
// believes traffic goes to their relay while the key goes to the vendor, and silently accepting a
// plaintext one puts that key on the wire in clear.

// BaseURLScope names which provider traffic an override applies to.
//
// A type rather than a string, so a caller cannot pass "rehersal" and be quietly given no overrides —
// the two values below are the whole set and there is no way to construct a third.
type BaseURLScope struct {
	name   string
	prefix string
	// carries describes what this path sends, for the boot log. An operator reading that a scope is
	// redirected should not have to know which of the two paths handles customer source.
	carries string
}

var (
	// ScopeRehearsal is the activation gate's calibration runs. They send the pinned fixtures only.
	ScopeRehearsal = BaseURLScope{
		name:    "rehearsal",
		prefix:  "HEROS_REHEARSAL_PROVIDER_",
		carries: "the activation gate's calibration runs, which send the pinned fixtures and nothing of any customer's",
	}
	// ScopeAnalysis is customers' analyses under a `platform` placement. They send customer SOURCE.
	ScopeAnalysis = BaseURLScope{
		name:    "analysis",
		prefix:  "HEROS_ANALYSIS_PROVIDER_",
		carries: "🔴 CUSTOMERS' ANALYSES, which send that customer's source code",
	}
)

// Name is the scope's short name, for logs and errors.
func (s BaseURLScope) Name() string { return s.name }

const baseURLEnvSuffix = "_BASE_URL"

// retiredEnvPrefix was the single-scope spelling. Kept only so it can be REFUSED.
const retiredEnvPrefix = "HEROS_PROVIDER_"

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

// BaseURLEnvName is the variable overriding one provider's endpoint on one scope. Exported so a
// deployment manifest, a document and an error message cannot disagree about the spelling.
func BaseURLEnvName(scope BaseURLScope, provider string) string {
	return scope.prefix + strings.ToUpper(provider) + baseURLEnvSuffix
}

// BaseURLOverrides is the set of endpoints one scope has redirected.
//
// Empty is the normal case and means every provider on that path keeps its vendor default.
type BaseURLOverrides struct {
	scope      BaseURLScope
	byProvider map[string]string
}

// BaseURLOverridesFromEnv reads one variable per known provider, for ONE scope.
//
// 🔴 It reads only its own scope's prefix. There is no fallback to the other scope and none to the
// retired single-scope name — see the file header for why widening the dangerous path must cost a
// deliberate act.
func BaseURLOverridesFromEnv(scope BaseURLScope) (BaseURLOverrides, error) {
	known := map[string]bool{}
	for _, p := range Providers() {
		known[p] = true
	}

	out := BaseURLOverrides{scope: scope, byProvider: map[string]string{}}
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasSuffix(name, baseURLEnvSuffix) {
			continue
		}
		// 🔴 The retired spelling is REFUSED, not ignored. It is the one name an operator is most
		// likely to still have set — this feature shipped with it — and ignoring it would leave them
		// certain they had redirected traffic that is going to the vendor.
		if strings.HasPrefix(name, retiredEnvPrefix) {
			return BaseURLOverrides{}, errRetiredEnvName(name)
		}
		if !strings.HasPrefix(name, scope.prefix) {
			continue
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(name, scope.prefix), baseURLEnvSuffix)
		provider := strings.ToLower(middle)
		if !known[provider] {
			return BaseURLOverrides{}, fmt.Errorf(
				"%s names %q, which is not a provider this gateway can reach. It is one of %s. "+
					"Refusing rather than ignoring it: an ignored endpoint override leaves the credential "+
					"going to the vendor while the operator believes it goes somewhere else",
				name, provider, strings.Join(Providers(), ", "))
		}
		// An explicitly EMPTY value is "no override", not an error.
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

// errRetiredEnvName explains the split rather than just rejecting the old name.
//
// The operator set one variable meaning "use my relay". The answer they need is not "unknown variable"
// but which of the two paths they meant — and that the safe one is not the default.
func errRetiredEnvName(name string) error {
	provider := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(name, retiredEnvPrefix), baseURLEnvSuffix))
	if !isKnownProvider(provider) {
		provider = "openai"
	}
	return fmt.Errorf(
		"%s is no longer read. It used to redirect BOTH the activation gate and customers' analyses, "+
			"which send very different data — the gate sends only this repository's pinned fixtures, "+
			"while an analysis sends a customer's source code. One variable covering both meant "+
			"redirecting the cheap path silently redirected the other.\n"+
			"Set the path you meant, and note that neither inherits from the other:\n"+
			"  %s   (the gate only — fixtures)\n"+
			"  %s   (customers' source)",
		name, BaseURLEnvName(ScopeRehearsal, provider), BaseURLEnvName(ScopeAnalysis, provider))
}

func isKnownProvider(p string) bool {
	for _, k := range Providers() {
		if k == p {
			return true
		}
	}
	return false
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
// 🔴 A redirected endpoint is a security-relevant fact about where this deployment's credentials and
// data go, and it is invisible everywhere else — the console shows a provider NAME, never its address.
// Each line names the SCOPE and what that scope carries, so a redirect of customers' source cannot be
// read as a redirect of the gate.
func (o BaseURLOverrides) Describe() []string {
	out := make([]string, 0, len(o.byProvider))
	for _, p := range sortedKeys(o.byProvider) {
		out = append(out, fmt.Sprintf("%s [%s] → %s (set by %s; this path carries %s)",
			p, o.scope.name, o.byProvider[p], BaseURLEnvName(o.scope, p), o.scope.carries))
	}
	return out
}

// Len is how many providers are redirected on this scope.
func (o BaseURLOverrides) Len() int { return len(o.byProvider) }

// Scope is the path these overrides apply to.
func (o BaseURLOverrides) Scope() BaseURLScope { return o.scope }

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
