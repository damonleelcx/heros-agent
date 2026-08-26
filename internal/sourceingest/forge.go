package sourceingest

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// forge.go holds the per-forge adapters: the narrowest grant each forge actually supports, the host it
// is cloned from, and how a read credential is presented on the wire.
//
// # Why one adapter per forge and not one parameterised client
//
// The three forges express "read exactly this repository" in three genuinely different objects
// (PRD §14 A2), and pretending otherwise is what produces the defect this phase is most exposed to: a
// workspace-scoped Bitbucket grant recorded as though it were repository-scoped, because the code that
// stored it had one shape for all three. `Describe` states each forge's narrowest form as customer-
// facing prose, so the consent screen renders what the grant ACTUALLY permits per forge rather than a
// generic sentence that is true of none of them.
//
// # 🔴 The clone host list is here and nowhere else
//
// `CloneHosts()` is the single source the egress allowlist and the DevOps manifests are checked
// against. One host set hard-coded in five places is a defect this repository has paid for before: the
// comment does not protect the second copy. A forge added to `Forges()` with no entry here fails
// `TestEveryForgeHasAnAdapter` rather than failing at 03:00 with a DNS error.

// forgeAdapter is one forge's shape.
type forgeAdapter struct {
	forge Forge
	// host is the HTTPS host a clone reaches. Exactly one per forge — a self-hosted GitLab is a
	// different decision with a different threat model (it is a customer-named endpoint), and it is
	// out of scope for this phase for §14 A1's reason.
	host string
	// grant is the kind this forge issues at repository scope.
	grant GrantKind
	// grantLabel is the customer-facing name of that object, for the consent screen.
	grantLabel string
	// permission is the forge's own name for the read permission the grant carries, as PROSE for the
	// consent screen.
	permission string
	// readOnlyScopes is the same fact in MACHINE form: every scope spelling this forge's narrowest
	// grant actually carries. It is the ALLOWLIST `Authorization.Validate` admits against.
	//
	// 🔴 Two fields for one fact, and the split is deliberate rather than duplication. `permission` is
	// read by a person on a consent screen and its exact prose is asserted by the console
	// (`web/console/tests/connections.test.mjs`); this is compared against a wire value. Deriving one
	// from the other would force a formatting decision — `contents: read` against `contents:read` —
	// into a security control. `TestTheDeclaredReadScopesAreWhatTheConsentScreenStates` asserts they
	// agree once normalised, so the drift that matters (a scope enforced but not disclosed, or
	// disclosed but not enforced) is a red build.
	readOnlyScopes []string
	// revokeHint tells a customer where to revoke it on THEIR side. A revocation that only works on
	// our side is half a revocation, and the half they cannot verify.
	revokeHint string
	// credentialUser is the username half of the HTTPS basic credential each forge expects; the token
	// is the password half. They differ per forge and getting it wrong presents as an authentication
	// failure that looks like a bad token.
	credentialUser func(externalID string) string
}

var forgeAdapters = map[Forge]forgeAdapter{
	ForgeGitHub: {
		forge:          ForgeGitHub,
		host:           "github.com",
		grant:          GrantAppInstallation,
		grantLabel:     "a GitHub App installation limited to this one repository",
		permission:     "contents: read, metadata: read",
		readOnlyScopes: []string{"contents:read", "metadata:read"},
		revokeHint:     "GitHub → Settings → Applications → Installed GitHub Apps → Configure → Uninstall",
		// GitHub's installation-token basic auth uses the literal `x-access-token` as the user.
		credentialUser: func(string) string { return "x-access-token" },
	},
	ForgeGitLab: {
		forge:          ForgeGitLab,
		host:           "gitlab.com",
		grant:          GrantAccessToken,
		grantLabel:     "a GitLab project access token scoped to this one project",
		permission:     "read_repository",
		readOnlyScopes: []string{"read_repository"},
		revokeHint:     "GitLab → Project → Settings → Access Tokens → Revoke",
		// GitLab project access tokens authenticate over HTTPS as any non-empty user with the token
		// as the password; the conventional literal is used so the request is recognisable in a log.
		credentialUser: func(string) string { return "project-access-token" },
	},
	ForgeBitbucket: {
		forge:          ForgeBitbucket,
		host:           "bitbucket.org",
		grant:          GrantAccessToken,
		grantLabel:     "a Bitbucket repository access token scoped to this one repository",
		permission:     "repository:read",
		readOnlyScopes: []string{"repository:read"},
		revokeHint:     "Bitbucket → Repository settings → Access tokens → Revoke",
		credentialUser: func(string) string { return "x-token-auth" },
	},
}

// ForgeDescription is one forge's grant shape, as customer-facing prose.
//
// It is DATA rather than strings in a TSX file because the consent screen must state what the grant
// permits per forge (FR10), and a sentence maintained in the console cannot be checked against the
// adapter that actually builds the grant. Generated into the console's types, so the two cannot drift.
type ForgeDescription struct {
	Forge Forge `json:"forge"`
	// Host is the one host this forge is cloned from.
	Host string `json:"host"`
	// GrantKind is the object the customer will be creating.
	GrantKind GrantKind `json:"grant_kind"`
	// GrantLabel names that object in a sentence a customer can act on.
	GrantLabel string `json:"grant_label"`
	// Permission is the forge's own name for the read permission.
	Permission string `json:"permission"`
	// RevokeHint is where to revoke it on the customer's side.
	RevokeHint string `json:"revoke_hint"`
}

// DescribeForge returns one forge's shape.
func DescribeForge(f Forge) (ForgeDescription, error) {
	a, ok := forgeAdapters[f]
	if !ok {
		return ForgeDescription{}, fmt.Errorf("sourceingest: %q is not a supported forge", f)
	}
	return ForgeDescription{
		Forge:      a.forge,
		Host:       a.host,
		GrantKind:  a.grant,
		GrantLabel: a.grantLabel,
		Permission: a.permission,
		RevokeHint: a.revokeHint,
	}, nil
}

// DescribeForges returns every forge's shape, sorted by forge name.
func DescribeForges() []ForgeDescription {
	out := make([]ForgeDescription, 0, len(forgeAdapters))
	for _, f := range Forges() {
		d, err := DescribeForge(f)
		if err != nil {
			continue // unreachable: Forges() and forgeAdapters are fenced to agree
		}
		out = append(out, d)
	}
	return out
}

// CloneHosts returns every host a clone may reach, sorted.
//
// 🔴 The egress allowlist is checked against THIS. Cloning is a new egress class and is not implicitly
// permitted because it is git (task 5.1) — `deploy/k8s/base/networkpolicy.yaml` and
// `TestForgeHostsAreOnTheEgressAllowlist` both read this function, so a fourth forge cannot be added
// without the manifest going red.
func CloneHosts() []string {
	out := make([]string, 0, len(forgeAdapters))
	for _, a := range forgeAdapters {
		out = append(out, a.host)
	}
	sort.Strings(out)
	return out
}

// ReadOnlyScopesFor reports every scope spelling this forge's narrowest grant carries — the set
// `Authorization.Validate` admits against.
//
// # 🔴 Why this is an ALLOWLIST, and what the denylist it replaced could not do
//
// The check used to refuse a scope containing a write VERB — `write`, `push`, `admin`, `delete`,
// `maintain`, `manage`, `create` — and its own comment named the weakness it was accepting: *"a table
// of exact names is a table that is missing the one the forge shipped last month."* That was right
// about the risk and wrong about which table has it. A denylist of dangerous spellings is the one that
// goes stale; an allowlist of the spellings we ask for cannot, because it is a fact about OUR request
// rather than about a forge's evolving vocabulary.
//
// The gap that proved it: **GitHub's classic `repo` scope grants full read AND write** and contains no
// verb, so it passed. It is not alone — `public_repo` (write to public repositories) and GitLab's `api`
// (complete read/write API access) are the same shape: NOUNS that confer every verb. A denylist would
// have needed all three added, and would still be missing the fourth.
//
// The denylist also refused things it should not have: a GitHub App's `administration:read` is
// read-only metadata and contains `admin`.
//
// # What "read-only" means here, precisely
//
// Not "any scope that cannot write" — **the scopes the platform's own narrowest grant carries.** That
// is the same rule `Validate` already applies to repositories: a grant covering a repository we did not
// name is refused even though it is a perfectly ordinary grant. Broader than asked for is refused,
// whether the excess is a repository or a permission.
//
// 🚫 It is deliberately PER FORGE. A GitHub grant reporting GitLab's `read_repository` is not a
// harmless spelling difference — it is evidence that whatever built the authorization is confused about
// which forge it is talking to, and admitting it would record that confusion as a connection.
func ReadOnlyScopesFor(f Forge) ([]string, error) {
	a, ok := forgeAdapters[f]
	if !ok {
		return nil, fmt.Errorf("sourceingest: %q is not a supported forge", f)
	}
	return append([]string(nil), a.readOnlyScopes...), nil
}

// ExpectedGrantKind reports the grant kind this forge issues at repository scope.
//
// Used at connect to refuse an authorization claiming a kind the forge does not issue — a GitHub
// `access_token` recorded where an App installation was expected is a fine-grained PAT wearing the
// App's label, and the difference is exactly the one §14 A2 decided.
func ExpectedGrantKind(f Forge) (GrantKind, error) {
	a, ok := forgeAdapters[f]
	if !ok {
		return "", fmt.Errorf("sourceingest: %q is not a supported forge", f)
	}
	return a.grant, nil
}

// CloneURL builds the HTTPS URL for a repository on a forge, with the credential embedded.
//
// # 🔴 Why the token is in the URL, and what that costs
//
// git's HTTPS transport takes credentials from the URL, a credential helper, or a prompt. A helper is
// a file on disk and a prompt is disabled (`GIT_TERMINAL_PROMPT=0`), which leaves the URL. The cost is
// that the token is briefly in an argv-shaped value, so THREE things are true of every caller:
//
//   - it is built inside the secret store's closure and never escapes it;
//   - it is passed to git through a file-backed `-c credential` config, NOT as a command-line
//     argument — argv is world-readable in /proc on Linux;
//   - `Redact` is applied to every byte of git's output before it reaches a log, an error, or a
//     record, and `TestNoForgeCredentialReachesAnOutputSurface` asserts it.
//
// The returned URL is therefore a value with a handling rule, and the rule is enforced by the one
// caller in git.go rather than trusted at each call site.
func CloneURL(f Forge, repository, externalID, token string) (string, error) {
	a, ok := forgeAdapters[f]
	if !ok {
		return "", fmt.Errorf("sourceingest: %q is not a supported forge", f)
	}
	if strings.Count(repository, "/") != 1 || strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return "", fmt.Errorf("sourceingest: repository %q is not owner/name", repository)
	}
	u := &url.URL{
		Scheme: "https",
		Host:   a.host,
		Path:   "/" + repository + ".git",
	}
	if token != "" {
		u.User = url.UserPassword(a.credentialUser(externalID), token)
	}
	return u.String(), nil
}

// PublicCloneURL is the same URL with no credential — what a log line or an error may show.
func PublicCloneURL(f Forge, repository string) (string, error) {
	return CloneURL(f, repository, "", "")
}
