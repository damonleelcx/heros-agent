package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// Scrubber is the collector pipeline's third stage (Decision 6): it strips secrets, API keys, prompt
// text, completion text, and PII from anything bound for a store, replacing content with a
// content-hashed reference. It runs on EVERY event and span so "secrets never touch a span/label/log"
// is enforced at one chokepoint, not trusted to each emitter.
type Scrubber interface {
	ScrubEvent(ev metricevent.Event) metricevent.Event
	ScrubSpan(sp Span) Span
}

// Redacted is the marker a scrubbed value is replaced with, so a reviewer sees that scrubbing happened
// rather than a suspicious blank. A blob reference (blobref:<hash>) is used instead when the value was
// substantial content (a prompt/completion), so it stays retrievable from the object store by hash.
const Redacted = "[REDACTED]"

// BlobRefPrefix marks a content-hash reference: the bytes live in the object store (P0/P2), telemetry
// carries only this pointer (Decision 6: "telemetry references content-hashed blobs only").
const BlobRefPrefix = "blobref:"

// secretScrubber is the default Scrubber. It is deliberately a CONSERVATIVE denylist over attribute and
// dimension VALUES: it never inspects keys (a key like "gen_ai.system" is safe), only string values,
// and it leaves numbers/tags untouched. The seven tags and metric payload are structural identifiers,
// never content, so they pass through; free-text values that look like a secret or PII are redacted,
// and long free-text (prompt/completion-shaped) is replaced by a blob reference.
type secretScrubber struct {
	patterns []*regexp.Regexp
	// contentKeys are attribute keys whose values are known to be free-text content (prompt/completion)
	// and are ALWAYS replaced by a blob reference, never inlined — even if they do not match a secret
	// pattern. Empty by default: the instrument never puts content on a span, so this guards accidental
	// future additions.
	contentKeys map[string]struct{}
	// longTextThreshold: a value longer than this is treated as content and blob-referenced, since a
	// short label cannot carry a prompt but a 2 KB attribute value can.
	longTextThreshold int
}

// secretPatterns are the shapes that must never appear in telemetry. Central list (no scattered
// literals), extendable without touching the scrub logic.
func secretPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}`),                            // OpenAI-style keys (incl. sk-ant-, sk-proj-)
		regexp.MustCompile(`AKIA[0-9A-Z]{12,}`),                                // AWS access key id
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{8,}`),                 // bearer tokens
		regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),                             // GitHub PAT
		regexp.MustCompile(`eyJ[A-Za-z0-9._\-]{20,}`),                          // JWT
		regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), // email (PII)
	}
}

// NewScrubber builds the default secret/PII scrubber.
func NewScrubber() Scrubber {
	return &secretScrubber{
		patterns:          secretPatterns(),
		contentKeys:       map[string]struct{}{},
		longTextThreshold: 512,
	}
}

// ScrubEvent scrubs a metric event's DIMENSIONS (its tags/payload are structural and safe). A cost
// event's pricebook_version or a model_version_id is not a secret; a stray free-text dimension is.
func (s *secretScrubber) ScrubEvent(ev metricevent.Event) metricevent.Event {
	if len(ev.Dimensions) == 0 {
		return ev
	}
	out := make(map[string]any, len(ev.Dimensions))
	for k, v := range ev.Dimensions {
		out[k] = s.scrubValue(k, v)
	}
	ev.Dimensions = out
	return ev
}

// ScrubSpan scrubs a span's attributes and its status message (a provider error string could echo
// content even though the gateway already stripped credentials — defense in depth).
func (s *secretScrubber) ScrubSpan(sp Span) Span {
	if len(sp.Attributes) > 0 {
		out := make(map[string]any, len(sp.Attributes))
		for k, v := range sp.Attributes {
			out[k] = s.scrubValue(k, v)
		}
		sp.Attributes = out
	}
	if sp.StatusMsg != "" {
		sp.StatusMsg = s.scrubString(sp.StatusMsg)
	}
	return sp
}

func (s *secretScrubber) scrubValue(key string, v any) any {
	str, ok := v.(string)
	if !ok {
		return v // numbers, bools, ints — not content
	}
	if _, isContent := s.contentKeys[key]; isContent {
		return blobRef(str)
	}
	if len(str) > s.longTextThreshold {
		// Prompt/completion-shaped: reference it, never inline it.
		return blobRef(str)
	}
	return s.scrubString(str)
}

// scrubString redacts every secret/PII match in a value. If a match is found in a longer string, the
// whole value is replaced by a blob reference — a partially-redacted secret-bearing string is still a
// leak risk (surrounding context can reveal the secret), so it is referenced, not patched.
func (s *secretScrubber) scrubString(str string) string {
	for _, re := range s.patterns {
		if re.MatchString(str) {
			// If the value is ESSENTIALLY the secret, redact; if it embeds one in more text, blob-ref it.
			if len(str) <= 128 {
				str = re.ReplaceAllString(str, Redacted)
			} else {
				return blobRef(str)
			}
		}
	}
	return str
}

// blobRef is the content-hash reference the bytes are replaced with. Same SHA-256 hex the blob catalog
// keys on, so the reference resolves in the object store.
func blobRef(content string) string {
	sum := sha256.Sum256([]byte(content))
	return BlobRefPrefix + hex.EncodeToString(sum[:])
}

// containsSecret reports whether any secret/PII pattern matches — used by tests and by the adversarial
// self-review to assert no store received a secret.
func containsSecret(s string) bool {
	for _, re := range secretPatterns() {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

var _ = strings.TrimSpace
