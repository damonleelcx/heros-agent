package forgedelivery

import (
	"fmt"
	"net/url"
	"strings"
)

// consoleref.go builds the CANONICAL console evidence reference the pull request body carries (task 3.3
// / FR). It must resolve to a canonical route per P9's rules and survive being pasted anywhere — into a
// PR body, a chat, an email — so it is an ABSOLUTE url over the canonical evidence path, with both key
// components percent-encoded exactly once.
//
// The path mirrors the console's single route definition (web/console/src/lib/routes.ts
// `routes.transform`): a transform is a two-part subject, config_hash AND the source_revision it was
// applied to. Keeping the path shape in exactly one Go place and one TypeScript place is the cost;
// the alternative — a link and the page disagreeing about where evidence lives — is worse.

// ConsoleEvidencePath is the canonical console path for a transform's full evidence. It mirrors
// routes.transform(configHash, sourceRevision).
func ConsoleEvidencePath(configHash, sourceRevision string) string {
	return "/app/transforms/" + url.PathEscape(configHash) + "/" + url.PathEscape(sourceRevision)
}

// ConsoleEvidenceRef builds the absolute, canonical evidence reference for the pull request body.
//
// baseURL is the console's public origin (e.g. "https://console.example.com"). It must be absolute so
// the reference resolves from anywhere it is pasted; a relative reference in a PR body opened on the
// forge would point at the forge, not the console. A blank or non-absolute base is an error rather than
// a silently-relative link, because a link that quietly points at the wrong host is the exact failure
// this function exists to prevent.
func ConsoleEvidenceRef(baseURL, configHash, sourceRevision string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("forgedelivery: console base %q is not an absolute URL; the evidence reference would not resolve when pasted", baseURL)
	}
	return base + ConsoleEvidencePath(configHash, sourceRevision), nil
}
